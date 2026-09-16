// Package telegramupload moves already-imported Telegram videos to cloud
// storage without changing their logical video identity.
package telegramupload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/drives/telegramstorage"
	"github.com/video-site/backend/internal/persistence"
)

type localSource interface {
	drives.LocalFileProvider
	drives.Remover
}

type Config struct {
	Catalog         *catalog.Catalog
	Target          drives.Drive
	LocalDirectory  string
	TargetDirectory string
	OnMigrated      func(*catalog.Video)
}

// Run makes one cancellable sweep. Failed videos remain local and are retried
// by the next scheduled run. Only durable Telegram imports are selected.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Catalog == nil || cfg.Target == nil {
		return errors.New("TG 转存服务未配置")
	}
	uploader, ok := cfg.Target.(drives.Uploader)
	if !ok {
		return errors.New("目标网盘不支持上传")
	}
	parent := ""
	var existing map[string][]drives.Entry
	after := ""
	failed, completed := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		videos, err := cfg.Catalog.ListLocalTelegramVideos(ctx, after, 100)
		if err != nil {
			return errors.New("无法读取待转存的 TG 视频")
		}
		if len(videos) == 0 {
			break
		}
		if parent == "" {
			parent, err = uploader.EnsureDir(ctx, cfg.TargetDirectory)
			if err != nil || strings.TrimSpace(parent) == "" {
				return errors.New("无法创建或读取 TG 转存目标目录")
			}
			entries, err := cfg.Target.List(ctx, parent)
			if err != nil {
				return errors.New("无法检查目标目录中已有的 TG 文件")
			}
			existing = make(map[string][]drives.Entry)
			for _, e := range entries {
				existing[e.Name] = append(existing[e.Name], e)
			}
		}
		for _, v := range videos {
			after = v.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := transferOne(ctx, cfg, uploader, parent, existing, v); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failed++
				log.Printf("[telegram-upload] video=%s failed: %v", v.ID, err)
				// Provider rate limits should stop this sweep rather than send
				// the rest of the library into the same failing service.
				if _, limited := drives.RateLimitRetryAfter(err); limited {
					return errors.New("TG 转存受到网盘限流，本轮已停止，下次定时任务重试")
				}
				continue
			}
			completed++
			if cfg.OnMigrated != nil {
				if saved, err := cfg.Catalog.GetVideo(ctx, v.ID); err == nil {
					cfg.OnMigrated(saved)
				}
			}
			log.Printf("[telegram-upload] video=%s stored on drive=%s", v.ID, cfg.Target.ID())
		}
	}
	log.Printf("[telegram-upload] finished: completed=%d failed=%d", completed, failed)
	if failed > 0 {
		return fmt.Errorf("%d 个 TG 视频转存失败，请检查网盘连接和本地文件；下次定时任务会重试", failed)
	}
	return nil
}

func transferOne(ctx context.Context, cfg Config, uploader drives.Uploader, parent string, existing map[string][]drives.Entry, v *catalog.Video) error {
	if v.FileID == "" || filepath.Base(v.FileID) != v.FileID || strings.ContainsAny(v.FileID, "\\\x00") {
		return errors.New("本地视频路径无效")
	}
	var storage localSource = localupload.New(cfg.LocalDirectory)
	if v.DriveID == telegramstorage.DriveID {
		storage = telegramstorage.New(cfg.Catalog)
	}
	localPath, err := storage.LocalPath(ctx, v.FileID)
	if err != nil {
		return errors.New("无法读取本地视频位置")
	}
	info, err := os.Lstat(localPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != v.Size {
		return errors.New("本地视频不存在或大小不一致")
	}
	// Local filenames can be reused after migration. Include the immutable
	// video ID so distinct same-title imports never bind to each other's file.
	name := destinationName(v)
	fileID := ""
	if matches := existing[name]; len(matches) > 0 {
		if len(matches) != 1 || matches[0].IsDir || matches[0].Size != info.Size() {
			return errors.New("目标目录存在冲突的同名文件，请先处理后重试")
		}
		fileID = matches[0].ID
		if fileID == "" {
			return errors.New("目标文件标识无效")
		}
	} else {
		f, err := os.Open(localPath)
		if err != nil {
			return errors.New("无法读取本地视频")
		}
		opened, statErr := f.Stat()
		if statErr != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
			f.Close()
			return errors.New("本地视频在读取期间发生变化")
		}
		fileID, err = uploader.Upload(ctx, parent, name, f, info.Size())
		closeErr := f.Close()
		if err != nil {
			if delay, limited := drives.RateLimitRetryAfter(err); limited {
				return &drives.RateLimitError{RetryAfter: delay, Err: errors.New("网盘上传受到限流")}
			}
			return errors.New("网盘上传失败")
		}
		if closeErr != nil || strings.TrimSpace(fileID) == "" {
			return errors.New("网盘上传未返回有效的文件结果")
		}
		// Remember the remote write even if its verification/catalog commit
		// fails. A subsequent sweep rebuilds this index from the cloud drive.
		existing[name] = []drives.Entry{{ID: fileID, Name: name, Size: info.Size()}}
	}
	remote, err := cfg.Target.Stat(ctx, fileID)
	if err != nil || remote == nil || remote.IsDir || remote.Size != info.Size() {
		return errors.New("无法确认网盘文件完整，本地来源保持不变")
	}
	link, err := cfg.Target.StreamURL(ctx, fileID)
	if err != nil || link == nil || strings.TrimSpace(link.URL) == "" {
		return errors.New("无法取得网盘播放地址，本地来源保持不变")
	}
	if err := persistence.RLockContext(ctx); err != nil {
		return err
	}
	defer persistence.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cfg.Catalog.MigrateVideoToDrive(ctx, v.ID, catalog.VideoDriveMigration{
		SourceDriveID: v.DriveID, SourceFileID: v.FileID,
		DriveID: cfg.Target.ID(), FileID: fileID, ContentHash: remote.Hash,
		ParentID: parent, DirName: path.Base(cfg.TargetDirectory), FileName: name,
	}); err != nil {
		return errors.New("视频记录已变化或保存失败，本地文件已保留")
	}
	current, err := os.Lstat(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return errors.New("视频已转存，但本地文件发生变化，已保留供检查")
	}
	if err := storage.Remove(ctx, v.FileID); err != nil {
		return errors.New("视频已转存，但无法删除本地副本")
	}
	return nil
}

func destinationName(v *catalog.Video) string {
	name := v.FileName
	if name == "" {
		name = v.FileID
	}
	ext := filepath.Ext(name)
	title := strings.TrimSuffix(name, ext)
	id := sha256.Sum256([]byte(v.ID))
	suffix := fmt.Sprintf("-%x", id[:12])
	for len(title)+len(suffix)+len(ext) > 240 && len(title) > 0 {
		_, size := utf8.DecodeLastRuneInString(title)
		title = title[:len(title)-size]
	}
	return title + suffix + ext
}
