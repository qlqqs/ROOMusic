package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var errNetworkMount = errors.New("网络共享目录")

type watchEvent struct {
	dirs        []string
	removedTree string
	move        uint32
	overflow    bool
}

type watchSystem struct {
	network func(string) (bool, error)
	add     func(int, string, uint32) (int, error)
}

func defaultWatchSystem() watchSystem {
	return watchSystem{network: networkMount, add: unix.InotifyAddWatch}
}

func networkMount(path string) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false, err
	}
	switch uint32(stat.Type) {
	case unix.NFS_SUPER_MAGIC, unix.CIFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC:
		return true, nil
	}
	return false, nil
}

// 一个根一个 fd，目录映射只在本读循环内修改。溢出后重新比对实际目录与 wd。
func (system watchSystem) watch(ctx context.Context, root registeredRoot, ready func(), notify func(watchEvent), opened []string) error {
	if !registeredRootAvailable(root.Path) {
		return fs.ErrNotExist
	}
	network, err := system.network(root.Path)
	if err != nil {
		return err
	}
	if network {
		return errNetworkMount
	}
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "library-inotify")
	defer file.Close()
	stop := context.AfterFunc(ctx, func() { _ = file.SetReadDeadline(time.Now()) })
	defer stop()
	dirs := map[int]string{}
	const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_ATTRIB | unix.IN_CLOSE_WRITE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_MOVE_SELF | unix.IN_DELETE_SELF | unix.IN_ONLYDIR | unix.IN_DONT_FOLLOW
	register := func(start string, seen map[int]bool, added func(string)) error {
		return filepath.WalkDir(filepath.Join(root.Path, start), func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if errors.Is(walkErr, fs.ErrNotExist) && path != root.Path {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				if path == root.Path {
					return fs.ErrInvalid
				}
				return nil
			}
			dir := safeRelativePath(root.Path, path)
			if dir != "" && root.filter.skipDir(dir, entry.Name()) {
				return fs.SkipDir
			}
			if dir != "" {
				network, err := system.network(path)
				if errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				if err != nil {
					return err
				}
				if network {
					return errNetworkMount
				}
			}
			wd, err := system.add(fd, path, mask)
			if err == nil {
				dirs[wd] = dir
				if seen != nil {
					seen[wd] = true
				}
				if added != nil {
					added(dir)
				}
				return nil
			}
			if errors.Is(err, fs.ErrNotExist) && dir != "" {
				return fs.SkipDir
			}
			return err
		})
	}
	var additions []string
	if err := register("", nil, func(dir string) {
		for _, path := range opened {
			if dir == path || strings.HasPrefix(dir, path+"/") {
				additions = append(additions, dir)
				break
			}
		}
	}); err != nil {
		return err
	}
	if len(additions) > 0 {
		notify(watchEvent{dirs: additions})
	}
	ready()
	buffer := make([]byte, 64*1024)
	for ctx.Err() == nil {
		n, err := file.Read(buffer)
		if err != nil {
			return err
		}
		for pos := 0; pos+unix.SizeofInotifyEvent <= n; {
			header := buffer[pos:]
			wd := int(int32(binary.NativeEndian.Uint32(header)))
			flags := binary.NativeEndian.Uint32(header[4:])
			cookie := binary.NativeEndian.Uint32(header[8:])
			size := int(binary.NativeEndian.Uint32(header[12:]))
			pos += unix.SizeofInotifyEvent
			name := string(bytes.TrimRight(buffer[pos:pos+size], "\x00"))
			pos += size
			if flags&unix.IN_Q_OVERFLOW != 0 {
				seen := map[int]bool{}
				if err := register("", seen, nil); err != nil {
					return err
				}
				for old := range dirs {
					if !seen[old] {
						delete(dirs, old)
						_, _ = unix.InotifyRmWatch(fd, uint32(old))
					}
				}
				notify(watchEvent{overflow: true})
				continue
			}
			parent, ok := dirs[wd]
			if !ok {
				continue
			}
			if flags&unix.IN_UNMOUNT != 0 || parent == "" && flags&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_IGNORED) != 0 {
				return fs.ErrNotExist
			}
			if flags&(unix.IN_DELETE_SELF|unix.IN_IGNORED) != 0 {
				delete(dirs, wd)
				continue
			}
			if name == "" {
				continue
			}
			if root.filter.skipFile(name) {
				continue
			}
			event := watchEvent{dirs: []string{parent}, move: cookie}
			if flags&unix.IN_ISDIR != 0 {
				child := filepath.Join(parent, name)
				if root.filter.skipDir(child, name) {
					continue
				}
				// 父目录有独立的安静窗口，兄弟专辑中的连续写入只推迟自己的扫描。
				notify(watchEvent{dirs: []string{parent}})
				event.dirs = []string{child}
				if flags&(unix.IN_MOVED_FROM|unix.IN_DELETE) != 0 {
					event.removedTree = child
					for old, dir := range dirs {
						if dir == child || strings.HasPrefix(dir, child+"/") {
							event.dirs = append(event.dirs, dir)
							delete(dirs, old)
							_, _ = unix.InotifyRmWatch(fd, uint32(old))
						}
					}
				}
				if flags&(unix.IN_MOVED_TO|unix.IN_CREATE) != 0 {
					if err := register(child, nil, func(dir string) { event.dirs = append(event.dirs, dir) }); err != nil {
						return err
					}
				}
			}
			notify(event)
		}
	}
	return ctx.Err()
}
