package scriptcrawler

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultActivityLogInterval = 30 * time.Second

type CrawlPhase string

const (
	CrawlPhaseDiscovering    CrawlPhase = "discovering"
	CrawlPhaseDownloading    CrawlPhase = "downloading"
	CrawlPhaseValidating     CrawlPhase = "validating"
	CrawlPhaseFingerprinting CrawlPhase = "fingerprinting"
	CrawlPhaseThumbnail      CrawlPhase = "thumbnail"
	CrawlPhaseDeduplicating  CrawlPhase = "deduplicating"
	CrawlPhaseCataloging     CrawlPhase = "cataloging"
)

type CrawlActivityEvent string

const (
	CrawlActivityStarted   CrawlActivityEvent = "started"
	CrawlActivityProgress  CrawlActivityEvent = "progress"
	CrawlActivityCompleted CrawlActivityEvent = "completed"
	CrawlActivityFailed    CrawlActivityEvent = "failed"
)

// CrawlActivity reports the currently active end-to-end crawler phase. It is
// intentionally separate from CrawlProgress: counters describe the whole run,
// while an activity describes one potentially long-running item operation.
type CrawlActivity struct {
	Event       CrawlActivityEvent
	Phase       CrawlPhase
	SourceID    string
	Title       string
	Transport   string
	Bytes       int64
	Elapsed     time.Duration
	ItemElapsed time.Duration
}

func (c *Crawler) publishActivity(activity CrawlActivity) {
	if c == nil || c.cfg.OnActivity == nil {
		return
	}
	c.cfg.OnActivity(activity)
}

type itemActivityTracker struct {
	crawler   *Crawler
	driveID   string
	sourceID  string
	title     string
	transport string
	interval  time.Duration
	startedAt time.Time

	mu           sync.Mutex
	phase        CrawlPhase
	phaseStarted time.Time
	observedPath string
	knownBytes   int64
	eventMu      sync.Mutex

	stopOnce sync.Once
	stop     chan struct{}
	stopped  chan struct{}
}

func (c *Crawler) startItemActivity(sourceID string, item Item, mediaPath string) *itemActivityTracker {
	now := time.Now()
	tracker := &itemActivityTracker{
		crawler:      c,
		driveID:      c.cfg.Driver.ID(),
		sourceID:     strings.TrimSpace(sourceID),
		title:        strings.TrimSpace(item.Title),
		transport:    mediaTransferKind(item.Media),
		interval:     c.cfg.ActivityLogInterval,
		startedAt:    now,
		phase:        CrawlPhaseDownloading,
		phaseStarted: now,
		observedPath: mediaPath,
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
	tracker.emit(tracker.snapshot(now), CrawlActivityStarted)
	go tracker.run()
	return tracker
}

func mediaTransferKind(ref MediaRef) string {
	if strings.TrimSpace(ref.LocalFile) != "" {
		return "local"
	}
	if looksLikeHLSURL(ref.URL) {
		return "hls"
	}
	return "http"
}

func (t *itemActivityTracker) run() {
	defer close(t.stopped)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			t.eventMu.Lock()
			t.emit(t.snapshot(now), CrawlActivityProgress)
			t.eventMu.Unlock()
		case <-t.stop:
			return
		}
	}
}

func (t *itemActivityTracker) Transition(phase CrawlPhase, observedPath string, knownBytes int64) {
	t.transition(phase, observedPath, knownBytes, CrawlActivityCompleted)
}

func (t *itemActivityTracker) TransitionAfterFailure(phase CrawlPhase, observedPath string, knownBytes int64) {
	t.transition(phase, observedPath, knownBytes, CrawlActivityFailed)
}

func (t *itemActivityTracker) transition(phase CrawlPhase, observedPath string, knownBytes int64, previousEvent CrawlActivityEvent) {
	if t == nil || phase == "" {
		return
	}
	t.eventMu.Lock()
	defer t.eventMu.Unlock()
	observedPath = strings.TrimSpace(observedPath)
	t.mu.Lock()
	if previousEvent == CrawlActivityCompleted && t.phase == phase && t.observedPath == observedPath {
		if knownBytes > t.knownBytes {
			t.knownBytes = knownBytes
		}
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	now := time.Now()
	if knownBytes > 0 {
		t.mu.Lock()
		t.knownBytes = knownBytes
		t.mu.Unlock()
	}
	t.emit(t.snapshot(now), previousEvent)

	t.mu.Lock()
	t.phase = phase
	t.phaseStarted = now
	t.observedPath = observedPath
	t.knownBytes = knownBytes
	t.mu.Unlock()
	t.emit(t.snapshot(now), CrawlActivityStarted)
}

func (t *itemActivityTracker) Finish(success bool) {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() {
		close(t.stop)
		<-t.stopped
		t.eventMu.Lock()
		defer t.eventMu.Unlock()
		event := CrawlActivityCompleted
		if !success {
			event = CrawlActivityFailed
		}
		t.emit(t.snapshot(time.Now()), event)
	})
}

func (t *itemActivityTracker) snapshot(now time.Time) CrawlActivity {
	t.mu.Lock()
	phase := t.phase
	phaseStarted := t.phaseStarted
	observedPath := t.observedPath
	knownBytes := t.knownBytes
	t.mu.Unlock()

	bytes := observedActivityBytes(observedPath, knownBytes)
	if bytes != knownBytes {
		t.mu.Lock()
		if t.observedPath == observedPath && bytes > t.knownBytes {
			t.knownBytes = bytes
		}
		t.mu.Unlock()
	}
	return CrawlActivity{
		Phase:       phase,
		SourceID:    t.sourceID,
		Title:       t.title,
		Transport:   t.transport,
		Bytes:       bytes,
		Elapsed:     nonNegativeDuration(now.Sub(phaseStarted)),
		ItemElapsed: nonNegativeDuration(now.Sub(t.startedAt)),
	}
}

func observedActivityBytes(path string, fallback int64) int64 {
	path = strings.TrimSpace(path)
	if path == "" {
		return fallback
	}
	for _, candidate := range []string{path + ".part", path} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return info.Size()
		}
	}
	return fallback
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func (t *itemActivityTracker) emit(activity CrawlActivity, event CrawlActivityEvent) {
	activity.Event = event
	phaseElapsed := activity.Elapsed.Round(time.Second)
	itemElapsed := activity.ItemElapsed.Round(time.Second)
	transport := ""
	if activity.Phase == CrawlPhaseDownloading && activity.Transport != "" {
		transport = " transport=" + activity.Transport
	}
	log.Printf(
		"[scriptcrawler] drive=%s source_id=%s phase=%s %s%s bytes=%d phase_elapsed=%s item_elapsed=%s title=%q",
		t.driveID,
		activity.SourceID,
		activity.Phase,
		event,
		transport,
		activity.Bytes,
		phaseElapsed,
		itemElapsed,
		activity.Title,
	)
	t.crawler.publishActivity(activity)
}
