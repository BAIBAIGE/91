package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	uploadStoragePartV1  = "part-v1"
	uploadStoragePartV2  = "part-v2"
	uploadStorageRangeV2 = "range-v2"
	minUploadRangeSize   = int64(16 << 20)
	maxUploadRangeSize   = int64(1 << 30)
)

type uploadSessionLock struct {
	mu   sync.Mutex
	refs int
}

type storedUploadSession struct {
	ID            string              `json:"id"`
	FileName      string              `json:"fileName"`
	Size          int64               `json:"size"`
	SHA256        string              `json:"sha256,omitempty"`
	ChunkSize     int64               `json:"chunkSize"`
	TotalChunks   int                 `json:"totalChunks"`
	Received      map[int]UploadChunk `json:"received"`
	State         string              `json:"state"`
	StorageFormat string              `json:"storageFormat,omitempty"`
	CreatedAt     time.Time           `json:"createdAt"`
	ExpiresAt     time.Time           `json:"expiresAt"`
}

func (m *Manager) BeginUpload(ctx context.Context, input BeginUploadInput) (UploadSession, error) {
	// Browser uploads declare their authoritative whole-file hash only when all
	// durability checkpoints are present. SHA256 on BeginUploadInput is reserved
	// for BeginRangeUpload's trusted server-transfer contract.
	input.SHA256 = ""
	return m.beginUpload(ctx, input, ChunkSize, uploadStoragePartV2)
}

// BeginRangeUpload creates the preallocated staging file used by trusted
// server-to-server transports. Ranges are durability checkpoints, not digest
// units: callers may write them concurrently and FinalizeUpload performs the
// one authoritative full-archive SHA-256 verification.
func (m *Manager) BeginRangeUpload(
	ctx context.Context,
	input BeginUploadInput,
	rangeSize int64,
) (UploadSession, error) {
	if rangeSize < minUploadRangeSize || rangeSize > maxUploadRangeSize || rangeSize%(1<<20) != 0 {
		return UploadSession{}, errors.New("备份传输区间大小无效")
	}
	if strings.TrimSpace(input.SHA256) == "" {
		return UploadSession{}, errors.New("服务器直传必须提供完整备份 SHA-256")
	}
	return m.beginUpload(ctx, input, rangeSize, uploadStorageRangeV2)
}

func (m *Manager) beginUpload(
	ctx context.Context,
	input BeginUploadInput,
	chunkSize int64,
	storageFormat string,
) (UploadSession, error) {
	if err := m.cleanupExpiredUploads(); err != nil {
		return UploadSession{}, err
	}
	input.FileName = filepath.Base(strings.TrimSpace(input.FileName))
	if input.FileName == "" || input.FileName == "." ||
		!strings.EqualFold(filepath.Ext(input.FileName), ".zip") {
		return UploadSession{}, errors.New("请选择 ZIP 备份文件")
	}
	if input.Size <= 0 || input.Size > maxExpandedBytes {
		return UploadSession{}, errors.New("备份文件大小无效")
	}
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	if input.SHA256 != "" && !validSHA256(input.SHA256) {
		return UploadSession{}, errors.New("备份文件 SHA-256 无效")
	}
	available, err := m.availableBytes(m.dataRoot)
	if err != nil {
		return UploadSession{}, err
	}
	required := requiredUploadBytes(input.Size)
	if available < required {
		return UploadSession{}, fmt.Errorf("%w：上传暂存需要至少 %d 字节，可用 %d 字节", ErrInsufficientSpace, required, available)
	}
	if err := ctx.Err(); err != nil {
		return UploadSession{}, err
	}
	id, err := randomID()
	if err != nil {
		return UploadSession{}, err
	}
	created := m.nowTime()
	totalChunks64 := (input.Size + chunkSize - 1) / chunkSize
	if totalChunks64 <= 0 || totalChunks64 > int64(^uint(0)>>1) {
		return UploadSession{}, errors.New("备份分片数量无效")
	}
	stored := storedUploadSession{
		ID:            id,
		FileName:      input.FileName,
		Size:          input.Size,
		SHA256:        input.SHA256,
		ChunkSize:     chunkSize,
		TotalChunks:   int(totalChunks64),
		Received:      make(map[int]UploadChunk),
		State:         "uploading",
		StorageFormat: storageFormat,
		CreatedAt:     created,
		ExpiresAt:     created.Add(UploadTTL),
	}
	unlock := m.lockUpload(id)
	defer unlock()
	dir := m.uploadDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return UploadSession{}, err
	}
	part, err := os.OpenFile(m.uploadPartPath(id), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return UploadSession{}, err
	}
	truncateErr := part.Truncate(input.Size)
	syncErr := part.Sync()
	closeErr := part.Close()
	if truncateErr != nil || syncErr != nil || closeErr != nil {
		_ = os.RemoveAll(dir)
		for _, candidate := range []error{truncateErr, syncErr, closeErr} {
			if candidate != nil {
				return UploadSession{}, candidate
			}
		}
	}
	if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return UploadSession{}, err
	}
	return publicUploadSession(stored), nil
}

func requiredUploadBytes(size int64) int64 {
	if size > (1<<62)-diskSafetyReserve {
		return 1<<62 - 1
	}
	return size + diskSafetyReserve
}

func (m *Manager) UploadStatus(id string) (UploadSession, error) {
	stored, err := m.loadUpload(id)
	if err != nil {
		return UploadSession{}, err
	}
	if !m.nowTime().Before(stored.ExpiresAt) {
		_ = m.CancelUpload(id)
		return UploadSession{}, ErrUploadNotFound
	}
	session := publicUploadSession(stored)
	session.Progress = m.uploadProgressSnapshot(id)
	return session, nil
}

func (m *Manager) PutChunk(
	ctx context.Context,
	id string,
	index int,
	body io.Reader,
) (UploadSession, error) {
	session, _, err := m.putUploadChunk(ctx, id, index, body, nil, false)
	return session, err
}

// PutRange streams one server-transfer range directly into its final offset.
// It deliberately does not buffer or hash the range. A successful return means
// the bytes were synced before the durable range checkpoint was published.
// The boolean result reports whether this request created a new checkpoint.
func (m *Manager) PutRange(
	ctx context.Context,
	id string,
	index int,
	body io.Reader,
	onProgress func(int64),
) (UploadSession, bool, error) {
	return m.putUploadChunk(ctx, id, index, body, onProgress, true)
}

func (m *Manager) putUploadChunk(
	ctx context.Context,
	id string,
	index int,
	body io.Reader,
	onProgress func(int64),
	serverRange bool,
) (UploadSession, bool, error) {
	if !validUploadID(id) {
		return UploadSession{}, false, ErrUploadNotFound
	}

	unlock := m.lockUpload(id)
	stored, err := m.loadUpload(id)
	if err != nil {
		unlock()
		return UploadSession{}, false, err
	}
	if serverRange && stored.StorageFormat != uploadStorageRangeV2 {
		unlock()
		return UploadSession{}, false, errors.New("该上传会话不支持服务器区间直传")
	}
	if !serverRange && stored.StorageFormat == uploadStorageRangeV2 {
		unlock()
		return UploadSession{}, false, errors.New("服务器直传会话不能使用浏览器分片接口")
	}
	if stored.State != "uploading" {
		unlock()
		return UploadSession{}, false, fmt.Errorf("%w，暂不能写入数据", ErrUploadFinalizing)
	}
	if !m.nowTime().Before(stored.ExpiresAt) {
		unlock()
		_ = m.CancelUpload(id)
		return UploadSession{}, false, ErrUploadNotFound
	}
	if err := m.ensureAssembledUpload(ctx, id, &stored); err != nil {
		unlock()
		return UploadSession{}, false, err
	}
	if !serverRange && stored.StorageFormat == uploadStoragePartV1 {
		stored.StorageFormat = uploadStoragePartV2
		if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
			unlock()
			return UploadSession{}, false, err
		}
	}
	expectedSize, ok := uploadChunkSize(stored, index)
	if !ok {
		unlock()
		return UploadSession{}, false, errors.New("上传分片序号无效")
	}
	if _, committed := stored.Received[index]; committed {
		stored.ExpiresAt = m.nowTime().Add(UploadTTL)
		if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
			unlock()
			return UploadSession{}, false, err
		}
		result := publicUploadSession(stored)
		unlock()
		return result, false, nil
	}

	m.mu.Lock()
	if m.uploadBusy[id] || m.uploadCanceling[id] {
		m.mu.Unlock()
		unlock()
		return UploadSession{}, false, ErrUploadNotFound
	}
	writers := m.uploadWriters[id]
	if writers == nil {
		writers = make(map[int]context.CancelFunc)
		m.uploadWriters[id] = writers
	}
	if _, active := writers[index]; active {
		m.mu.Unlock()
		unlock()
		return UploadSession{}, false, ErrUploadRangeBusy
	}
	writeCtx, cancelContext := context.WithCancel(ctx)
	var cancelOnce sync.Once
	cancelWrite := context.CancelFunc(func() {
		cancelOnce.Do(func() {
			cancelContext()
			if closer, ok := body.(io.Closer); ok {
				_ = closer.Close()
			}
		})
	})
	writers[index] = cancelWrite
	m.mu.Unlock()
	unlock()
	defer cancelContext()
	defer m.finishRangeWrite(id, index)

	part, err := os.OpenFile(m.uploadPartPath(id), os.O_WRONLY, 0o600)
	if err != nil {
		return UploadSession{}, false, err
	}
	offset := int64(index) * stored.ChunkSize
	writer := io.NewOffsetWriter(part, offset)
	written, copyErr := copyWithContext(
		writeCtx,
		writer,
		io.LimitReader(body, expectedSize),
		onProgress,
	)
	if copyErr == nil && written == expectedSize {
		var extra [1]byte
		extraBytes, extraErr := io.ReadFull(body, extra[:])
		if extraBytes != 0 {
			copyErr = fmt.Errorf("上传分片大小不匹配：收到超过 %d 字节", expectedSize)
		} else if extraErr != nil && !errors.Is(extraErr, io.EOF) {
			copyErr = extraErr
		}
	}
	if copyErr == nil && written != expectedSize {
		copyErr = fmt.Errorf("上传分片大小不匹配：收到 %d 字节，应为 %d 字节", written, expectedSize)
	}
	if copyErr == nil {
		copyErr = writeCtx.Err()
	}
	if copyErr == nil {
		copyErr = part.Sync()
	}
	closeErr := part.Close()
	if copyErr != nil {
		return UploadSession{}, false, copyErr
	}
	if closeErr != nil {
		return UploadSession{}, false, closeErr
	}

	unlock = m.lockUpload(id)
	defer unlock()
	m.mu.Lock()
	canceling := m.uploadCanceling[id]
	m.mu.Unlock()
	if canceling {
		return UploadSession{}, false, ErrUploadNotFound
	}
	latest, err := m.loadUpload(id)
	if err != nil {
		return UploadSession{}, false, err
	}
	validStorage := latest.StorageFormat == uploadStorageRangeV2
	if !serverRange {
		validStorage = latest.StorageFormat == uploadStoragePartV2
	}
	if !validStorage || latest.State != "uploading" {
		return UploadSession{}, false, ErrUploadFinalizing
	}
	if existing, committed := latest.Received[index]; committed {
		if existing.Size != expectedSize {
			return UploadSession{}, false, errors.New("上传分片断点不一致")
		}
		return publicUploadSession(latest), false, nil
	}
	latest.Received[index] = UploadChunk{Index: index, Size: expectedSize}
	latest.ExpiresAt = m.nowTime().Add(UploadTTL)
	if err := writeJSONAtomic(m.uploadSidecar(id), latest, 0o600); err != nil {
		return UploadSession{}, false, err
	}
	return publicUploadSession(latest), true, nil
}

func (m *Manager) finishRangeWrite(id string, index int) {
	m.mu.Lock()
	writers := m.uploadWriters[id]
	delete(writers, index)
	cleanup := false
	if len(writers) == 0 {
		delete(m.uploadWriters, id)
		cleanup = m.uploadCanceling[id]
	}
	m.mu.Unlock()
	if cleanup {
		_ = m.finishCanceledUpload(id)
	}
}

// FindImportedUpload resolves the durable receipt for an upload whose final
// HTTP response may have been lost after the archive was atomically published.
// It is intentionally keyed by the opaque upload ID embedded in archive meta.
func (m *Manager) FindImportedUpload(ctx context.Context, id string) (BackupRecord, bool, error) {
	if !validUploadID(id) {
		return BackupRecord{}, false, ErrUploadNotFound
	}
	return m.recoverFinalizedUpload(ctx, id)
}

func (m *Manager) ensureAssembledUpload(
	ctx context.Context,
	id string,
	stored *storedUploadSession,
) error {
	partPath := m.uploadPartPath(id)
	switch stored.StorageFormat {
	case uploadStoragePartV1, uploadStoragePartV2, uploadStorageRangeV2:
		info, err := os.Stat(partPath)
		if err != nil {
			if os.IsNotExist(err) {
				return errors.New("迁移上传暂存文件缺失")
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("迁移上传暂存文件大小异常")
		}
		if info.Size() != stored.Size {
			return m.resetAssembledUpload(stored)
		}
		return nil
	case "":
		// Legacy sessions stored one file per received chunk.
		// Convert them once, keeping the old chunks authoritative until the new
		// part file and sidecar have both been published.
	default:
		return errors.New("迁移上传存储格式不受支持")
	}

	migrationPath := partPath + ".migrating"
	_ = os.Remove(migrationPath)
	output, err := os.OpenFile(migrationPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		_ = output.Close()
		if !completed {
			_ = os.Remove(migrationPath)
		}
	}()
	if err := output.Truncate(stored.Size); err != nil {
		return err
	}
	indexes := make([]int, 0, len(stored.Received))
	for index := range stored.Received {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		expected := stored.Received[index]
		chunk, err := os.Open(m.uploadChunkPath(id, index))
		if err != nil {
			if os.IsNotExist(err) {
				return m.invalidateLegacyChunk(stored, index, "分片文件缺失")
			}
			return err
		}
		if _, err := output.Seek(int64(index)*stored.ChunkSize, io.SeekStart); err != nil {
			_ = chunk.Close()
			return err
		}
		written, copyErr := copyWithContext(ctx, output, chunk, nil)
		closeErr := chunk.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != expected.Size {
			return m.invalidateLegacyChunk(stored, index, "大小不匹配")
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Remove(partPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(migrationPath, partPath); err != nil {
		return err
	}
	oldFormat := stored.StorageFormat
	stored.StorageFormat = uploadStoragePartV2
	if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
		stored.StorageFormat = oldFormat
		return err
	}
	completed = true
	m.removeLegacyChunkFiles(id)
	return nil
}

func (m *Manager) invalidateLegacyChunk(stored *storedUploadSession, index int, reason string) error {
	delete(stored.Received, index)
	stored.State = "uploading"
	if err := writeJSONAtomic(m.uploadSidecar(stored.ID), stored, 0o600); err != nil {
		return fmt.Errorf("迁移上传分片 %d %s，且更新续传状态失败: %w", index, reason, err)
	}
	return fmt.Errorf("迁移上传分片 %d %s，请重新上传", index, reason)
}

func (m *Manager) removeLegacyChunkFiles(id string) {
	entries, err := os.ReadDir(m.uploadDir(id))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".chunk") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".chunk")
		if len(base) != 8 {
			continue
		}
		valid := true
		for _, character := range base {
			if character < '0' || character > '9' {
				valid = false
				break
			}
		}
		if valid {
			_ = os.Remove(filepath.Join(m.uploadDir(id), entry.Name()))
		}
	}
}

func (m *Manager) verifyAssembledUpload(
	ctx context.Context,
	id string,
	stored *storedUploadSession,
	onProgress func(int64),
) (string, error) {
	partPath := m.uploadPartPath(id)
	part, err := os.Open(partPath)
	if err != nil {
		return "", err
	}
	info, err := part.Stat()
	if err != nil {
		_ = part.Close()
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() != stored.Size {
		_ = part.Close()
		return "", m.resetAssembledUpload(stored)
	}
	defer part.Close()
	fullHash := sha256.New()
	written, err := copyWithContext(ctx, fullHash, part, onProgress)
	if err != nil {
		return "", err
	}
	if written != stored.Size {
		return "", m.resetAssembledUpload(stored)
	}
	return hex.EncodeToString(fullHash.Sum(nil)), nil
}

func (m *Manager) resetAssembledUpload(stored *storedUploadSession) error {
	part, err := os.OpenFile(m.uploadPartPath(stored.ID), os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	truncateErr := part.Truncate(stored.Size)
	syncErr := part.Sync()
	closeErr := part.Close()
	for _, candidate := range []error{truncateErr, syncErr, closeErr} {
		if candidate != nil {
			return candidate
		}
	}
	stored.Received = make(map[int]UploadChunk)
	stored.State = "uploading"
	if err := writeJSONAtomic(m.uploadSidecar(stored.ID), stored, 0o600); err != nil {
		return err
	}
	return errors.New("迁移上传暂存文件大小异常，请重新上传")
}

func (m *Manager) FinalizeUpload(
	ctx context.Context,
	id string,
	expectedSHA256 string,
) (record BackupRecord, returnErr error) {
	if !validUploadID(id) {
		return BackupRecord{}, ErrUploadNotFound
	}
	unlock := m.lockUpload(id)
	defer unlock()
	m.mu.Lock()
	if m.uploadBusy[id] || m.uploadCanceling[id] || len(m.uploadWriters[id]) > 0 {
		m.mu.Unlock()
		return BackupRecord{}, ErrUploadFinalizing
	}
	m.uploadBusy[id] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.uploadBusy, id)
		m.mu.Unlock()
		if returnErr != nil {
			latest, err := m.loadUpload(id)
			if err == nil {
				latest.State = "uploading"
				_ = writeJSONAtomic(m.uploadSidecar(id), latest, 0o600)
			}
		}
	}()
	stored, err := m.loadUpload(id)
	if err != nil {
		return BackupRecord{}, err
	}
	m.setUploadProgress(id, OperationProgress{
		Phase:      "preparing",
		TotalBytes: stored.Size,
		TotalFiles: stored.TotalChunks,
	})
	defer m.clearUploadProgress(id)
	if len(stored.Received) != stored.TotalChunks {
		return BackupRecord{}, ErrUploadIncomplete
	}
	for index := 0; index < stored.TotalChunks; index++ {
		if _, ok := stored.Received[index]; !ok {
			return BackupRecord{}, ErrUploadIncomplete
		}
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if !validSHA256(expectedSHA256) {
		return BackupRecord{}, errors.New("必须提供完整备份包的 SHA-256")
	}
	if stored.SHA256 != "" && !strings.EqualFold(stored.SHA256, expectedSHA256) {
		return BackupRecord{}, errors.New("完整备份包 SHA-256 与上传会话不一致")
	}
	stored.SHA256 = expectedSHA256
	if stored.StorageFormat == uploadStoragePartV1 {
		stored.StorageFormat = uploadStoragePartV2
	}
	stored.State = "finalizing"
	if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
		return BackupRecord{}, err
	}
	if recovered, ok, err := m.recoverFinalizedUpload(ctx, id); err != nil {
		return BackupRecord{}, err
	} else if ok {
		if !strings.EqualFold(recovered.SHA256, expectedSHA256) {
			return BackupRecord{}, errors.New("已入库备份的 SHA-256 与上传文件不一致")
		}
		m.setUploadProgress(id, OperationProgress{
			Phase:          "publishing",
			ProcessedBytes: stored.Size,
			TotalBytes:     stored.Size,
			ProcessedFiles: stored.TotalChunks,
			TotalFiles:     stored.TotalChunks,
		})
		_ = os.RemoveAll(m.uploadDir(id))
		return recovered, nil
	}
	if err := m.ensureAssembledUpload(ctx, id, &stored); err != nil {
		return BackupRecord{}, err
	}
	m.setUploadProgress(id, OperationProgress{
		Phase:      "hashing",
		TotalBytes: stored.Size,
		TotalFiles: stored.TotalChunks,
	})
	archiveHash, err := m.verifyAssembledUpload(ctx, id, &stored, func(processed int64) {
		m.setUploadProgress(id, OperationProgress{
			Phase:          "hashing",
			ProcessedBytes: processed,
			TotalBytes:     stored.Size,
			TotalFiles:     stored.TotalChunks,
		})
	})
	if err != nil {
		return BackupRecord{}, err
	}
	if !strings.EqualFold(stored.SHA256, archiveHash) {
		return BackupRecord{}, m.invalidateUploadCheckpoints(
			&stored,
			"完整备份包 SHA-256 校验失败，已清空断点，请重新上传",
		)
	}

	name := fmt.Sprintf(
		backupNamePrefix+"imported-%s-%s.zip",
		m.nowTime().Local().Format("20060102-150405"),
		id,
	)
	finalPath := filepath.Join(m.backupDir, name)
	partPath := m.uploadPartPath(id)
	available, err := m.availableBytes(m.dataRoot)
	if err != nil {
		return BackupRecord{}, err
	}
	m.setUploadProgress(id, OperationProgress{
		Phase:          "inspecting-archive",
		ProcessedBytes: stored.Size,
		TotalBytes:     stored.Size,
		ProcessedFiles: stored.TotalChunks,
		TotalFiles:     stored.TotalChunks,
	})
	report, err := VerifyArchive(ctx, partPath, VerifyOptions{
		CurrentVersion: m.appVersion,
		TempDir:        m.restoreDir,
		AvailableBytes: available,
		Progress: func(progress OperationProgress) {
			switch progress.Phase {
			case "database":
				progress.Phase = "verifying-database"
			default:
				progress.Phase = "verifying-archive"
			}
			m.setUploadProgress(id, progress)
		},
	})
	if err != nil {
		return BackupRecord{}, err
	}
	m.setUploadProgress(id, OperationProgress{
		Phase:          "publishing",
		ProcessedBytes: report.Manifest.TotalSize,
		TotalBytes:     report.Manifest.TotalSize,
		ProcessedFiles: report.Manifest.FileCount,
		TotalFiles:     report.Manifest.FileCount,
	})
	if err := os.Rename(partPath, finalPath); err != nil {
		return BackupRecord{}, err
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		_ = os.Rename(finalPath, partPath)
		return BackupRecord{}, err
	}
	meta := archiveMeta{
		ID:         strings.TrimSuffix(name, ".zip"),
		Name:       name,
		Size:       info.Size(),
		SHA256:     archiveHash,
		ModifiedAt: info.ModTime().UTC(),
		VerifiedAt: m.nowTime(),
		Imported:   true,
		UploadID:   id,
		Manifest:   report.Manifest,
	}
	if err := writeJSONAtomic(metaPath(finalPath), meta, 0o600); err != nil {
		_ = os.Rename(finalPath, partPath)
		return BackupRecord{}, err
	}
	// The completed archive is already durable and verified. A sidecar cleanup
	// failure must not turn success into an ambiguous retry that could publish
	// the same upload twice; the hourly expiry sweep will retry cleanup.
	_ = os.RemoveAll(m.uploadDir(id))
	record = BackupRecord{
		ID:                 meta.ID,
		Name:               name,
		Size:               info.Size(),
		SHA256:             archiveHash,
		CreatedAt:          archiveTimestamp(report.Manifest, info.ModTime()),
		VerificationStatus: "verified",
		Imported:           true,
	}
	applyManifestToRecord(&record, report.Manifest, info.ModTime())
	return record, nil
}

func (m *Manager) invalidateUploadCheckpoints(stored *storedUploadSession, message string) error {
	stored.Received = make(map[int]UploadChunk)
	stored.State = "uploading"
	if stored.StorageFormat == uploadStoragePartV1 {
		stored.StorageFormat = uploadStoragePartV2
	}
	if err := writeJSONAtomic(m.uploadSidecar(stored.ID), stored, 0o600); err != nil {
		return errors.Join(errors.New(message), err)
	}
	return errors.New(message)
}

func (m *Manager) CancelUpload(id string) error {
	if !validUploadID(id) {
		return ErrUploadNotFound
	}
	m.mu.Lock()
	if m.uploadBusy[id] {
		m.mu.Unlock()
		return fmt.Errorf("%w，暂不能取消", ErrUploadFinalizing)
	}
	m.mu.Unlock()
	unlock := m.lockUpload(id)
	m.mu.Lock()
	if m.uploadBusy[id] {
		m.mu.Unlock()
		unlock()
		return fmt.Errorf("%w，暂不能取消", ErrUploadFinalizing)
	}
	m.uploadCanceling[id] = true
	writers := make([]context.CancelFunc, 0, len(m.uploadWriters[id]))
	for _, cancel := range m.uploadWriters[id] {
		writers = append(writers, cancel)
	}
	m.mu.Unlock()
	for _, cancel := range writers {
		cancel()
	}
	if len(writers) > 0 {
		unlock()
		return nil
	}
	dir := m.uploadDir(id)
	if _, err := os.Stat(dir); err != nil {
		m.mu.Lock()
		delete(m.uploadCanceling, id)
		m.mu.Unlock()
		unlock()
		if os.IsNotExist(err) {
			return ErrUploadNotFound
		}
		return err
	}
	err := os.RemoveAll(dir)
	if err == nil {
		m.clearUploadProgress(id)
		m.mu.Lock()
		delete(m.uploadCanceling, id)
		m.mu.Unlock()
	}
	unlock()
	return err
}

func (m *Manager) finishCanceledUpload(id string) error {
	unlock := m.lockUpload(id)
	defer unlock()
	m.mu.Lock()
	ready := m.uploadCanceling[id] && !m.uploadBusy[id] && len(m.uploadWriters[id]) == 0
	m.mu.Unlock()
	if !ready {
		return nil
	}
	err := os.RemoveAll(m.uploadDir(id))
	if err != nil {
		return err
	}
	m.clearUploadProgress(id)
	m.mu.Lock()
	delete(m.uploadCanceling, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) recoverFinalizedUpload(ctx context.Context, id string) (BackupRecord, bool, error) {
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return BackupRecord{}, false, err
	}
	suffix := "-" + id + ".zip"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		archivePath := filepath.Join(m.backupDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return BackupRecord{}, false, err
		}
		var meta archiveMeta
		if err := readJSONFile(metaPath(archivePath), &meta); err == nil &&
			meta.Imported && (meta.UploadID == "" || meta.UploadID == id) &&
			meta.Size == info.Size() && meta.ModifiedAt.Equal(info.ModTime().UTC()) {
			record := BackupRecord{
				ID:                 strings.TrimSuffix(entry.Name(), ".zip"),
				Name:               entry.Name(),
				Size:               info.Size(),
				SHA256:             meta.SHA256,
				CreatedAt:          archiveTimestamp(meta.Manifest, info.ModTime()),
				VerificationStatus: "verified",
				Imported:           true,
			}
			applyManifestToRecord(&record, meta.Manifest, info.ModTime())
			return record, true, nil
		}
		report, err := VerifyArchive(ctx, archivePath, VerifyOptions{
			CurrentVersion: m.appVersion,
			TempDir:        m.restoreDir,
		})
		if err != nil {
			return BackupRecord{}, false, err
		}
		hash, size, err := hashFile(ctx, archivePath)
		if err != nil {
			return BackupRecord{}, false, err
		}
		meta = archiveMeta{
			ID:         strings.TrimSuffix(entry.Name(), ".zip"),
			Name:       entry.Name(),
			Size:       size,
			SHA256:     hash,
			ModifiedAt: info.ModTime().UTC(),
			VerifiedAt: m.nowTime(),
			Imported:   true,
			UploadID:   id,
			Manifest:   report.Manifest,
		}
		if err := writeJSONAtomic(metaPath(archivePath), meta, 0o600); err != nil {
			return BackupRecord{}, false, err
		}
		record := BackupRecord{
			ID:                 meta.ID,
			Name:               meta.Name,
			Size:               size,
			SHA256:             hash,
			CreatedAt:          archiveTimestamp(meta.Manifest, info.ModTime()),
			VerificationStatus: "verified",
			Imported:           true,
		}
		applyManifestToRecord(&record, meta.Manifest, info.ModTime())
		return record, true, nil
	}
	return BackupRecord{}, false, nil
}

func (m *Manager) loadUpload(id string) (storedUploadSession, error) {
	if !validUploadID(id) {
		return storedUploadSession{}, ErrUploadNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadUploadUnlocked(id)
}

func (m *Manager) loadUploadUnlocked(id string) (storedUploadSession, error) {
	var stored storedUploadSession
	if err := readJSONFile(m.uploadSidecar(id), &stored); err != nil {
		if os.IsNotExist(err) {
			return storedUploadSession{}, ErrUploadNotFound
		}
		return storedUploadSession{}, err
	}
	if stored.Size <= 0 || stored.Size > maxExpandedBytes {
		return storedUploadSession{}, errors.New("迁移上传状态文件已损坏")
	}
	if stored.ChunkSize <= 0 {
		return storedUploadSession{}, errors.New("迁移上传状态文件已损坏")
	}
	expectedChunks := (stored.Size-1)/stored.ChunkSize + 1
	validStorage := (stored.StorageFormat == "" || stored.StorageFormat == uploadStoragePartV1 ||
		stored.StorageFormat == uploadStoragePartV2) &&
		stored.ChunkSize == ChunkSize
	if stored.StorageFormat == uploadStorageRangeV2 {
		validStorage = stored.ChunkSize >= minUploadRangeSize &&
			stored.ChunkSize <= maxUploadRangeSize && stored.ChunkSize%(1<<20) == 0
	}
	if stored.ID != id || !validStorage || stored.TotalChunks <= 0 ||
		int64(stored.TotalChunks) != expectedChunks || stored.Received == nil ||
		(stored.State != "uploading" && stored.State != "finalizing") ||
		(stored.SHA256 != "" && !validSHA256(stored.SHA256)) ||
		(stored.StorageFormat == uploadStorageRangeV2 && !validSHA256(stored.SHA256)) {
		return storedUploadSession{}, errors.New("迁移上传状态文件已损坏")
	}
	for index, chunk := range stored.Received {
		expectedSize, ok := uploadChunkSize(stored, index)
		if !ok || chunk.Index != index || chunk.Size != expectedSize {
			return storedUploadSession{}, errors.New("迁移上传状态文件已损坏")
		}
	}
	return stored, nil
}

func publicUploadSession(stored storedUploadSession) UploadSession {
	received := make([]UploadChunk, 0, len(stored.Received))
	for _, chunk := range stored.Received {
		received = append(received, chunk)
	}
	sort.Slice(received, func(i, j int) bool { return received[i].Index < received[j].Index })
	return UploadSession{
		ID:          stored.ID,
		FileName:    stored.FileName,
		Size:        stored.Size,
		SHA256:      stored.SHA256,
		ChunkSize:   stored.ChunkSize,
		TotalChunks: stored.TotalChunks,
		Received:    received,
		State:       stored.State,
		CreatedAt:   stored.CreatedAt,
		ExpiresAt:   stored.ExpiresAt,
	}
}

func (m *Manager) cleanupExpiredUploads() error {
	entries, err := os.ReadDir(m.uploadRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(m.uploadRoot, 0o700)
		}
		return err
	}
	now := m.nowTime()
	for _, entry := range entries {
		if !entry.IsDir() || !validUploadID(entry.Name()) {
			continue
		}
		m.mu.Lock()
		busy := m.uploadBusy[entry.Name()] || len(m.uploadWriters[entry.Name()]) > 0
		m.mu.Unlock()
		if busy {
			continue
		}
		unlock := m.lockUpload(entry.Name())
		m.mu.Lock()
		busy = m.uploadBusy[entry.Name()] || len(m.uploadWriters[entry.Name()]) > 0
		m.mu.Unlock()
		if busy {
			unlock()
			continue
		}
		var stored storedUploadSession
		err := readJSONFile(m.uploadSidecar(entry.Name()), &stored)
		if err != nil || stored.ExpiresAt.IsZero() || !now.Before(stored.ExpiresAt) {
			_ = os.RemoveAll(m.uploadDir(entry.Name()))
		}
		unlock()
	}
	return nil
}

func (m *Manager) lockUpload(id string) func() {
	m.mu.Lock()
	lock := m.uploadLocks[id]
	if lock == nil {
		lock = &uploadSessionLock{}
		m.uploadLocks[id] = lock
	}
	lock.refs++
	m.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.mu.Lock()
		lock.refs--
		if lock.refs == 0 && m.uploadLocks[id] == lock {
			delete(m.uploadLocks, id)
		}
		m.mu.Unlock()
	}
}

func uploadChunkSize(stored storedUploadSession, index int) (int64, bool) {
	if index < 0 || index >= stored.TotalChunks {
		return 0, false
	}
	size := stored.ChunkSize
	if index == stored.TotalChunks-1 {
		size = stored.Size - int64(index)*stored.ChunkSize
	}
	return size, size > 0 && size <= stored.ChunkSize
}

func validUploadID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (m *Manager) uploadDir(id string) string {
	return filepath.Join(m.uploadRoot, id)
}

func (m *Manager) uploadSidecar(id string) string {
	return filepath.Join(m.uploadDir(id), "upload.json")
}

func (m *Manager) uploadPartPath(id string) string {
	return filepath.Join(m.uploadDir(id), "archive.part")
}

func (m *Manager) uploadChunkPath(id string, index int) string {
	return filepath.Join(m.uploadDir(id), fmt.Sprintf("%08d.chunk", index))
}
