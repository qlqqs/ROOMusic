package rtp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

func TestAirPlayUncompressedALAC(t *testing.T) {
	for _, depth := range []int{16, 24} {
		for _, frames := range []int{1, FramesPerPacket} {
			t.Run(fmt.Sprintf("%d-bit/%d-frames", depth, frames), func(t *testing.T) {
				pcm := make([]byte, frames*depth/4)
				for i := range pcm {
					pcm[i] = byte(1 + i*97)
				}
				data, err := ALAC(pcm, depth)
				if err != nil {
					t.Fatal(err)
				}
				offset := 0
				read := func(width int) uint32 {
					var x uint32
					for range width {
						x = x<<1 | uint32(data[offset/8]>>uint(7-offset%8)&1)
						offset++
					}
					return x
				}
				if element, reserved := read(3), read(16); element != 1 || reserved != 0 {
					t.Fatalf("ALAC CPE = %d，保留位 = %d", element, reserved)
				}
				if flag := read(4); frames == FramesPerPacket && flag != 1 || frames != FramesPerPacket && flag != 9 {
					t.Fatalf("ALAC 帧标志 = %#x", flag)
				}
				if frames != FramesPerPacket && read(32) != uint32(frames) {
					t.Fatal("短帧样本计数错误")
				}
				for i := 0; i < len(pcm); i += depth / 8 {
					var want uint32
					for j := range depth / 8 {
						want |= uint32(pcm[i+j]) << uint(8*j)
					}
					if got := read(depth); got != want {
						t.Fatalf("采样 %d = %#x，期望 %#x", i/(depth/8), got, want)
					}
				}
				if end := read(3); end != 7 {
					t.Fatalf("ALAC 结束标记 = %d", end)
				}
			})
		}
	}
	for _, pcm := range [][]byte{nil, {1, 2, 3}, make([]byte, (FramesPerPacket+1)*4)} {
		if _, err := ALAC(pcm, 16); err == nil {
			t.Errorf("接受了 %d 字节的无效 PCM", len(pcm))
		}
	}
	if _, err := ALAC(make([]byte, 4), 20); err == nil {
		t.Error("接受了 20 位 PCM")
	}
}

func loopUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func udpPacket(t *testing.T, c *net.UDPConn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 2048)
	n, _, err := c.ReadFromUDP(b)
	if err != nil {
		t.Fatal(err)
	}
	return b[:n]
}

func TestAirPlayRTPSequenceTimingAndRetransmit(t *testing.T) {
	audio, control, timing := loopUDP(t), loopUDP(t), loopUDP(t)
	transport := &Transport{
		audio: loopUDP(t), control: loopUDP(t), timing: loopUDP(t), peer: netip.MustParseAddr("127.0.0.1"),
		rate: 44100, depth: 16, seq: 65535, stamp: 0xffffff00, ssrc: 0x07301fce, first: true,
		latency: 88200,
	}
	transport.wg.Add(2)
	go transport.serveControl()
	go transport.serveTiming()
	t.Cleanup(transport.Close)
	transport.Configure(audio.LocalAddr().(*net.UDPAddr).AddrPort().Port(), control.LocalAddr().(*net.UDPAddr).AddrPort().Port(), nil)
	at := time.Unix(1_700_000_000, 500_000_000)
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, FramesPerPacket)
	send := func() []byte {
		t.Helper()
		if err := transport.Send(pcm, at); err != nil {
			t.Fatal(err)
		}
		return udpPacket(t, audio)
	}
	first := send()
	sync := udpPacket(t, control)
	const ntpAt = 3908988800<<32 | 1<<31
	if len(sync) != 20 || sync[0] != 0x90 || sync[1] != 0xd4 || be.Uint64(sync[8:]) != ntpAt ||
		be.Uint32(sync[4:]) != uint32(0xffffff00-88200) || be.Uint32(sync[16:]) != 0xffffff00 {
		t.Fatalf("NTP 同步包 = %x", sync)
	}
	second := send()
	if first[0] != 0x80 || first[1] != 0xe0 || be.Uint16(first[2:]) != 65535 || be.Uint32(first[4:]) != 0xffffff00 ||
		second[1] != 96 || be.Uint16(second[2:]) != 0 || be.Uint32(second[4:]) != 96 || be.Uint32(second[8:]) != 0x07301fce ||
		!bytes.Equal(first[12:], second[12:]) {
		t.Fatalf("RTP 首包和回绕包头 = %x / %x", first[:12], second[:12])
	}
	request := func(seq, count uint16) {
		t.Helper()
		query := []byte{0x80, 0xd5, 0, 3}
		query = be.AppendUint16(query, seq)
		query = be.AppendUint16(query, count)
		if _, err := control.WriteToUDP(query, transport.control.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
	}
	request(65535, 2)
	for _, expected := range [][]byte{first, second} {
		b := udpPacket(t, control)
		if len(b) != len(expected)+4 || b[0] != 0x80 || b[1] != 0xd6 ||
			!bytes.Equal(b[2:4], expected[2:4]) || !bytes.Equal(b[4:], expected) {
			t.Fatalf("RTP 重传头 = %x", b[:min(len(b), 16)])
		}
	}
	for range len(transport.history) - 1 {
		send()
	}
	request(65535, 2)
	if b := udpPacket(t, control); len(b) != len(second)+4 || !bytes.Equal(b[4:], second) {
		t.Fatalf("滚动窗口应只保留序号 0，响应 = %x", b[:min(len(b), 16)])
	}
	control.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, err := control.ReadFromUDP(make([]byte, 2048)); !os.IsTimeout(err) {
		t.Fatalf("过期重传仍收到响应，错误 %v", err)
	}

	query := make([]byte, 32)
	query[0], query[1] = 0x80, 0xd2
	be.PutUint64(query[24:], ntpAt)
	before := ntp(time.Now())
	if _, err := timing.WriteToUDP(query, transport.timing.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	b := udpPacket(t, timing)
	after := ntp(time.Now())
	if len(b) != 32 || b[0] != 0x80 || b[1] != 0xd3 || be.Uint64(b[8:]) != ntpAt {
		t.Fatalf("NTP 回复 = %x", b)
	}
	received, sent := be.Uint64(b[16:]), be.Uint64(b[24:])
	if received < before || sent < received || sent > after {
		t.Fatalf("NTP 接收、发送时间 %d/%d 超出 %d~%d", received, sent, before, after)
	}

	const gm = 0x123456789abcdef0
	transport.SetPTPClock(gm, 125_000)
	next, _, wantNS, _ := transport.PresentationAnchor(at)
	audioPacket := send()
	b = udpPacket(t, control)
	if len(b) != 28 || b[0] != 0x90 || b[1] != 0xd7 || be.Uint32(b[16:]) != next ||
		be.Uint64(b[20:]) != gm || be.Uint64(b[8:])+uint64(2*time.Second) != uint64(wantNS) ||
		be.Uint32(audioPacket[4:]) != next || be.Uint32(audioPacket[8:]) != 0 {
		t.Fatalf("PTP 锚点 = %x，RTP 头 = %x", b, audioPacket[:12])
	}
}

func TestAirPlayRTPChaChaAudioAuthentication(t *testing.T) {
	aead, _ := chacha20poly1305.New(bytes.Repeat([]byte{0x42}, 32))
	header, pcm := []byte{0x80, 0xe0, 0x00, 0x04, 1, 2, 3, 4, 5, 6, 7, 8}, []byte("ALAC fixture")
	const counter = 65537
	sealed := ChaCha(aead)(header, pcm, counter)
	nonce := binary.LittleEndian.AppendUint64(make([]byte, 4), counter)
	data, suffix := sealed[:len(sealed)-8], sealed[len(sealed)-8:]
	got, err := aead.Open(nil, nonce, data, header[4:])
	if !bytes.Equal(suffix, nonce[4:]) || err != nil || !bytes.Equal(got, pcm) {
		t.Fatalf("AirPlay 2 音频解密 = %q，错误 %v", got, err)
	}
	header[4] ^= 1
	if _, err := aead.Open(nil, nonce, data, header[4:]); err == nil {
		t.Fatal("修改的 RTP 时间戳仍通过认证")
	}
}
