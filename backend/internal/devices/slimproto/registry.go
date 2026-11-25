package slimproto

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var listenPort uint16 = 3483
var refreshInterval = 30 * time.Second

type Device struct {
	ready bool
	Hello
	Key, Name             string
	Address, LocalAddress netip.Addr
	Player                *Player
}

type Registry struct {
	logger *slog.Logger
	name   string
	// 每次握手保存实际声明，读回该物理设备的音量方式。
	configure func(context.Context, string, int) (bool, error)
	mu        sync.RWMutex
	devices   map[string]Device
	cancel    context.CancelFunc
	done      chan struct{}
	occupied  atomic.Bool
}

func NewRegistry(name string, logger *slog.Logger, configure func(context.Context, string, int) (bool, error)) *Registry {
	if name == "" {
		name = "ROOMusic"
	}
	return &Registry{name: name, logger: logger, configure: configure, devices: make(map[string]Device)}
}

func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		if d.ready {
			items = append(items, d)
		}
	}
	return items
}
func (r *Registry) Lookup(key string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[key]
	return d, ok && d.ready
}

// 握手中也接收音量方式更新。并发的设置写入均以数据库内的最新值为准。
func (r *Registry) RefreshFixed(key string, read func() (bool, error)) error {
	r.mu.RLock()
	p := r.devices[key].Player
	r.mu.RUnlock()
	if p == nil {
		return nil
	}
	err := p.refreshFixed(read)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (r *Registry) PortInUse() bool { return r.occupied.Load() }

// Start 与 Stop 由上层协议生命周期串行调用。
func (r *Registry) Start(parent context.Context) error {
	if r.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.occupied.Store(false)
		first := true
		for ctx.Err() == nil {
			err := r.serve(ctx, func() {
				r.occupied.Store(false)
				if first {
					ready <- nil
					first = false
				}
			})
			if ctx.Err() != nil {
				break
			}
			busy := errors.Is(err, syscall.EADDRINUSE)
			r.occupied.Store(busy)
			r.logger.Warn("SlimProto 监听中断", "event", "slimproto.listen.failed", "module", "slimproto", "error", err)
			if first {
				if !busy {
					ready <- err
					return
				}
				ready <- nil
				first = false
			}
			select {
			case <-ctx.Done():
			case <-time.After(refreshInterval):
			}
		}
		if first {
			ready <- ctx.Err()
		}
	}()
	if err := <-ready; err != nil {
		cancel()
		<-done
		return err
	}
	r.cancel, r.done = cancel, done
	return nil
}

func (r *Registry) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel = nil
}

func (r *Registry) serve(parent context.Context, ready func()) error {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{Port: int(listenPort)})
	if err != nil {
		return err
	}
	defer listener.Close()
	d := newDiscovery(ctx, r.name, r.logger, &wg)
	defer d.close()
	if err = d.refresh(); err != nil {
		return err
	}
	failures := make(chan error, 1)
	wg.Go(func() { failures <- r.accept(ctx, listener, &wg) })
	ready()
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case packet := <-d.packets:
			d.respond(packet)
		case <-ticker.C:
			if err := d.refresh(); err != nil {
				if errors.Is(err, syscall.EADDRINUSE) {
					return err
				}
				r.logger.Warn("刷新 SlimProto 网卡失败", "event", "slimproto.interfaces.failed", "module", "slimproto", "error", err)
			}
		}
	}
}

func (r *Registry) accept(ctx context.Context, listener *net.TCPListener, wg *sync.WaitGroup) error {
	slots := make(chan struct{}, 64)
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			return err
		}
		if !allowedPeer(conn.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap()) {
			conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
			wg.Go(func() { defer func() { <-slots }(); r.handle(ctx, conn) })
		default:
			conn.Close()
			r.logger.Warn("SlimProto 控制连接已达上限", "event", "slimproto.connection.limit", "module", "slimproto")
		}
	}
}

func (r *Registry) Offline(key string, player *Player) {
	r.mu.Lock()
	if r.devices[key].Player == player {
		delete(r.devices, key)
	}
	r.mu.Unlock()
}
