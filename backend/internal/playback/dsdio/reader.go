package dsdio

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// Reader 在调用方提供的句柄上读取，不拥有句柄；Read 与 SeekByteFrame 不能并发调用。
// 每次底层读取最多 4096 个字节帧，不按曲长分配内存。
type Reader struct {
	source   io.ReaderAt
	layout   Layout
	position int64
	scratch  []byte
}

func NewReader(source io.ReaderAt, layout Layout) (*Reader, error) {
	if source == nil {
		return nil, fmt.Errorf("nil DSD source")
	}
	if err := layout.validate(); err != nil {
		return nil, err
	}
	return &Reader{source: source, layout: layout, scratch: make([]byte, 4096*layout.Channels)}, nil
}

// SeekByteFrame 使用绝对字节帧坐标，一帧含每声道各一个字节，允许恰好定位到 EOF。
// 非法定位不改变原位置；滤波历史与比特级定位由来源转换层负责。
func (r *Reader) SeekByteFrame(byteFrame int64) error {
	if byteFrame < 0 || byteFrame > r.layout.ByteFrames() {
		return fmt.Errorf("DSD seek out of range")
	}
	r.position = byteFrame
	return nil
}

// Read 把同样多的字节写到各声道；dst 必须包含 Channels 个等长切片。
// 返回值 n 是每声道字节数；输出统一 MSB 先，不含块填充，末字节无效位填静音 0x69。
// 只在有效样本结束时返回 EOF，来源被截断或缺少完整声道帧返回 UnexpectedEOF。
func (r *Reader) Read(dst [][]byte) (n int, err error) {
	if len(dst) != r.layout.Channels {
		return 0, fmt.Errorf("DSD destination channel count mismatch")
	}
	want := len(dst[0])
	for _, channel := range dst {
		if len(channel) != want {
			return 0, fmt.Errorf("DSD destination lengths differ")
		}
	}
	if want == 0 {
		return 0, nil
	}
	for n < want {
		remaining := r.layout.ByteFrames() - r.position
		if remaining == 0 {
			return n, io.EOF
		}
		count := int(min(int64(want-n), remaining, 4096))
		if r.layout.Container == DSF {
			count = min(count, r.layout.DSFBlockSize-int(r.position%int64(r.layout.DSFBlockSize)))
		}
		got, readErr := r.readFrames(count)
		for ch := range dst {
			stored := ch
			if r.layout.ordered {
				stored = int(r.layout.channelOrder[ch])
			}
			for i := 0; i < got; i++ {
				index := stored*count + i
				if r.layout.Container == DFF {
					index = i*r.layout.Channels + stored
				}
				value := r.scratch[index]
				if r.layout.BitOrder == LSBFirst {
					value = bits.Reverse8(value)
				}
				if r.layout.Samples%8 != 0 && r.position+int64(i)+1 == r.layout.ByteFrames() {
					mask := byte(0xff << (8 - r.layout.Samples%8))
					value = value&mask | 0x69&^mask
				}
				dst[ch][n+i] = value
			}
		}
		r.position += int64(got)
		n += got
		if readErr != nil {
			return n, readErr
		}
	}
	return n, nil
}

func (r *Reader) readFrames(count int) (int, error) {
	l := r.layout
	if l.Container == DFF {
		start := l.DataStart + r.position*int64(l.Channels)
		n, err := r.source.ReadAt(r.scratch[:count*l.Channels], start)
		return n / l.Channels, payloadReadError(n, count*l.Channels, err)
	}
	block := int64(l.DSFBlockSize)
	group := r.position / block
	within := r.position % block
	got := count
	var firstErr error
	for ch := 0; ch < l.Channels; ch++ {
		// 每个物理声道独立核对范围；损坏的末组不能把下一块或标签当音频。
		groupStart := group * int64(l.Channels) * block
		available := l.DataEnd - l.DataStart - groupStart - int64(ch)*block - within
		if available <= 0 {
			got = 0
			if firstErr == nil {
				firstErr = io.ErrUnexpectedEOF
			}
			continue
		}
		length := int(min(int64(count), available))
		n, err := r.source.ReadAt(r.scratch[ch*count:ch*count+length], l.DataEnd-available)
		got = min(got, n)
		if firstErr == nil {
			firstErr = payloadReadError(n, length, err)
			if firstErr == nil && length != count {
				firstErr = io.ErrUnexpectedEOF
			}
		}
	}
	return got, firstErr
}

func payloadReadError(n, want int, err error) error {
	if n < want && (err == nil || errors.Is(err, io.EOF)) {
		return io.ErrUnexpectedEOF
	}
	// ReaderAt 允许完整读取恰好到文件末尾时同时返回 EOF。
	if n == want && errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
