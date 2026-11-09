package irload

import (
	"context"
	"encoding/binary"
	"io"
	"math"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

// 只读受控 ffmpeg 输出的 f64 WAV；头部、数据与尾部都独立有界。
// 管道上的 RIFF/data 长度可为 0xffffffff，不能据此分配缓冲。
func readFloatWAV(ctx context.Context, reader io.Reader) (*DecodedIR, error) {
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return nil, ErrDecodeFailed
	}
	rate, channels := 0, 0
	budget := int64(1 << 20)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			return nil, err
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		if string(chunk[:4]) == "data" {
			if channels == 0 {
				return nil, ErrDecodeFailed
			}
			maxBytes := int64(convolution.MaxTaps * channels * 8)
			if size != math.MaxUint32 && size > maxBytes {
				return nil, ErrTooLarge
			}
			var data []byte
			var err error
			if size == math.MaxUint32 {
				data, err = io.ReadAll(io.LimitReader(reader, maxBytes+1))
			} else {
				data, err = io.ReadAll(io.LimitReader(reader, size))
				if err == nil && int64(len(data)) != size {
					err = io.ErrUnexpectedEOF
				}
				if err == nil {
					n, drainErr := io.Copy(io.Discard, io.LimitReader(reader, budget+1))
					if n > budget {
						return nil, ErrTooLarge
					}
					err = drainErr
				}
			}
			if err != nil {
				return nil, err
			}
			if int64(len(data)) > maxBytes {
				return nil, ErrTooLarge
			}
			if len(data) == 0 || len(data)%(channels*8) != 0 {
				return nil, ErrDecodeFailed
			}
			frames := len(data) / (channels * 8)
			samples := make([][]float64, channels)
			for ch := range samples {
				samples[ch] = make([]float64, frames)
			}
			for frame := 0; frame < frames; frame++ {
				if frame%4096 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				for ch := 0; ch < channels; ch++ {
					v := math.Float64frombits(binary.LittleEndian.Uint64(data[(frame*channels+ch)*8:]))
					if !finite(v) {
						return nil, ErrDecodeFailed
					}
					samples[ch][frame] = v
				}
			}
			return &DecodedIR{SampleRate: rate, Samples: samples}, nil
		}
		padded := size + (size & 1)
		budget -= 8 + padded
		if budget < 0 {
			return nil, ErrTooLarge
		}
		if string(chunk[:4]) != "fmt " {
			if _, err := io.CopyN(io.Discard, reader, padded); err != nil {
				return nil, err
			}
			continue
		}
		if size < 16 || size > 4096 || channels != 0 {
			return nil, ErrDecodeFailed
		}
		format := make([]byte, padded)
		if _, err := io.ReadFull(reader, format); err != nil {
			return nil, err
		}
		tag := binary.LittleEndian.Uint16(format)
		if tag == 0xfffe {
			if size < 40 || binary.LittleEndian.Uint16(format[16:]) < 22 {
				return nil, ErrDecodeFailed
			}
			tag = binary.LittleEndian.Uint16(format[24:])
		}
		channels = int(binary.LittleEndian.Uint16(format[2:]))
		rate = int(binary.LittleEndian.Uint32(format[4:]))
		if channels < 1 || channels > convolution.MaxChannels {
			return nil, ErrTooLarge
		}
		if tag != 3 || rate <= 0 || binary.LittleEndian.Uint16(format[14:]) != 64 || int(binary.LittleEndian.Uint16(format[12:])) != channels*8 {
			return nil, ErrDecodeFailed
		}
	}
}
