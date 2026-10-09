//go:build unix

package derivation

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// platformSupported 表示本平台能按设计回收 ffmpeg 进程组并读取剩余磁盘空间。
const platformSupported = true

// configureProcess 让 ffmpeg 自成进程组，取消时向整个进程组发送 SIGKILL：ffmpeg 本身不应有子进程，
// 但取消必须能回收任何后代；WaitDelay 保证管道被后代占着时 Wait 也能返回。
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
}

// freeSpace 返回目录所在文件系统对非特权进程的剩余字节数。
func freeSpace(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil //nolint:unconvert // 字段类型随平台变化
}
