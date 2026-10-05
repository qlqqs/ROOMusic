//go:build unix

package playback

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// FIFO 在没有写入端时以阻塞方式只读打开会一直等待；O_NONBLOCK 让打开立即返回，再由 fstat 拒绝非普通文件。
func TestOpenVerifiedDoesNotBlockOnAFifo(t *testing.T) {
	root := realTempDir(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.flac"), 0o644); err != nil {
		t.Skipf("named pipes are not available: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		opened, err := openVerified(root, "pipe.flac", recorded(4096, scanTime), containerFLAC, "track-1")
		if opened != nil {
			opened.File.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("openVerified of a FIFO = %v, want ErrSourceUnavailable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openVerified blocked on a FIFO")
	}
}
