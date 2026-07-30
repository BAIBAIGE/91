package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/remoteupload"
)

const maxRemoteUploadRequestBytes = 64 << 10

type RemoteUploadService interface {
	Create(context.Context, remoteupload.CreateInput) (*catalog.RemoteUploadJob, error)
	List(context.Context, int) ([]*catalog.RemoteUploadJob, error)
	Cancel(context.Context, string) (*catalog.RemoteUploadJob, error)
}

type remoteUploadRequest struct {
	URL   string   `json:"url"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

type RemoteUploadJobDTO struct {
	ID               string   `json:"id"`
	State            string   `json:"state"`
	SourceLabel      string   `json:"sourceLabel"`
	Title            string   `json:"title,omitempty"`
	Tags             []string `json:"tags"`
	BytesDownloaded  int64    `json:"bytesDownloaded"`
	TotalBytes       int64    `json:"totalBytes"`
	CanCancel        bool     `json:"canCancel"`
	CancelRequested  bool     `json:"cancelRequested,omitempty"`
	Error            string   `json:"error,omitempty"`
	CompletedVideoID string   `json:"completedVideoId,omitempty"`
	VideoHref        string   `json:"videoHref,omitempty"`
	CreatedAt        string   `json:"createdAt"`
	StartedAt        string   `json:"startedAt,omitempty"`
	UpdatedAt        string   `json:"updatedAt"`
	FinishedAt       string   `json:"finishedAt,omitempty"`
}

func (s *Server) handleCreateRemoteUpload(w http.ResponseWriter, r *http.Request) {
	if s.RemoteUploads == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("remote upload is not configured"))
		return
	}
	var body remoteUploadRequest
	if err := decodeRemoteUploadJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("video URL is required"))
		return
	}
	tags, err := s.canonicalUploadTags(r.Context(), body.Tags)
	if err != nil {
		status := http.StatusInternalServerError
		if isUploadTagValidationError(err) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err)
		return
	}
	job, err := s.RemoteUploads.Create(r.Context(), remoteupload.CreateInput{
		URL:   body.URL,
		Title: body.Title,
		Tags:  tags,
	})
	if err != nil {
		if remoteupload.IsValidationError(err) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, errors.New("failed to create remote upload job"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, mapRemoteUploadJob(job))
}

func (s *Server) handleListRemoteUploads(w http.ResponseWriter, r *http.Request) {
	if s.RemoteUploads == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("remote upload is not configured"))
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeErr(w, http.StatusBadRequest, errors.New("invalid remote upload limit"))
			return
		}
		limit = value
	}
	jobs, err := s.RemoteUploads.List(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, errors.New("failed to list remote upload jobs"))
		return
	}
	out := make([]RemoteUploadJobDTO, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, mapRemoteUploadJob(job))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelRemoteUpload(w http.ResponseWriter, r *http.Request) {
	if s.RemoteUploads == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("remote upload is not configured"))
		return
	}
	id := strings.TrimSpace(routeParam(r, "jobId"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("remote upload job id is required"))
		return
	}
	job, err := s.RemoteUploads.Cancel(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeErr(w, http.StatusNotFound, errors.New("remote upload job not found"))
		case errors.Is(err, catalog.ErrRemoteUploadTerminal):
			writeErr(w, http.StatusConflict, err)
		default:
			writeErr(w, http.StatusInternalServerError, errors.New("failed to cancel remote upload job"))
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, mapRemoteUploadJob(job))
}

func decodeRemoteUploadJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRemoteUploadRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func mapRemoteUploadJob(job *catalog.RemoteUploadJob) RemoteUploadJobDTO {
	if job == nil {
		return RemoteUploadJobDTO{Tags: []string{}}
	}
	title := strings.TrimSpace(job.ResolvedTitle)
	if title == "" {
		title = strings.TrimSpace(job.RequestedTitle)
	}
	tags := append([]string{}, job.Tags...)
	if tags == nil {
		tags = []string{}
	}
	dto := RemoteUploadJobDTO{
		ID:              job.ID,
		State:           job.State,
		SourceLabel:     job.SourceLabel,
		Title:           title,
		Tags:            tags,
		BytesDownloaded: job.BytesDownloaded,
		TotalBytes:      job.TotalBytes,
		CanCancel:       job.CanCancel(),
		CancelRequested: job.CancelRequested,
		Error:           job.ErrorMessage,
		CreatedAt:       formatRemoteUploadTime(job.CreatedAt),
		UpdatedAt:       formatRemoteUploadTime(job.UpdatedAt),
	}
	if job.State == catalog.RemoteUploadCompleted && job.CompletedVideoID != "" {
		dto.CompletedVideoID = job.CompletedVideoID
		dto.VideoHref = "/video/" + pathSegment(job.CompletedVideoID)
	}
	dto.StartedAt = formatRemoteUploadTime(job.StartedAt)
	dto.FinishedAt = formatRemoteUploadTime(job.FinishedAt)
	return dto
}

func formatRemoteUploadTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
