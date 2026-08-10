package backuptransfer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/video-site/backend/internal/backup"
)

const receiveTokenPrefix = "v91r_"

func (m *Manager) GenerateReceiveToken(ttl time.Duration) (ReceiveToken, error) {
	if m == nil {
		return ReceiveToken{}, ErrUnavailable
	}
	if ttl <= 0 {
		ttl = defaultPairingTTL
	}
	if ttl > time.Hour {
		ttl = time.Hour
	}
	id, err := randomOpaqueID()
	if err != nil {
		return ReceiveToken{}, err
	}
	secret, err := randomSecret()
	if err != nil {
		return ReceiveToken{}, err
	}
	raw := receiveTokenPrefix + id + "_" + secret
	digest := sha256.Sum256([]byte(raw))
	now := m.nowTime()
	stored := storedReceiveToken{
		ID:        id,
		Hash:      hex.EncodeToString(digest[:]),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiver.Tokens[id] = stored
	if err := m.saveReceiverLocked(); err != nil {
		delete(m.receiver.Tokens, id)
		return ReceiveToken{}, err
	}
	return ReceiveToken{
		ID:        id,
		Token:     raw,
		CreatedAt: stored.CreatedAt,
		ExpiresAt: stored.ExpiresAt,
	}, nil
}

func (m *Manager) ListReceiveTokens() []ReceiveTokenInfo {
	if m == nil {
		return []ReceiveTokenInfo{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ReceiveTokenInfo, 0, len(m.receiver.Tokens))
	for _, token := range m.receiver.Tokens {
		out = append(out, ReceiveTokenInfo{
			ID:              token.ID,
			CreatedAt:       token.CreatedAt,
			ExpiresAt:       token.ExpiresAt,
			LastUsedAt:      token.LastUsedAt,
			BoundTransferID: token.BoundTransferID,
			Revoked:         token.Revoked,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *Manager) RevokeReceiveToken(id string) error {
	if m == nil {
		return ErrUnavailable
	}
	id = strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	token, ok := m.receiver.Tokens[id]
	if !ok {
		return ErrUnauthorized
	}
	revokedAt := m.nowTime()
	if revokedAt.Before(token.CreatedAt) {
		revokedAt = token.CreatedAt
	}
	token.Revoked = true
	token.ExpiresAt = revokedAt
	m.receiver.Tokens[id] = token
	return m.saveReceiverLocked()
}

func (m *Manager) AuthorizeReceiveToken(raw string) error {
	if m == nil {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.authenticateTokenLocked(raw, "")
	return err
}

func (m *Manager) BeginImport(ctx context.Context, rawToken string, request ImportRequest) (ImportStatus, error) {
	if m == nil {
		return ImportStatus{}, ErrUnavailable
	}
	request.FileName = filepath.Base(strings.TrimSpace(request.FileName))
	request.SHA256 = strings.ToLower(strings.TrimSpace(request.SHA256))
	if err := validateImportRequest(request); err != nil {
		return ImportStatus{}, err
	}
	m.receiveMu.Lock()
	defer m.receiveMu.Unlock()

	m.mu.Lock()
	token, err := m.authenticateTokenLocked(rawToken, request.TransferID)
	if err != nil {
		m.mu.Unlock()
		return ImportStatus{}, err
	}
	key := receiptKey(token.ID, request.TransferID)
	if existing, ok := m.receiver.Receipts[key]; ok {
		if !sameImportRequest(existing.Request, request) {
			m.mu.Unlock()
			return ImportStatus{}, ErrImportConflict
		}
		m.mu.Unlock()
		status, statusErr := m.statusForReceipt(ctx, existing)
		if !errors.Is(statusErr, ErrImportNotFound) {
			if statusErr == nil {
				statusErr = m.touchReceiveToken(existing.TokenID, request.TransferID)
			}
			return status, statusErr
		}
		// A sender may legitimately resume after the target's staging upload
		// expired. Keep the durable transfer receipt and replace only its
		// staging session, so a new pairing code and sender job are unnecessary.
		return m.restartExpiredImport(ctx, rawToken, key, existing)
	}
	if token.BoundTransferID != "" && token.BoundTransferID != request.TransferID {
		m.mu.Unlock()
		return ImportStatus{}, ErrTokenBound
	}
	m.mu.Unlock()

	session, err := m.backups.BeginUpload(ctx, backup.BeginUploadInput{
		FileName: request.FileName,
		Size:     request.Size,
		SHA256:   request.SHA256,
	})
	if err != nil {
		return ImportStatus{}, err
	}
	now := m.nowTime()
	receipt := storedReceipt{
		TokenID:   token.ID,
		Request:   request,
		UploadID:  session.ID,
		State:     ImportUploading,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	latest, authErr := m.authenticateTokenLocked(rawToken, request.TransferID)
	if authErr == nil && latest.BoundTransferID != "" && latest.BoundTransferID != request.TransferID {
		authErr = ErrTokenBound
	}
	if authErr == nil {
		latest.BoundTransferID = request.TransferID
		latest.LastUsedAt = now
		latest.ExpiresAt = now.Add(boundTokenTTL)
		m.receiver.Tokens[latest.ID] = latest
		m.receiver.Receipts[key] = receipt
		authErr = m.saveReceiverLocked()
	}
	m.mu.Unlock()
	if authErr != nil {
		_ = m.backups.CancelUpload(session.ID)
		return ImportStatus{}, authErr
	}
	return importStatusFromSession(receipt, session), nil
}

func (m *Manager) restartExpiredImport(
	ctx context.Context,
	rawToken string,
	key string,
	receipt storedReceipt,
) (ImportStatus, error) {
	session, err := m.backups.BeginUpload(ctx, backup.BeginUploadInput{
		FileName: receipt.Request.FileName,
		Size:     receipt.Request.Size,
		SHA256:   receipt.Request.SHA256,
	})
	if err != nil {
		return ImportStatus{}, err
	}
	now := m.nowTime()
	m.mu.Lock()
	token, authErr := m.authenticateTokenLocked(rawToken, receipt.Request.TransferID)
	current, exists := m.receiver.Receipts[key]
	if authErr == nil && (!exists || current.UploadID != receipt.UploadID) {
		authErr = ErrImportConflict
	}
	if authErr == nil {
		current.UploadID = session.ID
		current.State = ImportUploading
		current.Record = nil
		current.UpdatedAt = now
		token.LastUsedAt = now
		token.ExpiresAt = now.Add(boundTokenTTL)
		m.receiver.Tokens[token.ID] = token
		m.receiver.Receipts[key] = current
		authErr = m.saveReceiverLocked()
		receipt = current
	}
	m.mu.Unlock()
	if authErr != nil {
		_ = m.backups.CancelUpload(session.ID)
		return ImportStatus{}, authErr
	}
	return importStatusFromSession(receipt, session), nil
}

func (m *Manager) ImportStatus(ctx context.Context, rawToken, transferID string) (ImportStatus, error) {
	receipt, err := m.authorizedReceipt(rawToken, transferID)
	if err != nil {
		return ImportStatus{}, err
	}
	status, err := m.statusForReceipt(ctx, receipt)
	if err == nil {
		if touchErr := m.touchReceiveToken(receipt.TokenID, transferID); touchErr != nil {
			return ImportStatus{}, touchErr
		}
	}
	return status, err
}

func (m *Manager) PutImportChunk(
	ctx context.Context,
	rawToken string,
	transferID string,
	index int,
	digest string,
	body io.Reader,
) error {
	receipt, err := m.authorizedReceipt(rawToken, transferID)
	if err != nil {
		return err
	}
	if receipt.State != ImportUploading {
		if receipt.State == ImportCompleted {
			return nil
		}
		return backup.ErrUploadFinalizing
	}
	if _, err := m.backups.PutChunk(ctx, receipt.UploadID, index, digest, body); err != nil {
		return err
	}
	return m.touchReceiveToken(receipt.TokenID, transferID)
}

func (m *Manager) FinalizeImport(ctx context.Context, rawToken, transferID string) (ImportStatus, error) {
	if m == nil {
		return ImportStatus{}, ErrUnavailable
	}
	m.receiveMu.Lock()
	defer m.receiveMu.Unlock()
	receipt, err := m.authorizedReceipt(rawToken, transferID)
	if err != nil {
		return ImportStatus{}, err
	}
	if receipt.State == ImportCompleted && receipt.Record != nil {
		return importStatusFromReceipt(receipt), nil
	}
	if receipt.State == ImportCanceled {
		return ImportStatus{}, ErrImportNotFound
	}
	if err := m.setReceiptState(receipt.TokenID, transferID, ImportFinalizing, nil); err != nil {
		return ImportStatus{}, err
	}
	record, finalizeErr := m.backups.FinalizeUpload(ctx, receipt.UploadID)
	if errors.Is(finalizeErr, backup.ErrUploadNotFound) {
		var found bool
		record, found, finalizeErr = m.backups.FindImportedUpload(ctx, receipt.UploadID)
		if finalizeErr == nil && !found {
			finalizeErr = backup.ErrUploadNotFound
		}
	}
	if finalizeErr != nil {
		if stateErr := m.setReceiptState(receipt.TokenID, transferID, ImportUploading, nil); stateErr != nil {
			return ImportStatus{}, errors.Join(finalizeErr, stateErr)
		}
		return ImportStatus{}, finalizeErr
	}
	if err := m.setReceiptState(receipt.TokenID, transferID, ImportCompleted, &record); err != nil {
		return ImportStatus{}, err
	}
	_ = m.touchReceiveToken(receipt.TokenID, transferID)
	receipt.State = ImportCompleted
	receipt.Record = &record
	return importStatusFromReceipt(receipt), nil
}

func (m *Manager) CancelImport(ctx context.Context, rawToken, transferID string) error {
	if m == nil {
		return ErrUnavailable
	}
	m.receiveMu.Lock()
	defer m.receiveMu.Unlock()
	receipt, err := m.authorizedReceipt(rawToken, transferID)
	if err != nil {
		return err
	}
	if receipt.State == ImportCompleted {
		return ErrTransferTerminal
	}
	if receipt.State == ImportCanceled {
		return nil
	}
	if err := m.backups.CancelUpload(receipt.UploadID); err != nil && !errors.Is(err, backup.ErrUploadNotFound) {
		return err
	}
	return m.setReceiptState(receipt.TokenID, transferID, ImportCanceled, nil)
}

func (m *Manager) authorizedReceipt(rawToken, transferID string) (storedReceipt, error) {
	if !validOpaqueID(strings.TrimSpace(transferID)) {
		return storedReceipt{}, ErrImportNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	token, err := m.authenticateTokenLocked(rawToken, transferID)
	if err != nil {
		return storedReceipt{}, err
	}
	receipt, ok := m.receiver.Receipts[receiptKey(token.ID, transferID)]
	if !ok {
		return storedReceipt{}, ErrImportNotFound
	}
	return receipt, nil
}

func (m *Manager) authenticateTokenLocked(raw, transferID string) (storedReceiveToken, error) {
	id, ok := receiveTokenID(raw)
	if !ok {
		return storedReceiveToken{}, ErrUnauthorized
	}
	token, ok := m.receiver.Tokens[id]
	if !ok || token.Revoked || !m.nowTime().Before(token.ExpiresAt) {
		return storedReceiveToken{}, ErrUnauthorized
	}
	expected, err := hex.DecodeString(token.Hash)
	if err != nil || len(expected) != sha256.Size {
		return storedReceiveToken{}, ErrUnauthorized
	}
	actual := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return storedReceiveToken{}, ErrUnauthorized
	}
	if transferID != "" && token.BoundTransferID != "" && token.BoundTransferID != transferID {
		return storedReceiveToken{}, ErrTokenBound
	}
	return token, nil
}

func (m *Manager) touchReceiveToken(tokenID, transferID string) error {
	now := m.nowTime()
	m.mu.Lock()
	defer m.mu.Unlock()
	token, ok := m.receiver.Tokens[tokenID]
	if !ok || token.Revoked || token.BoundTransferID != transferID {
		return ErrUnauthorized
	}
	if !token.LastUsedAt.IsZero() && now.Sub(token.LastUsedAt) < time.Minute &&
		token.ExpiresAt.After(now.Add(boundTokenTTL-time.Minute)) {
		return nil
	}
	token.LastUsedAt = now
	token.ExpiresAt = now.Add(boundTokenTTL)
	m.receiver.Tokens[tokenID] = token
	return m.saveReceiverLocked()
}

func (m *Manager) setReceiptState(tokenID, transferID, state string, record *backup.BackupRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := receiptKey(tokenID, transferID)
	receipt, ok := m.receiver.Receipts[key]
	if !ok {
		return ErrImportNotFound
	}
	receipt.State = state
	receipt.UpdatedAt = m.nowTime()
	if record != nil {
		copy := *record
		receipt.Record = &copy
	}
	m.receiver.Receipts[key] = receipt
	return m.saveReceiverLocked()
}

func (m *Manager) statusForReceipt(ctx context.Context, receipt storedReceipt) (ImportStatus, error) {
	if receipt.State == ImportCompleted || receipt.State == ImportCanceled {
		return importStatusFromReceipt(receipt), nil
	}
	session, err := m.backups.UploadStatus(receipt.UploadID)
	if err == nil {
		return importStatusFromSession(receipt, session), nil
	}
	if !errors.Is(err, backup.ErrUploadNotFound) {
		return ImportStatus{}, err
	}
	record, found, recoverErr := m.backups.FindImportedUpload(ctx, receipt.UploadID)
	if recoverErr != nil && !errors.Is(recoverErr, backup.ErrUploadNotFound) {
		return ImportStatus{}, recoverErr
	}
	if !found {
		return ImportStatus{}, ErrImportNotFound
	}
	if err := m.setReceiptState(receipt.TokenID, receipt.Request.TransferID, ImportCompleted, &record); err != nil {
		return ImportStatus{}, err
	}
	receipt.State = ImportCompleted
	receipt.Record = &record
	return importStatusFromReceipt(receipt), nil
}

func importStatusFromSession(receipt storedReceipt, session backup.UploadSession) ImportStatus {
	state := ImportUploading
	if session.State == ImportFinalizing {
		state = ImportFinalizing
	}
	ranges := chunkRanges(session.Received)
	var receivedBytes int64
	for _, chunk := range session.Received {
		receivedBytes += chunk.Size
	}
	return ImportStatus{
		TransferID:    receipt.Request.TransferID,
		State:         state,
		Size:          session.Size,
		SHA256:        receipt.Request.SHA256,
		ChunkSize:     session.ChunkSize,
		TotalChunks:   session.TotalChunks,
		Received:      ranges,
		ReceivedBytes: receivedBytes,
		ExpiresAt:     session.ExpiresAt,
	}
}

func importStatusFromReceipt(receipt storedReceipt) ImportStatus {
	status := ImportStatus{
		TransferID: receipt.Request.TransferID,
		State:      receipt.State,
		Size:       receipt.Request.Size,
		SHA256:     receipt.Request.SHA256,
		ChunkSize:  backup.ChunkSize,
	}
	status.TotalChunks = int((status.Size-1)/status.ChunkSize + 1)
	if receipt.State == ImportCompleted && receipt.Record != nil {
		copy := *receipt.Record
		status.Record = &copy
		status.ReceivedBytes = status.Size
		if status.TotalChunks > 0 {
			status.Received = []ChunkRange{{Start: 0, End: status.TotalChunks - 1}}
		}
	}
	return status
}

func chunkRanges(chunks []backup.UploadChunk) []ChunkRange {
	if len(chunks) == 0 {
		return []ChunkRange{}
	}
	indexes := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		indexes = append(indexes, chunk.Index)
	}
	sort.Ints(indexes)
	ranges := make([]ChunkRange, 0, len(indexes))
	current := ChunkRange{Start: indexes[0], End: indexes[0]}
	for _, index := range indexes[1:] {
		if index == current.End+1 {
			current.End = index
			continue
		}
		ranges = append(ranges, current)
		current = ChunkRange{Start: index, End: index}
	}
	return append(ranges, current)
}

func validateImportRequest(request ImportRequest) error {
	if !validOpaqueID(request.TransferID) || !validOpaqueID(request.SourceServerID) {
		return errors.New("服务器传输编号无效")
	}
	if strings.TrimSpace(request.BackupID) == "" || request.FileName == "" || request.FileName == "." ||
		!strings.EqualFold(filepath.Ext(request.FileName), ".zip") {
		return errors.New("服务器传输备份名称无效")
	}
	if request.Size <= 0 {
		return errors.New("服务器传输备份大小无效")
	}
	if request.FormatVersion != backup.FormatVersion {
		return fmt.Errorf("不支持备份格式版本 %d", request.FormatVersion)
	}
	decoded, err := hex.DecodeString(request.SHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("服务器传输备份 SHA-256 无效")
	}
	return nil
}

func sameImportRequest(left, right ImportRequest) bool {
	return left.TransferID == right.TransferID &&
		left.SourceServerID == right.SourceServerID &&
		left.BackupID == right.BackupID &&
		left.FileName == right.FileName &&
		left.Size == right.Size &&
		strings.EqualFold(left.SHA256, right.SHA256) &&
		left.FormatVersion == right.FormatVersion
}

func receiveTokenID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, receiveTokenPrefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(raw, receiveTokenPrefix), "_")
	if len(parts) != 2 || !validOpaqueID(parts[0]) || len(parts[1]) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", false
	}
	return parts[0], true
}
