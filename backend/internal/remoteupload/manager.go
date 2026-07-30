package remoteupload

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/persistence"
	"github.com/video-site/backend/internal/transcode"
	"github.com/video-site/backend/internal/videoname"
)

const (
	defaultDiskReserve = int64(1 << 30)
	defaultIdleTimeout = 120 * time.Second
	retentionPeriod    = 7 * 24 * time.Hour
)

type ValidationError struct {
	message string
}

func (e *ValidationError) Error() string { return e.message }

func validationError(message string) error {
	return &ValidationError{message: message}
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

type CreateInput struct {
	URL   string
	Title string
	Tags  []string
}

type Config struct {
	Catalog         *catalog.Catalog
	UploadDir       string
	FFprobePath     string
	DiskReserve     int64
	IdleTimeout     time.Duration
	OnVideoUploaded func(*catalog.Video)
}

type Manager struct {
	catalog         *catalog.Catalog
	uploadDir       string
	ffprobePath     string
	diskReserve     int64
	idleTimeout     time.Duration
	onVideoUploaded func(*catalog.Video)

	policy         *urlPolicy
	client         *http.Client
	validateURL    func(context.Context, string) (*url.URL, error)
	availableBytes func(string) (int64, error)
	probeFile      func(context.Context, string, string) (transcode.MediaInfo, error)

	startMu sync.Mutex
	started bool
	runCtx  context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	done    chan struct{}

	currentMu     sync.Mutex
	currentID     string
	currentCancel context.CancelFunc
}

func New(cfg Config) (*Manager, error) {
	if cfg.Catalog == nil {
		return nil, errors.New("remote upload: catalog is required")
	}
	cfg.UploadDir = strings.TrimSpace(cfg.UploadDir)
	if cfg.UploadDir == "" {
		return nil, errors.New("remote upload: upload directory is required")
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("remote upload: create upload directory: %w", err)
	}
	if cfg.FFprobePath == "" {
		cfg.FFprobePath = "ffprobe"
	}
	if cfg.DiskReserve <= 0 {
		cfg.DiskReserve = defaultDiskReserve
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}

	policy := newURLPolicy()
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           policy.dialContext,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	m := &Manager{
		catalog:         cfg.Catalog,
		uploadDir:       cfg.UploadDir,
		ffprobePath:     cfg.FFprobePath,
		diskReserve:     cfg.DiskReserve,
		idleTimeout:     cfg.IdleTimeout,
		onVideoUploaded: cfg.OnVideoUploaded,
		policy:          policy,
		wake:            make(chan struct{}, 1),
		done:            make(chan struct{}),
		availableBytes:  diskAvailableBytes,
		probeFile:       transcode.ProbeFile,
	}
	m.validateURL = policy.validate
	m.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return validationError("视频直链重定向次数过多")
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Referer")
			return policy.validateParsed(req.Context(), req.URL)
		},
	}
	return m, nil
}

func (m *Manager) Start(parent context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.started {
		return nil
	}

	refs, err := m.catalog.ListInterruptedRemoteUploadArtifacts(parent)
	if err != nil {
		return fmt.Errorf("remote upload: inspect interrupted jobs: %w", err)
	}
	for _, ref := range refs {
		if err := m.cleanupArtifactRef(ref); err != nil {
			return fmt.Errorf("remote upload: clean interrupted job %s: %w", ref.JobID, err)
		}
	}
	if _, err := m.catalog.RecoverRemoteUploadJobs(parent); err != nil {
		return fmt.Errorf("remote upload: recover jobs: %w", err)
	}
	if err := m.cleanupStrayParts(); err != nil {
		return fmt.Errorf("remote upload: clean stale part files: %w", err)
	}
	if _, err := m.catalog.DeleteExpiredRemoteUploadJobs(parent, time.Now().Add(-retentionPeriod)); err != nil {
		return fmt.Errorf("remote upload: clean expired jobs: %w", err)
	}

	m.runCtx, m.cancel = context.WithCancel(parent)
	m.started = true
	go m.run()
	m.signal()
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.startMu.Lock()
	if !m.started {
		m.startMu.Unlock()
		return nil
	}
	cancel := m.cancel
	done := m.done
	m.startMu.Unlock()

	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Create(ctx context.Context, input CreateInput) (*catalog.RemoteUploadJob, error) {
	u, err := m.validateURL(ctx, input.URL)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(input.Title)
	if title != "" {
		if err := videoname.ValidateUploadTitle(title, ".webm"); err != nil {
			return nil, validationError(err.Error())
		}
	}
	id, err := randomID("remote")
	if err != nil {
		return nil, err
	}
	job, err := m.catalog.CreateRemoteUploadJob(
		ctx,
		id,
		u.String(),
		sourceLabel(u),
		title,
		input.Tags,
	)
	if err != nil {
		return nil, err
	}
	m.signal()
	return job, nil
}

func (m *Manager) List(ctx context.Context, limit int) ([]*catalog.RemoteUploadJob, error) {
	_, _ = m.catalog.DeleteExpiredRemoteUploadJobs(ctx, time.Now().Add(-retentionPeriod))
	return m.catalog.ListRemoteUploadJobs(ctx, limit)
}

func (m *Manager) Cancel(ctx context.Context, id string) (*catalog.RemoteUploadJob, error) {
	job, err := m.catalog.CancelRemoteUploadJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.CancelRequested {
		m.currentMu.Lock()
		if m.currentID == job.ID && m.currentCancel != nil {
			m.currentCancel()
		}
		m.currentMu.Unlock()
	}
	m.signal()
	return job, nil
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) run() {
	defer close(m.done)
	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		if err := m.runCtx.Err(); err != nil {
			return
		}
		select {
		case <-cleanupTicker.C:
			_, _ = m.catalog.DeleteExpiredRemoteUploadJobs(
				context.Background(),
				time.Now().Add(-retentionPeriod),
			)
		default:
		}
		job, err := m.catalog.ClaimNextRemoteUploadJob(m.runCtx)
		if err == nil {
			m.process(job)
			continue
		}
		retryDelay := 30 * time.Second
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			log.Printf("[remote-upload] queue lookup failed")
			retryDelay = time.Second
		}
		retryTimer := time.NewTimer(retryDelay)
		select {
		case <-m.runCtx.Done():
			retryTimer.Stop()
			return
		case <-m.wake:
			retryTimer.Stop()
		case <-cleanupTicker.C:
			retryTimer.Stop()
			_, _ = m.catalog.DeleteExpiredRemoteUploadJobs(
				context.Background(),
				time.Now().Add(-retentionPeriod),
			)
		case <-retryTimer.C:
		}
	}
}

func (m *Manager) process(job *catalog.RemoteUploadJob) {
	jobCtx, cancel := context.WithCancel(m.runCtx)
	m.currentMu.Lock()
	m.currentID = job.ID
	m.currentCancel = cancel
	m.currentMu.Unlock()

	err := m.processJob(jobCtx, job)
	cancel()
	m.currentMu.Lock()
	if m.currentID == job.ID {
		m.currentID = ""
		m.currentCancel = nil
	}
	m.currentMu.Unlock()
	if err == nil {
		return
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	current, getErr := m.catalog.GetRemoteUploadJob(cleanupCtx, job.ID)
	if getErr == nil {
		_ = m.cleanupArtifactRef(catalog.RemoteUploadCleanupRef{
			JobID:     current.ID,
			TempFile:  current.TempFile,
			FinalFile: current.FinalFile,
		})
	}

	if m.runCtx.Err() != nil {
		if requeueErr := m.catalog.RequeueRemoteUploadOnShutdown(cleanupCtx, job.ID); requeueErr != nil {
			log.Printf("[remote-upload] job=%s shutdown requeue failed", job.ID)
		}
		return
	}
	if errors.Is(err, catalog.ErrRemoteUploadCanceled) ||
		errors.Is(err, context.Canceled) ||
		(current != nil && current.CancelRequested) {
		if cancelErr := m.catalog.MarkRemoteUploadCanceled(cleanupCtx, job.ID); cancelErr != nil &&
			!errors.Is(cancelErr, catalog.ErrRemoteUploadTerminal) {
			log.Printf("[remote-upload] job=%s cancellation cleanup failed", job.ID)
		}
		return
	}

	message := publicJobError(err)
	if failErr := m.catalog.FailRemoteUploadJob(cleanupCtx, job.ID, message); failErr != nil &&
		!errors.Is(failErr, catalog.ErrRemoteUploadTerminal) {
		log.Printf("[remote-upload] job=%s failure state update failed", job.ID)
	}
	log.Printf("[remote-upload] job=%s source=%s failed: %s", job.ID, job.SourceLabel, message)
}

func (m *Manager) processJob(ctx context.Context, job *catalog.RemoteUploadJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	part, err := os.CreateTemp(m.uploadDir, ".remote-"+job.ID+"-*.part")
	if err != nil {
		return taskError("无法创建下载临时文件")
	}
	partPath := part.Name()
	partName := filepath.Base(partPath)
	if err := m.catalog.SetRemoteUploadTempFile(ctx, job.ID, partName); err != nil {
		_ = part.Close()
		_ = os.Remove(partPath)
		return err
	}

	metadata, err := m.download(ctx, job, part)
	closeErr := part.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return taskError("无法保存下载文件")
	}
	if metadata.Size <= 0 {
		return taskError("远程视频为空文件")
	}
	if err := os.Chmod(partPath, 0o644); err != nil {
		return taskError("无法设置下载文件权限")
	}
	if err := m.catalog.UpdateRemoteUploadProgress(ctx, job.ID, metadata.Size, metadata.Total); err != nil {
		return err
	}
	if err := m.catalog.TransitionRemoteUploadJob(
		ctx,
		job.ID,
		catalog.RemoteUploadDownloading,
		catalog.RemoteUploadValidating,
	); err != nil {
		return err
	}

	info, err := m.probeFile(ctx, m.ffprobePath, partPath)
	if err != nil || len(info.VideoCodecs) == 0 {
		return taskError("下载内容不是有效的视频文件")
	}
	ext, err := supportedExtension(info, metadata)
	if err != nil {
		return err
	}
	title, err := resolveTitle(job, metadata, ext)
	if err != nil {
		return err
	}
	if err := m.catalog.TransitionRemoteUploadJob(
		ctx,
		job.ID,
		catalog.RemoteUploadValidating,
		catalog.RemoteUploadSaving,
	); err != nil {
		return err
	}

	persistence.RLock()
	mutationLocked := true
	defer func() {
		if mutationLocked {
			persistence.RUnlock()
		}
	}()
	video, err := m.publishFile(ctx, job.ID, partName, title, ext, metadata.Size)
	if err != nil {
		return err
	}
	autoTags, err := m.catalog.MatchTagAssignments(
		ctx,
		video.Title,
		video.FileName,
		video.Author,
		"",
	)
	if err != nil {
		return taskError("无法匹配视频标签")
	}
	if err := m.catalog.FinalizeRemoteUpload(ctx, job.ID, video, job.Tags, autoTags); err != nil {
		return err
	}
	persistence.RUnlock()
	mutationLocked = false
	saved, err := m.catalog.GetVideo(ctx, video.ID)
	if err == nil {
		video = saved
	}
	if m.onVideoUploaded != nil {
		m.onVideoUploaded(video)
	}
	return nil
}

type downloadMetadata struct {
	Size               int64
	Total              int64
	ContentDisposition string
	ContentType        string
	FinalURL           *url.URL
	OriginalURL        *url.URL
}

func (m *Manager) download(
	ctx context.Context,
	job *catalog.RemoteUploadJob,
	dst *os.File,
) (downloadMetadata, error) {
	u, err := m.validateURL(ctx, job.SourceURL)
	if err != nil {
		return downloadMetadata{}, err
	}
	bodyCtx, cancelBody := context.WithCancel(ctx)
	defer cancelBody()
	req, err := http.NewRequestWithContext(bodyCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return downloadMetadata{}, taskError("无法创建远程下载请求")
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return downloadMetadata{}, ctx.Err()
		}
		return downloadMetadata{}, taskError("远程视频连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return downloadMetadata{}, taskError(
			"远程服务器返回 HTTP " + strconv.Itoa(response.StatusCode),
		)
	}
	contentType := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Type")))
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = strings.ToLower(mediaType)
	}
	if isHLSContentType(contentType) ||
		(response.Request != nil && response.Request.URL != nil && isHLSPath(response.Request.URL.EscapedPath())) {
		return downloadMetadata{}, taskError("不支持 HLS/m3u8 链接")
	}

	total := response.ContentLength
	if total < 0 {
		total = 0
	}
	if total > 0 {
		if err := m.ensureDiskSpace(total); err != nil {
			return downloadMetadata{}, err
		}
	}
	if err := m.catalog.UpdateRemoteUploadProgress(ctx, job.ID, 0, total); err != nil {
		return downloadMetadata{}, err
	}

	var idleFired atomic.Bool
	watchdog := time.AfterFunc(m.idleTimeout, func() {
		idleFired.Store(true)
		cancelBody()
	})
	defer watchdog.Stop()

	buffer := make([]byte, 1<<20)
	var downloaded int64
	lastProgress := time.Now()
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			watchdog.Reset(m.idleTimeout)
			if err := m.ensureDiskSpace(int64(n)); err != nil {
				return downloadMetadata{}, err
			}
			written, writeErr := dst.Write(buffer[:n])
			if writeErr != nil || written != n {
				return downloadMetadata{}, taskError("无法写入下载文件")
			}
			downloaded += int64(n)
			if time.Since(lastProgress) >= time.Second {
				if err := m.catalog.UpdateRemoteUploadProgress(ctx, job.ID, downloaded, total); err != nil {
					return downloadMetadata{}, err
				}
				lastProgress = time.Now()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if idleFired.Load() {
				return downloadMetadata{}, taskError(
					fmt.Sprintf("远程服务器连续 %d 秒未发送数据", int(m.idleTimeout.Seconds())),
				)
			}
			if ctx.Err() != nil {
				return downloadMetadata{}, ctx.Err()
			}
			return downloadMetadata{}, taskError("远程视频下载中断")
		}
	}
	if err := dst.Sync(); err != nil {
		return downloadMetadata{}, taskError("无法同步下载文件")
	}

	finalURL := u
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL
	}
	return downloadMetadata{
		Size:               downloaded,
		Total:              total,
		ContentDisposition: response.Header.Get("Content-Disposition"),
		ContentType:        contentType,
		FinalURL:           finalURL,
		OriginalURL:        u,
	}, nil
}

func (m *Manager) ensureDiskSpace(nextWrite int64) error {
	available, err := m.availableBytes(m.uploadDir)
	if err != nil {
		return taskError("无法检查上传目录可用空间")
	}
	if available < m.diskReserve || nextWrite > available-m.diskReserve {
		return taskError("磁盘空间不足，无法在保留配置的安全余量后继续下载")
	}
	return nil
}

func (m *Manager) publishFile(
	ctx context.Context,
	jobID, partName, title, ext string,
	size int64,
) (*catalog.Video, error) {
	partPath, err := m.artifactPath(partName)
	if err != nil {
		return nil, taskError("下载临时文件路径无效")
	}
	uploadID, err := randomID("upload")
	if err != nil {
		return nil, taskError("无法生成视频编号")
	}

	for attempt := 0; attempt < 20; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		collision := attempt > 0
		if attempt > 1 {
			uploadID, err = randomID("upload")
			if err != nil {
				return nil, taskError("无法生成视频编号")
			}
		}
		storedName := videoname.UploadFileName(title, ext, uploadID, collision)
		resolvedTitle := videoname.TitleFromFileName(storedName)
		videoID := localupload.DriveID + "-" + uploadID
		if err := m.catalog.PrepareRemoteUploadSaving(
			ctx,
			jobID,
			partName,
			storedName,
			videoID,
			resolvedTitle,
		); err != nil {
			return nil, err
		}
		finalPath, err := m.artifactPath(storedName)
		if err != nil {
			return nil, taskError("最终视频路径无效")
		}
		if err := os.Link(partPath, finalPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, taskError("无法保存最终视频文件")
		}
		if err := os.Remove(partPath); err != nil {
			_ = os.Remove(finalPath)
			return nil, taskError("无法完成视频文件落盘")
		}
		if err := ctx.Err(); err != nil {
			_ = os.Remove(finalPath)
			return nil, err
		}
		now := time.Now()
		return &catalog.Video{
			ID:            videoID,
			DriveID:       localupload.DriveID,
			FileID:        storedName,
			FileName:      storedName,
			Title:         resolvedTitle,
			Author:        "用户上传",
			Size:          size,
			Ext:           strings.TrimPrefix(ext, "."),
			PreviewStatus: "pending",
			PublishedAt:   now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}, nil
	}
	return nil, taskError("同名视频文件过多，无法生成可用文件名")
}

func supportedExtension(info transcode.MediaInfo, metadata downloadMetadata) (string, error) {
	formatNames := make(map[string]bool)
	for _, name := range strings.Split(strings.ToLower(info.FormatName), ",") {
		formatNames[strings.TrimSpace(name)] = true
	}
	candidate := preferredSourceExtension(metadata)
	switch {
	case formatNames["avi"]:
		return ".avi", nil
	case formatNames["matroska"] || formatNames["webm"]:
		if candidate == ".webm" || metadata.ContentType == "video/webm" {
			return ".webm", nil
		}
		return ".mkv", nil
	case formatNames["mov"] || formatNames["mp4"]:
		if candidate == ".mov" || metadata.ContentType == "video/quicktime" {
			return ".mov", nil
		}
		return ".mp4", nil
	default:
		return "", taskError("无法确认下载视频的受支持格式")
	}
}

func preferredSourceExtension(metadata downloadMetadata) string {
	for _, name := range []string{
		contentDispositionFileName(metadata.ContentDisposition),
		urlFileName(metadata.FinalURL),
	} {
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".avi", ".mkv", ".mov", ".mp4", ".webm":
			return ext
		}
	}
	switch metadata.ContentType {
	case "video/x-msvideo", "video/avi":
		return ".avi"
	case "video/x-matroska":
		return ".mkv"
	case "video/quicktime":
		return ".mov"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}

func resolveTitle(
	job *catalog.RemoteUploadJob,
	metadata downloadMetadata,
	ext string,
) (string, error) {
	candidates := []string{
		strings.TrimSpace(job.RequestedTitle),
		titleFromRemoteFileName(contentDispositionFileName(metadata.ContentDisposition)),
		titleFromRemoteFileName(urlFileName(metadata.FinalURL)),
		titleFromRemoteFileName(urlFileName(metadata.OriginalURL)),
	}
	for _, title := range candidates {
		if title == "" {
			continue
		}
		if err := videoname.ValidateUploadTitle(title, ext); err != nil {
			return "", taskError(err.Error())
		}
		return title, nil
	}
	return "", taskError("无法从直链生成视频名，请手动填写视频名")
}

func contentDispositionFileName(value string) string {
	_, params, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return baseRemoteFileName(params["filename"])
}

func urlFileName(u *url.URL) string {
	if u == nil {
		return ""
	}
	name := path.Base(u.EscapedPath())
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	return baseRemoteFileName(name)
}

func baseRemoteFileName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, `\`, "/"))
	name = path.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	return strings.TrimSpace(name)
}

func titleFromRemoteFileName(name string) string {
	name = baseRemoteFileName(name)
	if name == "" {
		return ""
	}
	ext := filepath.Ext(name)
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return strings.TrimSpace(name)
}

func isHLSContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "application/vnd.apple.mpegurl",
		"application/x-mpegurl",
		"audio/mpegurl",
		"audio/x-mpegurl":
		return true
	default:
		return false
	}
}

func (m *Manager) cleanupArtifactRef(ref catalog.RemoteUploadCleanupRef) error {
	tempPath, tempErr := m.artifactPath(ref.TempFile)
	finalPath, finalErr := m.artifactPath(ref.FinalFile)

	tempInfo, tempStatErr := os.Stat(tempPath)
	finalInfo, finalStatErr := os.Stat(finalPath)
	tempExists := tempErr == nil && tempStatErr == nil
	finalExists := finalErr == nil && finalStatErr == nil

	if finalExists {
		removeFinal := !tempExists
		if tempExists && os.SameFile(tempInfo, finalInfo) {
			removeFinal = true
		}
		if removeFinal {
			if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if tempExists {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *Manager) cleanupStrayParts() error {
	entries, err := os.ReadDir(m.uploadDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), ".remote-") ||
			!strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		path, err := m.artifactPath(entry.Name())
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *Manager) artifactPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("empty artifact name")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", errors.New("invalid artifact name")
	}
	root, err := filepath.Abs(m.uploadDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	if target == root || !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", errors.New("artifact escapes upload directory")
	}
	return target, nil
}

type taskError string

func (e taskError) Error() string { return string(e) }

func publicJobError(err error) string {
	var safe taskError
	if errors.As(err, &safe) {
		return safe.Error()
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation.Error()
	}
	return "后台任务处理失败"
}

func randomID(prefix string) (string, error) {
	var suffix [6]byte
	if _, err := crand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s-%d-%s",
		prefix,
		time.Now().UnixNano(),
		hex.EncodeToString(suffix[:]),
	), nil
}
