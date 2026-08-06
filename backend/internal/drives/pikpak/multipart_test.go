package pikpak

import (
	"bytes"
	"context"
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

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type fakePikPakOSSBucket struct {
	putObjectFn  func(string, io.Reader, ...oss.Option) error
	initiateFn   func(string, ...oss.Option) (oss.InitiateMultipartUploadResult, error)
	uploadPartFn func(oss.InitiateMultipartUploadResult, io.Reader, int64, int, ...oss.Option) (oss.UploadPart, error)
	completeFn   func(oss.InitiateMultipartUploadResult, []oss.UploadPart, ...oss.Option) (oss.CompleteMultipartUploadResult, error)
	abortFn      func(oss.InitiateMultipartUploadResult, ...oss.Option) error
}

func (b *fakePikPakOSSBucket) PutObject(key string, reader io.Reader, options ...oss.Option) error {
	if b.putObjectFn != nil {
		return b.putObjectFn(key, reader, options...)
	}
	return errors.New("unexpected PutObject")
}

func (b *fakePikPakOSSBucket) InitiateMultipartUpload(key string, options ...oss.Option) (oss.InitiateMultipartUploadResult, error) {
	if b.initiateFn != nil {
		return b.initiateFn(key, options...)
	}
	return oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: key, UploadID: "upload-id"}, nil
}

func (b *fakePikPakOSSBucket) UploadPart(
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

func (b *fakePikPakOSSBucket) CompleteMultipartUpload(
	imur oss.InitiateMultipartUploadResult,
	parts []oss.UploadPart,
	options ...oss.Option,
) (oss.CompleteMultipartUploadResult, error) {
	if b.completeFn != nil {
		return b.completeFn(imur, parts, options...)
	}
	return oss.CompleteMultipartUploadResult{}, errors.New("unexpected CompleteMultipartUpload")
}

func (b *fakePikPakOSSBucket) AbortMultipartUpload(imur oss.InitiateMultipartUploadResult, options ...oss.Option) error {
	if b.abortFn != nil {
		return b.abortFn(imur, options...)
	}
	return nil
}

func preparedBytes(data []byte) preparedUploadBody {
	r := bytes.NewReader(data)
	return preparedUploadBody{reader: r, readerAt: r}
}

func TestPlanPikPakMultipart(t *testing.T) {
	tests := []struct {
		name          string
		size          int64
		wantParts     int
		wantFirstSize int64
	}{
		{
			name:          "just over threshold uses one MiB parts",
			size:          pikpakMultipartThreshold + 1,
			wantParts:     11,
			wantFirstSize: pikpakMultipartMinPartSize,
		},
		{
			name:          "current 121007 video uses one hundred parts",
			size:          423410512,
			wantParts:     100,
			wantFirstSize: 4234106,
		},
		{
			name:          "current 121008 video uses one hundred parts",
			size:          274155376,
			wantParts:     100,
			wantFirstSize: 2741554,
		},
		{
			name:          "six GiB is supported",
			size:          6 * pikpakMultipartBandSize,
			wantParts:     600,
			wantFirstSize: 10737419,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := planPikPakMultipart(tt.size)
			if err != nil {
				t.Fatalf("plan multipart: %v", err)
			}
			if len(chunks) != tt.wantParts {
				t.Fatalf("parts = %d, want %d", len(chunks), tt.wantParts)
			}
			if chunks[0].size != tt.wantFirstSize {
				t.Fatalf("first part size = %d, want %d", chunks[0].size, tt.wantFirstSize)
			}

			var total int64
			for i, chunk := range chunks {
				if chunk.index != i || chunk.number != i+1 {
					t.Fatalf("chunk %d identity = index %d number %d", i, chunk.index, chunk.number)
				}
				if chunk.offset != total {
					t.Fatalf("chunk %d offset = %d, want %d", i, chunk.offset, total)
				}
				total += chunk.size
			}
			if total != tt.size {
				t.Fatalf("planned bytes = %d, want %d", total, tt.size)
			}
		})
	}
}

func TestUploadPreparedBodyUsesPutObjectForSmallFile(t *testing.T) {
	data := []byte("prefix-small payload")
	r := bytes.NewReader(data)
	if _, err := r.Seek(int64(len("prefix-")), io.SeekStart); err != nil {
		t.Fatalf("seek body: %v", err)
	}
	body := preparedUploadBody{reader: r, readerAt: r, start: int64(len("prefix-"))}

	var got []byte
	bucket := &fakePikPakOSSBucket{
		putObjectFn: func(key string, reader io.Reader, _ ...oss.Option) error {
			if key != "object-key" {
				return fmt.Errorf("key = %q", key)
			}
			var err error
			got, err = io.ReadAll(reader)
			return err
		},
	}
	p := &s3Params{Key: "object-key", SecurityToken: "token"}
	if err := uploadPreparedBodyToOSS(context.Background(), bucket, p, body, int64(len("small payload"))); err != nil {
		t.Fatalf("upload small body: %v", err)
	}
	if string(got) != "small payload" {
		t.Fatalf("uploaded body = %q, want small payload", got)
	}
}

func TestUploadMultipartRunsConcurrentlyAndCompletesInPartOrder(t *testing.T) {
	payload := bytes.Repeat([]byte("multipart-payload-"), 620000)
	if int64(len(payload)) <= pikpakMultipartThreshold {
		t.Fatalf("test payload = %d, must exceed multipart threshold", len(payload))
	}

	var (
		mu             sync.Mutex
		active         int
		maxActive      int
		abortCalls     int
		completedParts []oss.UploadPart
		partData       = make(map[int][]byte)
	)
	release := make(chan struct{})
	var releaseOnce sync.Once

	bucket := &fakePikPakOSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, reader io.Reader, size int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			if active >= 2 {
				releaseOnce.Do(func() { close(release) })
			}
			mu.Unlock()

			select {
			case <-release:
			case <-time.After(2 * time.Second):
				return oss.UploadPart{}, errors.New("multipart workers did not overlap")
			}

			data, err := io.ReadAll(reader)
			mu.Lock()
			active--
			if err == nil {
				partData[number] = data
			}
			mu.Unlock()
			if err != nil {
				return oss.UploadPart{}, err
			}
			if int64(len(data)) != size {
				return oss.UploadPart{}, fmt.Errorf("part %d bytes = %d, want %d", number, len(data), size)
			}
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, parts []oss.UploadPart, _ ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			completedParts = append([]oss.UploadPart(nil), parts...)
			return oss.CompleteMultipartUploadResult{}, nil
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	p := &s3Params{Key: "object-key", SecurityToken: "token"}
	if err := uploadMultipartBodyToOSS(context.Background(), bucket, p, preparedBytes(payload), int64(len(payload))); err != nil {
		t.Fatalf("multipart upload: %v", err)
	}
	if maxActive < 2 {
		t.Fatalf("max concurrent parts = %d, want at least 2", maxActive)
	}
	if abortCalls != 0 {
		t.Fatalf("abort calls = %d, want 0 after success", abortCalls)
	}
	if len(completedParts) == 0 {
		t.Fatal("complete received no parts")
	}

	var rebuilt []byte
	for i, part := range completedParts {
		if part.PartNumber != i+1 {
			t.Fatalf("completed part %d number = %d, want %d", i, part.PartNumber, i+1)
		}
		rebuilt = append(rebuilt, partData[part.PartNumber]...)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatalf("rebuilt payload length = %d, want %d", len(rebuilt), len(payload))
	}
}

func TestUploadMultipartWorksThroughAliyunOSSSDK(t *testing.T) {
	payload := bytes.Repeat([]byte{0x63}, int(pikpakMultipartThreshold+1))
	var (
		mu         sync.Mutex
		partData   = make(map[int][]byte)
		completed  bool
		abortCalls int
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/object-key", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(ossSecurityTokenHeaderName); got != "security-token" {
			t.Errorf("security token = %q, want security-token", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>object-key</Key><UploadId>upload-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut:
			number, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
			if err != nil || number <= 0 {
				http.Error(w, "invalid part number", http.StatusBadRequest)
				return
			}
			if got := r.URL.Query().Get("uploadId"); got != "upload-id" {
				http.Error(w, "invalid upload id", http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			mu.Lock()
			partData[number] = data
			mu.Unlock()
			w.Header().Set("ETag", fmt.Sprintf("etag-%d", number))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "upload-id":
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Contains(body, []byte("<PartNumber>1</PartNumber>")) {
				http.Error(w, "invalid completion body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			completed = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Location>test</Location><Bucket>bucket</Bucket><Key>object-key</Key><ETag>etag</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete:
			mu.Lock()
			abortCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected multipart request", http.StatusBadRequest)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	p := &s3Params{
		AccessKeyID:     "access-key",
		AccessKeySecret: "access-secret",
		Bucket:          "bucket",
		Endpoint:        "http://upload-test.mypikpak.net",
		Key:             "object-key",
		SecurityToken:   "security-token",
	}
	httpClient := &http.Client{Transport: &rewritingTransport{
		base:   http.DefaultTransport,
		target: server.Listener.Addr().String(),
	}}
	client, err := newPikPakOSSClient(p, oss.HTTPClient(httpClient))
	if err != nil {
		t.Fatalf("new OSS client: %v", err)
	}
	bucket, err := client.Bucket(p.Bucket)
	if err != nil {
		t.Fatalf("open OSS bucket: %v", err)
	}
	if err := uploadPreparedBodyToOSS(context.Background(), bucket, p, preparedBytes(payload), int64(len(payload))); err != nil {
		t.Fatalf("multipart upload through SDK: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !completed {
		t.Fatal("multipart upload was not completed")
	}
	if abortCalls != 0 {
		t.Fatalf("abort calls = %d, want 0", abortCalls)
	}
	var rebuilt []byte
	for number := 1; number <= len(partData); number++ {
		rebuilt = append(rebuilt, partData[number]...)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatalf("rebuilt payload length = %d, want %d", len(rebuilt), len(payload))
	}
}

func TestUploadMultipartRetriesOnlyFailedPart(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, int(pikpakMultipartThreshold+1))
	var (
		mu       sync.Mutex
		attempts = make(map[int]int)
		aborted  int
	)
	bucket := &fakePikPakOSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, reader io.Reader, _ int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			mu.Lock()
			attempts[number]++
			attempt := attempts[number]
			mu.Unlock()
			if number == 1 && attempt == 1 {
				return oss.UploadPart{}, &net.DNSError{Err: "temporary failure", Name: "oss.example"}
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return oss.UploadPart{}, err
			}
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, _ []oss.UploadPart, _ ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			return oss.CompleteMultipartUploadResult{}, nil
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			aborted++
			return nil
		},
	}

	p := &s3Params{Key: "object-key", SecurityToken: "token"}
	if err := uploadMultipartBodyToOSS(context.Background(), bucket, p, preparedBytes(payload), int64(len(payload))); err != nil {
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
	if aborted != 0 {
		t.Fatalf("abort calls = %d, want 0", aborted)
	}
}

func TestUploadMultipartAbortsOnPartFailure(t *testing.T) {
	payload := bytes.Repeat([]byte{0x4f}, int(pikpakMultipartThreshold+1))
	var abortCalls, completeCalls int
	bucket := &fakePikPakOSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, _ io.Reader, _ int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			if number == 1 {
				return oss.UploadPart{}, errors.New("permission denied")
			}
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
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

	p := &s3Params{Key: "object-key", SecurityToken: "token"}
	err := uploadMultipartBodyToOSS(context.Background(), bucket, p, preparedBytes(payload), int64(len(payload)))
	if err == nil || !strings.Contains(err.Error(), "upload part 1/") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want part failure", err)
	}
	if abortCalls != 1 {
		t.Fatalf("abort calls = %d, want 1", abortCalls)
	}
	if completeCalls != 0 {
		t.Fatalf("complete calls = %d, want 0", completeCalls)
	}
}

func TestUploadMultipartCancellationStopsWorkersAndAborts(t *testing.T) {
	payload := bytes.Repeat([]byte{0x43}, int(pikpakMultipartThreshold+1))
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	var (
		startOnce  sync.Once
		abortCalls int
	)
	bucket := &fakePikPakOSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, reader io.Reader, _ int64, _ int, _ ...oss.Option) (oss.UploadPart, error) {
			startOnce.Do(func() { close(started) })
			<-release
			_, err := reader.Read(make([]byte, 1))
			return oss.UploadPart{}, err
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		p := &s3Params{Key: "object-key", SecurityToken: "token"}
		result <- uploadMultipartBodyToOSS(ctx, bucket, p, preparedBytes(payload), int64(len(payload)))
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("multipart worker did not start")
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

func TestUploadMultipartTreatsEOFCompletionAsSuccess(t *testing.T) {
	payload := bytes.Repeat([]byte{0x45}, int(pikpakMultipartThreshold+1))
	var abortCalls int
	bucket := &fakePikPakOSSBucket{
		uploadPartFn: func(_ oss.InitiateMultipartUploadResult, reader io.Reader, _ int64, number int, _ ...oss.Option) (oss.UploadPart, error) {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return oss.UploadPart{}, err
			}
			return oss.UploadPart{PartNumber: number, ETag: fmt.Sprintf("etag-%d", number)}, nil
		},
		completeFn: func(_ oss.InitiateMultipartUploadResult, _ []oss.UploadPart, _ ...oss.Option) (oss.CompleteMultipartUploadResult, error) {
			return oss.CompleteMultipartUploadResult{}, io.EOF
		},
		abortFn: func(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
			abortCalls++
			return nil
		},
	}

	p := &s3Params{Key: "object-key", SecurityToken: "token"}
	if err := uploadMultipartBodyToOSS(context.Background(), bucket, p, preparedBytes(payload), int64(len(payload))); err != nil {
		t.Fatalf("multipart upload: %v", err)
	}
	if abortCalls != 0 {
		t.Fatalf("abort calls = %d, want 0", abortCalls)
	}
}
