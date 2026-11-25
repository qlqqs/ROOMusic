package slimproto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func upstreamFrame(command string, payload []byte) []byte {
	frame := append([]byte(command), 0, 0, 0, 0)
	binary.BigEndian.PutUint32(frame[4:], uint32(len(payload)))
	return append(frame, payload...)
}

func TestSlimProtoReadFrame(t *testing.T) {
	first := upstreamFrame("TEST", []byte{1, 2, 3})
	second := upstreamFrame("STAT", []byte{4, 5})
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		for _, part := range [][]byte{first[:2], first[2:9], append(first[9:], second...)} {
			if _, err := writer.Write(part); err != nil {
				return
			}
		}
	}()
	for _, want := range [][]byte{first, second} {
		command, payload, err := readFrame(reader)
		if err != nil || command != string(want[:4]) || !bytes.Equal(payload, want[8:]) {
			t.Fatalf("readFrame = %q %x %v，期望 %q %x", command, payload, err, want[:4], want[8:])
		}
	}
	if _, _, err := readFrame(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("两帧之后期望 EOF，实际 %v", err)
	}
	max := bytes.Repeat([]byte{0x5a}, 64<<10)
	gotCommand, gotPayload, err := readFrame(bytes.NewReader(upstreamFrame("RESP", max)))
	if err != nil || gotCommand != "RESP" || !bytes.Equal(gotPayload, max) {
		t.Fatalf("64 KiB 上行载荷读取结果 %q，长度 %d，错误 %v", gotCommand, len(gotPayload), err)
	}
}

func TestSlimProtoEncodeFrame(t *testing.T) {
	got, err := encodeFrame("aude", []byte{1, 1})
	if want := []byte{0, 6, 'a', 'u', 'd', 'e', 1, 1}; err != nil || !bytes.Equal(got, want) {
		t.Fatalf("encodeFrame = %x %v，期望 %x", got, err, want)
	}
	got, err = encodeFrame("strm", make([]byte, 4092))
	if err != nil || len(got) != 4098 || !bytes.Equal(got[:2], []byte{0x10, 0x00}) {
		t.Fatalf("4096 字节命令字和载荷编码后长度 %d，错误 %v", len(got), err)
	}
	if _, err := encodeFrame("strm", make([]byte, 4093)); err == nil {
		t.Fatal("接受了超过 4096 字节的命令字和载荷")
	}
	if _, err := encodeFrame("bad", nil); err == nil {
		t.Fatal("接受了三字节命令字")
	}
}

func helloPayload(channels uint16, capabilities string) []byte {
	p := make([]byte, 36)
	copy(p, []byte{12, 5, 0x02, 0xab, 0xcd, 0xef, 0x00, 0x01})
	binary.BigEndian.PutUint16(p[24:], channels)
	binary.BigEndian.PutUint64(p[26:], 0x0123456789abcdef)
	return append(p, capabilities...)
}

func TestSlimProtoParseHello(t *testing.T) {
	want := Hello{
		DeviceID: 12, Revision: 5, MAC: "02:ab:cd:ef:00:01", Reconnect: true,
		BytesReceived: 0x0123456789abcdef, Model: "squeezelite", ModelName: "SqueezeLite",
		Firmware: "2.0.0-1613", MaxSampleRate: 384000, AccuratePlayPoints: true,
		Codecs: []string{"dsf", "flc", "pcm", "mp3"},
	}
	p := helloPayload(0xc001, "Model=squeezelite,ModelName=SqueezeLite,Firmware=2.0.0-1613,AccuratePlayPoints=1,MaxSampleRate=384000,HasDigitalOut=1,dsf, flc,pcm,mp3\x00,ogg")
	if got, ok := parseHello(p); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseHello = %+v, %t，期望 %+v", got, ok, want)
	}
	for _, caps := range []string{"pcm", "MaxSampleRate=,pcm", "MaxSampleRate=48000Hz,pcm", "MaxSampleRate=0,pcm", "MaxSampleRate=-1,pcm", "MaxSampleRate=2147483648,pcm"} {
		t.Run(caps, func(t *testing.T) {
			got, ok := parseHello(helloPayload(0x8001, caps))
			if !ok || got.Reconnect || got.MaxSampleRate != 44100 || !slices.Equal(got.Codecs, []string{"pcm"}) {
				t.Fatalf("parseHello = %+v, %t，期望缺省 44100、仅 pcm、无重连标记", got, ok)
			}
		})
	}
	if _, ok := parseHello(p[:35]); ok {
		t.Fatal("接受了 35 字节 HELO")
	}
}

func TestSlimProtoParseStatus(t *testing.T) {
	// 按调研 §6 的线序排列，未解析的字段也填入占位字节以独立校验各偏移。
	p := []byte{
		'S', 'T', 'M', 't', 0xf1, 0xf2, 0xf3,
		0x01, 0x02, 0x03, 0x04, 0x11, 0x12, 0x13, 0x14,
		0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0xff, 0xff,
		0x31, 0x32, 0x33, 0x34, 0x41, 0x42, 0x43, 0x44, 0x51, 0x52, 0x53, 0x54,
		0x61, 0x62, 0x63, 0x64, 0xfe, 0xfd, 0x71, 0x72, 0x73, 0x74,
		0x81, 0x82, 0x83, 0x84, 0x91, 0x92,
	}
	want := Status{
		Event: "STMt", StreamSize: 0x01020304, StreamFull: 0x11121314, BytesReceived: 0x2122232425262728,
		Jiffies: 0x31323334, OutputSize: 0x41424344, OutputFull: 0x51525354, ElapsedSeconds: 0x61626364,
		ElapsedMS: 0x71727374, ServerTimestamp: 0x81828384, ErrorCode: 0x9192,
	}
	if got, ok := parseStatus(p); !ok || got != want {
		t.Fatalf("parseStatus = %+v, %t，期望 %+v", got, ok, want)
	}
	if _, ok := parseStatus(p[:52]); ok {
		t.Fatal("接受了 52 字节 STAT")
	}
}

func TestSlimProtoStreamCommands(t *testing.T) {
	const request = "GET /upnp/streams/test HTTP/1.0\r\nHost: 192.168.1.23:4660\r\n\r\n"
	for _, tc := range []struct {
		rate      int
		threshold byte
	}{{96000, 0}, {176400, 20}, {384000, 20}} {
		want := []byte{0, byte(28 + len(request)), 's', 't', 'r', 'm',
			's', '1', 'f', '?', '?', '?', '?', 255, 0, 0, '0', 0, tc.threshold, 0,
			0, 0, 0, 0, 0x12, 0x34, 192, 168, 1, 23}
		want = append(want, request...)
		got, err := startStream(netip.MustParseAddr("192.168.1.23"), 0x1234, "/upnp/streams/test", tc.rate)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%d Hz strm s = %x %v，期望 %x", tc.rate, got, err, want)
		}
	}
	for _, command := range []byte("puqft") {
		var want [30]byte
		copy(want[:], []byte{0, 28, 's', 't', 'r', 'm', command})
		got, err := encodeFrame("strm", control(command))
		if err != nil || !bytes.Equal(got, want[:]) {
			t.Fatalf("strm %c = %x %v，期望 %x", command, got, err, want)
		}
	}
	if _, err := startStream(netip.MustParseAddr("192.168.1.23"), 4660, "/"+strings.Repeat("x", 4096), 44100); err == nil {
		t.Fatal("接受了超过播放器帧上限的 HTTP 请求")
	}
}

func TestSlimProtoVolumePayload(t *testing.T) {
	for _, tc := range []struct {
		name      string
		level     float64
		fixed     bool
		adjust    byte
		old, gain uint32
	}{
		{"全音量", 1, false, 1, 128, 65536},
		{"低音量分段", .125, false, 1, 0, 110},
		{"分段边界", .25, false, 1, 2, 926},
		{"高音量分段", .625, false, 1, 15, 7680},
		{"固定音量", 1, true, 0, 128, 65536},
		{"静音", 0, false, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := volumePayload(tc.level, tc.fixed)
			if len(got) != 18 || got[8] != tc.adjust {
				t.Fatalf("audg 长度或 adjust 错误，报文 %x", got)
			}
			for _, field := range []struct {
				offset int
				want   uint32
			}{{0, tc.old}, {4, tc.old}, {10, tc.gain}, {14, tc.gain}} {
				if n := binary.BigEndian.Uint32(got[field.offset:]); n != field.want {
					t.Errorf("audg 偏移 %d 的增益 = %d，期望 %d", field.offset, n, field.want)
				}
			}
		})
	}
}
