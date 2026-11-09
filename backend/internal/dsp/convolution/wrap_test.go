package convolution_test

import (
	"math"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

func TestConvolutionWrapCompensatesPeakWithoutExtendingTail(t *testing.T) {
	ir := make([]float64, 4096)
	ir[2048] = 1
	processor, err := convolution.New(convolution.Config{Channels: 1, Paths: []convolution.Path{{
		IR: ir, Inputs: []convolution.Weighted{{Channel: 0, Weight: 1}}, Outputs: []convolution.Weighted{{Channel: 0, Weight: 1}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	format := chain.Format{SampleRate: 48000, Channels: 1}
	wrapped := chain.New(chain.Wrap(processor, format, format, chain.SignalStep{Kind: "convolution"}, true))
	// 两个冲激分别贴近首尾，同时验证丢头补尾和真实输入长度。
	input := make([]float64, 3000)
	input[0], input[len(input)-1] = 1, 0.5
	var output []float64
	collect := func(buffer *chain.Buffer) { output = append(output, buffer.Samples[0][:buffer.Frames]...) }
	for start := 0; start < len(input); start += 1000 {
		end := min(start+1000, len(input))
		collect(wrapped.Process(&chain.Buffer{Samples: [][]float64{input[start:end]}, Frames: end - start}))
	}
	for buffer := wrapped.Drain(); buffer != nil; buffer = wrapped.Drain() {
		collect(buffer)
	}
	if len(output) != len(input) {
		t.Fatalf("补偿后帧数=%d，真实输入=%d", len(output), len(input))
	}
	for i, got := range output {
		want := 0.0
		if i == 0 {
			want = 1
		}
		if i == len(input)-1 {
			want = 0.5
		}
		if math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
			t.Fatalf("补偿后第 %d 帧=%g，期望=%g", i, got, want)
		}
	}
}
