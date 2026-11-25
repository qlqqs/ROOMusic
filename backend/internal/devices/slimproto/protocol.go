// Package slimproto 接收局域网播放器的控制连接并交付托管音频。
package slimproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

var be = binary.BigEndian

func readFrame(r io.Reader) (string, []byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", nil, err
	}
	size := be.Uint32(header[4:])
	if size > 64<<10 {
		return "", nil, errors.New("SlimProto 上行帧过长")
	}
	payload := make([]byte, size)
	_, err := io.ReadFull(r, payload)
	return string(header[:4]), payload, err
}

func encodeFrame(command string, payload []byte) ([]byte, error) {
	if len(command) != 4 || len(payload) > 4096-4 {
		return nil, errors.New("SlimProto 下行帧无效")
	}
	frame := make([]byte, 6+len(payload))
	be.PutUint16(frame, uint16(len(frame)-2))
	copy(frame[2:6], command)
	copy(frame[6:], payload)
	return frame, nil
}

type Hello struct {
	DeviceID, Revision              byte
	MAC, Model, ModelName, Firmware string
	MaxSampleRate                   int
	Reconnect, AccuratePlayPoints   bool
	BytesReceived                   uint64
	Codecs                          []string
}

func parseHello(p []byte) (Hello, bool) {
	if len(p) < 36 {
		return Hello{}, false
	}
	h := Hello{DeviceID: p[0], Revision: p[1], MAC: net.HardwareAddr(p[2:8]).String(), Reconnect: be.Uint16(p[24:])&0x4000 != 0, BytesReceived: be.Uint64(p[26:]), MaxSampleRate: 44100}
	for _, part := range strings.Split(cleanText(p[36:]), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "Model":
			h.Model = value
		case "ModelName":
			h.ModelName = value
		case "Firmware":
			h.Firmware = value
		case "MaxSampleRate":
			if rate, err := strconv.Atoi(value); err == nil && rate > 0 && rate <= math.MaxInt32 {
				h.MaxSampleRate = rate
			}
		case "AccuratePlayPoints":
			h.AccuratePlayPoints = value == "1"
		default:
			if !ok && key != "" {
				h.Codecs = append(h.Codecs, key)
			}
		}
	}
	return h, true
}

func cleanText(p []byte) string {
	text, _, _ := strings.Cut(string(p), "\x00")
	return strings.TrimSpace(strings.ToValidUTF8(text, "�"))
}

type Status struct {
	Event                                          string
	Jiffies, ElapsedMS, ElapsedSeconds             uint32
	StreamSize, StreamFull, OutputSize, OutputFull uint32
	BytesReceived                                  uint64
	ServerTimestamp                                uint32
	ErrorCode                                      uint16
}

func parseStatus(p []byte) (Status, bool) {
	if len(p) < 53 {
		return Status{}, false
	}
	return Status{
		Event: string(p[:4]), StreamSize: be.Uint32(p[7:]), StreamFull: be.Uint32(p[11:]),
		BytesReceived: be.Uint64(p[15:]), Jiffies: be.Uint32(p[25:]),
		OutputSize: be.Uint32(p[29:]), OutputFull: be.Uint32(p[33:]),
		ElapsedSeconds: be.Uint32(p[37:]), ElapsedMS: be.Uint32(p[43:]),
		ServerTimestamp: be.Uint32(p[47:]), ErrorCode: be.Uint16(p[51:]),
	}, true
}

func control(command byte) []byte {
	payload := make([]byte, 24)
	payload[0] = command
	return payload
}

func controlMS(command byte, ms uint32) []byte {
	p := control(command)
	be.PutUint32(p[14:], ms)
	return p
}

func startStream(ip netip.Addr, port uint16, path string, rate int, options ...byte) ([]byte, error) {
	if !ip.Is4() || port == 0 || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \r\n") {
		return nil, errors.New("SlimProto 取流地址无效")
	}
	p := control('s')
	copy(p[1:7], "1f????")
	p[7], p[10] = 255, '0'
	switch len(options) {
	case 0:
	case 2:
		p[1], p[7] = options[0], options[1]
	default:
		return nil, errors.New("开流参数须同时指定自动起播和缓冲阈值")
	}
	if rate >= 176400 {
		p[12] = 20
	}
	be.PutUint16(p[18:], port)
	addr := ip.As4()
	copy(p[20:], addr[:])
	request := fmt.Sprintf("GET %s HTTP/1.0\r\nHost: %s\r\n\r\n", path, netip.AddrPortFrom(ip, port))
	return encodeFrame("strm", append(p, request...))
}

func volumePayload(level float64, fixed bool) []byte {
	p := make([]byte, 18)
	if !fixed {
		p[8] = 1
	}
	p[9] = 255
	if level <= 0 {
		return p
	}
	level = min(level, 1)
	db := -74 + 37*level/0.25
	if level > 0.25 {
		db = 37 * (level - 1) / 0.75
	}
	amplitude := math.Pow(10, db/20)
	gain := uint32(math.Round(amplitude * 65536))
	if db >= -30 {
		gain = uint32(math.Round(amplitude*256)) << 8
	}
	old := uint32(math.Round(amplitude * 128))
	be.PutUint32(p, old)
	be.PutUint32(p[4:], old)
	be.PutUint32(p[10:], gain)
	be.PutUint32(p[14:], gain)
	return p
}
