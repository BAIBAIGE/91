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
	"time"
)

type storedUploadSession struct {
	ID          string              `json:"id"`
	FileName    string              `json:"fileName"`
	Size        int64               `json:"size"`
	SHA256      string              `json:"sha256,omitempty"`
	ChunkSize   int64               `json:"chunkSize"`
	TotalChunks int                 `json:"totalChunks"`
	Received    map[int]UploadChunk `json:"received"`
	State       string              `json:"state"`
	CreatedAt   time.Time           `json:"createdAt"`
	ExpiresAt   time.Time           `json:"expiresAt"`
}

func (m *Manager) BeginUpload(ctx context.Context, input BeginUploadInput) (UploadSession, error) {
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
		return UploadSession{}, fmt.Errorf("%w：上传及合并需要至少 %d 字节，可用 %d 字节", ErrInsufficientSpace, required, available)
	}
	if err := ctx.Err(); err != nil {
		return UploadSession{}, err
	}
	id, err := randomID()
	if err != nil {
		return UploadSession{}, err
	}
	created := m.nowTime()
	totalChunks64 := (input.Size + ChunkSize - 1) / ChunkSize
	if totalChunks64 <= 0 || totalChunks64 > int64(^uint(0)>>1) {
		return UploadSession{}, errors.New("备份分片数量无效")
	}
	stored := storedUploadSession{
		ID:          id,
		FileName:    input.FileName,
		Size:        input.Size,
		SHA256:      input.SHA256,
		ChunkSize:   ChunkSize,
		TotalChunks: int(totalChunks64),
		Received:    make(map[int]UploadChunk),
		State:       "uploading",
		CreatedAt:   created,
		ExpiresAt:   created.Add(UploadTTL),
	}
	dir := m.uploadDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return UploadSession{}, err
	}
	if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return UploadSession{}, err
	}
	return publicUploadSession(stored), nil
}

func requiredUploadBytes(size int64) int64 {
	if size > (1<<62-diskSafetyReserve)/2 {
		return 1<<62 - 1
	}
	return size*2 + diskSafetyReserve
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
	return publicUploadSession(stored), nil
}

func (m *Manager) PutChunk(
	ctx context.Context,
	id string,
	index int,
	expectedSHA256 string,
	body io.Reader,
) (UploadSession, error) {
	stored, err := m.loadUpload(id)
	if err != nil {
		return UploadSession{}, err
	}
	if stored.State != "uploading" {
		return UploadSession{}, errors.New("迁移上传正在合并，暂不能写入分片")
	}
	if !m.nowTime().Before(stored.ExpiresAt) {
		_ = m.CancelUpload(id)
		return UploadSession{}, ErrUploadNotFound
	}
	if index < 0 || index >= stored.TotalChunks {
		return UploadSession{}, errors.New("分片序号无效")
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if !validSHA256(expectedSHA256) {
		return UploadSession{}, errors.New("必须提供有效的分片 SHA-256")
	}
	expectedSize := stored.ChunkSize
	if index == stored.TotalChunks-1 {
		expectedSize = stored.Size - int64(index)*stored.ChunkSize
	}
	if expectedSize <= 0 || expectedSize > stored.ChunkSize {
		return UploadSession{}, errors.New("分片大小计算失败")
	}
	tempName := fmt.Sprintf("%08d.chunk.part-%d", index, time.Now().UnixNano())
	tempPath := filepath.Join(m.uploadDir(id), tempName)
	output, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return UploadSession{}, err
	}
	hash := sha256.New()
	written, copyErr := copyWithContext(
		ctx,
		io.MultiWriter(output, hash),
		io.LimitReader(body, expectedSize+1),
		nil,
	)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != expectedSize {
		_ = os.Remove(tempPath)
		switch {
		case copyErr != nil:
			return UploadSession{}, copyErr
		case syncErr != nil:
			return UploadSession{}, syncErr
		case closeErr != nil:
			return UploadSession{}, closeErr
		default:
			return UploadSession{}, fmt.Errorf("分片大小不匹配：收到 %d 字节，应为 %d 字节", written, expectedSize)
		}
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if actualHash != expectedSHA256 {
		_ = os.Remove(tempPath)
		return UploadSession{}, errors.New("分片 SHA-256 校验失败")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	latest, err := m.loadUploadUnlocked(id)
	if err != nil {
		_ = os.Remove(tempPath)
		return UploadSession{}, err
	}
	if latest.State != "uploading" {
		_ = os.Remove(tempPath)
		return UploadSession{}, errors.New("迁移上传正在合并，暂不能写入分片")
	}
	if existing, ok := latest.Received[index]; ok &&
		existing.Size == written && strings.EqualFold(existing.SHA256, actualHash) {
		_ = os.Remove(tempPath)
		return publicUploadSession(latest), nil
	}
	finalPath := m.uploadChunkPath(id, index)
	_ = os.Remove(finalPath)
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return UploadSession{}, err
	}
	latest.Received[index] = UploadChunk{Index: index, Size: written, SHA256: actualHash}
	if err := writeJSONAtomic(m.uploadSidecar(id), latest, 0o600); err != nil {
		_ = os.Remove(finalPath)
		return UploadSession{}, err
	}
	return publicUploadSession(latest), nil
}

func (m *Manager) FinalizeUpload(ctx context.Context, id string) (record BackupRecord, returnErr error) {
	if !validUploadID(id) {
		return BackupRecord{}, ErrUploadNotFound
	}
	m.mu.Lock()
	if m.uploadBusy[id] {
		m.mu.Unlock()
		return BackupRecord{}, errors.New("迁移上传正在合并")
	}
	m.uploadBusy[id] = true
	stored, err := m.loadUploadUnlocked(id)
	if err != nil {
		delete(m.uploadBusy, id)
		m.mu.Unlock()
		return BackupRecord{}, err
	}
	if len(stored.Received) != stored.TotalChunks {
		delete(m.uploadBusy, id)
		m.mu.Unlock()
		return BackupRecord{}, ErrUploadIncomplete
	}
	for index := 0; index < stored.TotalChunks; index++ {
		if _, ok := stored.Received[index]; !ok {
			delete(m.uploadBusy, id)
			m.mu.Unlock()
			return BackupRecord{}, ErrUploadIncomplete
		}
	}
	stored.State = "finalizing"
	if err := writeJSONAtomic(m.uploadSidecar(id), stored, 0o600); err != nil {
		delete(m.uploadBusy, id)
		m.mu.Unlock()
		return BackupRecord{}, err
	}
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
	if recovered, ok, err := m.recoverFinalizedUpload(ctx, id); err != nil {
		return BackupRecord{}, err
	} else if ok {
		_ = os.RemoveAll(m.uploadDir(id))
		return recovered, nil
	}

	name := fmt.Sprintf(
		"video-site-91-full-imported-%s-%s.zip",
		m.nowTime().Local().Format("20060102-150405"),
		id,
	)
	finalPath := filepath.Join(m.backupDir, name)
	partPath := finalPath + ".part"
	_ = os.Remove(partPath)
	output, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return BackupRecord{}, err
	}
	fullHash := sha256.New()
	var totalWritten int64
	for index := 0; index < stored.TotalChunks; index++ {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			_ = os.Remove(partPath)
			return BackupRecord{}, err
		}
		chunkPath := m.uploadChunkPath(id, index)
		chunk, err := os.Open(chunkPath)
		if err != nil {
			_ = output.Close()
			_ = os.Remove(partPath)
			return BackupRecord{}, ErrUploadIncomplete
		}
		chunkHash := sha256.New()
		written, copyErr := copyWithContext(ctx, io.MultiWriter(output, fullHash, chunkHash), chunk, nil)
		closeErr := chunk.Close()
		if copyErr != nil || closeErr != nil {
			_ = output.Close()
			_ = os.Remove(partPath)
			if copyErr != nil {
				return BackupRecord{}, copyErr
			}
			return BackupRecord{}, closeErr
		}
		expected := stored.Received[index]
		if written != expected.Size ||
			!strings.EqualFold(hex.EncodeToString(chunkHash.Sum(nil)), expected.SHA256) {
			_ = output.Close()
			_ = os.Remove(partPath)
			return BackupRecord{}, fmt.Errorf("分片 %d 在磁盘上校验失败，请重新上传", index)
		}
		totalWritten += written
	}
	syncErr := output.Sync()
	closeErr := output.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(partPath)
		if syncErr != nil {
			return BackupRecord{}, syncErr
		}
		return BackupRecord{}, closeErr
	}
	if totalWritten != stored.Size {
		_ = os.Remove(partPath)
		return BackupRecord{}, errors.New("合并后的备份大小不匹配")
	}
	archiveHash := hex.EncodeToString(fullHash.Sum(nil))
	if stored.SHA256 != "" && !strings.EqualFold(stored.SHA256, archiveHash) {
		_ = os.Remove(partPath)
		return BackupRecord{}, errors.New("完整备份 SHA-256 校验失败")
	}
	available, _ := m.availableBytes(m.dataRoot)
	if available <= (1<<62)-stored.Size {
		available += stored.Size // chunk directory is removed immediately after success
	}
	report, err := VerifyArchive(ctx, partPath, VerifyOptions{
		CurrentVersion: m.appVersion,
		TempDir:        m.restoreDir,
		AvailableBytes: available,
	})
	if err != nil {
		_ = os.Remove(partPath)
		return BackupRecord{}, err
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		return BackupRecord{}, err
	}
	info, err := os.Stat(finalPath)
	if err != nil {
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
		_ = os.Remove(finalPath)
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

func (m *Manager) CancelUpload(id string) error {
	if !validUploadID(id) {
		return ErrUploadNotFound
	}
	m.mu.Lock()
	if m.uploadBusy[id] {
		m.mu.Unlock()
		return errors.New("迁移上传正在合并，暂不能取消")
	}
	dir := m.uploadDir(id)
	m.mu.Unlock()
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrUploadNotFound
		}
		return err
	}
	return os.RemoveAll(dir)
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
	if stored.ID != id || stored.ChunkSize != ChunkSize || stored.TotalChunks <= 0 ||
		stored.Size <= 0 || stored.Received == nil {
		return storedUploadSession{}, errors.New("迁移上传状态文件已损坏")
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
		busy := m.uploadBusy[entry.Name()]
		m.mu.Unlock()
		if busy {
			continue
		}
		var stored storedUploadSession
		err := readJSONFile(m.uploadSidecar(entry.Name()), &stored)
		if err != nil || stored.ExpiresAt.IsZero() || !now.Before(stored.ExpiresAt) {
			_ = os.RemoveAll(m.uploadDir(entry.Name()))
		}
	}
	return nil
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

func (m *Manager) uploadChunkPath(id string, index int) string {
	return filepath.Join(m.uploadDir(id), fmt.Sprintf("%08d.chunk", index))
}
