package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
)

const driftTestBlock = 2000

// 左声道为可独立求解分数延迟的正弦，右声道的锯齿同时覆盖有符号高位、低位和陡沿。
func driftSine(position, fullScale float64) float64 {
	return .65 * fullScale * math.Sin(2*math.Pi*position/47)
}

func driftInput(frames, width int) []byte {
	fullScale := 1 << (8*width - 1)
	input := make([]byte, 0, frames*2*width)
	var packed [4]byte
	for frame := range frames {
		for _, sample := range []int{int(math.Round(driftSine(float64(frame), float64(fullScale)))),
			-fullScale + (7*frame)%30000} {
			binary.LittleEndian.PutUint32(packed[:], uint32(sample))
			input = append(input, packed[:width]...)
		}
	}
	return input
}

func driftTestSample(pcm []byte, width, channels, frame, channel int) int32 {
	b := pcm[(frame*channels+channel)*width:]
	value := int32(int8(b[width-1]))
	for i := width - 2; i >= 0; i-- {
		value = value<<8 | int32(b[i])
	}
	return value
}

// 直接在分数源位置计算 32 抽头 Kaiser 窗 sinc，独立于渲染器的分相表和 PCM 解包。
func driftReference(input []byte, width, channels int, position float64) []float64 {
	base := int(math.Floor(position))
	sum := make([]float64, channels)
	if position == float64(base) {
		for ch := range sum {
			sum[ch] = float64(driftTestSample(input, width, channels, base, ch))
		}
		return sum
	}
	last := len(input)/(channels*width) - 1
	var norm float64
	for frame := base - 15; frame <= base+16; frame++ {
		x := position - float64(frame)
		z := 16 * (1 - x*x/(16*16)) // β²/4 = 16
		window, term := 1., 1.
		for n := 1; n <= 24; n++ {
			term *= z / float64(n*n)
			window += term
		}
		weight := window * math.Sin(math.Pi*x) / (math.Pi * x)
		norm += weight
		for ch := range sum {
			sum[ch] += weight * float64(driftTestSample(input, width, channels, min(max(frame, 0), last), ch))
		}
	}
	for ch := range sum {
		sum[ch] /= norm
	}
	return sum
}

func checkDriftReference(t *testing.T, output, input []byte, width, channels, frame int, position float64) {
	t.Helper()
	fullScale := float64(int(1) << (width*8 - 1))
	maxError := 1.0
	if width == 3 {
		maxError = 4
	}
	for ch, sample := range driftReference(input, width, channels, position) {
		got := float64(driftTestSample(output, width, channels, frame, ch))
		target := min(fullScale-1, max(-fullScale, math.Round(sample)))
		if math.Abs(got-target) > maxError {
			t.Fatalf("%d 位输出帧 %d 声道 %d 源位置 %.6f 得到 %.0f，参考 %.0f，容差 %.0f LSB",
				width*8, frame, ch, position, got, target, maxError)
		}
	}
}

func checkDriftPCM(t *testing.T, output, input []byte, width, adjust int) {
	t.Helper()
	frameBytes := 2 * width
	if want := len(input) - adjust*frameBytes; len(output) != want {
		t.Fatalf("输出 %d 字节，期望 %d", len(output), want)
	}

	step := 1
	if adjust < 0 {
		step = -1
	}
	fullScale := float64(int(1) << (width*8 - 1))
	var worstSineError float64
	source, out := 0, 0
	for range max(adjust, -adjust) {
		// 块为半开区间。左边界和下一块（或尾段）的首帧都应逐位对齐。
		for _, edge := range []int{0, driftTestBlock} {
			start := (source + edge + step*edge/driftTestBlock) * frameBytes
			at := (out + edge) * frameBytes
			if !bytes.Equal(output[at:at+frameBytes], input[start:start+frameBytes]) {
				t.Fatalf("调整块边界：输出帧 %d 与源帧 %d 不同", out+edge, start/frameBytes)
			}
		}
		for i := 1; i < driftTestBlock; i++ {
			position := float64(source+i) + float64(i*step)/driftTestBlock
			checkDriftReference(t, output, input, width, 2, out+i, position)
			left := float64(driftTestSample(output, width, 2, out+i, 0))
			worstSineError = max(worstSineError, math.Abs(left-driftSine(position, fullScale)))
		}
		source += driftTestBlock + step
		out += driftTestBlock
	}
	if !bytes.Equal(output[out*frameBytes:], input[source*frameBytes:]) {
		t.Fatalf("从输出帧 %d 开始的直通样本与源数据不同", out)
	}
	if db := 20 * math.Log10(worstSineError/fullScale); db >= -80 {
		t.Fatalf("调整块内最大正弦误差 %.3f dBFS，要求低于 −80 dBFS", db)
	}
}

func checkDriftPosition(t *testing.T, d *driftFrames, output, head, adjustment, pending int64) {
	t.Helper()
	if source := d.Source(output); source != output+adjustment {
		t.Fatalf("播放到 %d 帧：源位置 %d，期望 %d", output, source, output+adjustment)
	}
	gotHead, gotPending := d.RenderState(output)
	if gotHead != head || gotPending != pending || d.Pending(output) != pending {
		t.Fatalf("播放到 %d 帧：渲染头 %d、在途调整 %d（Pending %d），期望 %d、%d",
			output, gotHead, gotPending, d.Pending(output), head, pending)
	}
}

func TestDriftFramesAcrossBlocks(t *testing.T) {
	const frames = 12345
	for _, width := range []int{2, 3} {
		for _, adjust := range []int{6, -6} {
			t.Run(fmt.Sprintf("%dbit/%+d", width*8, adjust), func(t *testing.T) {
				var d driftFrames
				checkDriftPosition(t, &d, 0, 0, 0, 0)
				d.Adjust(int64(adjust))
				checkDriftPosition(t, &d, 0, 0, 0, int64(adjust))

				input := driftInput(frames, width)
				var output bytes.Buffer
				if err := d.copyPCM(bytes.NewReader(input), &output, width, 2); err != nil {
					t.Fatal(err)
				}
				checkDriftPCM(t, output.Bytes(), input, width, adjust)
				head := int64(frames - adjust)
				blocks := max(adjust, -adjust)
				step := int64(adjust / blocks)
				for i := range blocks {
					before := int64(i) * step
					mid := int64(i*driftTestBlock + driftTestBlock/2)
					checkDriftPosition(t, &d, mid-1, head, before, int64(adjust)-before)
					checkDriftPosition(t, &d, mid, head, before+step, int64(adjust)-before-step)
				}
				checkDriftPosition(t, &d, head, head, int64(adjust), 0)
			})
		}
	}
}

func TestDriftSetRate(t *testing.T) {
	const outputFrames = 40000
	for _, tc := range []struct {
		ppm, offset, steps, pending int
		interval                    float64
	}{
		{100, 0, 4, 0, 10000},
		{-300, 0, -12, 0, 1e6 / 300.},
		{800, 0, 20, 0, 2000},
		{-800, 0, -20, 0, 2000},
		{300, 24, 20, 16, 2000},
		{-300, -24, -20, -16, 2000},
	} {
		t.Run(fmt.Sprintf("%+dppm/offset%+d", tc.ppm, tc.offset), func(t *testing.T) {
			var d driftFrames
			d.SetRate(float64(tc.ppm))
			d.Adjust(int64(tc.offset))
			checkDriftPosition(t, &d, 0, 0, 0, int64(tc.offset))
			input := driftInput(outputFrames+tc.steps, 2)
			var output bytes.Buffer
			if err := d.copyPCM(bytes.NewReader(input), &output, 2, 2); err != nil {
				t.Fatal(err)
			}
			if frames := output.Len() / 4; frames != outputFrames {
				t.Fatalf("输出 %d 帧，期望 %d", frames, outputFrames)
			}
			var prev int64
			count, last := 0, 0
			for frame := 0; frame <= outputFrames; frame++ {
				delta := d.Source(int64(frame)) - int64(frame)
				if delta == prev {
					continue
				}
				if delta-prev != int64(tc.steps/max(tc.steps, -tc.steps)) {
					t.Fatalf("输出帧 %d 出现错误调整 %+d", frame, delta-prev)
				}
				// 最近整数在每次局部拉伸的中点改变，非整数间隔允许一帧舍入差。
				wantMid := (float64(count) + .5) * tc.interval
				if math.Abs(float64(frame)-wantMid) > 1 || (count > 0 && frame-last < 2000) {
					t.Fatalf("调整 %d 在输出帧 %d 生效，期望 %.3f，上一次在 %d", count+1, frame, wantMid, last)
				}
				count++
				last, prev = frame, delta
			}
			if count != max(tc.steps, -tc.steps) {
				t.Fatalf("调整 %d 次，期望 %d", count, max(tc.steps, -tc.steps))
			}
			checkDriftPosition(t, &d, 0, outputFrames, 0, int64(tc.steps+tc.pending))
			checkDriftPosition(t, &d, outputFrames, outputFrames, int64(tc.steps), int64(tc.pending))
		})
	}
}

// 沿用固定种子的二值白噪声，将每个插值样本与带限参考比较。
func TestDriftNoiseBlockReference(t *testing.T) {
	const frames = 2 * driftTestBlock
	for _, width := range []int{2, 3} {
		t.Run(fmt.Sprintf("%dbit", width*8), func(t *testing.T) {
			// 两种位深使用相同的 ±0.25 满幅信号，容差按各自位深的 LSB 计算。
			amplitude := 1 << (8*width - 3)
			rng := rand.New(rand.NewPCG(17, 43))
			input := make([]byte, 0, frames*width)
			var packed [4]byte
			for range frames {
				sample := amplitude * (2*rng.IntN(2) - 1)
				binary.LittleEndian.PutUint32(packed[:], uint32(sample))
				input = append(input, packed[:width]...)
			}

			var d driftFrames
			d.Adjust(-1)
			var output bytes.Buffer
			if err := d.copyPCM(bytes.NewReader(input), &output, width, 1); err != nil {
				t.Fatal(err)
			}
			if output.Len() != (frames+1)*width {
				t.Fatalf("插帧后得到 %d 字节，期望 %d", output.Len(), (frames+1)*width)
			}
			for i := range driftTestBlock {
				position := float64(i*(driftTestBlock-1)) / driftTestBlock
				checkDriftReference(t, output.Bytes(), input, width, 1, i, position)
			}
			if !bytes.Equal(output.Bytes()[driftTestBlock*width:], input[(driftTestBlock-1)*width:]) {
				t.Fatal("插帧块外的单声道白噪声与源不同")
			}
		})
	}
}
