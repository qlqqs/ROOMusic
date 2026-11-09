package dsdpcm

import (
	"fmt"
	"math/big"

	"github.com/qlqq/roomusic/backend/internal/dsp/dsd"
	"github.com/qlqq/roomusic/backend/internal/dsp/peq"
	"github.com/qlqq/roomusic/backend/internal/dsp/src"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdio"
)

// Options 是显式转换配置，不自动补默认值；零值表示 0 dB、无后置低通。
// Filter 为 0/1/2/3，分别表示关闭、四阶 70 kHz、四阶 50 kHz、八阶 30 kHz。
type Options struct {
	GainDB float64
	Filter int
}

// Format 描述来源转换后的 PCM；Layout 沿用来源的扬声器掩码，零表示未知。
type Format struct {
	SampleRate int
	Channels   int
	Layout     uint32
}

// pipeline 仅组合算法状态；延迟裁剪、尾部冲刷与定位由读取器负责。
// factor 是每输出帧的输入字节帧数，quantum 是每声道的输入字节数；
// latency、preroll、frames 均以最终 PCM 帧为单位。实例不可并发使用。
type pipeline struct {
	converter *dsd.Converter
	filter    *peq.IIR
	resampler *src.Resampler
	format    Format
	factor    int
	quantum   int
	latency   int
	preroll   int64
	frames    int64
}

// OutputRate 返回 DSD 来源的固定 PCM 率，只检查倍率，不构建滤波器。
// 安全打开、格式规划和分析共用这一规则。
func OutputRate(dsdRate int) (int, error) {
	for _, base := range []int{2822400, 3072000} {
		if dsdRate >= base && dsdRate%base == 0 {
			factor := dsdRate / base
			if factor&(factor-1) == 0 {
				return base / 8, nil
			}
		}
	}
	return 0, fmt.Errorf("dsdpcm: 不支持的 DSD 采样率 %d", dsdRate)
}

func newPipeline(layout dsdio.Layout, options Options) (*pipeline, error) {
	if options.Filter < 0 || options.Filter > 3 {
		return nil, fmt.Errorf("dsdpcm: 后置低通档位必须在 0–3 之间")
	}
	target, err := OutputRate(layout.Rate)
	if err != nil {
		return nil, err
	}
	byteRate := layout.Rate / 8
	factor := byteRate / target
	plan, err := src.Choose(src.Request{
		SourceRate: byteRate,
		Strategy:   "custom",
		CustomRate: target,
		Filter:     src.LinearPrecise,
		Supported:  func(rate int) bool { return rate == target },
	})
	if err != nil {
		return nil, fmt.Errorf("dsdpcm: 规划来源降采样: %w", err)
	}
	converter, err := dsd.New(layout.Channels, options.GainDB)
	if err != nil {
		return nil, fmt.Errorf("dsdpcm: 创建 DSD 转换器: %w", err)
	}

	var filter *peq.IIR
	if options.Filter != 0 {
		order, cutoff := 4, 70000.0
		switch options.Filter {
		case 2:
			cutoff = 50000
		case 3:
			order, cutoff = 8, 30000
		}
		// 在八比特抽取后的速率设计，逐声道展开共享固定 Q 节，不补偿增益。
		prototype := peq.ButterworthLowPass(order, cutoff, float64(byteRate))
		sections := make([]peq.Section, 0, len(prototype)*layout.Channels)
		for channel := 0; channel < layout.Channels; channel++ {
			for _, section := range prototype {
				section.Channel = channel
				sections = append(sections, section)
			}
		}
		filter = peq.NewIIR(layout.Channels, sections, 0)
	}

	latency := pipelineLatency(plan.Stages, converter.Latency(), factor)
	return &pipeline{
		converter: converter,
		filter:    filter,
		resampler: src.New(plan.Stages, layout.Channels),
		format:    Format{SampleRate: target, Channels: layout.Channels, Layout: layout.Mask},
		factor:    factor,
		quantum:   byteRate / 5,
		latency:   latency,
		preroll:   max(int64(target), int64(2*latency+1)),
		frames:    int64(layout.Samples / uint64(8*factor)),
	}, nil
}

// pipelineLatency 把各级精确群延迟换到最终帧域后只四舍五入一次。
// SRC.Latency 已取整，不能拿它与转换器延迟相加；IIR 申报延迟为零。
func pipelineLatency(stages []src.Stage, converterLatency, factor int) int {
	delay := new(big.Rat)
	for _, stage := range stages {
		delay.Mul(delay, big.NewRat(int64(stage.L), int64(stage.M)))
		kernel := src.Kernel(stage.Phase, stage.Quality, stage.L, stage.M)
		delay.Add(delay, big.NewRat(int64(len(kernel)-1), 2*int64(stage.M)))
	}
	delay.Add(delay, big.NewRat(int64(converterLatency), int64(factor)))
	// 延迟非负；整数分子加半个分母再整除，避免浮点半帧边界误差。
	half := new(big.Int).Rsh(new(big.Int).Set(delay.Denom()), 1)
	rounded := new(big.Int).Add(delay.Num(), half)
	rounded.Quo(rounded, delay.Denom())
	return int(rounded.Int64())
}

// process 接受等长、MSB 先的逐声道字节；不裁剪起点、不计入尾部补料长度。
// 返回样本归调用方所有，后续 process/reset 不会覆盖它们。
func (p *pipeline) process(in [][]byte) [][]float64 {
	samples := p.converter.Process(in)
	if p.filter != nil {
		samples = p.filter.Process(samples)
	}
	return p.resampler.Process(samples)
}

// reset 恢复转换器静音历史并清空 IIR/SRC 状态，不改变格式和长度事实。
func (p *pipeline) reset() {
	p.converter.Reset()
	if p.filter != nil {
		p.filter.Reset()
	}
	p.resampler.Reset()
}
