package bplist

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf16"
)

var errFormat = errors.New("无效或不支持的二进制 plist")

type decoder struct {
	data, offsets       []byte
	offsetSize, refSize int
	values              []any
	visiting            []bool
	budget              int
}

func number(b []byte) uint64 {
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n
}

// Unmarshal 接收 *any 或 *map[string]any。正整数读作 uint64，负整数读作 int64。
func Unmarshal(data []byte, v any) error {
	if len(data) < 41 || len(data) > maxSize || string(data[:8]) != magic {
		return errFormat
	}
	trailer := data[len(data)-32:]
	os, rs := int(trailer[6]), int(trailer[7])
	count := number(trailer[8:16])
	root := number(trailer[16:24])
	table := number(trailer[24:])
	end := uint64(len(data) - 32)
	if os < 1 || os > 8 || rs < 1 || rs > 8 || table < 9 || table >= end ||
		count > (end-table)/uint64(os) || count*uint64(os) != end-table || root >= count {
		return errFormat
	}
	d := decoder{data: data[:table], offsets: data[table:end], offsetSize: os, refSize: rs,
		values: make([]any, int(count)), visiting: make([]bool, int(count)), budget: 4 * len(data)}
	out, err := d.object(root, 0)
	if err != nil {
		return err
	}
	switch dest := v.(type) {
	case *any:
		if dest != nil {
			*dest = out
			return nil
		}
	case *map[string]any:
		if m, ok := out.(map[string]any); ok && dest != nil {
			*dest = m
			return nil
		}
	}
	return errors.New("plist 根对象与解码目标不匹配")
}

func (d *decoder) object(id uint64, depth int) (any, error) {
	if id >= uint64(len(d.values)) || depth > maxDepth || d.visiting[id] {
		return nil, errFormat
	}
	if result := d.values[id]; result != nil {
		return result, nil
	}
	start := int(id) * d.offsetSize
	off := number(d.offsets[start : start+d.offsetSize])
	if off < 8 || off >= uint64(len(d.data)) {
		return nil, errFormat
	}
	d.visiting[id] = true
	result, err := d.parse(d.data[off:], depth)
	d.visiting[id] = false
	if err == nil {
		d.values[id] = result
	}
	return result, err
}

func (d *decoder) parse(b []byte, depth int) (any, error) {
	tag := b[0]
	kind, info := tag>>4, tag&15
	b = b[1:]
	switch kind {
	case 0:
		if tag == 8 || tag == 9 {
			return tag == 9, nil
		}
		return nil, errFormat
	case 1, 2:
		size := 1 << info
		if size > len(b) || size > 16 {
			return nil, errFormat
		}
		if kind == 2 {
			switch size {
			case 4:
				return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
			case 8:
				return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
			default:
				return nil, errFormat
			}
		}
		if size == 16 {
			if number(b[:8]) != 0 {
				return nil, errFormat
			}
			return number(b[8:16]), nil
		}
		n := number(b[:size])
		if size == 8 && n > math.MaxInt64 {
			return int64(n), nil
		}
		return n, nil
	}
	n := uint64(info)
	if info == 15 {
		if len(b) == 0 || b[0]>>4 != 1 || b[0]&15 > 3 {
			return nil, errFormat
		}
		width := 1 << (b[0] & 15)
		if len(b) < 1+width {
			return nil, errFormat
		}
		n = number(b[1 : 1+width])
		b = b[1+width:]
	}
	unit := 1
	switch kind {
	case 4, 5:
	case 6:
		unit = 2
	case 10:
		unit = d.refSize
	case 13:
		unit = 2 * d.refSize
	default:
		return nil, errFormat
	}
	if n > uint64(len(b)/unit) || n > uint64(d.budget/unit) {
		return nil, errFormat
	}
	count := int(n)
	d.budget -= count * unit
	b = b[:count*unit]
	switch kind {
	case 4:
		return b[:len(b):len(b)], nil
	case 5:
		return string(b), nil
	case 6:
		words := make([]uint16, count)
		for i := range words {
			words[i] = binary.BigEndian.Uint16(b[i*2:])
		}
		return string(utf16.Decode(words)), nil
	}
	child := func(i int) (any, error) {
		start := i * d.refSize
		return d.object(number(b[start:start+d.refSize]), depth+1)
	}
	if kind == 10 {
		a := make([]any, count)
		for i := range a {
			v, err := child(i)
			if err != nil {
				return nil, err
			}
			a[i] = v
		}
		return a, nil
	}
	m := make(map[string]any, count)
	for i := range count {
		k, err := child(i)
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, errFormat
		}
		v, err := child(i + count)
		if err != nil {
			return nil, err
		}
		m[key] = v
	}
	return m, nil
}
