package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/drives/scriptcrawler"
	"github.com/video-site/backend/internal/fingerprint"
	"github.com/video-site/backend/internal/persistence"
	"github.com/video-site/backend/internal/preview"
	"github.com/video-site/backend/internal/scanner"
)

// scheduleScan admits an asynchronous scan for one drive. Different drives can
// scan concurrently, while each drive shares one operation gate with its
// generation and configuration tasks.
func (a *App) scheduleScan(ctx context.Context, driveID string) bool {
	if a.driveHasActiveWork(driveID) {
		log.Printf("[scan] drive=%s has active work, skip duplicate request", driveID)
		return false
	}
	taskCtx, done, admitted := a.registerDriveTaskContext(ctx, driveID, driveTaskScopeScan)
	if !admitted {
		log.Printf("[scan] drive=%s configuration update in progress, reject scan", driveID)
		return false
	}
	if !a.beginDriveScanOrCrawl(driveID) {
		done()
		log.Printf("[scan] drive=%s already queued or running, skip duplicate request", driveID)
		return false
	}

	go func() {
		defer func() {
			a.endDriveScanOrCrawl(driveID)
			done()
		}()
		a.runScanWithTaskContext(taskCtx, driveID)
	}()
	return true
}

// runScan is the synchronous entry point used by the nightly pipeline.
func (a *App) runScan(ctx context.Context, driveID string) {
	taskCtx, done, admitted := a.registerDriveTaskContext(ctx, driveID, driveTaskScopeScan)
	if !admitted {
		log.Printf("[scan] drive=%s configuration update in progress, reject direct scan", driveID)
		return
	}
	defer done()
	if !a.beginDriveScanOrCrawl(driveID) {
		log.Printf("[scan] drive=%s already queued or running, skip direct scan", driveID)
		return
	}
	defer a.endDriveScanOrCrawl(driveID)
	a.runScanWithTaskContext(taskCtx, driveID)
}

func (a *App) runScanWithTaskContext(ctx context.Context, driveID string) {
	if err := ctx.Err(); err != nil {
		log.Printf("[scan] drive=%s canceled before start: %v", driveID, err)
		return
	}
	if err := a.ensureDriveAttached(ctx, driveID); err != nil {
		log.Printf("[scan] drive=%s attach failed: %v", driveID, err)
		return
	}
	drv, ok := a.registry.Get(driveID)
	if !ok {
		log.Printf("[scan] drive=%s not attached", driveID)
		return
	}
	driveConfig, err := a.activeDriveConfig(ctx, driveID)
	if err != nil {
		log.Printf("[scan] get active drive config %s: %v", driveID, err)
		return
	}
	// Skip-directory policy is one deliberate first stage: exact ancestry cleanup
	// and legacy subtree discovery both finish (or record retryable failures)
	// before normal discovery starts.
	rateLimitBudget := scanner.NewRateLimitBudget()
	if err := a.cleanupSkippedDriveVideos(ctx, drv, driveConfig, rateLimitBudget); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Printf("[skip-cleanup] drive=%s canceled: %v", driveID, ctxErr)
			return
		}
		if errors.Is(err, scanner.ErrRateLimitBudgetExhausted) {
			log.Printf("[skip-cleanup] drive=%s rate-limit retry budget exhausted: %v", driveID, err)
			return
		}
		log.Printf("[skip-cleanup] drive=%s error; continuing scan: %v", driveID, err)
	}

	result, err := a.scanDrive(ctx, drv, driveConfig, rateLimitBudget)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Printf("[scan] drive=%s canceled: %v", driveID, ctxErr)
		} else if errors.Is(err, scanner.ErrRateLimitBudgetExhausted) {
			log.Printf("[scan] drive=%s rate-limit retry budget exhausted: %v", driveID, err)
		} else {
			log.Printf("[scan] drive=%s error: %v", driveID, err)
		}
		return
	}
	if err := ctx.Err(); err != nil {
		log.Printf("[scan] drive=%s canceled after reconciliation: %v", driveID, err)
		return
	}

	stats := result.Stats
	log.Printf(
		"[scan] drive=%s done scanned=%d added=%d updated=%d duplicates=%d tombstoned=%d enumerated_dirs=%d failed_dirs=%d excluded_dirs=%d errors=%d",
		driveID, stats.Scanned, stats.Added, result.Updated, result.Duplicates,
		result.Tombstoned, len(result.Snapshot.EnumeratedDirIDs), len(result.Snapshot.FailedDirIDs),
		len(result.Snapshot.ExcludedDirIDs), stats.Errors,
	)
	if err := a.cleanupScanSnapshot(ctx, drv, result.Snapshot); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Printf("[cleanup] canceled stale cleanup drive=%s kind=%s: %v", drv.ID(), drv.Kind(), ctxErr)
			return
		}
		log.Printf("[cleanup] stale cleanup drive=%s kind=%s error: %v", drv.ID(), drv.Kind(), err)
	}
	if err := ctx.Err(); err != nil {
		log.Printf("[scan] drive=%s canceled before derived-task dispatch: %v", driveID, err)
		return
	}

	a.mu.Lock()
	previewWorker := a.workers[driveID]
	thumbnailWorker := a.thumbWorkers[driveID]
	fingerprintWorker := a.fingerprintWorkers[driveID]
	a.mu.Unlock()
	enqueueNewScanVideos(result.NewVideos, thumbnailWorker, fingerprintWorker)
	a.scheduleFingerprintBackfill(ctx, driveID, fingerprintWorker)
	a.enqueueDriveGeneration(ctx, driveID, previewWorker, thumbnailWorker)
}

func (a *App) scanDrive(
	ctx context.Context,
	drv drives.Drive,
	driveConfig *catalog.Drive,
	rateLimitBudget *scanner.RateLimitBudget,
) (scanner.Result, error) {
	if a == nil || a.cfg == nil {
		return scanner.Result{}, errors.New("scan configuration is unavailable")
	}
	if driveConfig == nil {
		return scanner.Result{}, errors.New("drive scan configuration is unavailable")
	}
	scan := scanner.New(
		a.cat,
		drv,
		a.cfg.Scanner.VideoExtensions,
		driveConfig.SkipDirIDs,
		nil,
	)
	a.configureScannerRetries(scan, drv, rateLimitBudget)
	scan.OnProgress = func(stats scanner.Stats) {
		a.updateDriveScanProgress(drv.ID(), stats.Scanned, stats.Added)
	}
	log.Printf("[scan] drive=%s start=%s skip_dirs=%d", drv.ID(), driveConfig.RootID, len(driveConfig.SkipDirIDs))
	return scan.Scan(ctx, driveConfig.RootID)
}

// cleanupScanSnapshot derives a safe cleanup decision for each catalog row from
// the discovery directory sets. A failed subtree is protected without blocking
// healthy sibling directories. Reconciliation issues do not affect presence,
// which comes from the read-only snapshot.
func (a *App) cleanupScanSnapshot(ctx context.Context, drv drives.Drive, snapshot scanner.Snapshot) error {
	if drv.Kind() == scriptcrawler.Kind || drv.ID() == localupload.DriveID {
		return nil
	}
	if !snapshot.Complete() {
		log.Printf(
			"[cleanup] partial stale cleanup drive=%s kind=%s enumerated_dirs=%d failed_dirs=%d",
			drv.ID(), drv.Kind(), len(snapshot.EnumeratedDirIDs), len(snapshot.FailedDirIDs),
		)
	}
	removed, err := a.cleanupMissingDriveVideos(
		ctx,
		drv.ID(),
		snapshot.SeenFileIDs,
		catalog.ScanPresenceScope{
			EnumeratedDirIDs: snapshot.EnumeratedDirIDs,
			FailedDirIDs:     snapshot.FailedDirIDs,
			ExcludedDirIDs:   snapshot.ExcludedDirIDs,
			FullDriveScan:    snapshot.FullDriveScan,
		},
	)
	if err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("[cleanup] removed %d stale videos for drive=%s kind=%s", removed, drv.ID(), drv.Kind())
	}
	return nil
}

func enqueueNewScanVideos(
	videos []*catalog.Video,
	thumbnailWorker *preview.ThumbWorker,
	fingerprintWorker *fingerprint.Worker,
) {
	for _, video := range videos {
		if video == nil {
			continue
		}
		if fingerprintWorker != nil {
			fingerprintWorker.Enqueue(video)
		}
		if thumbnailWorker != nil && video.ThumbnailURL == "" {
			thumbnailWorker.Enqueue(video)
		}
	}
}

func (a *App) cleanupMissingDriveVideos(
	ctx context.Context,
	driveID string,
	liveFileIDs map[string]struct{},
	scope catalog.ScanPresenceScope,
) (int, error) {
	const confirmationThreshold = 2
	confirmedMissing, err := func() (map[string]struct{}, error) {
		if err := persistence.RLockContext(ctx); err != nil {
			return nil, err
		}
		defer persistence.RUnlock()
		return a.cat.ConfirmMissingDriveFiles(
			ctx, driveID, liveFileIDs, scope, confirmationThreshold,
		)
	}()
	if err != nil {
		return 0, fmt.Errorf("confirm missing drive files: %w", err)
	}
	if len(confirmedMissing) == 0 {
		return 0, nil
	}
	items, err := a.cat.ListVideosByDrive(ctx, driveID)
	if err != nil {
		return 0, err
	}

	missing := make([]*catalog.Video, 0, len(confirmedMissing))
	for _, video := range items {
		if _, ok := confirmedMissing[video.FileID]; ok {
			missing = append(missing, video)
		}
	}
	return a.deleteScanCleanupVideos(ctx, missing)
}

func (a *App) cleanupSkippedDriveVideos(
	ctx context.Context,
	drv drives.Drive,
	driveConfig *catalog.Drive,
	rateLimitBudget *scanner.RateLimitBudget,
) error {
	if drv == nil || driveConfig == nil || drv.Kind() == scriptcrawler.Kind || drv.ID() == localupload.DriveID {
		return nil
	}
	state, err := a.cat.GetDriveSkipCleanupState(ctx, drv.ID())
	if err != nil {
		return fmt.Errorf("read cleanup progress: %w", err)
	}
	currentDirIDs := normalizedDirIDSet(driveConfig.SkipDirIDs)
	pendingLegacyDirIDs := pendingDirIDs(currentDirIDs, state.LegacyDoneDirIDs)
	if state.Initialized && len(pendingLegacyDirIDs) == 0 && equalDirIDSets(state.DirIDs, currentDirIDs) {
		return nil
	}

	exactItems, err := a.cat.ListVideosInAncestorDirs(ctx, drv.ID(), currentDirIDs)
	if err != nil {
		return fmt.Errorf("list videos selected by skip policy: %w", err)
	}
	exactRemoved, err := a.deleteScanCleanupVideos(ctx, exactItems)
	if err != nil {
		return fmt.Errorf("exact cleanup: %w", err)
	}
	if exactRemoved > 0 {
		log.Printf("[skip-cleanup] drive=%s removed=%d mode=exact", drv.ID(), exactRemoved)
	}

	var cleanupErrors []error
	if len(pendingLegacyDirIDs) > 0 {
		hasLegacyVideos, legacyQueryErr := a.cat.DriveHasVideosWithoutAncestorDirIDs(ctx, drv.ID())
		switch {
		case legacyQueryErr != nil:
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("check videos without ancestor directories: %w", legacyQueryErr))
		case !hasLegacyVideos:
			for _, skippedDirID := range pendingLegacyDirIDs {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := a.cat.MarkDriveSkipCleanupLegacyDirDone(ctx, drv.ID(), skippedDirID); err != nil {
					cleanupErrors = append(cleanupErrors,
						fmt.Errorf("mark legacy cleanup complete for directory %s: %w", skippedDirID, err))
				}
			}
		default:
			for _, skippedDirID := range pendingLegacyDirIDs {
				complete, cleanupErr := a.cleanupLegacySkippedDirectory(
					ctx, drv, currentDirIDs, skippedDirID, rateLimitBudget,
				)
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if cleanupErr != nil {
					if errors.Is(cleanupErr, scanner.ErrRateLimitBudgetExhausted) {
						return cleanupErr
					}
					cleanupErrors = append(cleanupErrors, cleanupErr)
					continue
				}
				if !complete {
					continue
				}
				if err := a.cat.MarkDriveSkipCleanupLegacyDirDone(ctx, drv.ID(), skippedDirID); err != nil {
					cleanupErrors = append(cleanupErrors,
						fmt.Errorf("mark legacy cleanup complete for directory %s: %w", skippedDirID, err))
				}
			}
		}
	}

	// Exact cleanup completion is independent from per-directory legacy work.
	// Failed legacy directories stay pending without forcing completed siblings
	// to be traversed again.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.cat.SetDriveSkipCleanupDirIDs(ctx, drv.ID(), currentDirIDs); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("record skip cleanup directory IDs: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

func (a *App) cleanupLegacySkippedDirectory(
	ctx context.Context,
	drv drives.Drive,
	currentDirIDs []string,
	skippedDirID string,
	rateLimitBudget *scanner.RateLimitBudget,
) (bool, error) {
	videoExtensions := []string(nil)
	if a.cfg != nil {
		videoExtensions = a.cfg.Scanner.VideoExtensions
	}
	scan := scanner.New(a.cat, drv, videoExtensions, currentDirIDs, nil)
	scan.LogPrefix = "skip-cleanup"
	a.configureScannerRetries(scan, drv, rateLimitBudget)
	snapshot, _, discoverErr := scan.Discover(ctx, skippedDirID)
	if err := ctx.Err(); err != nil {
		return false, err
	}

	legacyDirIDs := make([]string, 0, len(snapshot.EnumeratedDirIDs)+1)
	legacyDirIDs = append(legacyDirIDs, skippedDirID)
	for dirID := range snapshot.EnumeratedDirIDs {
		legacyDirIDs = append(legacyDirIDs, dirID)
	}
	legacyItems, err := a.cat.ListVideosByParentDirIDs(ctx, drv.ID(), legacyDirIDs)
	if err != nil {
		return false, fmt.Errorf("list legacy videos under skipped directory %s: %w", skippedDirID, err)
	}
	legacyRemoved, err := a.deleteScanCleanupVideos(ctx, legacyItems)
	if err != nil {
		return false, fmt.Errorf("legacy cleanup under skipped directory %s: %w", skippedDirID, err)
	}
	if legacyRemoved > 0 {
		log.Printf("[skip-cleanup] drive=%s dir=%s removed=%d mode=legacy", drv.ID(), skippedDirID, legacyRemoved)
	}
	if discoverErr != nil {
		if errors.Is(discoverErr, scanner.ErrRateLimitBudgetExhausted) {
			return false, discoverErr
		}
		log.Printf("[skip-cleanup] drive=%s dir=%s legacy discovery incomplete: %v", drv.ID(), skippedDirID, discoverErr)
		return false, nil
	}
	if !snapshot.Complete() {
		log.Printf(
			"[skip-cleanup] drive=%s dir=%s legacy discovery incomplete failed_dirs=%d",
			drv.ID(), skippedDirID, len(snapshot.FailedDirIDs),
		)
		return false, nil
	}
	return true, nil
}

func (a *App) deleteScanCleanupVideos(ctx context.Context, videos []*catalog.Video) (int, error) {
	if len(videos) == 0 {
		return 0, nil
	}
	if err := persistence.RLockContext(ctx); err != nil {
		return 0, err
	}
	defer persistence.RUnlock()

	localDir := ""
	if a.cfg != nil {
		localDir = a.cfg.Storage.LocalPreviewDir
	}
	removed := 0
	for _, video := range videos {
		if video == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if err := removeLocalVideoAssets(localDir, video); err != nil {
			return removed, fmt.Errorf("remove local assets for %s: %w", video.ID, err)
		}
		if err := a.cat.DeleteVideo(ctx, video.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return removed, fmt.Errorf("delete catalog video %s: %w", video.ID, err)
		}
		removed++
	}
	return removed, nil
}

func normalizedDirIDSet(dirIDs []string) []string {
	seen := make(map[string]struct{}, len(dirIDs))
	out := make([]string, 0, len(dirIDs))
	for _, dirID := range dirIDs {
		dirID = strings.TrimSpace(dirID)
		if dirID == "" {
			continue
		}
		if _, exists := seen[dirID]; exists {
			continue
		}
		seen[dirID] = struct{}{}
		out = append(out, dirID)
	}
	return out
}

func pendingDirIDs(current, completed []string) []string {
	completedSet := make(map[string]struct{}, len(completed))
	for _, dirID := range normalizedDirIDSet(completed) {
		completedSet[dirID] = struct{}{}
	}
	pending := make([]string, 0, len(current))
	for _, dirID := range normalizedDirIDSet(current) {
		if _, done := completedSet[dirID]; !done {
			pending = append(pending, dirID)
		}
	}
	return pending
}

func equalDirIDSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, dirID := range right {
		rightSet[dirID] = struct{}{}
	}
	for _, dirID := range left {
		if _, exists := rightSet[dirID]; !exists {
			return false
		}
	}
	return true
}

func (a *App) configureScannerRetries(
	scan *scanner.Scanner,
	drv drives.Drive,
	rateLimitBudget *scanner.RateLimitBudget,
) {
	if scan == nil || drv == nil {
		return
	}
	if rateLimitBudget != nil {
		scan.RateLimitBudget = rateLimitBudget
	}
	scan.OnCooldown = func(until time.Time) {
		a.updateDriveScanCooldown(drv.ID(), until)
	}
}
