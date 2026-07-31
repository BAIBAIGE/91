package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/video-site/backend/internal/backup"
)

func (a *AdminServer) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	result, err := a.Backups.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (a *AdminServer) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	status, err := a.Backups.Create(r.Context())
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backup.ErrTaskRunning) || errors.Is(err, backup.ErrRestorePending) {
			code = http.StatusConflict
		} else if errors.Is(err, backup.ErrInsufficientSpace) {
			code = http.StatusInsufficientStorage
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (a *AdminServer) handleCancelBackup(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	if err := a.Backups.Cancel(); err != nil {
		code := http.StatusConflict
		if !errors.Is(err, backup.ErrNoRunningTask) {
			code = http.StatusInternalServerError
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (a *AdminServer) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	file, info, name, err := a.Backups.OpenBackup(routeParam(r, "id"))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backup.ErrBackupNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (a *AdminServer) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	if err := a.Backups.Delete(routeParam(r, "id")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backup.ErrBackupNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, backup.ErrRestorePending) ||
			strings.Contains(err.Error(), "等待恢复") {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AdminServer) handleBeginBackupUpload(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	var input backup.BeginUploadInput
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	session, err := a.Backups.BeginUpload(r.Context(), input)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, backup.ErrInsufficientSpace) {
			code = http.StatusInsufficientStorage
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (a *AdminServer) handleBackupUploadStatus(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	session, err := a.Backups.UploadStatus(routeParam(r, "id"))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backup.ErrUploadNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, session)
}

func (a *AdminServer) handleBackupUploadChunk(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	index, err := strconv.Atoi(routeParam(r, "index"))
	if err != nil || index < 0 {
		writeErr(w, http.StatusBadRequest, errors.New("分片序号无效"))
		return
	}
	session, err := a.Backups.PutChunk(
		r.Context(),
		routeParam(r, "id"),
		index,
		r.Header.Get("X-Chunk-SHA256"),
		r.Body,
	)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, backup.ErrUploadNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, backup.ErrUploadFinalizing) {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (a *AdminServer) handleFinalizeBackupUpload(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	record, err := a.Backups.FinalizeUpload(r.Context(), routeParam(r, "id"))
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, backup.ErrUploadNotFound):
			code = http.StatusNotFound
		case errors.Is(err, backup.ErrUploadIncomplete), errors.Is(err, backup.ErrUploadFinalizing):
			code = http.StatusConflict
		case errors.Is(err, backup.ErrInsufficientSpace):
			code = http.StatusInsufficientStorage
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (a *AdminServer) handleCancelBackupUpload(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	if err := a.Backups.CancelUpload(routeParam(r, "id")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, backup.ErrUploadNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, backup.ErrUploadFinalizing) {
			code = http.StatusConflict
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AdminServer) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if !a.backupsAvailable(w) {
		return
	}
	var request backup.RestoreRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if request.Confirmation != "确认恢复" {
		writeErr(w, http.StatusBadRequest, errors.New("请输入固定确认文本“确认恢复”"))
		return
	}
	report, err := a.Backups.PrepareRestore(r.Context(), routeParam(r, "id"))
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, backup.ErrBackupNotFound):
			code = http.StatusNotFound
		case errors.Is(err, backup.ErrTaskRunning), errors.Is(err, backup.ErrRestorePending):
			code = http.StatusConflict
		case errors.Is(err, backup.ErrInsufficientSpace):
			code = http.StatusInsufficientStorage
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusAccepted, backup.RestoreResult{
		OK:             true,
		Restarting:     true,
		RestartManaged: a.Backups.RestartManaged(),
		Report:         report,
	})
	// Give net/http enough time to flush the accepted response before main
	// starts its graceful shutdown sequence.
	time.AfterFunc(500*time.Millisecond, a.Backups.RequestRestart)
}

func (a *AdminServer) backupsAvailable(w http.ResponseWriter) bool {
	if a.Backups != nil {
		return true
	}
	writeErr(w, http.StatusServiceUnavailable, errors.New("备份服务未配置"))
	return false
}
