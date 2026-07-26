// transcode-one 对单条视频立即执行浏览器兼容性转码，走与管理后台
// "开始转码"完全相同的流程（远程探测 → 需要时下载转码 → 上传回网盘
// → 写回 transcode_status）。用于紧急修复单个黑屏视频，不必等整盘
// 队列轮到它。目前只支持 p115 盘。
//
// 用法（在 backend 目录下）：
//
//	go run ./cmd/transcode-one <videoID> [dbPath]
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/p115"
	"github.com/video-site/backend/internal/transcode"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: transcode-one <videoID> [dbPath]")
	}
	videoID := os.Args[1]
	dbPath := "data/video-site.db"
	if len(os.Args) > 2 {
		dbPath = os.Args[2]
	}

	ctx := context.Background()
	cat, err := catalog.Open(dbPath)
	if err != nil {
		log.Fatalf("open catalog: %v", err)
	}

	v, err := cat.GetVideo(ctx, videoID)
	if err != nil {
		log.Fatalf("get video %s: %v", videoID, err)
	}
	log.Printf("video: %s (%s, ext=%s, transcode_status=%q)", v.Title, v.FileName, v.Ext, v.TranscodeStatus)

	d, err := cat.GetDrive(ctx, v.DriveID)
	if err != nil {
		log.Fatalf("get drive %s: %v", v.DriveID, err)
	}
	if d.Kind != "p115" {
		log.Fatalf("only p115 drives are supported, drive %s is %q", d.ID, d.Kind)
	}
	// 与服务端 transcodeWorkDir 一致：挂在数据目录下，避免 /tmp（tmpfs）
	// 放不下整个原片。
	workDir := filepath.Join(filepath.Dir(dbPath), "transcode-tmp")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		log.Fatalf("create work dir: %v", err)
	}

	drv := p115.New(p115.Config{
		ID:            d.ID,
		Cookie:        d.Credentials["cookie"],
		RootID:        d.RootID,
		UploadTempDir: workDir,
	})
	if err := drv.Init(ctx); err != nil {
		log.Fatalf("init drive: %v", err)
	}

	worker := transcode.NewWorker(transcode.Config{WorkDir: workDir}, cat, drv)
	worker.Run(ctx, []*catalog.Video{v})

	after, err := cat.GetVideo(ctx, videoID)
	if err != nil {
		log.Fatalf("re-read video: %v", err)
	}
	log.Printf("done: transcode_status=%q transcoded_file_id=%q error=%q",
		after.TranscodeStatus, after.TranscodedFileID, after.TranscodeError)
}
