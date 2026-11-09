package irload

import (
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

// DecodedIR 是解码边界提供的同长度 planar 样本，不包含文件或进程句柄。
// 音频文件使用实际解码采样率；裸浮点文件使用 cfg 的 SampleRate。
type DecodedIR struct {
	SampleRate int
	Samples    [][]float64
}

// Prepare 将已解码 IR 重采样并映射到流的 sampleRate/channels，保持输入、输出声道数相等。
// cfg 为 nil 时 sources 必须只有一个多声道 IR；否则 sources 与 cfg.Paths（亦即
// Archive.Sources）逐项对应。同一文件应复用同一个 *DecodedIR，各文件声道只重采样一次。
// 不修改 cfg 或 sources；返回配置与输入样本不共享内存，但路径之间可共享只读 IR。
// 输入/输出延时独立保留，不能折进 IR 而改变卷积器的峰位置 Latency。
func Prepare(cfg *CFG, sources []*DecodedIR, sampleRate, channels int) (convolution.Config, error) {
	if err := validatePreparation(cfg, sources, sampleRate, channels); err != nil {
		return convolution.Config{}, err
	}
	result := convolution.Config{
		Channels: channels,
		InDelay:  make([]int, channels),
		OutDelay: make([]int, channels),
	}
	type sourceChannel struct {
		source  *DecodedIR
		channel int
	}
	prepared := make(map[sourceChannel][]float64)
	load := func(index, channel int) ([]float64, error) {
		ir := sources[index]
		key := sourceChannel{ir, channel}
		if samples, ok := prepared[key]; ok {
			return samples, nil
		}
		samples, err := ResampleIR(ir.Samples[channel], ir.SampleRate, sampleRate)
		if err != nil {
			return nil, fmt.Errorf("irload: source %d channel %d: %w", index, channel, err)
		}
		prepared[key] = samples
		return samples, nil
	}
	if cfg == nil {
		count := channels
		if len(sources[0].Samples) > 1 {
			count = min(count, len(sources[0].Samples))
		}
		for ch := 0; ch < count; ch++ {
			irChannel := ch
			if len(sources[0].Samples) == 1 {
				irChannel = 0
			}
			samples, err := load(0, irChannel)
			if err != nil {
				return convolution.Config{}, err
			}
			result.Paths = append(result.Paths, convolution.Path{
				IR: samples, Inputs: []convolution.Weighted{{Channel: ch, Weight: 1}},
				Outputs: []convolution.Weighted{{Channel: ch, Weight: 1}},
			})
		}
		return result, nil
	}
	inDelay, err := prepareDelays(cfg.InDelayMS, sampleRate)
	if err != nil {
		return convolution.Config{}, err
	}
	outDelay, err := prepareDelays(cfg.OutDelayMS, sampleRate)
	if err != nil {
		return convolution.Config{}, err
	}
	if len(cfg.Paths) == 1 && len(cfg.Paths[0].Inputs) == 1 && len(cfg.Paths[0].Outputs) == 1 {
		p := cfg.Paths[0]
		samples, err := load(0, p.Channel)
		if err != nil {
			return convolution.Config{}, err
		}
		input, output := p.Inputs[0], p.Outputs[0]
		for ch := 0; ch < channels; ch++ {
			result.Paths = append(result.Paths, convolution.Path{
				IR: samples, Inputs: []convolution.Weighted{{Channel: ch, Weight: input.Weight}},
				Outputs: []convolution.Weighted{{Channel: ch, Weight: output.Weight}},
			})
			result.InDelay[ch], result.OutDelay[ch] = inDelay[input.Channel], outDelay[output.Channel]
		}
		return result, nil
	}
	copy(result.InDelay, inDelay)
	copy(result.OutDelay, outDelay)
	for i, p := range cfg.Paths {
		samples, err := load(i, p.Channel)
		if err != nil {
			return convolution.Config{}, err
		}
		// 不存在的流输入不参与求和，等价于填零；超出流范围的输出没有接收者。
		// 保留无有效路由的路径 IR，峰位置仍按设计 §4 的全部路径计算。
		result.Paths = append(result.Paths, convolution.Path{
			IR: samples, Inputs: mapWeights(p.Inputs, channels), Outputs: mapWeights(p.Outputs, channels),
		})
	}
	return result, nil
}

func validatePreparation(cfg *CFG, sources []*DecodedIR, sampleRate, channels int) error {
	if sampleRate <= 0 || channels < 1 || channels > convolution.MaxChannels {
		return fmt.Errorf("irload: invalid stream format")
	}
	if cfg == nil {
		if len(sources) != 1 {
			return fmt.Errorf("irload: IR without cfg requires exactly one decoded source")
		}
	} else {
		if cfg.SampleRate <= 0 || cfg.InputChannels < 1 || cfg.InputChannels > convolution.MaxChannels ||
			cfg.OutputChannels < 1 || cfg.OutputChannels > convolution.MaxChannels ||
			len(cfg.InDelayMS) != cfg.InputChannels || len(cfg.OutDelayMS) != cfg.OutputChannels ||
			len(cfg.Paths) < 1 || len(cfg.Paths) > convolution.MaxPaths || len(sources) != len(cfg.Paths) {
			return fmt.Errorf("irload: invalid cfg shape or decoded source count")
		}
		for _, p := range cfg.Paths {
			if !validWeights(p.Inputs, cfg.InputChannels) || !validWeights(p.Outputs, cfg.OutputChannels) {
				return cfgError(max(1, p.Line), "invalid routing weights")
			}
		}
	}
	// 所有来源的几何都先核验，不能先分配前几条路径，再发现后面的目标抽头超限。
	seen := make(map[*DecodedIR]bool)
	for i, ir := range sources {
		if ir == nil || len(ir.Samples) < 1 || len(ir.Samples) > convolution.MaxChannels {
			return fmt.Errorf("irload: source %d has invalid channel count", i)
		}
		if cfg != nil && (cfg.Paths[i].Channel < 0 || cfg.Paths[i].Channel >= len(ir.Samples)) {
			return cfgError(max(1, cfg.Paths[i].Line), "file channel out of range")
		}
		if seen[ir] {
			continue
		}
		seen[ir] = true
		taps := len(ir.Samples[0])
		if _, err := resampledLength(taps, ir.SampleRate, sampleRate); err != nil {
			return fmt.Errorf("irload: source %d: %w", i, err)
		}
		for _, samples := range ir.Samples {
			if len(samples) != taps {
				return fmt.Errorf("irload: source %d has mismatched channel lengths", i)
			}
		}
	}
	return nil
}

func validWeights(weights []Weighted, channels int) bool {
	if len(weights) == 0 {
		return false
	}
	for _, w := range weights {
		if w.Channel < 0 || w.Channel >= channels || !finite(w.Weight) {
			return false
		}
	}
	return true
}

func prepareDelays(milliseconds []float64, sampleRate int) ([]int, error) {
	delays := make([]int, len(milliseconds))
	for i, ms := range milliseconds {
		n, err := DelaySamples(ms, sampleRate)
		if err != nil {
			return nil, err
		}
		delays[i] = n
	}
	return delays, nil
}

func mapWeights(weights []Weighted, channels int) []convolution.Weighted {
	var result []convolution.Weighted
	for _, w := range weights {
		if w.Channel < channels {
			result = append(result, convolution.Weighted{Channel: w.Channel, Weight: w.Weight})
		}
	}
	return result
}
