// Package slimtest 提供进程内 SlimProto 测试播放器。
package slimtest

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

type Player struct {
	net.Conn
	reader  *bufio.Reader
	started time.Time
}

func Dial(t testing.TB, address string) *Player {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &Player{Conn: conn, reader: bufio.NewReader(conn), started: time.Now()}
}

func (p *Player) Send(t testing.TB, command string, body []byte) {
	t.Helper()
	frame := append([]byte(command), 0, 0, 0, 0)
	binary.BigEndian.PutUint32(frame[4:], uint32(len(body)))
	p.WritePacket(t, append(frame, body...))
}

func (p *Player) WritePacket(t testing.TB, packet []byte) {
	t.Helper()
	if err := p.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(packet); err != nil {
		t.Fatal(err)
	}
}

func (p *Player) Status(t testing.TB, event string, elapsedMS uint32) {
	t.Helper()
	var body [53]byte
	copy(body[:4], event)
	binary.BigEndian.PutUint32(body[25:], uint32(time.Since(p.started).Milliseconds()))
	binary.BigEndian.PutUint32(body[37:], elapsedMS/1000)
	binary.BigEndian.PutUint32(body[43:], elapsedMS)
	p.Send(t, "STAT", body[:])
}

func (p *Player) ReadFrame() ([]byte, error) {
	header, err := p.reader.Peek(2)
	if err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(header))
	if size < 4 || size > 4096 {
		return nil, fmt.Errorf("服务端 SlimProto 帧长度 = %d", size)
	}
	frame := make([]byte, size+2)
	if _, err := io.ReadFull(p.reader, frame); err != nil {
		return nil, fmt.Errorf("读取 SlimProto 帧载荷: %w", err)
	}
	return frame[2:], nil
}

// OpenStream 将 strm s 中的地址和请求字节直接用于取流。
func OpenStream(t testing.TB, payload []byte) *http.Response {
	t.Helper()
	if len(payload) < 24 || payload[0] != 's' {
		t.Fatalf("等待 strm s，收到 %q", payload)
	}
	port := binary.BigEndian.Uint16(payload[18:20])
	addr := netip.AddrFrom4([4]byte(payload[20:24]))
	conn, err := net.DialTimeout("tcp4", netip.AddrPortFrom(addr, port).String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload[24:]); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
