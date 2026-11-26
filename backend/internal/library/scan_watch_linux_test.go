package library

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type directoryTestClock struct {
	mu    sync.Mutex
	now   time.Time
	timer *directoryTestTimer
}

type directoryTestTimer struct {
	ticks   chan time.Time
	due     time.Time
	stopped bool
	clock   *directoryTestClock
}

func newDirectoryTestClock() *directoryTestClock {
	return &directoryTestClock{now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (c *directoryTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *directoryTestClock) NewTimer(delay time.Duration) ScanSchedulerTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &directoryTestTimer{ticks: make(chan time.Time, 1), due: c.now.Add(delay), clock: c}
	c.timer = timer
	return timer
}

func (timer *directoryTestTimer) C() <-chan time.Time { return timer.ticks }

func (timer *directoryTestTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	timer.stopped = true
	return true
}

func (c *directoryTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	if c.timer != nil && !c.timer.stopped && !c.now.Before(c.timer.due) {
		select {
		case c.timer.ticks <- c.now:
		default:
		}
	}
}

func TestDirectoryWatchDebounce(t *testing.T) {
	clock := newDirectoryTestClock()
	w := newDirectoryWatcher()
	w.clock = clock
	root := &watchedRoot{root: registeredRoot{ID: "root"}, mode: "realtime"}
	w.roots[root.root.ID] = root
	record := func(dirs ...string) { w.record(root, watchEvent{dirs: dirs}) }
	due := func(want ...string) {
		t.Helper()
		got := map[string]bool{}
		for _, batch := range root.pending {
			if !clock.Now().Before(batch.due) {
				for dir := range batch.directories {
					got[dir] = true
				}
			}
		}
		if len(got) != len(want) {
			t.Fatalf("到期目录 %v，期望 %v", got, want)
		}
		for _, dir := range want {
			if !got[dir] {
				t.Fatalf("到期目录 %v 缺少 %q", got, dir)
			}
		}
	}

	record("Album")
	clock.advance(time.Second)
	record("Other")
	clock.advance(999 * time.Millisecond)
	due()
	clock.advance(time.Millisecond)
	due("Album")
	record("Album")
	due()
	clock.advance(time.Second)
	due("Other")
	clock.advance(time.Second)
	due("Album", "Other")

	root.pending = nil
	record("Box/CD1")
	clock.advance(time.Second)
	record("Box/CD2")
	if len(root.pending) != 1 {
		t.Fatalf("两张碟应共用一个安静窗口，现有 %d 个", len(root.pending))
	}
	clock.advance(1999 * time.Millisecond)
	due()
	clock.advance(time.Millisecond)
	due("Box", "Box/CD1", "Box/CD2")

	root.pending = nil
	w.record(root, watchEvent{dirs: []string{"Old"}, removedTree: "Old", move: 23})
	clock.advance(time.Second)
	w.record(root, watchEvent{dirs: []string{"New"}, move: 23})
	clock.advance(2 * time.Second)
	due("Old", "New")
	if len(root.pending) != 1 || !root.pending[0].removedTrees["Old"] {
		t.Fatalf("同一 move cookie 的两端应在同批并保留移除子树：%+v", root.pending)
	}
}

func TestDirectoryWatchFallback(t *testing.T) {
	root := registeredRoot{Path: t.TempDir()}
	child := filepath.Join(root.Path, "album")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"NFS", "CIFS", "SMB", "SMB2"} {
		t.Run(name, func(t *testing.T) {
			system := watchSystem{
				network: func(string) (bool, error) { return true, nil },
				add: func(int, string, uint32) (int, error) {
					t.Fatal("网络挂载尝试注册了 watch")
					return 0, nil
				},
			}
			err := system.watch(t.Context(), root, func() { t.Fatal("网络挂载报告实时") }, nil, nil)
			if !errors.Is(err, errNetworkMount) {
				t.Fatalf("网络挂载误判：%v", err)
			}
		})
	}
	t.Run("子目录 AddWatch 失败关闭整个根", func(t *testing.T) {
		var paths []string
		fd := -1
		system := watchSystem{
			network: func(string) (bool, error) { return false, nil },
			add: func(descriptor int, path string, _ uint32) (int, error) {
				paths = append(paths, path)
				fd = descriptor
				if path == child {
					return 0, unix.ENOSPC
				}
				return 1, nil
			},
		}
		err := system.watch(t.Context(), root, func() { t.Fatal("部分注册报告实时") }, nil, nil)
		if !errors.Is(err, unix.ENOSPC) || len(paths) != 2 || paths[0] != root.Path || paths[1] != child {
			t.Fatalf("注册 %v，错误 %v，期望根成功、子目录 ENOSPC", paths, err)
		}
		requireClosedWatchFD(t, fd)
	})
	t.Run("其他本地根仍可完成注册", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		count := 0
		system := watchSystem{
			network: func(string) (bool, error) { return false, nil },
			add: func(int, string, uint32) (int, error) {
				count++
				return count, nil
			},
		}
		if err := system.watch(ctx, root, cancel, nil, nil); !errors.Is(err, context.Canceled) || count != 2 {
			t.Fatalf("本地监听注册 %d 个目录，退出 %v", count, err)
		}
	})

	t.Run("五档轮询间隔", func(t *testing.T) {
		clock := newDirectoryTestClock()
		s := NewService(t.Context(), nil, "", nil).WithDirectoryWatchClock(clock)
		for _, minutes := range []int{15, 60, 240, 720, 1440} {
			t.Run(strconv.Itoa(minutes), func(t *testing.T) {
				id := "scheduled"
				s.watch.roots[id] = &watchedRoot{mode: "scheduled", root: registeredRoot{
					ID: id, interval: minutes, lastPoll: sql.NullTime{Time: clock.Now(), Valid: true},
				}}
				beforeDue := clock.Now().Add(time.Duration(minutes)*time.Minute - time.Millisecond)
				if delay := s.handleWatchedRoot(t.Context(), id, beforeDue); delay != time.Millisecond {
					t.Errorf("%d 分钟的到期前 1 毫秒，返回等待 %v", minutes, delay)
				}
			})
		}
	})
	t.Run("溢出通知后继续读取事件", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var pipe [2]int
		if err := unix.Pipe2(pipe[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
			t.Fatal(err)
		}
		defer unix.Close(pipe[0])
		defer func() {
			if pipe[1] >= 0 {
				_ = unix.Close(pipe[1])
			}
		}()
		added := 0
		system := watchSystem{
			network: func(string) (bool, error) { return false, nil },
			add: func(fd int, path string, _ uint32) (int, error) {
				added++
				if added == 1 {
					// 在首次读取前用管道提供合成记录。真实监听事件由生命周期用例验证。
					if err := unix.Dup3(pipe[0], fd, unix.O_CLOEXEC); err != nil {
						return 0, err
					}
				}
				if path == root.Path {
					return 1, nil
				}
				return 2, nil
			},
		}
		var events []watchEvent
		err := system.watch(ctx, root, func() {
			header := unix.SizeofInotifyEvent
			data := make([]byte, 2*header+8)
			binary.NativeEndian.PutUint32(data, ^uint32(0))
			binary.NativeEndian.PutUint32(data[4:], unix.IN_Q_OVERFLOW)
			binary.NativeEndian.PutUint32(data[header:], 2)
			binary.NativeEndian.PutUint32(data[header+4:], unix.IN_CLOSE_WRITE)
			binary.NativeEndian.PutUint32(data[header+12:], 8)
			copy(data[2*header:], "01.flac")
			_, writeErr := unix.Write(pipe[1], data)
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			// 写端关闭后，即使读取没有产生下面预期的事件也会退出。
			if closeErr := unix.Close(pipe[1]); closeErr != nil {
				t.Fatal(closeErr)
			}
			pipe[1] = -1
		}, func(event watchEvent) {
			events = append(events, event)
			if len(events) == 2 {
				cancel()
			}
		}, nil)
		if !errors.Is(err, context.Canceled) || added != 4 || len(events) != 2 ||
			!events[0].overflow || events[1].overflow || len(events[1].dirs) != 1 || events[1].dirs[0] != "album" {
			t.Fatalf("溢出后应核对两处注册并继续读新事件：注册 %d 次，事件 %+v，错误 %v", added, events, err)
		}
	})
}

func requireClosedWatchFD(t *testing.T, fd int) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); !errors.Is(err, unix.EBADF) {
		t.Fatalf("监听 fd %d 应已关闭，Fstat 返回 %v", fd, err)
	}
}
