package pikpak

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

const (
	// PikPak/OpenList both use a normal PutObject for very small objects. Larger
	// files use multipart so a high-latency or lossy route is not constrained by
	// one TCP congestion window.
	pikpakMultipartThreshold    int64 = 10 * 1024 * 1024
	pikpakMultipartMinPartSize  int64 = 1 * 1024 * 1024
	pikpakMultipartConcurrency        = 10
	pikpakMultipartPartAttempts       = 3
	pikpakMultipartAbortTimeout       = 30 * time.Second

	// Keep at most 1000 parts, matching the proven PikPak/OpenList strategy and
	// staying well below OSS's 10000-part protocol limit.
	pikpakMultipartMaxParts       = 1000
	pikpakMultipartBandSize int64 = 1024 * 1024 * 1024

	maxPikPakMultipartObjectSize int64 = int64(oss.MaxPartSize) * pikpakMultipartMaxParts
)

// pikpakOSSBucket is the small subset of the Aliyun OSS SDK needed by PikPak
// uploads. Keeping the protocol orchestration behind this interface lets us
// exercise cancellation, retry and cleanup behavior without a live account.
type pikpakOSSBucket interface {
	PutObject(objectKey string, reader io.Reader, options ...oss.Option) error
	InitiateMultipartUpload(objectKey string, options ...oss.Option) (oss.InitiateMultipartUploadResult, error)
	UploadPart(imur oss.InitiateMultipartUploadResult, reader io.Reader, partSize int64, partNumber int, options ...oss.Option) (oss.UploadPart, error)
	CompleteMultipartUpload(imur oss.InitiateMultipartUploadResult, parts []oss.UploadPart, options ...oss.Option) (oss.CompleteMultipartUploadResult, error)
	AbortMultipartUpload(imur oss.InitiateMultipartUploadResult, options ...oss.Option) error
}

type pikpakMultipartChunk struct {
	index  int
	number int
	offset int64
	size   int64
}

type pikpakMultipartResult struct {
	index int
	part  oss.UploadPart
	err   error
}

func validatePikPakUploadSize(size int64) error {
	if size < 0 {
		return fmt.Errorf("pikpak upload: invalid size %d", size)
	}
	if size > maxPikPakMultipartObjectSize {
		return fmt.Errorf("pikpak upload: file size %d exceeds multipart limit %d", size, maxPikPakMultipartObjectSize)
	}
	return nil
}

// planPikPakMultipart follows OpenList's useful sizing properties without its
// exact-boundary bugs: roughly 100 parts per GiB, at least 1 MiB per part and
// no more than 1000 parts. The returned offsets are relative to the upload
// body's starting position.
func planPikPakMultipart(size int64) ([]pikpakMultipartChunk, error) {
	if size <= 0 {
		return nil, fmt.Errorf("pikpak multipart: invalid size %d", size)
	}
	if err := validatePikPakUploadSize(size); err != nil {
		return nil, err
	}

	bands := (size + pikpakMultipartBandSize - 1) / pikpakMultipartBandSize
	targetParts := bands * 100
	if targetParts < 100 {
		targetParts = 100
	}
	if targetParts > pikpakMultipartMaxParts {
		targetParts = pikpakMultipartMaxParts
	}

	partSize := (size + targetParts - 1) / targetParts
	if partSize < pikpakMultipartMinPartSize {
		partSize = pikpakMultipartMinPartSize
	}
	if partSize > int64(oss.MaxPartSize) {
		return nil, fmt.Errorf("pikpak multipart: part size %d exceeds OSS limit %d", partSize, oss.MaxPartSize)
	}

	partCount := (size + partSize - 1) / partSize
	if partCount > pikpakMultipartMaxParts {
		return nil, fmt.Errorf("pikpak multipart: part count %d exceeds limit %d", partCount, pikpakMultipartMaxParts)
	}

	chunks := make([]pikpakMultipartChunk, 0, int(partCount))
	for offset, number := int64(0), 1; offset < size; offset, number = offset+partSize, number+1 {
		chunkSize := partSize
		if remaining := size - offset; remaining < chunkSize {
			chunkSize = remaining
		}
		chunks = append(chunks, pikpakMultipartChunk{
			index:  len(chunks),
			number: number,
			offset: offset,
			size:   chunkSize,
		})
	}
	return chunks, nil
}

func pikpakOSSOptions(ctx context.Context, p *s3Params) []oss.Option {
	return []oss.Option{
		oss.SetHeader(ossSecurityTokenHeaderName, p.SecurityToken),
		oss.UserAgentHeader(ossUserAgent),
		oss.WithContext(ctx),
	}
}

func uploadPreparedBodyToOSS(ctx context.Context, bucket pikpakOSSBucket, p *s3Params, body preparedUploadBody, size int64) error {
	if size <= pikpakMultipartThreshold {
		if err := body.rewind(); err != nil {
			return fmt.Errorf("rewind body: %w", err)
		}
		return bucket.PutObject(p.Key, &readerWithCtx{ctx: ctx, r: body.reader}, pikpakOSSOptions(ctx, p)...)
	}
	return uploadMultipartBodyToOSS(ctx, bucket, p, body, size)
}

func uploadMultipartBodyToOSS(ctx context.Context, bucket pikpakOSSBucket, p *s3Params, body preparedUploadBody, size int64) (retErr error) {
	chunks, err := planPikPakMultipart(size)
	if err != nil {
		return err
	}
	if body.readerAt == nil {
		return errors.New("pikpak multipart: upload body does not support random access")
	}

	imur, err := bucket.InitiateMultipartUpload(p.Key, pikpakOSSOptions(ctx, p)...)
	if err != nil {
		return fmt.Errorf("initiate multipart: %w", err)
	}

	completed := false
	defer func() {
		if completed {
			return
		}
		abortCtx, abortCancel := context.WithTimeout(context.WithoutCancel(ctx), pikpakMultipartAbortTimeout)
		defer abortCancel()
		if abortErr := bucket.AbortMultipartUpload(imur, pikpakOSSOptions(abortCtx, p)...); abortErr != nil {
			abortErr = fmt.Errorf("abort multipart: %w", abortErr)
			if retErr == nil {
				retErr = abortErr
			} else {
				retErr = errors.Join(retErr, abortErr)
			}
		}
	}()

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan pikpakMultipartChunk, len(chunks))
	results := make(chan pikpakMultipartResult, len(chunks))
	for _, chunk := range chunks {
		jobs <- chunk
	}
	close(jobs)

	workerCount := min(pikpakMultipartConcurrency, len(chunks))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case chunk, ok := <-jobs:
					if !ok {
						return
					}
					part, uploadErr := uploadPikPakPart(workerCtx, bucket, imur, p, body, chunk, len(chunks))
					results <- pikpakMultipartResult{index: chunk.index, part: part, err: uploadErr}
					if uploadErr != nil {
						return
					}
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	uploadedParts := make([]oss.UploadPart, len(chunks))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		uploadedParts[result.index] = result.part
	}
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := bucket.CompleteMultipartUpload(imur, uploadedParts, pikpakOSSOptions(ctx, p)...); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("complete multipart: %w", err)
	}
	completed = true
	return nil
}

func uploadPikPakPart(
	ctx context.Context,
	bucket pikpakOSSBucket,
	imur oss.InitiateMultipartUploadResult,
	p *s3Params,
	body preparedUploadBody,
	chunk pikpakMultipartChunk,
	totalParts int,
) (oss.UploadPart, error) {
	var lastErr error
	attemptsMade := 0
	for attempt := 1; attempt <= pikpakMultipartPartAttempts; attempt++ {
		attemptsMade = attempt
		if err := ctx.Err(); err != nil {
			return oss.UploadPart{}, err
		}

		section := io.NewSectionReader(body.readerAt, body.start+chunk.offset, chunk.size)
		part, err := bucket.UploadPart(
			imur,
			&readerWithCtx{ctx: ctx, r: section},
			chunk.size,
			chunk.number,
			pikpakOSSOptions(ctx, p)...,
		)
		if err == nil {
			return part, nil
		}
		lastErr = err
		if attempt >= pikpakMultipartPartAttempts || !isRetryablePikPakUploadError(err) {
			break
		}
		if err := pikpakSleepContext(ctx, pikpakUploadRetryDelay(attempt)); err != nil {
			return oss.UploadPart{}, err
		}
	}
	return oss.UploadPart{}, fmt.Errorf(
		"upload part %d/%d after %d attempt(s): %w",
		chunk.number,
		totalParts,
		attemptsMade,
		lastErr,
	)
}
