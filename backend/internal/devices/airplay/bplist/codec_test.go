package bplist

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestAirPlayBinaryPlistRoundTrip(t *testing.T) {
	value := map[string]any{
		"接收端🎶":    []any{true, false, int64(-12782), uint64(math.MaxUint64), 0.125},
		"streams": []any{map[string]any{"type": uint64(96), "key": bytes.Repeat([]byte{0xff, 0}, 128)}},
		"long":    strings.Repeat("A", 260),
	}
	data, err := Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, value) {
		t.Fatalf("plist 往返 = %#v，错误 %v", got, err)
	}
	next, err := Marshal(got)
	if err != nil || !bytes.Equal(data, next) {
		t.Fatalf("重复编码结果不同，错误 %v", err)
	}
}

func TestAirPlayBinaryPlistMalformed(t *testing.T) {
	data, err := Marshal([]any{true})
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(data) {
		var got any
		if err := Unmarshal(data[:n], &got); err == nil {
			t.Errorf("接受了截断到 %d 字节的 plist", n)
		}
	}
	for _, tc := range []struct {
		name string
		edit func([]byte)
	}{
		{"bad header", func(b []byte) { b[0] = 'x' }},
		{"bad offset width", func(b []byte) { b[len(b)-26] = 0 }},
		{"huge object count", func(b []byte) { binary.BigEndian.PutUint64(b[len(b)-24:], math.MaxUint64) }},
		{"invalid reference", func(b []byte) { b[9] = 0xff }},
		{"cyclic reference", func(b []byte) { b[9] = 0 }},
		{"invalid extended length", func(b []byte) { b[8], b[9] = 0xaf, 0x1f }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := bytes.Clone(data)
			tc.edit(input)
			var got any
			if err := Unmarshal(input, &got); err == nil {
				t.Fatalf("接受了畸形 plist：%#v", got)
			}
		})
	}
	for _, value := range []any{nil, complex(1, 2), "\xff", bytes.Repeat([]byte{1}, maxSize)} {
		if _, err := Marshal(value); err == nil {
			t.Errorf("接受了不受支持的值 %T", value)
		}
	}
}
