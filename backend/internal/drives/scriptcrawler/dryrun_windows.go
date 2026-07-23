//go:build windows

package scriptcrawler

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func setCrawlerProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func killCrawlerProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Windows has no Unix-style process-group signal. taskkill /T terminates the
	// whole descendant tree; fall back to the direct process if taskkill is not
	// available or the tree has already changed.
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func setDryRunProcAttr(cmd *exec.Cmd) {
	setCrawlerProcAttr(cmd)
}

func killDryRunProcess(cmd *exec.Cmd) error {
	return killCrawlerProcess(cmd)
}
