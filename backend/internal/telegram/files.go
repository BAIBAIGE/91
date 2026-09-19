package telegram

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/telegramstorage"
	"github.com/video-site/backend/internal/mediaimport"
	"github.com/video-site/backend/internal/persistence"
)

// Check both new messages and persisted jobs before getFile can start a download.
func validateVideoSize(size, limit int64) error {
	if size <= 0 {
		return errors.New("无法确认视频大小，已拒绝下载，请重新发送视频")
	}
	if size > limit {
		return errors.New("视频超过配置的单文件大小限制")
	}
	return nil
}

func (s *Service) Fetch(ctx context.Context, j *catalog.RemoteUploadJob, progress func(string, int64, int64) error) (mediaimport.SourceFile, error) {
	// A configuration change cancels downloads using the old bot identity.
	if s.runCtx != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(s.runCtx, cancel)
		defer stop()
		defer cancel()
	}
	var out mediaimport.SourceFile
	source, err := catalog.DecodeTelegramSource(j)
	if err != nil {
		return out, &mediaimport.SourceError{Message: "TG 文件信息无效"}
	}
	if source.BotID != s.BotID() {
		return out, &mediaimport.SourceError{Message: "请连接提交任务的机器人", RetryAfter: time.Minute, WaitForAvailability: true}
	}
	if !s.Allowed(source.SenderID) {
		return out, &mediaimport.SourceError{Message: "发送者已不在允许用户列表中"}
	}
	size := source.Size
	if err := validateVideoSize(size, s.cfg.MaxFileSizeBytes); err != nil {
		return out, &mediaimport.SourceError{Message: err.Error()}
	}
	// Persist the destination before moving bytes, including across restarts.
	if err = persistence.RLockContext(ctx); err != nil {
		return out, err
	}
	local, localErr := s.cat.TelegramLocalFile(ctx, j.ID+".media")
	if errors.Is(localErr, sql.ErrNoRows) {
		local, err = s.cat.ReserveTelegramLocalFile(ctx, catalog.TelegramLocalFile{FileID: j.ID + ".media", JobID: j.ID})
	} else {
		err = localErr
	}
	if err == nil {
		err = os.MkdirAll(filepath.Join(s.cfg.LocalFilesRoot, "library"), 0750)
	}
	persistence.RUnlock()
	if err != nil {
		return out, &mediaimport.SourceError{Message: "无法准备 TG 视频存储目录", WaitForAvailability: true}
	}
	destination, err := telegramstorage.Path(s.cfg.LocalFilesRoot, local.FileID)
	if err != nil {
		return out, &mediaimport.SourceError{Message: "TG 视频存储目录不可用", WaitForAvailability: true}
	}
	if info, e := os.Stat(destination); e == nil {
		if info.Size() <= 0 || info.Size() > s.cfg.MaxFileSizeBytes || source.Size != info.Size() {
			return out, &mediaimport.SourceError{Message: "TG 视频大小无效"}
		}
		if err = syncLibrary(destination, filepath.Dir(destination)); err != nil {
			return out, &mediaimport.SourceError{Message: "无法确认 TG 视频已落盘", WaitForAvailability: true}
		}
		return acquiredFile(source, local, destination, info.Size()), nil
	}
	free, err := mediaimport.AvailableBytes(s.cfg.LocalFilesRoot)
	if err != nil || free < s.reserve || size > free-s.reserve {
		return out, &mediaimport.SourceError{Message: "TG 共享目录空间不足或不可用", WaitForAvailability: true}
	}
	if err = progress("telegram_download", 0, source.Size); err != nil {
		return out, err
	}
	fileID, err := s.cat.LatestTelegramFileID(ctx, source)
	if err != nil {
		return out, err
	}
	s.mu.RLock()
	c := s.client
	s.mu.RUnlock()
	if c == nil {
		return out, &mediaimport.SourceError{Message: "Telegram 尚未连接", RetryAfter: time.Minute}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.FetchTimeoutSeconds)*time.Second)
	defer cancel()
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err = c.call(fetchCtx, "getFile", map[string]string{"file_id": fileID}, &file); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			if apiErr.Code == 401 || apiErr.Code == 403 {
				s.setState("error", errors.New("机器人鉴权失败，请检查 Token"))
			}
			return out, &mediaimport.SourceError{Message: apiErr.Error(), RetryAfter: apiErr.RetryAfter}
		}
		return out, &mediaimport.SourceError{Message: "TG 文件获取超时或响应无效", RetryAfter: 10 * time.Second}
	}
	localPath, err := mapAPIFilePath(s.cfg.APIFilesRoot, s.cfg.LocalFilesRoot, file.Path)
	if err != nil {
		return out, &mediaimport.SourceError{Message: "TG 返回的文件路径不在 Bot API 文件目录内，请检查目录映射"}
	}
	f, info, err := openCachedFile(s.cfg.LocalFilesRoot, localPath)
	if err != nil {
		return out, &mediaimport.SourceError{Message: "TG 返回的文件路径不可用，请检查共享目录挂载"}
	}
	defer f.Close()
	libraryRoot, _ := filepath.EvalSymlinks(filepath.Join(s.cfg.LocalFilesRoot, "library"))
	if rel, e := filepath.Rel(libraryRoot, f.Name()); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return out, &mediaimport.SourceError{Message: "TG 返回了已接管的视频路径"}
	}
	if info.Size() <= 0 || info.Size() > s.cfg.MaxFileSizeBytes {
		return out, &mediaimport.SourceError{Message: "视频为空或超过当前大小限制"}
	}
	if info.Size() != source.Size || (file.Size > 0 && info.Size() != file.Size) {
		return out, &mediaimport.SourceError{Message: "TG 文件大小不一致，请重新获取", RetryAfter: 10 * time.Second}
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// Both locations must be on the same filesystem. Never fall back to copying.
	if err = persistence.RLockContext(ctx); err != nil {
		return out, err
	}
	defer persistence.RUnlock()
	current, e := os.Lstat(f.Name())
	if e != nil || !os.SameFile(info, current) {
		return out, &mediaimport.SourceError{Message: "TG 下载文件发生变化，请重试", RetryAfter: time.Second}
	}
	if err = f.Close(); err != nil {
		return out, &mediaimport.SourceError{Message: "无法关闭 TG 下载文件"}
	}
	if err = os.Rename(f.Name(), destination); err != nil {
		return out, &mediaimport.SourceError{Message: "无法接管 TG 视频，请检查共享目录写入权限及 library 是否位于同一文件系统", WaitForAvailability: true}
	}
	if err = syncLibrary(destination, filepath.Dir(destination), filepath.Dir(f.Name())); err != nil {
		return out, &mediaimport.SourceError{Message: "无法确认 TG 视频已落盘", WaitForAvailability: true}
	}
	if err = progress("telegram_download", info.Size(), info.Size()); err != nil {
		return out, err
	}
	return acquiredFile(source, local, destination, info.Size()), nil
}
func openCachedFile(root, path string) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("expected local Bot API absolute path")
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, err
	}
	rel, err := filepath.Rel(base, actual)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return nil, nil, errors.New("file outside cache")
	}
	before, err := os.Stat(actual)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, errors.New("cache file is not regular")
	}
	f, err := os.Open(actual)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		f.Close()
		return nil, nil, errors.New("cache file changed")
	}
	return f, after, nil
}

func acquiredFile(source catalog.TelegramSource, local catalog.TelegramLocalFile, path string, size int64) mediaimport.SourceFile {
	name := source.FileName
	if name == "" {
		name = "telegram-video.mp4"
	}
	return mediaimport.SourceFile{Name: name, MIME: source.MIME, Size: size, Path: path, DriveID: telegramstorage.DriveID, FileID: local.FileID}
}
func (s *Service) Discard(ctx context.Context, j *catalog.RemoteUploadJob) error {
	return telegramstorage.New(s.cat, func() string { return s.cfg.LocalFilesRoot }).Remove(ctx, j.ID+".media")
}
