package castv2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestCastFrameWireFormat(t *testing.T) {
	// 独立列出 CastMessage 的六个字段，避免读写端的同一种错误相互抵消。
	wire := []byte("\x00\x00\x00\x11\x08\x00\x12\x01s\x1a\x01d\x22\x01n\x28\x00\x32\x02{}")
	want := Message{Source: "s", Destination: "d", Namespace: "n", Payload: []byte("{}")}
	var buf bytes.Buffer
	if err := Write(&buf, want); err != nil || !bytes.Equal(buf.Bytes(), wire) {
		t.Fatalf("编码 = %x，错误 %v，期望 %x", buf.Bytes(), err, wire)
	}
	// 分片读取两条连续帧，第二条含接收端可能添加的未知 protobuf 字段。
	extended := append(append([]byte(nil), wire...), 0x38, 0x01)
	binary.BigEndian.PutUint32(extended, uint32(len(extended)-4))
	stream := iotest.OneByteReader(bytes.NewReader(append(wire, extended...)))
	for i := range 2 {
		if got, err := Read(stream); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 帧 = %+v，错误 %v", i+1, got, err)
		}
	}
	if _, err := Read(stream); !errors.Is(err, io.EOF) {
		t.Fatalf("读完两帧后的错误 = %v", err)
	}
}

func TestCastFrameRejectsInvalidInput(t *testing.T) {
	valid := []byte("\x08\x00\x12\x01s\x1a\x01d\x22\x01n\x28\x00\x32\x02{}")
	for _, tc := range []struct {
		name string
		body []byte
		size int
		want error
	}{
		{"zero", nil, 0, errFrame},
		{"too_large", nil, 65537, errFrame},
		{"truncated", valid, len(valid) + 1, io.ErrUnexpectedEOF},
		{"missing_version", valid[2:], len(valid) - 2, errFrame},
		{"wrong_wire_type", append([]byte{0x0a}, valid[1:]...), len(valid), errFrame},
		{"binary_payload", append(append([]byte{}, valid[:12]...), append([]byte{1}, valid[13:]...)...), len(valid), errFrame},
		{"invalid_json", append(append([]byte{}, valid[:15]...), 'x', 'x'), len(valid), errFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := append(binary.BigEndian.AppendUint32(nil, uint32(tc.size)), tc.body...)
			if _, err := Read(bytes.NewReader(wire)); !errors.Is(err, tc.want) {
				t.Fatalf("读取错误 = %v，期望 %v", err, tc.want)
			}
		})
	}
	if _, err := Read(strings.NewReader("\x00\x00\x00")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("截断长度前缀的错误 = %v", err)
	}
	var buf bytes.Buffer
	m := Message{Source: "s", Destination: "d", Namespace: "n", Payload: []byte(`"` + strings.Repeat("a", 65534) + `"`)}
	if err := Write(&buf, m); !errors.Is(err, errFrame) || buf.Len() != 0 {
		t.Fatalf("含 protobuf 字段后超长的帧写入了 %d 字节，错误 %v", buf.Len(), err)
	}
}
