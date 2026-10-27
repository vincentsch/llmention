//go:build !windows

package notify

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCommandCancellation starts the script in its own process group.
// When it runs too long, llmention kills the script and every process it
// started, and nothing else.
func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
