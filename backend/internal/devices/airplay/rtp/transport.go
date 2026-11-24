package rtp

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

var be = binary.BigEndian

// Encrypt 接收已定序的包头、ALAC 载荷与独立于 RTP 序号的包计数。
type Encrypt func(header, payload []byte, counter uint64) []byte

func ChaCha(aead cipher.AEAD) Encrypt {
	return func(header, payload []byte, counter uint64) []byte {
		var nonce [12]byte
		binary.LittleEndian.PutUint64(nonce[4:], counter)
		sealed := aead.Seal(nil, nonce[:], payload, header[4:12])
		return append(sealed, nonce[4:]...)
	}
}

type Transport struct {
	audio, control, timing *net.UDPConn
	peer                   netip.Addr
	rate, depth            int
	mu                     sync.Mutex
	audioPort, controlPort uint16
	seq                    uint16
	stamp, ssrc, latency   uint32
	counter                uint64
	grandmaster            uint64
	ptpOffsetNS            int64
	first                  bool
	lastSync               time.Time
	encrypt                Encrypt
	history                [512][]byte
	wg                     sync.WaitGroup
}

func New(local, peer netip.Addr, rate, depth int) (*Transport, error) {
	if !lan.IsLANAddress(local) || !lan.IsLANAddress(peer) {
		return nil, errors.New("RTP 需要局域网地址")
	}
	addr := net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, 0))
	t := &Transport{peer: peer, rate: rate, depth: depth, latency: uint32(rate * 2), first: true}
	var seed [10]byte
	rand.Read(seed[:])
	t.seq = be.Uint16(seed[:])
	t.stamp = be.Uint32(seed[2:])
	t.ssrc = be.Uint32(seed[6:])
	var err error
	for _, socket := range []**net.UDPConn{&t.audio, &t.control, &t.timing} {
		*socket, err = net.ListenUDP("udp4", addr)
		if err != nil {
			t.Close()
			return nil, err
		}
	}
	t.wg.Add(2)
	go t.serveTiming()
	go t.serveControl()
	return t, nil
}

func (t *Transport) Close() {
	for _, c := range []*net.UDPConn{t.timing, t.control, t.audio} {
		if c != nil {
			c.Close()
		}
	}
	t.wg.Wait()
}

func (t *Transport) Ports() (control, timing uint16) {
	return uint16(t.control.LocalAddr().(*net.UDPAddr).Port), uint16(t.timing.LocalAddr().(*net.UDPAddr).Port)
}

func (t *Transport) Configure(audio, control uint16, encrypt Encrypt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.audioPort, t.controlPort, t.encrypt = audio, control, encrypt
}

// SetPTPClock 将 RTP 锚点映射到时钟服务测得的 grandmaster 时间。
func (t *Transport) SetPTPClock(grandmaster uint64, offsetNS int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.grandmaster != grandmaster {
		t.first = true
	}
	t.grandmaster, t.ptpOffsetNS = grandmaster, offsetNS
}

func (t *Transport) SetLatency(samples uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.latency = samples
}

func (t *Transport) Latency() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Second * time.Duration(t.latency) / time.Duration(t.rate)
}

func (t *Transport) Position() (uint16, uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seq, t.stamp
}

// PresentationAnchor 返回待发首包在主钟上的预计呈现时刻。其与 syncPacket 的
// 路径共用下一个 RTP 时间戳、主时钟映射和端到端延迟。
func (t *Transport) PresentationAnchor(at time.Time) (stamp uint32, grandmaster uint64, ns int64, first bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stamp, t.grandmaster, at.UnixNano() + t.ptpOffsetNS + int64(time.Second)*int64(t.latency)/int64(t.rate), t.first
}

func (t *Transport) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.history[:])
	t.first = true
}

func ntp(at time.Time) uint64 {
	return uint64(at.Unix()+2208988800)<<32 | uint64(at.Nanosecond())<<32/1_000_000_000
}

// Send 发送一个包并在控制端口建立播放时间戳与当前 NTP 时间的映射。
// 字段见 Unofficial AirPlay Specification 的 audio/rtp_streams，行为参考 pyatv（MIT）。
func (t *Transport) Send(pcm []byte, at time.Time) error {
	payload, err := ALAC(pcm, t.depth)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.audioPort == 0 || t.controlPort == 0 {
		return errors.New("RTP 接收端端口未协商")
	}
	if t.first || at.Sub(t.lastSync) >= time.Second {
		b := t.syncPacket(at)
		if _, err = t.control.WriteToUDPAddrPort(b, netip.AddrPortFrom(t.peer, t.controlPort)); err != nil {
			return err
		}
		t.lastSync = at
	}
	b := make([]byte, 12, len(payload)+36)
	b[0], b[1] = 0x80, 96
	if t.first {
		b[1] |= 0x80
	}
	be.PutUint16(b[2:], t.seq)
	be.PutUint32(b[4:], t.stamp)
	if t.grandmaster == 0 {
		be.PutUint32(b[8:], t.ssrc)
	}
	if t.encrypt != nil {
		payload = t.encrypt(b, payload, t.counter)
	}
	b = append(b, payload...)
	if _, err = t.audio.WriteToUDPAddrPort(b, netip.AddrPortFrom(t.peer, t.audioPort)); err != nil {
		return err
	}
	t.history[int(t.seq)%len(t.history)] = b
	t.first = false
	t.counter++
	t.seq++
	t.stamp += uint32(len(pcm) / (2 * t.depth / 8))
	return nil
}

func (t *Transport) serveTiming() {
	defer t.wg.Done()
	var b [64]byte
	for {
		n, from, err := t.timing.ReadFromUDPAddrPort(b[:])
		if err != nil {
			return
		}
		at := time.Now()
		if from.Addr() != t.peer || n != 32 || b[1]&0x7f != 82 {
			continue
		}
		var reply [32]byte
		reply[0], reply[1] = 0x80, 0xd3
		be.PutUint16(reply[2:], 7)
		copy(reply[8:16], b[24:32])
		be.PutUint64(reply[16:], ntp(at))
		be.PutUint64(reply[24:], ntp(time.Now()))
		t.timing.WriteToUDPAddrPort(reply[:], from)
	}
}

func (t *Transport) serveControl() {
	defer t.wg.Done()
	var b [64]byte
	for {
		n, from, err := t.control.ReadFromUDPAddrPort(b[:])
		if err != nil {
			return
		}
		if n != 8 || from.Addr() != t.peer || b[1]&0x7f != 85 {
			continue
		}
		t.mu.Lock()
		if from.Port() == t.controlPort {
			first := be.Uint16(b[4:])
			for i := 0; i < min(int(be.Uint16(b[6:])), len(t.history)); i++ {
				seq := first + uint16(i)
				pkt := t.history[int(seq)%len(t.history)]
				if len(pkt) == 0 || be.Uint16(pkt[2:]) != seq {
					continue
				}
				reply := make([]byte, 4, len(pkt)+4)
				reply[0], reply[1] = 0x80, 0xd6
				be.PutUint16(reply[2:], seq)
				t.control.WriteToUDPAddrPort(append(reply, pkt...), from)
			}
		}
		t.mu.Unlock()
	}
}

// 行为参考 shairport-sync 5.6.3 的实时流：0xd7 锚点带 PTP 纳秒及主时钟标识，
// 两个 RTP 时间戳的差包含发送预缓冲，接收端另计四分之一秒的渲染延迟。
func (t *Transport) syncPacket(at time.Time) []byte {
	flags := byte(0x80)
	if t.first {
		flags = 0x90
	}
	if t.grandmaster == 0 {
		p := make([]byte, 20)
		p[0], p[1] = flags, 0xd4
		be.PutUint16(p[2:], 7)
		be.PutUint32(p[4:], t.stamp-t.latency)
		be.PutUint64(p[8:], ntp(at))
		be.PutUint32(p[16:], t.stamp)
		return p
	}
	p := make([]byte, 28)
	p[0], p[1] = flags, 0xd7
	be.PutUint16(p[2:], 6)
	receiverLatency := uint32(t.rate / 4)
	be.PutUint32(p[4:], t.stamp-t.latency+receiverLatency)
	be.PutUint64(p[8:], uint64(at.UnixNano()+t.ptpOffsetNS))
	be.PutUint32(p[16:], t.stamp)
	be.PutUint64(p[20:], t.grandmaster)
	return p
}
