//go:build !windows

package scriptcrawler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCrawlerTimeoutKillsChildProcessTree(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	body := fmt.Sprintf(`
sleep 30 &
child=$!
echo "$child" > %q
sleep 30
`, pidFile)
	crawler := newRuntimeTestCrawler(t, body, ProtocolV2, func(cfg *CrawlerConfig) {
		cfg.IdleTimeout = 80 * time.Millisecond
		cfg.CandidateIdleTimeout = time.Second
	})
	_, err := crawler.RunOnce(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "heartbeat timeout") {
		t.Fatalf("error = %v, want heartbeat timeout", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processIsRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processIsRunning(pid) {
		t.Fatalf("child process %d survived crawler termination", pid)
	}
}

func TestExpectedKilledProcessDoesNotHideScriptExitFailure(t *testing.T) {
	// terminated=true throughout: the wait status is authoritative on unix, so
	// a script that failed on its own must stay visible even when the backend
	// also issued a termination request.
	failed := exec.Command("/bin/sh", "-c", "exit 7")
	if err := failed.Run(); err == nil || isExpectedKilledProcess(err, true) {
		t.Fatalf("ordinary non-zero exit = %v, must remain observable", err)
	}

	killed := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := killed.Start(); err != nil {
		t.Fatalf("start killed process: %v", err)
	}
	if err := killed.Process.Kill(); err != nil {
		t.Fatalf("kill process: %v", err)
	}
	if err := killed.Wait(); err == nil || !isExpectedKilledProcess(err, true) {
		t.Fatalf("killed process = %v, want expected termination", err)
	}
}

func processIsRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil && strings.Contains(string(stat), ") Z ") {
		return false
	}
	return true
}
