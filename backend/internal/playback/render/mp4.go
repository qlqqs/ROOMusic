package render

import (
	"encoding/binary"
	"fmt"
	"io"
)

type mp4Box struct {
	kind         string
	raw, payload []byte
}

type mp4Timing struct {
	trackID, timescale, defaultDuration uint32
}

// boxHeader 同时支持普通长度和扩展长度；管道中的 box 不能延伸到 EOF。
func boxHeader(data []byte) (int, int, error) {
	if len(data) < 8 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	size, header := uint64(binary.BigEndian.Uint32(data)), 8
	if size == 1 {
		if len(data) < 16 {
			return 0, 0, io.ErrUnexpectedEOF
		}
		size, header = binary.BigEndian.Uint64(data[8:]), 16
	}
	if size < uint64(header) || size > uint64(^uint(0)>>1) {
		return 0, 0, fmt.Errorf("render: MP4 box 长度无效")
	}
	return int(size), header, nil
}

func readMP4Box(r io.Reader) (mp4Box, error) {
	var header [16]byte
	if _, err := io.ReadFull(r, header[:8]); err != nil {
		return mp4Box{}, err
	}
	if binary.BigEndian.Uint32(header[:4]) == 1 {
		if _, err := io.ReadFull(r, header[8:]); err != nil {
			return mp4Box{}, fmt.Errorf("render: MP4 扩展头不完整: %w", io.ErrUnexpectedEOF)
		}
	}
	size, offset, err := boxHeader(header[:])
	if err != nil {
		return mp4Box{}, err
	}
	data := make([]byte, size)
	copy(data, header[:offset])
	if _, err := io.ReadFull(r, data[offset:]); err != nil {
		return mp4Box{}, fmt.Errorf("render: MP4 box 内容不完整: %w", io.ErrUnexpectedEOF)
	}
	return mp4Box{kind: string(data[4:8]), raw: data, payload: data[offset:]}, nil
}

func mp4Children(box mp4Box) ([]mp4Box, error) {
	var children []mp4Box
	for data := box.payload; len(data) > 0; {
		size, offset, err := boxHeader(data)
		if err != nil {
			return nil, err
		}
		if size > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		children = append(children, mp4Box{kind: string(data[4:8]), raw: data[:size], payload: data[offset:size]})
		data = data[size:]
	}
	return children, nil
}

func mp4Child(box mp4Box, kind string) (mp4Box, error) {
	children, err := mp4Children(box)
	if err != nil {
		return mp4Box{}, err
	}
	var result mp4Box
	for _, child := range children {
		if child.kind == kind {
			if result.kind != "" {
				return mp4Box{}, fmt.Errorf("render: MP4 %s 含重复的 %s", box.kind, kind)
			}
			result = child
		}
	}
	if result.kind == "" {
		return mp4Box{}, fmt.Errorf("render: MP4 %s 缺少 %s", box.kind, kind)
	}
	return result, nil
}

func mp4FullBox(box mp4Box, version byte, flags uint32, size int) (uint32, error) {
	if len(box.payload) < size || len(box.payload) < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	actual := binary.BigEndian.Uint32(box.payload) & 0xffffff
	if box.payload[0] > version || actual & ^flags != 0 {
		return 0, fmt.Errorf("render: MP4 %s 版本或标志无效", box.kind)
	}
	return actual, nil
}

func parseMP4Timing(moov mp4Box) (mp4Timing, error) {
	var timing mp4Timing
	trak, err := mp4Child(moov, "trak")
	if err != nil {
		return timing, err
	}
	mdia, err := mp4Child(trak, "mdia")
	if err != nil {
		return timing, err
	}
	mdhd, err := mp4Child(mdia, "mdhd")
	if err != nil {
		return timing, err
	}
	if _, err = mp4FullBox(mdhd, 1, 0, 24); err != nil {
		return timing, err
	}
	offset := 12
	if mdhd.payload[0] == 1 {
		offset = 20
		if len(mdhd.payload) < 36 {
			return timing, io.ErrUnexpectedEOF
		}
	}
	timing.timescale = binary.BigEndian.Uint32(mdhd.payload[offset:])
	mvex, err := mp4Child(moov, "mvex")
	if err != nil {
		return timing, err
	}
	trex, err := mp4Child(mvex, "trex")
	if err != nil {
		return timing, err
	}
	if _, err = mp4FullBox(trex, 0, 0, 24); err != nil {
		return timing, err
	}
	timing.trackID = binary.BigEndian.Uint32(trex.payload[4:])
	timing.defaultDuration = binary.BigEndian.Uint32(trex.payload[12:])
	if timing.timescale == 0 || timing.trackID == 0 {
		return timing, fmt.Errorf("render: MP4 音轨或时间刻度无效")
	}
	return timing, nil
}

func fragmentSeconds(moof mp4Box, timing mp4Timing) (float64, error) {
	traf, err := mp4Child(moof, "traf")
	if err != nil {
		return 0, err
	}
	tfhd, err := mp4Child(traf, "tfhd")
	if err != nil {
		return 0, err
	}
	flags, err := mp4FullBox(tfhd, 0, 0x02003b, 8)
	if err != nil {
		return 0, err
	}
	if binary.BigEndian.Uint32(tfhd.payload[4:]) != timing.trackID || timing.timescale == 0 {
		return 0, fmt.Errorf("render: MP4 分片音轨或时间刻度无效")
	}
	duration, offset := timing.defaultDuration, 8
	for _, field := range []struct {
		flag uint32
		size int
	}{{1, 8}, {2, 4}, {8, 4}, {16, 4}, {32, 4}} {
		if flags&field.flag == 0 {
			continue
		}
		if len(tfhd.payload)-offset < field.size {
			return 0, io.ErrUnexpectedEOF
		}
		if field.flag == 8 {
			duration = binary.BigEndian.Uint32(tfhd.payload[offset:])
		}
		offset += field.size
	}
	children, err := mp4Children(traf)
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, trun := range children {
		if trun.kind != "trun" {
			continue
		}
		ticks, err := mp4RunDuration(trun, duration)
		if err != nil {
			return 0, err
		}
		if ticks > ^uint64(0)-total {
			return 0, fmt.Errorf("render: MP4 分片时长溢出")
		}
		total += ticks
	}
	if total == 0 {
		return 0, fmt.Errorf("render: MP4 分片没有样本时长")
	}
	return float64(total) / float64(timing.timescale), nil
}

func mp4RunDuration(trun mp4Box, defaultDuration uint32) (uint64, error) {
	flags, err := mp4FullBox(trun, 1, 0xf05, 8)
	if err != nil {
		return 0, err
	}
	count, offset, stride := uint64(binary.BigEndian.Uint32(trun.payload[4:])), 8, 0
	if flags&1 != 0 {
		offset += 4
	}
	if flags&4 != 0 {
		offset += 4
	}
	for _, flag := range []uint32{0x100, 0x200, 0x400, 0x800} {
		if flags&flag != 0 {
			stride += 4
		}
	}
	if offset > len(trun.payload) || count*uint64(stride) != uint64(len(trun.payload)-offset) {
		return 0, fmt.Errorf("render: MP4 trun 样本字段不完整")
	}
	if flags&0x100 == 0 {
		if count > 0 && defaultDuration == 0 {
			return 0, fmt.Errorf("render: MP4 样本缺少默认时长")
		}
		return count * uint64(defaultDuration), nil
	}
	var total uint64
	for i := uint64(0); i < count; i++ {
		duration := binary.BigEndian.Uint32(trun.payload[offset:])
		if duration == 0 {
			return 0, fmt.Errorf("render: MP4 样本时长无效")
		}
		total += uint64(duration)
		offset += stride
	}
	return total, nil
}
