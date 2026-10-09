//go:build !unix

package derivation

import (
	"errors"
	"os/exec"
	"time"
)

// platformSupported 为假：本平台没有进程组回收与剩余空间读取，派生保持关闭。
const platformSupported = false

func configureProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}

func freeSpace(string) (uint64, error) { return 0, errors.ErrUnsupported }
