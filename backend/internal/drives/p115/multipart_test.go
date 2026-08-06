package p115

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type fakeP115OSSBucket struct {
	putObjectFn  func(string, io.Reader, ...oss.Option) error
	initiateFn   func(string, ...oss.Option) (oss.InitiateMultipartUploadResult, error)
	uploadPartFn func(oss.InitiateMultipartUploadResult, io.Reader, int64, int, ...oss.Option) (oss.UploadPart, error)
	completeFn   func(oss.InitiateMultipartUploadResult, []oss.UploadPart, ...oss.Option) (oss.CompleteMultipartUploadResult, error)
	abortFn      func(oss.InitiateMultipartUploadResult, ...oss.Option) error
}

func (b *fakeP115OSSBucket) PutObject(key string, reader io.Reader, options ...oss.Option) error {
	if b.putObjectFn != nil {
		return b.putObjectFn(key, reader, options...)
	}
	return errors.New("unexpected PutObject")
}

func (b *fakeP115OSSBucket) InitiateMultipartUpload(key string, options ...oss.Option) (oss.InitiateMultipartUploadResult, error) {
	if b.initiateFn != nil {
		return b.initiateFn(key, options...)
	}
	return oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: key, UploadID: "upload-id"}, nil
}

func (b *fakeP115OSSBucket) UploadPart(
	imur oss.InitiateMultipartUploadResult,
	reader io.Reader,
	partSize int64,
	partNumber int,
	options ...oss.Option,
) (oss.UploadPart, error) {
	if b.uploadPartFn != nil {
		return b.uploadPartFn(imur, reader, partSize, partNumber, options...)
	}
	return oss.UploadPart{}, errors.New("unexpected UploadPart")
}

func (b *fakeP115OSSBucket) CompleteMultipartUpload(
	imur oss.InitiateMultipartUploadResult,
	parts []oss.UploadPart,
	options ...oss.Option,
) (oss.CompleteMultipartUploadResult, error) {
	if b.completeFn != nil {
		return b.completeFn(imur, parts, options...)
	}
	return oss.CompleteMultipartUploadResult{}, errors.New("unexpected CompleteMultipartUpload")
}

func (b *fakeP115OSSBucket) AbortMultipartUpload(imur oss.InitiateMultipartUploadResult, options ...oss.Option) error {
	if b.abortFn != nil {
		return b.abortFn(imur, options...)
	}
	return nil
}

func preparedP115Bytes(data []byte) p115PreparedUploadBody {
	reader := bytes.NewReader(data)
	return p115PreparedUploadBody{reader: reader, readerAt: reader}
}

func testP115OSSParams() *sdk.UploadOSSParams {
	params := &sdk.UploadOSSParams{Bucket: "bucket", Object: "object-key"}
	params.Callback.Callback = `{"callbackUrl":"https://example.invalid/callback"}`
	params.Callback.CallbackVar = `{"x:cid":"0"}`
	return params
}

func setP115CallbackResult(options []oss.Option, body []byte) error {
	value, err := oss.FindOption(options, "x-response-body", nil)
	if err != nil {
		return err
	}
	target, ok := value.(*[]byte)
	if !ok || target == nil {
		return errors.New("callback result option missing")
	}
	*target = append((*target)[:0], body...)
	return nil
}

func staticP115OSSProvider(bucket p115OSSBucket) p115OSSAccessProvider {
	return func(context.Context) (p115OSSAccess, error) {
		return p115OSSAccess{
			bucket: bucket,
			token: sdk.UploadOSSTokenResp{
				AccessKeyID:     "access-key",
				AccessKeySecret: "access-secret",
				SecurityToken:   "security-token",
			},
		}, nil
	}
}

func TestPlanP115Multipart(t *testing.T) {
	tests := []struct {
		name          string
		size          int64
		wantParts     int
		wantFirstSize int64
	}{
		{name: "just over threshold", size: p115MultipartThreshold + 1, wantParts: 2, wantFirstSize: p115MultipartTargetPartSize},
		{name: "274 MB video", size: 274155376, wantParts: 33, wantFirstSize: p115MultipartTargetPartSize},
		{name: "423 MB video", size: 423410512, wantParts: 51, wantFirstSize: p115MultipartTargetPartSize},
		{name: "maximum object", size: p115MaxMultipartObjectSize, wantParts: p115MultipartMaxParts, wantFirstSize: int64(oss.MaxPartSize)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := planP115Multipart(tt.size)
			if err != nil {
				t.Fatalf("plan multipart: %v", err)
			}
			if len(chunks) != tt.wantParts {
				t.Fatalf("parts = %d, want %d", len(chunks), tt.wantParts)
			}
			if chunks[0].size != tt.wantFirstSize {
				t.Fatalf("first part = %d, want %d", chunks[0].size, tt.wantFirstSize)
			}
			var total int64
			for index, chunk := range chunks {
				if chunk.number != index+1 || chunk.offset != total || chunk.size <= 0 {
					t.Fatalf("chunk %d = %#v, preceding bytes=%d", index, chunk, total)
				}
				total += chunk.size
			}
			if total != tt.size {
				t.Fatalf("planned bytes = %d, want %d", total, tt.size)
			}
		})
	}

	if _, err := planP115Multipart(0); err == nil {
		t.Fatal("zero-sized multipart plan was accepted")
	}
	if err := validateP115UploadSize(p115MaxMultipartObjectSize + 1); err == nil {
		t.Fatal("object above OSS multipart limit was accepted")
	}
}

func TestUploadPreparedP115BodyUsesPutObjectForSmallFile(t *testing.T) {
	data := []byte("prefix-small-payload")
	reader := bytes.NewReader(data)
	start := int64(len("prefix-"))
	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		t.Fatalf("seek source: %v", err)
	}
	body := p115PreparedUploadBody{reader: reader, readerAt: reader, start: start}
	callback := []byte(`{"state":true,"data":{"file_id":"file-1"}}`)

	var uploaded []byte
	bucket := &fakeP115OSSBucket{
		putObjectFn: func(key string, source io.Reader, options ...oss.Option) error {
			if key != "object-key" {
				return fmt.Errorf("object key = %q", key)
			}
			var err error
			uploaded, err = io.ReadAll(source)
			if err != nil {
				return err
			}
			if got, _ := oss.FindOption(options, sdk.OssSecurityTokenHeaderName, ""); got != "security-token" {
				return fmt.Errorf("security token = %q", got)
			}
			return setP115CallbackResult(options, callback)
		},
	}

	gotCallback, err := uploadPreparedP115BodyToOSS(
		context.Background(),
		testP115OSSParams(),
		body,
		int64(len("small-payload")),
		staticP115OSSProvider(bucket),
	)
	if err != nil {
		t.Fatalf("put small object: %v", err)
	}
	if string(uploaded) != "small-payload" {
		t.Fatalf("uploaded = %q, want small-payload", uploaded)
	}
	if !bytes.Equal(gotCallback, callback) {
		t.Fatalf("callback = %q, want %q", gotCallback, callback)
	}
}

func TestUploadP115MultipartIsSequentialAndCompletesExactly(t *testing.T) {
	payload := bytes.Repeat([]byte("p115-multipart-"), 750000)
	if int64(len(payload)) <= p115MultipartThreshold {
		t.Fatalf("payload = %d, must exceed multipart threshold", len(payload))
	}
	callback := []byte(`{"state":true,"data":{"file_id":"file-2"}}`)
	var (
		partData       = make(map[int][]byte)
		lastPart       int
		completed      []oss.UploadPart
		abortCalls     int
		initHasSHA1    bool
		initSequential bool
	)
	bucket := &fakeP115OSSBucket{
		initiateFn: func(key string, options ...oss.Option) (oss.InitiateMultipartUploadResult, error) {
			params, err := oss.GetRawParams(options)
			if err != nil {
				return oss.InitiateMultipartUploadResult{}, err
			}
			_, initHasSHA1 = params["x-oss-enable-sha1"]
			_, initSequential = params["sequential"]
			return oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: key, UploadID: "upload-id"}, nil
		},
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, source io.Reader, size int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			if number != lastPart+1 {
				return oss.UploadPart{}, fmt.Errorf("part %d arrived after %d", number, lastPart)
			}
			data, err := io.ReadAll(source)
			if err != nil {
				return oss.UploadPart{}, err
			}
			if int64(len(data)) != size {
				return oss.UploadPart{}, fmt.Errorf("part %d bytes = %d, want %d", number, len(data), size)
			}
			lastPart = number
			partData[number] = data
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, parts []oss.UploadPart, options ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			completed = append([]oss.UploadPart(nil), parts...)
			return oss.CompleteMultipartUploadResult{}, setP115CallbackResult(options, callback)
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	gotCallback, err := uploadPreparedP115BodyToOSS(
		context.Background(),
		testP115OSSParams(),
		preparedP115Bytes(payload),
		int64(len(payload)),
		staticP115OSSProvider(bucket),
	)
	if err != nil {
		t.Fatalf("multipart upload: %v", err)
	}
	if !initHasSHA1 || !initSequential {
		t.Fatalf("init options SHA1=%v sequential=%v, want both true", initHasSHA1, initSequential)
	}
	if abortCalls != 0 {
		t.Fatalf("abort calls = %d, want 0", abortCalls)
	}
	if !bytes.Equal(gotCallback, callback) {
		t.Fatalf("callback = %q, want %q", gotCallback, callback)
	}
	var rebuilt []byte
	for index, part := range completed {
		if part.PartNumber != index+1 {
			t.Fatalf("completed part %d number = %d", index, part.PartNumber)
		}
		rebuilt = append(rebuilt, partData[part.PartNumber]...)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatalf("rebuilt payload bytes = %d, want %d", len(rebuilt), len(payload))
	}
}

func TestUploadP115MultipartRetriesOnlyFailedPart(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, int(p115MultipartThreshold+1))
	attempts := make(map[int]int)
	bucket := &fakeP115OSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, source io.Reader, _ int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			attempts[number]++
			if number == 1 && attempts[number] == 1 {
				return oss.UploadPart{}, &net.DNSError{Err: "temporary failure", Name: "oss.example"}
			}
			if _, err := io.Copy(io.Discard, source); err != nil {
				return oss.UploadPart{}, err
			}
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, _ []oss.UploadPart, options ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			return oss.CompleteMultipartUploadResult{}, setP115CallbackResult(options, []byte(`{"state":true,"data":{"file_id":"file-3"}}`))
		},
	}

	_, err := uploadP115Multipart(
		context.Background(),
		testP115OSSParams(),
		preparedP115Bytes(payload),
		int64(len(payload)),
		staticP115OSSProvider(bucket),
	)
	if err != nil {
		t.Fatalf("multipart upload: %v", err)
	}
	if attempts[1] != 2 {
		t.Fatalf("part 1 attempts = %d, want 2", attempts[1])
	}
	for number, count := range attempts {
		if number != 1 && count != 1 {
			t.Fatalf("part %d attempts = %d, want 1", number, count)
		}
	}
}

func TestUploadP115MultipartAbortsOnPermanentPartFailure(t *testing.T) {
	payload := bytes.Repeat([]byte{0x4f}, int(p115MultipartThreshold+1))
	var abortCalls, completeCalls int
	bucket := &fakeP115OSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, _ io.Reader, _ int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			return oss.UploadPart{}, fmt.Errorf("part %d permission denied", number)
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, _ []oss.UploadPart, _ ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			completeCalls++
			return oss.CompleteMultipartUploadResult{}, nil
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	_, err := uploadP115Multipart(
		context.Background(),
		testP115OSSParams(),
		preparedP115Bytes(payload),
		int64(len(payload)),
		staticP115OSSProvider(bucket),
	)
	if err == nil || !strings.Contains(err.Error(), "upload part 1/") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want part failure", err)
	}
	if abortCalls != 1 || completeCalls != 0 {
		t.Fatalf("abort calls=%d complete calls=%d, want 1/0", abortCalls, completeCalls)
	}
}

func TestUploadP115MultipartCancellationAborts(t *testing.T) {
	payload := bytes.Repeat([]byte{0x43}, int(p115MultipartThreshold+1))
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var abortCalls int
	bucket := &fakeP115OSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, source io.Reader, _ int64, _ int, _ ...oss.Option) (oss.UploadPart, error) {
			startOnce.Do(func() { close(started) })
			<-release
			_, err := source.Read(make([]byte, 1))
			return oss.UploadPart{}, err
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		_, err := uploadP115Multipart(ctx, testP115OSSParams(), preparedP115Bytes(payload), int64(len(payload)), staticP115OSSProvider(bucket))
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("multipart upload did not start")
	}
	cancel()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("multipart upload did not stop after cancellation")
	}
	if abortCalls != 1 {
		t.Fatalf("abort calls = %d, want 1", abortCalls)
	}
}

type p115RewritingTransport struct {
	base   http.RoundTripper
	target string
}

func (t *p115RewritingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Hostname(), "aliyuncs.com") {
		request.URL.Scheme = "http"
		request.URL.Host = t.target
	}
	return t.base.RoundTrip(request)
}

func TestUploadP115MultipartThroughAliyunSDK(t *testing.T) {
	payload := bytes.Repeat([]byte{0x63}, int(p115MultipartThreshold+1))
	callback := []byte(`{"state":true,"data":{"file_id":"file-sdk","sha1":"ABC"}}`)
	var (
		mu         sync.Mutex
		lastPart   int
		partData   = make(map[int][]byte)
		completed  bool
		abortCalls int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get(sdk.OssSecurityTokenHeaderName); got != "security-token" {
			t.Errorf("security token = %q, want security-token", got)
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Query().Has("uploads"):
			if !request.URL.Query().Has("sequential") || !request.URL.Query().Has("x-oss-enable-sha1") {
				http.Error(w, "missing sequential SHA1 options", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>object-key</Key><UploadId>upload-id</UploadId></InitiateMultipartUploadResult>`)
		case request.Method == http.MethodPut:
			number, err := strconv.Atoi(request.URL.Query().Get("partNumber"))
			if err != nil || number <= 0 || request.URL.Query().Get("uploadId") != "upload-id" {
				http.Error(w, "invalid part request", http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if request.ContentLength != int64(len(data)) {
				http.Error(w, fmt.Sprintf("content length %d does not match body %d", request.ContentLength, len(data)), http.StatusBadRequest)
				return
			}
			mu.Lock()
			if number != lastPart+1 {
				mu.Unlock()
				http.Error(w, "parts out of order", http.StatusBadRequest)
				return
			}
			lastPart = number
			partData[number] = data
			mu.Unlock()
			w.Header().Set("ETag", fmt.Sprintf("etag-%d", number))
			w.WriteHeader(http.StatusOK)
		case request.Method == http.MethodPost && request.URL.Query().Get("uploadId") == "upload-id":
			encodedCallback := request.Header.Get(oss.HTTPHeaderOssCallback)
			decodedCallback, err := base64.StdEncoding.DecodeString(encodedCallback)
			if err != nil || string(decodedCallback) != testP115OSSParams().Callback.Callback {
				http.Error(w, "invalid callback header", http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || !bytes.Contains(body, []byte("<PartNumber>1</PartNumber>")) {
				http.Error(w, "invalid completion body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			completed = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(callback)
		case request.Method == http.MethodDelete:
			mu.Lock()
			abortCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	token := sdk.UploadOSSTokenResp{
		AccessKeyID:     "access-key",
		AccessKeySecret: "access-secret",
		SecurityToken:   "security-token",
	}
	httpClient := &http.Client{Transport: &p115RewritingTransport{base: http.DefaultTransport, target: server.Listener.Addr().String()}}
	client, err := newP115OSSClient(&token, oss.HTTPClient(httpClient))
	if err != nil {
		t.Fatalf("new OSS client: %v", err)
	}
	bucket, err := client.Bucket("bucket")
	if err != nil {
		t.Fatalf("open bucket: %v", err)
	}
	provider := staticP115OSSProvider(bucket)
	gotCallback, err := uploadP115Multipart(context.Background(), testP115OSSParams(), preparedP115Bytes(payload), int64(len(payload)), provider)
	if err != nil {
		t.Fatalf("multipart upload through SDK: %v", err)
	}
	if !bytes.Equal(gotCallback, callback) {
		t.Fatalf("callback = %q, want %q", gotCallback, callback)
	}

	mu.Lock()
	defer mu.Unlock()
	if !completed || abortCalls != 0 {
		t.Fatalf("completed=%v abort calls=%d, want true/0", completed, abortCalls)
	}
	var rebuilt []byte
	for number := 1; number <= len(partData); number++ {
		rebuilt = append(rebuilt, partData[number]...)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatalf("rebuilt bytes = %d, want %d", len(rebuilt), len(payload))
	}
}
