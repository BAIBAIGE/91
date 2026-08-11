package guangyapan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type fakeGuangYaOSSBucket struct {
	mu sync.Mutex

	partCalls   map[int]int
	partData    map[int][][]byte
	partHook    func(partNumber, call int, data []byte) error
	putHook     func(call int) error
	putCalls    int
	aborts      int
	completes   int
	completeErr error
	completed   []oss.UploadPart
}

type scriptedGuangYaOSSBucket struct {
	*fakeGuangYaOSSBucket
	uploadPart func(reader io.Reader, partNumber int, options []oss.Option) (oss.UploadPart, error)
}

func (b *scriptedGuangYaOSSBucket) UploadPart(
	_ oss.InitiateMultipartUploadResult,
	reader io.Reader,
	_ int64,
	partNumber int,
	options ...oss.Option,
) (oss.UploadPart, error) {
	return b.uploadPart(reader, partNumber, options)
}

func guangYaUploadOptionContext(options []oss.Option) (context.Context, error) {
	value, err := oss.FindOption(options, "x-context-arg", nil)
	if err != nil {
		return nil, err
	}
	ctx, ok := value.(context.Context)
	if !ok || ctx == nil {
		return nil, fmt.Errorf("upload options contain context %T, want context.Context", value)
	}
	return ctx, nil
}

func newFakeGuangYaOSSBucket() *fakeGuangYaOSSBucket {
	return &fakeGuangYaOSSBucket{
		partCalls: make(map[int]int),
		partData:  make(map[int][][]byte),
	}
}

func (b *fakeGuangYaOSSBucket) PutObject(_ string, reader io.Reader, _ ...oss.Option) error {
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	b.mu.Lock()
	b.putCalls++
	call := b.putCalls
	hook := b.putHook
	b.mu.Unlock()
	if hook != nil {
		return hook(call)
	}
	return nil
}

func (b *fakeGuangYaOSSBucket) InitiateMultipartUpload(objectKey string, _ ...oss.Option) (oss.InitiateMultipartUploadResult, error) {
	return oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: objectKey, UploadID: "upload-1"}, nil
}

func (b *fakeGuangYaOSSBucket) UploadPart(
	_ oss.InitiateMultipartUploadResult,
	reader io.Reader,
	_ int64,
	partNumber int,
	_ ...oss.Option,
) (oss.UploadPart, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return oss.UploadPart{}, err
	}
	b.mu.Lock()
	b.partCalls[partNumber]++
	call := b.partCalls[partNumber]
	b.partData[partNumber] = append(b.partData[partNumber], append([]byte(nil), data...))
	hook := b.partHook
	b.mu.Unlock()
	if hook != nil {
		if err := hook(partNumber, call, data); err != nil {
			return oss.UploadPart{}, err
		}
	}
	return oss.UploadPart{PartNumber: partNumber, ETag: fmt.Sprintf("etag-%d", partNumber)}, nil
}

func (b *fakeGuangYaOSSBucket) CompleteMultipartUpload(
	_ oss.InitiateMultipartUploadResult,
	parts []oss.UploadPart,
	_ ...oss.Option,
) (oss.CompleteMultipartUploadResult, error) {
	b.mu.Lock()
	b.completes++
	b.completed = append([]oss.UploadPart(nil), parts...)
	err := b.completeErr
	b.mu.Unlock()
	return oss.CompleteMultipartUploadResult{}, err
}

func (b *fakeGuangYaOSSBucket) AbortMultipartUpload(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
	b.mu.Lock()
	b.aborts++
	b.mu.Unlock()
	return nil
}

func TestPlanGuangYaMultipartRespectsProtocolLimits(t *testing.T) {
	sizes := []int64{
		1,
		guangYaMultipartTargetPartSize,
		guangYaMultipartTargetPartSize + 1,
		40 * 1024 * 1024 * 1024,
		guangYaMaxMultipartObjectSize,
	}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			chunks, err := planGuangYaMultipart(size)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if len(chunks) == 0 || len(chunks) > guangYaMultipartMaxParts {
				t.Fatalf("part count = %d", len(chunks))
			}
			var offset int64
			for i, chunk := range chunks {
				if chunk.number != i+1 || chunk.offset != offset || chunk.size <= 0 || chunk.size > int64(oss.MaxPartSize) {
					t.Fatalf("invalid chunk %d: %#v, expected offset=%d", i, chunk, offset)
				}
				offset += chunk.size
			}
			if offset != size {
				t.Fatalf("planned bytes = %d, want %d", offset, size)
			}
		})
	}
	if _, err := planGuangYaMultipart(guangYaMaxMultipartObjectSize + 1); err == nil {
		t.Fatal("oversized object unexpectedly received a multipart plan")
	}
}

type concurrentGuangYaOSSBucket struct {
	mu sync.Mutex

	started        chan int
	release        chan struct{}
	releaseOnce    sync.Once
	active         int
	maxActive      int
	initSequential bool
	aborts         int
	completes      int
	completed      []oss.UploadPart
}

func (b *concurrentGuangYaOSSBucket) releaseWorkers() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func newConcurrentGuangYaOSSBucket() *concurrentGuangYaOSSBucket {
	return &concurrentGuangYaOSSBucket{
		started: make(chan int, guangYaMultipartConcurrency+1),
		release: make(chan struct{}),
	}
}

func (b *concurrentGuangYaOSSBucket) PutObject(_ string, _ io.Reader, _ ...oss.Option) error {
	return nil
}

func (b *concurrentGuangYaOSSBucket) InitiateMultipartUpload(objectKey string, options ...oss.Option) (oss.InitiateMultipartUploadResult, error) {
	params, err := oss.GetRawParams(options)
	if err != nil {
		return oss.InitiateMultipartUploadResult{}, err
	}
	_, sequential := params["sequential"]
	b.mu.Lock()
	b.initSequential = sequential
	b.mu.Unlock()
	return oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: objectKey, UploadID: "upload-concurrent"}, nil
}

func (b *concurrentGuangYaOSSBucket) UploadPart(
	_ oss.InitiateMultipartUploadResult,
	reader io.Reader,
	_ int64,
	partNumber int,
	_ ...oss.Option,
) (oss.UploadPart, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	b.mu.Unlock()
	b.started <- partNumber

	<-b.release
	_, err := io.Copy(io.Discard, reader)
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	if err != nil {
		return oss.UploadPart{}, err
	}
	return oss.UploadPart{PartNumber: partNumber, ETag: fmt.Sprintf("etag-%d", partNumber)}, nil
}

func (b *concurrentGuangYaOSSBucket) CompleteMultipartUpload(
	_ oss.InitiateMultipartUploadResult,
	parts []oss.UploadPart,
	_ ...oss.Option,
) (oss.CompleteMultipartUploadResult, error) {
	b.mu.Lock()
	b.completes++
	b.completed = append([]oss.UploadPart(nil), parts...)
	b.mu.Unlock()
	return oss.CompleteMultipartUploadResult{}, nil
}

func (b *concurrentGuangYaOSSBucket) AbortMultipartUpload(_ oss.InitiateMultipartUploadResult, _ ...oss.Option) error {
	b.mu.Lock()
	b.aborts++
	b.mu.Unlock()
	return nil
}

type zeroGuangYaReaderAt struct{}

func (zeroGuangYaReaderAt) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	return len(p), nil
}

func TestUploadGuangYaMultipartUsesSixWorkersAndCompletesInPartOrder(t *testing.T) {
	const partCount = guangYaMultipartConcurrency + 1
	size := int64(partCount) * guangYaMultipartTargetPartSize
	body := guangYaPreparedUploadBody{readerAt: zeroGuangYaReaderAt{}}
	bucket := newConcurrentGuangYaOSSBucket()
	defer bucket.releaseWorkers()

	result := make(chan error, 1)
	go func() {
		result <- uploadGuangYaMultipart(context.Background(), bucket, "object", body, size)
	}()

	startedParts := make(map[int]bool, guangYaMultipartConcurrency)
	for len(startedParts) < guangYaMultipartConcurrency {
		select {
		case number := <-bucket.started:
			startedParts[number] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d multipart workers started, want %d", len(startedParts), guangYaMultipartConcurrency)
		}
	}
	select {
	case number := <-bucket.started:
		t.Fatalf("part %d started before one of the six workers was released", number)
	case <-time.After(100 * time.Millisecond):
	}
	bucket.releaseWorkers()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("multipart upload: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("multipart upload did not finish after releasing workers")
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if bucket.maxActive != guangYaMultipartConcurrency {
		t.Fatalf("max concurrent parts = %d, want %d", bucket.maxActive, guangYaMultipartConcurrency)
	}
	if bucket.initSequential {
		t.Fatal("multipart upload unexpectedly opted into OSS sequential mode")
	}
	if bucket.completes != 1 || bucket.aborts != 0 {
		t.Fatalf("completes=%d aborts=%d, want 1/0", bucket.completes, bucket.aborts)
	}
	if len(bucket.completed) != partCount {
		t.Fatalf("completed parts = %d, want %d", len(bucket.completed), partCount)
	}
	for index, part := range bucket.completed {
		if part.PartNumber != index+1 {
			t.Fatalf("completed part %d number = %d, want %d", index, part.PartNumber, index+1)
		}
	}
}

func TestUploadGuangYaMultipartRetriesOnlyFailedPart(t *testing.T) {
	data := bytes.Repeat([]byte("a"), int(guangYaMultipartTargetPartSize+17))
	body := guangYaPreparedUploadBody{readerAt: bytes.NewReader(data)}
	bucket := newFakeGuangYaOSSBucket()
	bucket.partHook = func(partNumber, call int, _ []byte) error {
		if partNumber == 1 && call == 1 {
			return oss.ServiceError{StatusCode: 500, Code: "InternalError"}
		}
		return nil
	}

	if err := uploadGuangYaMultipart(context.Background(), bucket, "object", body, int64(len(data))); err != nil {
		t.Fatalf("multipart upload: %v", err)
	}
	if got := bucket.partCalls[1]; got != 2 {
		t.Fatalf("part 1 calls = %d, want 2", got)
	}
	if got := bucket.partCalls[2]; got != 1 {
		t.Fatalf("part 2 calls = %d, want 1", got)
	}
	if !bytes.Equal(bucket.partData[1][0], bucket.partData[1][1]) {
		t.Fatal("retried part was not replayed from the same byte range")
	}
	if bucket.completes != 1 || bucket.aborts != 0 {
		t.Fatalf("completes=%d aborts=%d, want 1/0", bucket.completes, bucket.aborts)
	}
}

func TestUploadGuangYaPartRetriesAfterNoProgressTimeout(t *testing.T) {
	const noProgressTimeout = 30 * time.Millisecond
	data := []byte("payload")
	body := guangYaPreparedUploadBody{readerAt: bytes.NewReader(data)}
	upload := oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: "object", UploadID: "upload-stall"}
	chunk := guangYaMultipartChunk{number: 1, size: int64(len(data))}
	calls := 0
	var replayed []byte
	bucket := &scriptedGuangYaOSSBucket{fakeGuangYaOSSBucket: newFakeGuangYaOSSBucket()}
	bucket.uploadPart = func(reader io.Reader, partNumber int, options []oss.Option) (oss.UploadPart, error) {
		calls++
		if calls == 1 {
			attemptCtx, err := guangYaUploadOptionContext(options)
			if err != nil {
				return oss.UploadPart{}, err
			}
			<-attemptCtx.Done()
			return oss.UploadPart{}, attemptCtx.Err()
		}
		var err error
		replayed, err = io.ReadAll(reader)
		if err != nil {
			return oss.UploadPart{}, err
		}
		return oss.UploadPart{PartNumber: partNumber, ETag: "etag-retry"}, nil
	}

	started := time.Now()
	part, err := uploadGuangYaPartWithNoProgressTimeout(
		context.Background(),
		bucket,
		upload,
		body,
		chunk,
		1,
		noProgressTimeout,
	)
	if err != nil {
		t.Fatalf("upload part: %v", err)
	}
	if calls != 2 {
		t.Fatalf("part calls = %d, want 2", calls)
	}
	if part.ETag != "etag-retry" || !bytes.Equal(replayed, data) {
		t.Fatalf("part=%#v replayed=%q, want retried payload", part, replayed)
	}
	if elapsed := time.Since(started); elapsed < noProgressTimeout || elapsed > 2*time.Second {
		t.Fatalf("retry elapsed = %s, want timeout followed by prompt retry", elapsed)
	}
}

func TestUploadGuangYaPartProgressRenewsNoProgressTimeout(t *testing.T) {
	const (
		noProgressTimeout = 100 * time.Millisecond
		readInterval      = 25 * time.Millisecond
	)
	data := []byte("progress")
	body := guangYaPreparedUploadBody{readerAt: bytes.NewReader(data)}
	upload := oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: "object", UploadID: "upload-progress"}
	chunk := guangYaMultipartChunk{number: 1, size: int64(len(data))}
	calls := 0
	bucket := &scriptedGuangYaOSSBucket{fakeGuangYaOSSBucket: newFakeGuangYaOSSBucket()}
	bucket.uploadPart = func(reader io.Reader, partNumber int, _ []oss.Option) (oss.UploadPart, error) {
		calls++
		buf := make([]byte, 1)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				time.Sleep(readInterval)
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return oss.UploadPart{}, err
			}
		}
		return oss.UploadPart{PartNumber: partNumber, ETag: "etag-progress"}, nil
	}

	started := time.Now()
	part, err := uploadGuangYaPartWithNoProgressTimeout(
		context.Background(),
		bucket,
		upload,
		body,
		chunk,
		1,
		noProgressTimeout,
	)
	if err != nil {
		t.Fatalf("upload part: %v", err)
	}
	if calls != 1 || part.ETag != "etag-progress" {
		t.Fatalf("calls=%d part=%#v, want one successful attempt", calls, part)
	}
	if elapsed := time.Since(started); elapsed <= noProgressTimeout {
		t.Fatalf("upload elapsed = %s, test did not outlive one idle interval", elapsed)
	}
}

func TestUploadGuangYaPartParentCancellationDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := []byte("payload")
	body := guangYaPreparedUploadBody{readerAt: bytes.NewReader(data)}
	upload := oss.InitiateMultipartUploadResult{Bucket: "bucket", Key: "object", UploadID: "upload-cancel"}
	chunk := guangYaMultipartChunk{number: 1, size: int64(len(data))}
	started := make(chan struct{})
	calls := 0
	bucket := &scriptedGuangYaOSSBucket{fakeGuangYaOSSBucket: newFakeGuangYaOSSBucket()}
	bucket.uploadPart = func(_ io.Reader, _ int, options []oss.Option) (oss.UploadPart, error) {
		calls++
		attemptCtx, err := guangYaUploadOptionContext(options)
		if err != nil {
			return oss.UploadPart{}, err
		}
		close(started)
		<-attemptCtx.Done()
		return oss.UploadPart{}, attemptCtx.Err()
	}

	result := make(chan error, 1)
	go func() {
		_, err := uploadGuangYaPartWithNoProgressTimeout(
			ctx,
			bucket,
			upload,
			body,
			chunk,
			1,
			time.Second,
		)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("part upload did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("part upload did not stop after parent cancellation")
	}
	if calls != 1 {
		t.Fatalf("part calls = %d, want no retry after parent cancellation", calls)
	}
}

func TestUploadGuangYaMultipartAbortsOnPermanentFailure(t *testing.T) {
	data := []byte("payload")
	reader := bytes.NewReader(data)
	body := guangYaPreparedUploadBody{readerAt: reader}
	bucket := newFakeGuangYaOSSBucket()
	bucket.partHook = func(_, _ int, _ []byte) error { return errors.New("access denied") }

	err := uploadGuangYaMultipart(context.Background(), bucket, "object", body, int64(len(data)))
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error = %v, want access denied", err)
	}
	if bucket.partCalls[1] != 1 || bucket.aborts != 1 || bucket.completes != 0 {
		t.Fatalf("calls=%d aborts=%d completes=%d", bucket.partCalls[1], bucket.aborts, bucket.completes)
	}
}

func TestUploadGuangYaMultipartAbortsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	data := []byte("payload")
	reader := bytes.NewReader(data)
	body := guangYaPreparedUploadBody{readerAt: reader}
	bucket := newFakeGuangYaOSSBucket()
	bucket.partHook = func(_, _ int, _ []byte) error {
		cancel()
		return ctx.Err()
	}

	err := uploadGuangYaMultipart(ctx, bucket, "object", body, int64(len(data)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if bucket.aborts != 1 || bucket.completes != 0 {
		t.Fatalf("aborts=%d completes=%d, want 1/0", bucket.aborts, bucket.completes)
	}
}

func TestUploadGuangYaMultipartAbortsWhenCompleteFails(t *testing.T) {
	data := []byte("payload")
	reader := bytes.NewReader(data)
	body := guangYaPreparedUploadBody{readerAt: reader}
	bucket := newFakeGuangYaOSSBucket()
	bucket.completeErr = errors.New("complete response lost")

	err := uploadGuangYaMultipart(context.Background(), bucket, "object", body, int64(len(data)))
	if err == nil || !strings.Contains(err.Error(), "complete response lost") {
		t.Fatalf("error = %v, want complete failure", err)
	}
	if bucket.completes != 1 || bucket.aborts != 1 {
		t.Fatalf("completes=%d aborts=%d, want 1/1", bucket.completes, bucket.aborts)
	}
}

func TestPrepareUploadBodyStagesReaderAndCleansUp(t *testing.T) {
	tempDir := t.TempDir()
	d := New(Config{UploadTempDir: tempDir})
	source := struct{ io.Reader }{Reader: bytes.NewBufferString("payload")}
	body, err := d.prepareUploadBody(context.Background(), source, 7)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	staged, ok := body.readerAt.(*os.File)
	if !ok {
		t.Fatalf("reader = %T, want staged file", body.readerAt)
	}
	if filepath.Dir(staged.Name()) != tempDir {
		t.Fatalf("staged path = %q, want under %q", staged.Name(), tempDir)
	}
	buf := make([]byte, 7)
	if _, err := body.readerAt.ReadAt(buf, 0); err != nil {
		t.Fatalf("read staged body: %v", err)
	}
	if string(buf) != "payload" {
		t.Fatalf("staged body = %q", buf)
	}
	body.cleanup()
	if _, err := os.Stat(staged.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary upload body still exists: %v", err)
	}

	short := struct{ io.Reader }{Reader: bytes.NewBufferString("short")}
	if _, err := d.prepareUploadBody(context.Background(), short, 6); err == nil {
		t.Fatal("short source unexpectedly passed size validation")
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files leaked after error: %#v", entries)
	}
}
