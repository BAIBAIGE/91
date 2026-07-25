//go:build !windows

package scriptcrawler

import (
	"errors"
	"os/exec"
	"syscall"
)

func setCrawlerProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCrawlerProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return cmd.Process.Kill()
	}
	return nil
}

// isExpectedKilledProcess reports whether a wait error only reflects the
// backend's own termination. The wait status is precise here, so the caller's
// terminated hint is unnecessary: a script that failed on its own before the
// stop request still carries its exit code and stays visible in the logs.
func isExpectedKilledProcess(err error, _ bool) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

func setDryRunProcAttr(cmd *exec.Cmd) {
	setCrawlerProcAttr(cmd)
}

func killDryRunProcess(cmd *exec.Cmd) error {
	return killCrawlerProcess(cmd)
}
