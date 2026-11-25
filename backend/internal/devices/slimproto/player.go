package slimproto

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

type playerEvent struct {
	Status
	at time.Time
}

type writeRequest struct {
	frame   []byte
	written chan struct{}
}
type Player struct {
	lastSTAT           time.Time
	clock              ClockEstimator
	modeMu             sync.Mutex
	local              netip.Addr
	ctx                context.Context
	cancel             context.CancelFunc
	queue              chan writeRequest
	mu                 sync.Mutex
	events             chan playerEvent
	fixed, modeSet     bool
	grouped, gainDirty bool
	volume             float64
}

func (p *Player) Done() <-chan struct{} { return p.ctx.Done() }

func (p *Player) send(frame []byte) error {
	return p.enqueue(writeRequest{frame: frame})
}
func (p *Player) enqueue(req writeRequest) error {
	if p.ctx.Err() != nil {
		return net.ErrClosed
	}
	select {
	case p.queue <- req:
		return nil
	default:
		p.cancel()
		return net.ErrClosed
	}
}
func (p *Player) command(name string, payload []byte) error {
	frame, err := encodeFrame(name, payload)
	if err != nil {
		return err
	}
	return p.send(frame)
}

// 写完停止命令后才允许交接，协议关闭时也有机会先停掉播放器的音频。
func (p *Player) requestStop() <-chan struct{} {
	frame, _ := encodeFrame("strm", control('q'))
	done := make(chan struct{})
	if p.enqueue(writeRequest{frame: frame, written: done}) != nil {
		close(done)
	}
	return done
}

func (p *Player) stopOutput() { p.waitStop(p.requestStop()) }

func (p *Player) waitStop(done <-chan struct{}) {
	select {
	case <-done:
	case <-p.ctx.Done():
	case <-time.After(5 * time.Second):
		p.cancel()
	}
}

func (p *Player) refreshFixed(read func() (bool, error)) error {
	p.modeMu.Lock()
	defer p.modeMu.Unlock()
	fixed, err := read()
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modeSet = true
	p.fixed = fixed
	if p.grouped {
		p.gainDirty = true
		return nil
	}
	return p.command("audg", volumePayload(p.volume, p.fixed))
}
func (p *Player) setVolume(v float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v == p.volume && !p.gainDirty {
		return nil
	}
	p.gainDirty = false
	p.volume = v
	return p.command("audg", volumePayload(v, p.fixed))
}

func (p *Player) groupControl(active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grouped = active
	if !active && p.gainDirty {
		p.gainDirty = false
		// 输出交回前应用最后一次增益变更；队列已关闭时由连接收尾。
		p.command("audg", volumePayload(p.volume, p.fixed))
	}
}

func (p *Player) subscribe() chan playerEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = make(chan playerEvent, 64)
	return p.events
}
func (p *Player) unsubscribe(ch chan playerEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events == ch {
		p.events = nil
	}
}
func (p *Player) silentSince(at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.lastSTAT.After(at)
}

func (p *Player) target(now, at time.Time) (uint32, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	target, valid := p.clock.Target(now, at)
	// Squeezelite 在写音频时直接比较 now + 10000；回绕前的最后十秒会错误地立即播放。
	current := uint32(now.Sub(p.clock.epoch).Milliseconds())
	wrap := target < current || target > ^uint32(0)-10000
	return target, valid, wrap
}

func (p *Player) stat(st Status, now time.Time) {
	p.mu.Lock()
	p.lastSTAT = now
	at := p.clock.Observe(st.Jiffies, now)
	p.mu.Unlock()
	p.notify(playerEvent{st, at})
}

func (p *Player) notify(e playerEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events != nil {
		select {
		case p.events <- e:
		default:
			p.cancel()
		}
	}
}

func (r *Registry) handle(parent context.Context, conn *net.TCPConn) {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		} else {
			conn.Close()
		}
	}()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	op, body, err := readFrame(conn)
	if err != nil || op != "HELO" {
		return
	}
	hello, ok := parseHello(body)
	if !ok {
		return
	}
	key := "slim:" + hello.MAC
	local := conn.LocalAddr().(*net.TCPAddr).AddrPort().Addr().Unmap()
	p := &Player{ctx: ctx, cancel: cancel, local: local, queue: make(chan writeRequest, 64), volume: 1}
	name := hello.ModelName
	if name == "" {
		name = hello.Model
	}
	if name == "" {
		name = key
	}
	d := Device{Hello: hello, Key: key, Name: name, Address: conn.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap(), LocalAddress: local, Player: p}
	r.mu.Lock()
	old := r.devices[key]
	r.devices[key] = d
	r.mu.Unlock()
	if old.Player != nil {
		old.Player.cancel()
	}
	defer r.Offline(key, p)
	fixed := false
	if r.configure != nil {
		configCtx, finishConfig := context.WithTimeout(ctx, 5*time.Second)
		fixed, err = r.configure(configCtx, key, hello.MaxSampleRate)
		finishConfig()
		if err != nil {
			if ctx.Err() == nil {
				r.logger.Warn("读取播放器音量方式失败", "event", "slimproto.configure.failed", "module", "slimproto", "error", err)
			}
			return
		}
	}
	p.mu.Lock()
	if !p.modeSet {
		p.fixed = fixed
	}
	initialGain := volumePayload(p.volume, p.fixed)
	p.mu.Unlock()
	r.mu.Lock()
	if r.devices[key].Player == p {
		d.ready = true
		r.devices[key] = d
	}
	r.mu.Unlock()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer cancel()
		p.writeLoop(conn, initialGain)
	}()
	defer func() { cancel(); <-writerDone }()
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for ctx.Err() == nil {
		op, body, err := readFrame(conn)
		if err != nil {
			return
		}
		now := time.Now()
		switch op {
		case "STAT":
			st, ok := parseStatus(body)
			if !ok {
				continue
			}
			conn.SetReadDeadline(now.Add(15 * time.Second))
			p.stat(st, now)
		case "SETD":
			if len(body) > 1 && body[0] == 0 {
				if name := cleanText(body[1:]); name != "" {
					r.mu.Lock()
					if r.devices[key].Player == p {
						d.Name = name
						r.devices[key] = d
					}
					r.mu.Unlock()
				}
			}
		case "DSCO":
			if len(body) > 0 && body[0] != 0 {
				p.notify(playerEvent{Status{Event: "DSCO", ErrorCode: uint16(body[0])}, now})
			}
		}
	}
}

func (p *Player) writeLoop(conn net.Conn, gain []byte) {
	for _, cmd := range []struct {
		name    string
		payload []byte
	}{
		{"aude", []byte{1, 1}}, {"audg", gain}, {"setd", []byte{0}}, {"strm", control('q')},
	} {
		frame, err := encodeFrame(cmd.name, cmd.payload)
		if err != nil || writePlayer(conn, frame) != nil {
			return
		}
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	heartbeat, _ := encodeFrame("strm", control('t'))
	for p.ctx.Err() == nil {
		var req writeRequest
		select {
		case <-p.ctx.Done():
			return
		case req = <-p.queue:
		case <-ticker.C:
			p.mu.Lock()
			grouped := p.grouped
			p.mu.Unlock()
			if grouped {
				continue
			}
			req.frame = heartbeat
		}
		if writePlayer(conn, req.frame) != nil {
			return
		}
		if req.written != nil {
			close(req.written)
		}
	}
}

func writePlayer(conn net.Conn, frame []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	n, err := conn.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}
