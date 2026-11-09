//go:build !unix

package ffmpeg

import (
	"os/exec"
	"time"
)

const platformSupported = false

func configureProcess(cmd *exec.Cmd)  { cmd.WaitDelay = 2 * time.Second }
func killProcess(cmd *exec.Cmd) error { return cmd.Process.Kill() }
