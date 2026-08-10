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
