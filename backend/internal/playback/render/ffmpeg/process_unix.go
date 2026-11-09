//go:build unix

package ffmpeg

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const platformSupported = true

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcess(cmd) }
	cmd.WaitDelay = 2 * time.Second
}
func killProcess(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
