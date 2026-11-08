package parser

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	wavpackInitialBlock = 0x800
	wavpackFinalBlock   = 0x1000
)

var wavpackSampleRates = [...]int{6000, 8000, 9600, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 64000, 88200, 96000, 192000, 0}

type wavpackBlock struct {
	start, end   int64
	flags        uint32
	samples      uint32
	totalSamples int64
}

func parseWavPack(path string) (AudioObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	observation := AudioObservation{SourceKind: "wavpack_apev2", Codec: "wavpack"}
	var foundAudio, firstGroupComplete bool
	var declaredChannels, groupChannels int
	var totalSamples, countedSamples int64
	err = walkWavPackBlocks(context.Background(), file, info.Size(), func(block wavpackBlock) (bool, error) {
		if block.samples == 0 {
			return false, nil
		}
		if block.flags&wavpackInitialBlock != 0 {
			countedSamples += int64(block.samples)
		}
		if !foundAudio {
			if block.flags&wavpackInitialBlock == 0 {
				return false, fmt.Errorf("missing initial WavPack block")
			}
			foundAudio = true
			totalSamples = block.totalSamples
			observation.BitDepth = int((block.flags&3)+1)*8 - int((block.flags>>13)&0x1f)
			observation.SampleRate = wavpackSampleRates[(block.flags>>23)&0xf]
			switch {
			case block.flags&0x80000000 != 0:
				observation.Codec = "wavpack_dsd"
			case block.flags&0x80 != 0:
				observation.Codec = "wavpack_float"
				observation.BitDepth = 32
			case block.flags&8 != 0:
				observation.Codec = "wavpack_hybrid"
			}
			rate, channels, dsdMultiplier, err := readWavPackMetadata(file, block)
			if err != nil {
				return false, err
			}
			if observation.SampleRate == 0 {
				observation.SampleRate = rate
			}
			if observation.Codec == "wavpack_dsd" {
				if dsdMultiplier == 0 || observation.SampleRate <= 0 || uint64(observation.SampleRate) > math.MaxInt32/(dsdMultiplier*8) {
					return false, fmt.Errorf("invalid WavPack DSD sample rate")
				}
				observation.SampleRate = int(uint64(observation.SampleRate) * dsdMultiplier * 8)
				observation.BitDepth = 1
			}
			declaredChannels = channels
			firstGroupComplete = declaredChannels > 0
		}
		if !firstGroupComplete {
			if groupChannels > 0 && block.flags&wavpackInitialBlock != 0 {
				return false, fmt.Errorf("incomplete WavPack channel group")
			}
			if block.flags&4 != 0 {
				groupChannels++
			} else {
				groupChannels += 2
			}
			firstGroupComplete = block.flags&wavpackFinalBlock != 0
		}
		return totalSamples >= 0 && firstGroupComplete, nil
	})
	if err != nil {
		return AudioObservation{}, err
	}
	observation.Channels = groupChannels
	if declaredChannels > 0 {
		observation.Channels = declaredChannels
	}
	if !foundAudio || !firstGroupComplete || observation.SampleRate <= 0 || observation.Channels <= 0 || observation.BitDepth <= 0 {
		return AudioObservation{}, fmt.Errorf("invalid WavPack audio facts")
	}
	if totalSamples < 0 {
		totalSamples = countedSamples
	}
	// DSD 头的样本数以打包字节计数，原生采样率以单个位计数。
	observation.DurationSeconds = float64(totalSamples) / float64(observation.SampleRate)
	if observation.Codec == "wavpack_dsd" {
		observation.DurationSeconds *= 8
	}
	observation.Bitrate = estimatedBitrateKbps(info.Size(), observation.DurationSeconds)
	if err := readAPEv2(file, info.Size(), &observation); err != nil {
		return AudioObservation{}, err
	}
	finalizeTags(&observation)
	return applyFilenameFallback(path, observation), nil
}

// walkWavPackBlocks 只读取连续块的头部；音频之后的 APEv2/ID3v1 不属于块序列。
// visit 返回 true 时提前结束，供总样本数已知的参数解析只读取首个声道组。
func walkWavPackBlocks(ctx context.Context, reader io.ReaderAt, size int64, visit func(wavpackBlock) (stop bool, err error)) error {
	for position := int64(0); position < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var magic [4]byte
		if _, err := reader.ReadAt(magic[:], position); err != nil {
			return fmt.Errorf("read WavPack marker: %w", err)
		}
		if string(magic[:]) != "wvpk" {
			if position == 0 {
				return fmt.Errorf("invalid WavPack stream")
			}
			break
		}
		var header [32]byte
		if _, err := reader.ReadAt(header[:], position); err != nil {
			return fmt.Errorf("read WavPack header: %w", err)
		}
		length := int64(binary.LittleEndian.Uint32(header[4:8]))
		version := binary.LittleEndian.Uint16(header[8:10])
		if length < 24 || length > 16<<20 || length+8 > size-position || version < 0x402 || version > 0x410 {
			return fmt.Errorf("invalid WavPack block")
		}
		lowSamples := binary.LittleEndian.Uint32(header[12:16])
		// WavPack 的 40 位总数以 0xffffffff 为进位基数，低 32 位全 1 单独表示未知。
		totalSamples := int64(lowSamples) + int64(header[11])*0xffffffff
		if lowSamples == 0xffffffff {
			totalSamples = -1
		}
		block := wavpackBlock{
			start: position, end: position + length + 8, totalSamples: totalSamples,
			samples: binary.LittleEndian.Uint32(header[20:24]), flags: binary.LittleEndian.Uint32(header[24:28]),
		}
		stop, err := visit(block)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
		position = block.end
	}
	return nil
}

// readWavPackMetadata 仅取参数子块，不把压缩音频或封装恢复数据读入内存。
func readWavPackMetadata(reader io.ReaderAt, block wavpackBlock) (rate, channels int, dsdMultiplier uint64, err error) {
	for position := block.start + 32; position < block.end; {
		if block.end-position < 2 {
			return 0, 0, 0, fmt.Errorf("truncated WavPack metadata header")
		}
		var header [4]byte
		if _, err := reader.ReadAt(header[:2], position); err != nil {
			return 0, 0, 0, err
		}
		position += 2
		length := int64(header[1]) * 2
		if header[0]&0x80 != 0 {
			if block.end-position < 2 {
				return 0, 0, 0, fmt.Errorf("truncated WavPack metadata size")
			}
			if _, err := reader.ReadAt(header[2:4], position); err != nil {
				return 0, 0, 0, err
			}
			length += int64(header[2])<<9 | int64(header[3])<<17
			position += 2
		}
		paddedLength := length
		if header[0]&0x40 != 0 {
			length--
		}
		if length < 0 || paddedLength > block.end-position {
			return 0, 0, 0, fmt.Errorf("invalid WavPack metadata size")
		}
		id := header[0] & 0x3f
		if id == 0x27 || id == 0x0d {
			if (id == 0x27 && length != 3 && length != 4) || (id == 0x0d && (length < 1 || length > 7)) {
				return 0, 0, 0, fmt.Errorf("invalid WavPack parameter metadata")
			}
			var value [7]byte
			if _, err := reader.ReadAt(value[:length], position); err != nil {
				return 0, 0, 0, err
			}
			if id == 0x27 {
				rate = int(value[0]) | int(value[1])<<8 | int(value[2])<<16
				if length == 4 {
					rate |= int(value[3]&0x7f) << 24
				}
			} else {
				channels = int(value[0])
				if length >= 6 {
					channels += int(value[2]&0xf)<<8 + 1
				}
				if channels == 0 {
					return 0, 0, 0, fmt.Errorf("invalid WavPack channel count")
				}
			}
		}
		if id == 0x0e && block.flags&0x80000000 != 0 {
			if length < 2 {
				return 0, 0, 0, fmt.Errorf("invalid WavPack DSD metadata size")
			}
			var exponent [1]byte
			if _, err := reader.ReadAt(exponent[:], position); err != nil {
				return 0, 0, 0, err
			}
			if exponent[0] > 31 {
				return 0, 0, 0, fmt.Errorf("invalid WavPack DSD multiplier")
			}
			dsdMultiplier = uint64(1) << exponent[0]
		}
		position += paddedLength
	}
	return rate, channels, dsdMultiplier, nil
}
