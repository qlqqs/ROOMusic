package irload

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

func TestPrepareChannelMatching(t *testing.T) {
	mono := &DecodedIR{SampleRate: 48000, Samples: [][]float64{{2, -0.5}}}
	stereo := &DecodedIR{SampleRate: 48000, Samples: [][]float64{{2, -0.5}, {3, 0.25}}}
	diagonal := func(ir []float64, ch int) convolution.Path {
		return convolution.Path{IR: ir, Inputs: []convolution.Weighted{{Channel: ch, Weight: 1}}, Outputs: []convolution.Weighted{{Channel: ch, Weight: 1}}}
	}
	for _, tt := range []struct {
		name   string
		cfg    *CFG
		source *DecodedIR
		want   convolution.Config
	}{
		{
			name: "无配置单声道复制", source: mono,
			want: convolution.Config{Channels: 2, InDelay: []int{0, 0}, OutDelay: []int{0, 0},
				Paths: []convolution.Path{diagonal(mono.Samples[0], 0), diagonal(mono.Samples[0], 1)}},
		},
		{
			name: "无配置声道数相等", source: stereo,
			want: convolution.Config{Channels: 2, InDelay: []int{0, 0}, OutDelay: []int{0, 0},
				Paths: []convolution.Path{diagonal(stereo.Samples[0], 0), diagonal(stereo.Samples[1], 1)}},
		},
		{
			name: "无配置多出的流声道无路径", source: stereo,
			want: convolution.Config{Channels: 3, InDelay: []int{0, 0, 0}, OutDelay: []int{0, 0, 0},
				Paths: []convolution.Path{diagonal(stereo.Samples[0], 0), diagonal(stereo.Samples[1], 1)}},
		},
		{
			name: "无配置多出的IR声道不用", source: stereo,
			want: convolution.Config{Channels: 1, InDelay: []int{0}, OutDelay: []int{0},
				Paths: []convolution.Path{diagonal(stereo.Samples[0], 0)}},
		},
		{
			name: "单入单出复制保留所用声道权重和延时", source: stereo,
			cfg: &CFG{SampleRate: 44100, InputChannels: 3, OutputChannels: 3,
				InDelayMS: []float64{0.02, 0.04, 0.07}, OutDelayMS: []float64{0.09, 0.11, 0.14},
				Paths: []CFGPath{{Channel: 1, Inputs: []Weighted{{Channel: 2, Weight: -0.4}}, Outputs: []Weighted{{Channel: 1, Weight: 0.7}}}}},
			want: convolution.Config{Channels: 2, InDelay: []int{3, 3}, OutDelay: []int{5, 5}, Paths: []convolution.Path{
				{IR: stereo.Samples[1], Inputs: []convolution.Weighted{{Channel: 0, Weight: -0.4}}, Outputs: []convolution.Weighted{{Channel: 0, Weight: 0.7}}},
				{IR: stereo.Samples[1], Inputs: []convolution.Weighted{{Channel: 1, Weight: -0.4}}, Outputs: []convolution.Weighted{{Channel: 1, Weight: 0.7}}},
			}},
		},
		{
			name: "单路径多输入不复制且不存在的流输入填零", source: mono,
			cfg: &CFG{SampleRate: 48000, InputChannels: 3, OutputChannels: 1,
				InDelayMS: []float64{0.02, 0.04, 0.07}, OutDelayMS: []float64{0.09},
				Paths: []CFGPath{{Inputs: []Weighted{{Channel: 1, Weight: -0.4}, {Channel: 2, Weight: 0.7}}, Outputs: []Weighted{{Channel: 0, Weight: 0.3}}}}},
			want: convolution.Config{Channels: 2, InDelay: []int{0, 1}, OutDelay: []int{4, 0}, Paths: []convolution.Path{
				{IR: mono.Samples[0], Inputs: []convolution.Weighted{{Channel: 1, Weight: -0.4}}, Outputs: []convolution.Weighted{{Channel: 0, Weight: 0.3}}},
			}},
		},
		{
			name: "单路径多输出不复制且超出流的输出丢弃", source: mono,
			cfg: &CFG{SampleRate: 48000, InputChannels: 1, OutputChannels: 3,
				InDelayMS: []float64{0.07}, OutDelayMS: []float64{0.09, 0.11, 0.14},
				Paths: []CFGPath{{Inputs: []Weighted{{Channel: 0, Weight: -0.4}}, Outputs: []Weighted{{Channel: 1, Weight: 0.7}, {Channel: 2, Weight: 0.3}}}}},
			want: convolution.Config{Channels: 2, InDelay: []int{3, 0}, OutDelay: []int{4, 5}, Paths: []convolution.Path{
				{IR: mono.Samples[0], Inputs: []convolution.Weighted{{Channel: 0, Weight: -0.4}}, Outputs: []convolution.Weighted{{Channel: 1, Weight: 0.7}}},
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Prepare(tt.cfg, []*DecodedIR{tt.source}, 48000, tt.want.Channels)
			if err != nil {
				t.Fatalf("准备失败：%v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("准备结果 = %+v，期望 %+v", got, tt.want)
			}
		})
	}
}

func TestPrepareOwnershipAndSharedIR(t *testing.T) {
	fixture := func() (*CFG, *DecodedIR) {
		return &CFG{SampleRate: 44100, InputChannels: 2, OutputChannels: 2,
			InDelayMS: []float64{0.07, 0.11}, OutDelayMS: []float64{0.09, 0.14},
			Paths: []CFGPath{
				{Channel: 1, Inputs: []Weighted{{Channel: 0, Weight: -0.4}}, Outputs: []Weighted{{Channel: 1, Weight: 0.7}}},
				{Channel: 1, Inputs: []Weighted{{Channel: 1, Weight: 0.3}}, Outputs: []Weighted{{Channel: 0, Weight: 0.6}}},
			}}, &DecodedIR{SampleRate: 44100, Samples: [][]float64{{3, 0.25}, {2, -0.5}}}
	}
	cfg, source := fixture()
	wantCFG, wantSource := fixture()
	sources := []*DecodedIR{source, source}
	got, err := Prepare(cfg, sources, 48000, 2)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	if len(got.Paths) != 2 || len(got.Paths[0].IR) != 3 || len(got.Paths[1].IR) != 3 {
		t.Fatalf("共享来源应得到两条重采样路径：%+v", got.Paths)
	}
	if &got.Paths[0].IR[0] != &got.Paths[1].IR[0] {
		t.Fatal("同一来源的同一声道应共享已重采样的IR切片")
	}
	if !reflect.DeepEqual(cfg, wantCFG) || !reflect.DeepEqual(sources, []*DecodedIR{wantSource, wantSource}) {
		t.Fatal("准备过程修改了配置或来源样本")
	}
	// 修改返回值，确认其样本、权重和延时均不借用调用方的内存。
	got.Paths[0].IR[0] = 99
	got.Paths[0].Inputs[0].Weight = 99
	got.Paths[0].Outputs[0].Weight = 99
	got.InDelay[0], got.OutDelay[0] = 99, 99
	if !reflect.DeepEqual(cfg, wantCFG) || !reflect.DeepEqual(sources, []*DecodedIR{wantSource, wantSource}) {
		t.Fatal("返回值与配置或来源样本共享可变内存")
	}
}
