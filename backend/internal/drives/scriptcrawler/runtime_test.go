package scriptcrawler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
)

func newRuntimeTestCrawler(t *testing.T, body, protocol string, mutate func(*CrawlerConfig)) *Crawler {
	t.Helper()
	tmp := t.TempDir()
	cat, err := catalog.Open(filepath.Join(tmp, "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	drv := New(Config{ID: "runtime-test", RootDir: filepath.Join(tmp, "crawler")})
	scriptPath := filepath.Join(tmp, "crawler.py")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cfg := CrawlerConfig{
		Driver:               drv,
		Catalog:              cat,
		Protocol:             protocol,
		PythonPath:           "/bin/sh",
		ScriptPath:           scriptPath,
		RunTimeout:           2 * time.Second,
		CandidateIdleTimeout: time.Second,
		IdleTimeout:          500 * time.Millisecond,
		DoneGrace:            100 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewCrawler(cfg)
}

func TestCrawlerV2WritesLimitsAndAcceptsDone(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `
echo '{"type":"done","stats":{"checked":12,"emitted":0}}'
`, ProtocolV2, nil)
	result, err := crawler.RunOnce(context.Background(), 1)
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	data, err := os.ReadFile(result.JobFile)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.Protocol != ProtocolV2 || job.Limits == nil {
		t.Fatalf("job protocol=%q limits=%+v", job.Protocol, job.Limits)
	}
	if job.Limits.MaxRuntimeSeconds != 2 || job.Limits.IdleTimeoutSeconds != 1 || job.Limits.CandidateIdleTimeoutSeconds != 1 {
		t.Fatalf("limits = %+v", job.Limits)
	}
	if _, err := time.Parse(time.RFC3339, job.Limits.DeadlineAt); err != nil {
		t.Fatalf("deadline_at = %q: %v", job.Limits.DeadlineAt, err)
	}
}

func TestCrawlerV2RequiresDoneOnNaturalExit(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `echo '{"type":"progress","checked":1,"emitted":0}'`, ProtocolV2, nil)
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "without a done event") {
		t.Fatalf("error = %v, want missing done error", err)
	}
}

func TestCrawlerV2RejectsInvalidStdout(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `echo 'ordinary log on stdout'`, ProtocolV2, nil)
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "JSON objects") {
		t.Fatalf("error = %v, want strict JSON error", err)
	}
}

func TestCrawlerV2HeartbeatTimeout(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `sleep 30`, ProtocolV2, func(cfg *CrawlerConfig) {
		cfg.IdleTimeout = 50 * time.Millisecond
		cfg.CandidateIdleTimeout = 500 * time.Millisecond
	})
	started := time.Now()
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "heartbeat timeout") {
		t.Fatalf("error = %v, want heartbeat timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("heartbeat protection took %s", elapsed)
	}
}

func TestCrawlerOverallRuntimeTimeout(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `sleep 30`, ProtocolV1, func(cfg *CrawlerConfig) {
		cfg.RunTimeout = 60 * time.Millisecond
		cfg.CandidateIdleTimeout = time.Second
	})
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "maximum runtime exceeded") {
		t.Fatalf("error = %v, want runtime timeout", err)
	}
}

func TestCrawlerV2DoneRequiresPromptProcessExit(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `
echo '{"type":"done","stats":{"checked":0,"emitted":0}}'
sleep 30
`, ProtocolV2, func(cfg *CrawlerConfig) {
		cfg.DoneGrace = 50 * time.Millisecond
	})
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("error = %v, want done exit timeout", err)
	}
}

func TestCrawlerCandidateIdleTimeoutIsNotResetByProgress(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `
while :; do
  echo '{"type":"progress","checked":1,"emitted":0}'
  sleep 0.02
done
`, ProtocolV2, func(cfg *CrawlerConfig) {
		cfg.IdleTimeout = 100 * time.Millisecond
		cfg.CandidateIdleTimeout = 180 * time.Millisecond
	})
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "no item event") {
		t.Fatalf("error = %v, want candidate idle timeout", err)
	}
}

func TestCrawlerV1EnforcesCandidateBudget(t *testing.T) {
	crawler := newRuntimeTestCrawler(t, `
i=0
while [ "$i" -lt 80 ]; do
  printf '{"type":"item","source_id":"bad-%s","title":"Bad candidate"}\n' "$i"
  i=$((i + 1))
done
sleep 30
`, ProtocolV1, nil)
	result, err := crawler.RunOnce(context.Background(), 1)
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if result.TotalEntries != result.CandidateBudget {
		t.Fatalf("total entries=%d candidate budget=%d", result.TotalEntries, result.CandidateBudget)
	}
	if result.Failed != result.CandidateBudget {
		t.Fatalf("failed=%d candidate budget=%d", result.Failed, result.CandidateBudget)
	}
}

func TestScanScriptOutputEnforcesLineAndTotalLimits(t *testing.T) {
	ctx := context.Background()
	lineOutput := scanScriptOutput(ctx, strings.NewReader(strings.Repeat("x", 33)), 32, 1024)
	lineResult := <-lineOutput
	if lineResult.err == nil || !strings.Contains(lineResult.err.Error(), "line exceeds") {
		t.Fatalf("line error = %v", lineResult.err)
	}

	totalOutput := scanScriptOutput(ctx, strings.NewReader("12345\n67890\n"), 32, 10)
	first := <-totalOutput
	second := <-totalOutput
	if first.err != nil || second.err == nil || !strings.Contains(second.err.Error(), "stdout exceeded") {
		t.Fatalf("outputs = %+v / %+v", first, second)
	}
}
