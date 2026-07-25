//go:build windows

package scriptcrawler

import (
	"errors"
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
	// A failure here normally means the tree already exited. Report it instead
	// of swallowing it: callers use a successful termination to tell a
	// backend-initiated exit apart from a script that failed on its own, and
	// os.ErrProcessDone is the outcome cmd.Cancel is documented to expect.
	return cmd.Process.Kill()
}

// isExpectedKilledProcess reports whether a wait error only reflects the
// backend's own termination. Windows reports that termination as an ordinary
// positive exit code — both TerminateProcess and taskkill /F use 1 — so the
// status cannot be told apart from a script exiting 1 by itself. Whether the
// backend actually terminated a live process is the only reliable signal.
func isExpectedKilledProcess(err error, terminated bool) bool {
	var exitErr *exec.ExitError
	return terminated && errors.As(err, &exitErr)
}

func setDryRunProcAttr(cmd *exec.Cmd) {
	setCrawlerProcAttr(cmd)
}

func killDryRunProcess(cmd *exec.Cmd) error {
	return killCrawlerProcess(cmd)
}
