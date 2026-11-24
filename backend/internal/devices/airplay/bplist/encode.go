// Package bplist 编解码 AirPlay 控制消息使用的 bplist00 子集。
package bplist

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

const magic = "bplist00"
const maxDepth = 64
const maxSize = 1 << 20

type object struct {
	payload []byte
	refs    []uint64
}

type encoder struct {
	objects []object
}

func width(n uint64) int {
	switch {
	case n <= math.MaxUint8:
		return 1
	case n <= math.MaxUint16:
		return 2
	case n <= math.MaxUint32:
		return 4
	default:
		return 8
	}
}

func appendNumber(dst []byte, n uint64, size int) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	return append(dst, buf[8-size:]...)
}

func integer(n uint64) []byte {
	size := width(n)
	tag := map[int]byte{1: 0x10, 2: 0x11, 4: 0x12, 8: 0x13}[size]
	return appendNumber([]byte{tag}, n, size)
}

func prefix(tag byte, n int) []byte {
	if n < 15 {
		return []byte{tag | byte(n)}
	}
	return append([]byte{tag | 15}, integer(uint64(n))...)
}

func (e *encoder) add(value any, depth int) (uint64, error) {
	if depth > maxDepth || len(e.objects) >= maxSize {
		return 0, errors.New("plist 对象过多或嵌套过深")
	}
	id := uint64(len(e.objects))
	e.objects = append(e.objects, object{})
	var obj object
	addRef := func(v any) error {
		ref, err := e.add(v, depth+1)
		if err == nil {
			obj.refs = append(obj.refs, ref)
		}
		return err
	}
	switch v := value.(type) {
	case string:
		if !utf8.ValidString(v) {
			return 0, errors.New("plist 字符串编码无效")
		}
		if utf8.RuneCountInString(v) == len(v) {
			obj.payload = append(prefix(0x50, len(v)), v...)
		} else {
			words := utf16.Encode([]rune(v))
			obj.payload = prefix(0x60, len(words))
			for _, w := range words {
				obj.payload = binary.BigEndian.AppendUint16(obj.payload, w)
			}
		}
	case bool:
		obj.payload = []byte{8}
		if v {
			obj.payload[0] = 9
		}
	case []byte:
		obj.payload = append(prefix(0x40, len(v)), v...)
	case int:
		obj.payload = integer(uint64(v))
	case int64:
		obj.payload = integer(uint64(v))
	case uint64:
		obj.payload = integer(v)
		if v > math.MaxInt64 {
			obj.payload = appendNumber(appendNumber([]byte{0x14}, 0, 8), v, 8)
		}
	case uint32:
		obj.payload = integer(uint64(v))
	case uint16:
		obj.payload = integer(uint64(v))
	case float64:
		obj.payload = appendNumber([]byte{0x23}, math.Float64bits(v), 8)
	case []any:
		obj.payload = prefix(0xa0, len(v))
		for _, item := range v {
			if err := addRef(item); err != nil {
				return 0, err
			}
		}
	case map[string]any:
		obj.payload = prefix(0xd0, len(v))
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := addRef(k); err != nil {
				return 0, err
			}
		}
		for _, k := range keys {
			if err := addRef(v[k]); err != nil {
				return 0, err
			}
		}
	default:
		return 0, fmt.Errorf("plist 不支持 %T", value)
	}
	e.objects[id] = obj
	return id, nil
}

func Marshal(v any) ([]byte, error) {
	var e encoder
	root, err := e.add(v, 0)
	if err != nil {
		return nil, err
	}
	out := []byte(magic)
	refSize := width(uint64(len(e.objects) - 1))
	offsets := make([]uint64, len(e.objects))
	for i, obj := range e.objects {
		offsets[i] = uint64(len(out))
		out = append(out, obj.payload...)
		for _, ref := range obj.refs {
			out = appendNumber(out, ref, refSize)
		}
	}
	table := uint64(len(out))
	offsetSize := width(table)
	for _, off := range offsets {
		out = appendNumber(out, off, offsetSize)
	}
	out = slices.Grow(out, 32)
	out = append(out, 0, 0, 0, 0, 0, 0, byte(offsetSize), byte(refSize))
	for _, n := range []uint64{uint64(len(e.objects)), root, table} {
		out = binary.BigEndian.AppendUint64(out, n)
	}
	if len(out) > maxSize {
		return nil, errors.New("plist 超出控制消息大小上限")
	}
	return out, nil
}
