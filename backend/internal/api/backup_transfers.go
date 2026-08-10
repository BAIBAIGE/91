package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/video-site/backend/internal/backup"
	"github.com/video-site/backend/internal/backuptransfer"
)

const maxBackupTransferJSONBytes int64 = 32 << 10

func (a *AdminServer) handleListBackupTransfers(w http.ResponseWriter, _ *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.BackupTransfers.ListTransfers())
}

func (a *AdminServer) handleCreateBackupTransfer(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	var input struct {
		TargetURL    string `json:"targetUrl"`
		ReceiveToken string `json:"receiveToken"`
	}
	if err := decodeBackupTransferJSON(w, r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	job, err := a.BackupTransfers.CreateTransfer(r.Context(), backuptransfer.CreateTransferInput{
		BackupID:     routeParam(r, "id"),
		TargetURL:    input.TargetURL,
		ReceiveToken: input.ReceiveToken,
	})
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, backup.ErrBackupNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, backuptransfer.ErrTransferBusy) {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *AdminServer) handleCancelBackupTransfer(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	job, err := a.BackupTransfers.CancelTransfer(routeParam(r, "id"))
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, backuptransfer.ErrTransferNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *AdminServer) handleRetryBackupTransfer(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	job, err := a.BackupTransfers.RetryTransfer(routeParam(r, "id"))
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, backuptransfer.ErrTransferNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *AdminServer) handleListBackupReceiveTokens(w http.ResponseWriter, _ *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.BackupTransfers.ListReceiveTokens())
}

func (a *AdminServer) handleCreateBackupReceiveToken(w http.ResponseWriter, _ *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	token, err := a.BackupTransfers.GenerateReceiveToken(10 * time.Minute)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, token)
}

func (a *AdminServer) handleRevokeBackupReceiveToken(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	if err := a.BackupTransfers.RevokeReceiveToken(routeParam(r, "id")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backuptransfer.ErrUnauthorized) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AdminServer) handlePeerBackupCapabilities(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	if err := a.BackupTransfers.AuthorizeReceiveToken(peerBearerToken(r)); err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.BackupTransfers.Capabilities())
}

func (a *AdminServer) handlePeerBeginBackupImport(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	var input backuptransfer.ImportRequest
	if err := decodeBackupTransferJSON(w, r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	status, err := a.BackupTransfers.BeginImport(r.Context(), peerBearerToken(r), input)
	if err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

func (a *AdminServer) handlePeerBackupImportStatus(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	status, err := a.BackupTransfers.ImportStatus(r.Context(), peerBearerToken(r), routeParam(r, "id"))
	if err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

func (a *AdminServer) handlePeerBackupImportChunk(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	index, err := strconv.Atoi(routeParam(r, "index"))
	if err != nil || index < 0 {
		writeErr(w, http.StatusBadRequest, errors.New("分片序号无效"))
		return
	}
	digest, err := peerChunkDigest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, backup.ChunkSize+1)
	if err := a.BackupTransfers.PutImportChunk(
		r.Context(),
		peerBearerToken(r),
		routeParam(r, "id"),
		index,
		digest,
		r.Body,
	); err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *AdminServer) handlePeerFinalizeBackupImport(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	status, err := a.BackupTransfers.FinalizeImport(r.Context(), peerBearerToken(r), routeParam(r, "id"))
	if err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, status)
}

func (a *AdminServer) handlePeerCancelBackupImport(w http.ResponseWriter, r *http.Request) {
	if !a.backupTransfersAvailable(w) {
		return
	}
	if err := a.BackupTransfers.CancelImport(r.Context(), peerBearerToken(r), routeParam(r, "id")); err != nil {
		writePeerTransferError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func decodeBackupTransferJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupTransferJSONBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("请求只能包含一个 JSON 对象")
		}
		return err
	}
	return nil
}

func peerBearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func peerChunkDigest(r *http.Request) (string, error) {
	legacy := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Chunk-SHA256")))
	standard := strings.TrimSpace(r.Header.Get("Content-Digest"))
	if standard == "" {
		if decoded, err := hex.DecodeString(legacy); err != nil || len(decoded) != 32 {
			return "", errors.New("必须提供有效的分片 SHA-256")
		}
		return legacy, nil
	}
	const prefix = "sha-256=:"
	if !strings.HasPrefix(strings.ToLower(standard), prefix) || !strings.HasSuffix(standard, ":") {
		return "", errors.New("Content-Digest 格式无效")
	}
	encoded := standard[len(prefix) : len(standard)-1]
	digest, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(digest) != 32 {
		return "", errors.New("Content-Digest SHA-256 无效")
	}
	hexDigest := hex.EncodeToString(digest)
	if legacy != "" && !strings.EqualFold(legacy, hexDigest) {
		return "", errors.New("分片摘要请求头不一致")
	}
	return hexDigest, nil
}

func writePeerTransferError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, backuptransfer.ErrUnavailable):
		code = http.StatusServiceUnavailable
	case errors.Is(err, backuptransfer.ErrUnauthorized):
		code = http.StatusUnauthorized
		w.Header().Set("WWW-Authenticate", `Bearer realm="backup-transfer"`)
	case errors.Is(err, backuptransfer.ErrTokenBound):
		code = http.StatusForbidden
	case errors.Is(err, backuptransfer.ErrImportNotFound), errors.Is(err, backup.ErrUploadNotFound):
		code = http.StatusNotFound
	case errors.Is(err, backuptransfer.ErrImportConflict),
		errors.Is(err, backup.ErrUploadIncomplete),
		errors.Is(err, backup.ErrUploadFinalizing),
		errors.Is(err, backuptransfer.ErrTransferTerminal):
		code = http.StatusConflict
	case errors.Is(err, backup.ErrInsufficientSpace):
		code = http.StatusInsufficientStorage
	}
	writeErr(w, code, err)
}

func (a *AdminServer) backupTransfersAvailable(w http.ResponseWriter) bool {
	if a.BackupTransfers != nil {
		return true
	}
	writeErr(w, http.StatusServiceUnavailable, backuptransfer.ErrUnavailable)
	return false
}
