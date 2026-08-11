package main

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/scriptcrawler"
)

func (a *App) scheduleScriptCrawlerCrawl(ctx context.Context, driveID string) bool {
	if a.driveHasActiveWork(driveID) {
		log.Printf("[scriptcrawler] drive=%s has active work, skip duplicate crawl request", driveID)
		return false
	}
	if !a.beginDriveScanOrCrawl(driveID) {
		log.Printf("[scriptcrawler] drive=%s already queued or running, skip duplicate crawl request", driveID)
		return false
	}
	taskCtx, done := a.registerDriveTaskContext(ctx, driveID)

	go func() {
		defer func() {
			a.endDriveScanOrCrawl(driveID)
			done()
		}()
		if a.runScriptCrawlerCrawlWithTaskContext(taskCtx, driveID) {
			a.runCrawlerMigrationAfterManualCrawl(taskCtx, driveID)
		}
	}()
	return true
}

func (a *App) runScriptCrawlerCrawl(ctx context.Context, driveID string) {
	if !a.beginDriveScanOrCrawl(driveID) {
		log.Printf("[scriptcrawler] drive=%s already queued or running, skip direct crawl", driveID)
		return
	}
	defer a.endDriveScanOrCrawl(driveID)
	taskCtx, done := a.registerDriveTaskContext(ctx, driveID)
	defer done()
	a.runScriptCrawlerCrawlWithTaskContext(taskCtx, driveID)
}

func (a *App) runScriptCrawlerCrawlWithTaskContext(ctx context.Context, driveID string) bool {
	if err := ctx.Err(); err != nil {
		log.Printf("[scriptcrawler] drive=%s crawl canceled before start: %v", driveID, err)
		return false
	}
	a.mu.Lock()
	c := a.scriptCrawlers[driveID]
	a.mu.Unlock()
	if c == nil {
		if err := a.ensureDriveAttached(ctx, driveID); err != nil {
			log.Printf("[scriptcrawler] drive=%s attach failed: %v", driveID, err)
			return false
		}
		a.mu.Lock()
		c = a.scriptCrawlers[driveID]
		a.mu.Unlock()
		if c == nil {
			log.Printf("[scriptcrawler] drive=%s crawler not attached", driveID)
			return false
		}
	}

	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil || d == nil {
		log.Printf("[scriptcrawler] drive=%s lookup failed: %v", driveID, err)
		return false
	}
	targetNew := crawlerIntCred(d, "target_new", scriptcrawler.DefaultTargetNew)
	if targetNew <= 0 {
		targetNew = scriptcrawler.DefaultTargetNew
	}

	log.Printf("[scriptcrawler] drive=%s start crawl target_new=%d", driveID, targetNew)
	res, runErr := c.RunOnce(ctx, targetNew)
	if runErr != nil {
		log.Printf("[scriptcrawler] drive=%s crawl failed: %v", driveID, runErr)
	} else if res != nil {
		log.Printf("[scriptcrawler] drive=%s crawl done target=%d candidate_budget=%d total=%d new=%d skipped=%d failed=%d seen_snapshot=%d",
			driveID, res.TargetNew, res.CandidateBudget, res.TotalEntries, res.NewVideos, res.Skipped, res.Failed, res.SeenSnapshot)
	}

	if err := a.updateScriptCrawlerRunState(ctx, driveID, runErr); err != nil {
		log.Printf("[scriptcrawler] drive=%s update last_crawl_at: %v", driveID, err)
	}
	if err := ctx.Err(); err != nil {
		log.Printf("[scriptcrawler] drive=%s crawl canceled after run: %v", driveID, err)
		return false
	}

	a.mu.Lock()
	worker := a.workers[driveID]
	thumbWorker := a.thumbWorkers[driveID]
	fingerprintWorker := a.fingerprintWorkers[driveID]
	a.mu.Unlock()
	a.scheduleFingerprintBackfill(ctx, driveID, fingerprintWorker)
	a.enqueueDriveGeneration(ctx, driveID, worker, thumbWorker)
	return runErr == nil
}

func (a *App) updateScriptCrawlerRunState(ctx context.Context, driveID string, runErr error) error {
	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil {
		return err
	}
	if d.Credentials == nil {
		d.Credentials = make(map[string]string)
	}
	d.Credentials["last_crawl_at"] = strconv.FormatInt(time.Now().Unix(), 10)
	if runErr != nil {
		d.Status = "error"
		d.LastError = runErr.Error()
	} else {
		d.Status = "ok"
		d.LastError = ""
	}
	return a.cat.UpsertDrive(ctx, d)
}

func (a *App) scheduleManualCrawlerUploadMigration(ctx context.Context, driveID string) (bool, string) {
	driveID = strings.TrimSpace(driveID)
	if driveID == "" || a == nil || a.cat == nil {
		return false, "爬虫不存在"
	}
	if a.crawlerUploader == nil {
		return false, "上传迁移器未初始化"
	}
	if a.driveHasActiveWork(driveID) {
		return false, "当前爬虫有正在进行的任务，请稍后重试"
	}
	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil || d == nil || d.Kind != scriptcrawler.Kind {
		return false, "爬虫不存在"
	}
	targetDriveID := strings.TrimSpace(d.Credentials["upload_drive_id"])
	if targetDriveID == "" {
		return false, "请先配置上传网盘"
	}
	assets, err := a.cat.CountCrawlerAssets(ctx, driveID, crawlerCatalogVideoIDPrefixes(d))
	if err != nil {
		log.Printf("[scriptcrawler] drive=%s manual upload count assets: %v", driveID, err)
		return false, "读取待上传视频失败"
	}
	if reason := crawlerUploadAssetBlockReason(d, assets); reason != "" {
		return false, reason
	}
	if err := a.ensureDriveAttached(ctx, driveID); err != nil {
		log.Printf("[scriptcrawler] drive=%s manual upload source attach: %v", driveID, err)
		return false, "爬虫本地存储不可用"
	}
	if err := a.ensureDriveAttached(ctx, targetDriveID); err != nil {
		log.Printf("[scriptcrawler] drive=%s manual upload target=%s attach: %v", driveID, targetDriveID, err)
		return false, "上传网盘不可用：" + err.Error()
	}

	a.crawlerUploadMu.Lock()
	if a.crawlerUploadRunning == nil {
		a.crawlerUploadRunning = make(map[string]bool)
	}
	if a.crawlerUploadRunning[driveID] {
		a.crawlerUploadMu.Unlock()
		return false, "当前爬虫已有上传任务正在运行"
	}
	a.crawlerUploadRunning[driveID] = true
	a.crawlerUploadMu.Unlock()

	taskCtx, done := a.registerDriveTaskContext(ctx, driveID)
	runDone, accepted := a.crawlerUploader.StartDrive(taskCtx, driveID)
	if !accepted {
		done()
		a.crawlerUploadMu.Lock()
		delete(a.crawlerUploadRunning, driveID)
		a.crawlerUploadMu.Unlock()
		return false, "已有其他爬虫上传任务正在运行，请稍后重试"
	}
	log.Printf("[scriptcrawler] drive=%s running manual upload migration target=%s", driveID, targetDriveID)
	go func() {
		defer func() {
			done()
			a.crawlerUploadMu.Lock()
			delete(a.crawlerUploadRunning, driveID)
			a.crawlerUploadMu.Unlock()
		}()
		if err := <-runDone; err != nil {
			log.Printf("[scriptcrawler] drive=%s manual upload migration: %v", driveID, err)
		}
	}()
	return true, ""
}

func crawlerUploadAssetBlockReason(d *catalog.Drive, assets catalog.CrawlerAssetCounts) string {
	if assets.Local <= 0 {
		return "没有待上传的本地视频"
	}
	if assets.Fingerprint.Pending > 0 {
		return "还有待生成的视频指纹"
	}
	if assets.Fingerprint.Failed > 0 {
		return "存在指纹生成失败的视频，请先重试或处理失败项"
	}
	if d != nil && d.TeaserEnabled {
		if assets.Teaser.Pending > 0 {
			return "还有待生成的预览视频"
		}
		if assets.Teaser.Failed > 0 {
			return "存在预览视频生成失败的视频，请先重试或处理失败项"
		}
	}
	return ""
}

func crawlerCatalogVideoIDPrefixes(d *catalog.Drive) []string {
	if d == nil {
		return nil
	}
	return []string{
		scriptcrawler.Kind + "-" + d.ID + "-",
	}
}

func (a *App) runCrawlerMigrationAfterManualCrawl(ctx context.Context, driveID string) {
	if err := ctx.Err(); err != nil {
		log.Printf("[scriptcrawler] drive=%s skip post-crawl migration: %v", driveID, err)
		return
	}
	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil || d == nil {
		log.Printf("[scriptcrawler] drive=%s skip post-crawl migration lookup: %v", driveID, err)
		return
	}
	targetDriveID := strings.TrimSpace(d.Credentials["upload_drive_id"])
	log.Printf("[scriptcrawler] drive=%s waiting for generation queues before post-crawl completion", driveID)
	if err := a.waitDriveGenerationQueuesIdle(ctx, driveID); err != nil {
		log.Printf("[scriptcrawler] drive=%s post-crawl migration wait canceled: %v", driveID, err)
		return
	}
	if err := ctx.Err(); err != nil {
		log.Printf("[scriptcrawler] drive=%s skip post-crawl migration after wait: %v", driveID, err)
		return
	}
	if targetDriveID != "" {
		if a.crawlerUploader == nil {
			log.Printf("[scriptcrawler] drive=%s skip post-crawl migration: migrator not configured", driveID)
		} else {
			log.Printf("[scriptcrawler] drive=%s running post-crawl migration target=%s", driveID, targetDriveID)
			runDone, accepted := a.crawlerUploader.StartDrive(ctx, driveID)
			if !accepted {
				log.Printf("[scriptcrawler] drive=%s skip post-crawl migration: another crawler upload is running", driveID)
			} else if err := <-runDone; err != nil {
				log.Printf("[scriptcrawler] drive=%s post-crawl migration: %v", driveID, err)
			}
		}
	}
	if err := a.restoreScriptCrawlerVideos(ctx, driveID); err != nil {
		log.Printf("[scriptcrawler] drive=%s post-crawl restore: %v", driveID, err)
	}
}

func (a *App) restoreScriptCrawlerVideos(ctx context.Context, driveID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requests, err := a.cat.ListCrawlerRestoreRequests(ctx, driveID)
	if err != nil || len(requests) == 0 {
		return err
	}
	if err := a.ensureDriveAttached(ctx, driveID); err != nil {
		return err
	}
	a.mu.Lock()
	crawler := a.scriptCrawlers[driveID]
	a.mu.Unlock()
	if crawler == nil {
		return nil
	}
	restored, err := crawler.RestoreRequestedVideos(ctx)
	if restored > 0 {
		a.mu.Lock()
		worker := a.workers[driveID]
		thumbWorker := a.thumbWorkers[driveID]
		fingerprintWorker := a.fingerprintWorkers[driveID]
		a.mu.Unlock()
		a.scheduleFingerprintBackfill(ctx, driveID, fingerprintWorker)
		a.enqueueDriveGeneration(ctx, driveID, worker, thumbWorker)
	}
	return err
}

// crawlerIntCred 解析 credentials 中的整数字段，缺省时返回 def。
func crawlerIntCred(d *catalog.Drive, key string, def int) int {
	if d == nil || d.Credentials == nil {
		return def
	}
	raw := strings.TrimSpace(d.Credentials[key])
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
