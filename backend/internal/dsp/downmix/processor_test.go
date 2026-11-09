package downmix_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/downmix"
)

func TestProcessorBlocksAndBufferReuse(t *testing.T) {
	m, err := downmix.Build(0x60f, 0x3)
	if err != nil {
		t.Fatal(err)
	}
	p := downmix.New(m)
	var previous [2]*float64
	largest := 0
	for block, frames := range []int{0, 3, 1, 5, 2, 5, 0, 4} {
		in := make([][]float64, 6)
		original := make([][]float64, 6)
		for channel := range in {
			in[channel] = make([]float64, frames)
			original[channel] = make([]float64, frames)
			for frame := range in[channel] {
				in[channel][frame] = float64((block+1)*(channel+1)*(frame+1)) / 10
				original[channel][frame] = in[channel][frame]
			}
		}
		out := p.Process(in)
		if len(out) != 2 {
			t.Fatalf("块 %d：输出声道数=%d，期望 2", block, len(out))
		}
		for channel, samples := range out {
			if len(samples) != frames {
				t.Fatalf("块 %d，声道 %d：帧数=%d，期望 %d", block, channel, len(samples), frames)
			}
			if frames > 0 {
				if frames <= largest && &samples[0] != previous[channel] {
					t.Errorf("块 %d，声道 %d：未复用已有容量", block, channel)
				}
				previous[channel] = &samples[0]
			}
			for frame, got := range samples {
				// 侧环绕 5.1 的顺序为 FL、FR、FC、LFE、SL、SR。
				want := math.Pow(10, -6.0/20)*in[channel][frame] +
					math.Pow(10, -9.0/20)*in[2][frame] +
					math.Pow(10, -12.0/20)*in[4+channel][frame]
				if !(math.Abs(got-want) <= 1e-9) {
					t.Errorf("块 %d，声道 %d，帧 %d：输出=%g，期望 %g", block, channel, frame, got, want)
				}
			}
		}
		if frames > largest {
			largest = frames
		}
		if !reflect.DeepEqual(in, original) {
			t.Fatalf("块 %d：Process 修改了输入", block)
		}
		if frames > 0 {
			out[0][0] = 99
			if !reflect.DeepEqual(in, original) {
				t.Fatalf("块 %d：输出与输入共享样本缓冲", block)
			}
		}
	}
}

func TestProcessorCopiesMatrix(t *testing.T) {
	m, err := downmix.Build(0x60f, 0x3)
	if err != nil {
		t.Fatal(err)
	}
	p := downmix.New(m)
	for _, row := range m.Gains {
		for input := range row {
			row[input] = 0
		}
	}
	m.Gains[1] = []float64{9, 9, 9, 9, 9, 9}
	out := p.Process([][]float64{{1}, {2}, {3}, {4}, {5}, {6}})
	want := [][]float64{
		{math.Pow(10, -6.0/20) + 3*math.Pow(10, -9.0/20) + 5*math.Pow(10, -12.0/20)},
		{2*math.Pow(10, -6.0/20) + 3*math.Pow(10, -9.0/20) + 6*math.Pow(10, -12.0/20)},
	}
	if len(out) != len(want) {
		t.Fatalf("输出声道数=%d，期望 %d", len(out), len(want))
	}
	for channel, row := range want {
		if len(out[channel]) != 1 {
			t.Fatalf("输出 %d：帧数=%d，期望 1", channel, len(out[channel]))
		}
		if got := out[channel][0]; !(math.Abs(got-row[0]) <= 1e-9) {
			t.Errorf("声道 %d：修改原矩阵后输出=%g，期望 %g", channel, got, row[0])
		}
	}
}
