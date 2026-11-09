package dsdpcm_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
)

// 输入均在内存生成；DSF 保留物理块填充，Samples 只描述有效比特。
func sourceLayout(container dsdio.Container, rate int, samples uint64, channels ...[]byte) ([]byte, dsdio.Layout) {
	l := dsdio.Layout{Container: container, Rate: rate, Channels: len(channels), Samples: samples, BitOrder: dsdio.MSBFirst}
	if len(channels) == 2 {
		l.Mask = 3
	}
	n := int((samples + 7) / 8)
	var data []byte
	if container == dsdio.DSF {
		l.DSFBlockSize, l.BitOrder = 4096, dsdio.LSBFirst
		data = make([]byte, ((n+4095)/4096)*4096*len(channels))
		for ch, input := range channels {
			for i := 0; i < n; i++ {
				data[(i/4096*len(channels)+ch)*4096+i%4096] = bits.Reverse8(input[i])
			}
		}
	} else {
		data = make([]byte, n*len(channels))
		for ch, input := range channels {
			for i := 0; i < n; i++ {
				data[i*len(channels)+ch] = input[i]
			}
		}
	}
	l.DataEnd, l.ContainerEnd = int64(len(data)), int64(len(data))
	return data, l
}

func explicitReader(t *testing.T, data []byte, l dsdio.Layout, options dsdpcm.Options) *dsdpcm.Reader {
	t.Helper()
	r, err := dsdpcm.NewWithOptions(context.Background(), bytes.NewReader(data), l, options)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pcmSample(data []byte, frame, channel, channels int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(data[(frame*channels+channel)*4:])))
}

func TestReaderFormatAndLength(t *testing.T) {
	for _, base := range []int{2822400, 3072000} {
		for _, factor := range []int{1, 2, 4, 8} {
			for _, container := range []dsdio.Container{dsdio.DSF, dsdio.DFF} {
				for _, samples := range []uint64{0, 7, uint64(8*factor - 1), uint64(8 * factor), uint64(8*(4096+factor-1) + 3)} {
					t.Run(fmt.Sprintf("%s_DSD%d_族%d_有效比特%d", container, 64*factor, base, samples), func(t *testing.T) {
						input := bytes.Repeat([]byte{0x69}, int((samples+7)/8))
						data, l := sourceLayout(container, base*factor, samples, input, input)
						r := explicitReader(t, data, l, dsdpcm.Options{})
						wantFrames := int64(samples / uint64(8*factor))
						wantFormat := dsdpcm.Format{SampleRate: base / 8, Channels: 2, Layout: 3}
						if r.Format() != wantFormat || r.Frames() != wantFrames {
							t.Fatalf("格式或长度错误：%+v，%d 帧；期望 %+v，%d 帧", r.Format(), r.Frames(), wantFormat, wantFrames)
						}
						if got := len(readAll(t, r)); int64(got) != wantFrames*2*4 {
							t.Fatalf("整轨字节数 %d，期望 %d", got, wantFrames*2*4)
						}
					})
				}
			}
		}
	}
}

func TestReaderLatencyCompensatedOnceAcrossQuanta(t *testing.T) {
	for _, factor := range []int{1, 2} {
		t.Run(fmt.Sprintf("DSD%d", 64*factor), func(t *testing.T) {
			const frames = 74000
			input := bytes.Repeat([]byte{0x69}, frames*factor)
			positions := []int{1024, 352800/5 + 1024}
			for _, position := range positions {
				for i := 0; i < factor; i++ {
					input[position*factor+i] = 0xff
				}
			}
			data, l := sourceLayout(dsdio.DFF, 2822400*factor, uint64(len(input)*8), input)
			// IIR 保留自身相位，不参与来源缓冲延迟的峰位置断言。
			out := readAll(t, explicitReader(t, data, l, dsdpcm.Options{}))
			for _, position := range positions {
				peak, peakFrame := 0.0, -1
				for frame := position - 512; frame < position+512; frame++ {
					value := math.Abs(pcmSample(out, frame, 0, 1))
					if value > peak {
						peak, peakFrame = value, frame
					}
				}
				if peak < 0.05 || math.Abs(float64(peakFrame-position)) > 1 {
					t.Fatalf("输入脉冲 %d，输出峰 %d（幅度 %.6g），偏移应不超过一帧", position, peakFrame, peak)
				}
			}
		})
	}
}

// 一阶 ΔΣ：误差反馈把归一化正弦转换成按 MSB 排列的 ±1 比特。
func sineDSD(rate, byteFrames int, frequency, amplitude float64) []byte {
	input := make([]byte, byteFrames)
	accumulator := 0.0
	for bit := 0; bit < byteFrames*8; bit++ {
		accumulator += amplitude * math.Sin(2*math.Pi*frequency*float64(bit)/float64(rate))
		if accumulator >= 0 {
			input[bit/8] |= 1 << (7 - bit%8)
			accumulator--
		} else {
			accumulator++
		}
	}
	return input
}

func TestReaderSeekAdjacentSegmentsAndFragmentedReads(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		factor, frames, channels, seek int
	}{
		{"DSD64_超过一秒前滚", 1, 360000, 1, 356001},
		{"DSD128_双声道跨量子", 2, 74000, 2, 71003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := sineDSD(2822400*tc.factor, tc.frames*tc.factor, 1000, 0.4)
			channels := [][]byte{input}
			if tc.channels == 2 {
				channels = append(channels, bytes.Repeat([]byte{0x69}, len(input)))
			}
			data, l := sourceLayout(dsdio.DFF, 2822400*tc.factor, uint64(len(input)*8), channels...)
			newReader := func() *dsdpcm.Reader { return explicitReader(t, data, l, dsdpcm.Options{GainDB: 6, Filter: 1}) }
			whole := readAll(t, newReader())
			r := newReader()
			stride := tc.channels * 4
			// 同一实例按帧限读；分段边界既不定位，也不冲刷。
			first := readAll(t, io.LimitReader(r, int64(12345*stride)))
			second := readAll(t, io.LimitReader(r, int64((tc.frames-12345)*stride)))
			if !bytes.Equal(append(first, second...), whole) {
				t.Fatal("相邻分段拼接不等于整轨")
			}
			if n, err := r.Read(make([]byte, 1)); n != 0 || err != io.EOF {
				t.Fatalf("分段读完应到父 EOF：%d，%v", n, err)
			}
			if err := r.SeekFrame(0); err != nil {
				t.Fatal(err)
			}
			var fragmented []byte
			buffer := make([]byte, 37)
			for {
				n, err := r.Read(buffer)
				fragmented = append(fragmented, buffer[:n]...)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					t.Fatal("非 EOF 空读")
				}
			}
			if !bytes.Equal(fragmented, whole) {
				t.Fatal("37 字节碎片读取与整轨不一致")
			}
			for _, frame := range []int64{123, int64(tc.seek)} {
				if err := r.SeekFrame(frame); err != nil {
					t.Fatal(err)
				}
				// 先留下未满帧的缓冲状态，再验证两种非法定位均不改变状态。
				got := make([]byte, 1024*stride)
				if _, err := io.ReadFull(r, got[:37]); err != nil {
					t.Fatal(err)
				}
				for _, invalid := range []int64{-1, r.Frames() + 1} {
					if err := r.SeekFrame(invalid); err == nil {
						t.Fatalf("非法定位 %d 未报错", invalid)
					}
				}
				if _, err := io.ReadFull(r, got[37:]); err != nil {
					t.Fatal(err)
				}
				want := whole[int(frame)*stride : int(frame)*stride+len(got)]
				// 从零前滚应逐字节相同；非零前滚容许 IIR 历史收敛的浮点误差。
				if frame < 352800 {
					if !bytes.Equal(got, want) {
						t.Fatalf("定位 %d 后与整轨不逐字节一致", frame)
					}
				} else {
					for i := 0; i < len(got)/4; i++ {
						if delta := math.Abs(pcmSample(got, i, 0, 1) - pcmSample(want, i, 0, 1)); !(delta <= 1e-6) {
							t.Fatalf("定位 %d 后样本 %d 误差 %.9g 超过 1e-6", frame, i, delta)
						}
					}
				}
			}
			if err := r.SeekFrame(r.Frames()); err != nil {
				t.Fatal(err)
			}
			if n, err := r.Read(buffer); n != 0 || err != io.EOF {
				t.Fatalf("定位末帧后应立即 EOF：%d，%v", n, err)
			}
		})
	}
}

// 舍弃首尾各 5 ms，以 20 ms 整周期窗投影，避开起始历史与 EOF 冲刷。
func toneAmplitude(data []byte, channels int, frequency float64) float64 {
	const start, count = 1764, 7056
	var sine, cosine float64
	for i := 0; i < count; i++ {
		s, c := math.Sincos(2 * math.Pi * frequency * float64(i) / 352800)
		v := pcmSample(data, start+i, 0, channels)
		sine += v * s
		cosine += v * c
	}
	return 2 * math.Hypot(sine, cosine) / count
}

func TestReaderDefaultGainAndChannelIsolation(t *testing.T) {
	input := sineDSD(2822400, 10584, 1000, math.Pow(10, -6.0/20))
	data, l := sourceLayout(dsdio.DSF, 2822400, uint64(len(input)*8), input, bytes.Repeat([]byte{0x69}, len(input)))
	r, err := dsdpcm.New(context.Background(), bytes.NewReader(data), l)
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, r)
	if level := 20 * math.Log10(toneAmplitude(out, 2, 1000)); !(math.Abs(level) <= 0.1) {
		t.Fatalf("默认 +6 dB 后电平 %.6f dBFS，期望 0±0.1", level)
	}
	for frame := 1764; frame < 8820; frame++ {
		if v := pcmSample(out, frame, 1, 2); !(math.Abs(v) <= 1e-6) {
			t.Fatalf("静音声道第 %d 帧出现信号 %.9g", frame, v)
		}
	}
	if want := readAll(t, explicitReader(t, data, l, dsdpcm.Options{GainDB: 6, Filter: 1})); !bytes.Equal(out, want) {
		t.Fatal("New 不等于显式 +6 dB、filter 1")
	}
}

func TestReaderLowPassFilterOptions(t *testing.T) {
	measure := func(frequency float64, filter int) (float64, float64) {
		input := sineDSD(2822400, 10584, frequency, 0.25)
		data, l := sourceLayout(dsdio.DFF, 2822400, uint64(len(input)*8), input)
		// 以实际比特流的基波为输入电平，不能把一阶调制误差算作滤波衰减。
		var sine, cosine float64
		const start, count = 1764 * 8, 7056 * 8
		for i := 0; i < count; i++ {
			bit := start + i
			value := -1.0
			if input[bit/8]&(1<<(7-bit%8)) != 0 {
				value = 1
			}
			s, c := math.Sincos(2 * math.Pi * frequency * float64(i) / 2822400)
			sine += value * s
			cosine += value * c
		}
		return toneAmplitude(readAll(t, explicitReader(t, data, l, dsdpcm.Options{Filter: filter})), 1, frequency), 2 * math.Hypot(sine, cosine) / count
	}
	baseline20, input20 := measure(20000, 0)
	for _, tc := range []struct {
		filter            int
		cutoff, maxLoss20 float64
	}{
		{0, 90000, 0.01},
		{1, 70000, 0.004},
		{2, 50000, 0.03},
		{3, 30000, 0.15},
	} {
		t.Run(fmt.Sprintf("档位%d", tc.filter), func(t *testing.T) {
			at20 := baseline20
			nearCutoff, reference := measure(tc.cutoff, tc.filter)
			if tc.filter != 0 {
				at20, _ = measure(20000, tc.filter)
				reference, _ = measure(tc.cutoff, 0)
			}
			base20 := baseline20
			if tc.filter == 0 {
				base20 = input20
			}
			loss20 := -20 * math.Log10(at20/base20)
			lossCutoff := -20 * math.Log10(nearCutoff/reference)
			if !(math.Abs(loss20) <= tc.maxLoss20) || !(lossCutoff >= 2.5 && lossCutoff <= 3.5) {
				t.Fatalf("20 kHz 衰减 %.6f dB（上限 %.3f）；截止附近衰减 %.6f dB（期望 2.5–3.5）", loss20, tc.maxLoss20, lossCutoff)
			}
		})
	}
	for _, filter := range []int{-1, 4} {
		t.Run(fmt.Sprintf("非法档位%d", filter), func(t *testing.T) {
			data, l := sourceLayout(dsdio.DFF, 2822400, 8, []byte{0x69})
			if _, err := dsdpcm.NewWithOptions(context.Background(), bytes.NewReader(data), l, dsdpcm.Options{Filter: filter}); err == nil {
				t.Fatal("非法档位未报错")
			}
		})
	}
}

type failingReaderAt struct {
	data  []byte
	limit int64
	err   error
}

func (r failingReaderAt) ReadAt(dst []byte, offset int64) (int, error) {
	if offset >= r.limit {
		return 0, r.err
	}
	n := copy(dst, r.data[offset:min(r.limit, offset+int64(len(dst)))])
	if n < len(dst) {
		return n, r.err
	}
	return n, nil
}

func TestReaderErrorPreservesPrefixAndCancellationDiscardsBufferedAudio(t *testing.T) {
	input := sineDSD(2822400, 12000, 1000, 0.4)
	data, l := sourceLayout(dsdio.DFF, 2822400, uint64(len(input)*8), input)
	t.Run("中途错误不伪装成EOF", func(t *testing.T) {
		sentinel := errors.New("合成来源中途读取失败")
		r, err := dsdpcm.NewWithOptions(context.Background(), failingReaderAt{data: data, limit: 5003, err: sentinel}, l, dsdpcm.Options{})
		if err != nil {
			t.Fatal(err)
		}
		prefix, err := io.ReadAll(r)
		if !errors.Is(err, sentinel) || len(prefix) == 0 || int64(len(prefix)) >= r.Frames()*4 {
			t.Fatalf("期望非空有效前缀及原错误，得到 %d 字节，%v", len(prefix), err)
		}
		whole := readAll(t, explicitReader(t, data, l, dsdpcm.Options{}))
		if !bytes.Equal(prefix, whole[:len(prefix)]) {
			t.Fatal("出错前已转换的前缀不一致")
		}
		if n, err := r.Read(make([]byte, 37)); n != 0 || !errors.Is(err, sentinel) {
			t.Fatalf("错误后继续读取应保留原错误：%d，%v", n, err)
		}
	})
	t.Run("取消后丢弃待交付缓冲", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r, err := dsdpcm.New(ctx, bytes.NewReader(data), l)
		if err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 37)
		if _, err := io.ReadFull(r, buffer); err != nil {
			t.Fatal(err)
		}
		cancel()
		if n, err := r.Read(buffer); n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后应返回取消错误而非缓冲音频：%d，%v", n, err)
		}
	})
}
