package render

import (
	"errors"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/src"
	"github.com/qlqq/roomusic/backend/internal/playback/dsdpcm"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

var (
	ErrNoStreamFormat = errors.New("render: 没有可用的流格式")
	ErrUnavailable    = errors.New("render: 渲染器不可用")
	ErrBusy           = errors.New("render: 渲染进程繁忙")
)

type deliveryFormat struct {
	codec, mime, delivery, encoder string
	lossless                       bool
}

var deliveryFormats = [...]deliveryFormat{
	{"flac", "audio/flac", "stream", "flac", true},
	{"wav", "audio/wav", "stream", "", true},
	{"opus", `audio/ogg; codecs="opus"`, "stream", "libopus", false},
	{"mp3", "audio/mpeg", "stream", "libmp3lame", false},
	{"aac", "audio/aac", "stream", "aac", false},
	{"alac", "application/vnd.apple.mpegurl", "hls", "alac", true},
	{"aac", "application/vnd.apple.mpegurl", "hls", "aac", false},
}

func codecInfo(codec, mime string) (deliveryFormat, bool) {
	for _, format := range deliveryFormats {
		if format.codec == codec && format.mime == mime {
			return format, true
		}
	}
	return deliveryFormat{}, false
}

func (r *Renderer) Negotiate(caps zones.Capabilities, quality string) (zones.StreamFormat, error) {
	var fallback *zones.StreamFormat
	for _, capability := range caps.StreamFormats {
		info, ok := codecInfo(capability.Codec, capability.MIMEType)
		if !ok || info.encoder != "" && (r.options.FFmpeg == nil || !r.options.FFmpeg.HasEncoder(info.encoder)) {
			continue
		}
		// 不相信声明中的 lossless 标记能够改变编码器本身的性质。
		if capability.Lossless != info.lossless {
			continue
		}
		format := zones.StreamFormat{Codec: capability.Codec, MimeType: info.mime, Delivery: info.delivery, Quality: "lossy", MaxChannels: caps.MaxChannels}
		if info.lossless {
			format.Quality = "lossless"
		}
		if capability.MaxSampleRate != nil {
			format.MaxSampleRate = *capability.MaxSampleRate
		}
		if capability.MaxBitDepth != nil {
			format.MaxBitDepth = *capability.MaxBitDepth
		}
		if format.MaxChannels <= 0 || format.MaxChannels > 32 || format.MaxSampleRate < 0 || format.MaxBitDepth < 0 {
			continue
		}
		if info.lossless && format.MaxBitDepth > 0 && format.MaxBitDepth < 16 {
			continue
		}
		if capability.Codec == "opus" && format.MaxSampleRate > 0 && format.MaxSampleRate < 48000 {
			continue
		}
		if capability.Codec == "mp3" && format.MaxSampleRate > 0 && format.MaxSampleRate < 32000 {
			continue
		}
		if capability.Codec == "aac" && format.MaxSampleRate > 0 && format.MaxSampleRate < 44100 {
			continue
		}
		if format.Quality == quality {
			return format, nil
		}
		if fallback == nil {
			copy := format
			fallback = &copy
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return zones.StreamFormat{}, ErrNoStreamFormat
}

func integerSource(t Track) bool {
	if t.DSDLayout != nil || t.IsFloat {
		return false
	}
	switch strings.ToLower(t.Codec) {
	case "flac", "alac", "ape", "wavpack", "wavpack_hybrid", "pcm", "aiff", "wav_extensible":
		return true
	}
	return strings.HasPrefix(strings.ToLower(t.Codec), "pcm_s") || strings.HasPrefix(strings.ToLower(t.Codec), "pcm_u")
}

// sourceFormat 比较来源段的有效格式，不拿 DSD 比特率充当 PCM 时钟。
// 浮点统一为非精确样本，整数仍保留有效位深边界。
type sourceFormat struct {
	pcm     chain.Format
	integer bool
	bits    int
}

func effectiveSource(t Track) sourceFormat {
	rate := t.SampleRate
	if t.DSDLayout != nil {
		rate, _ = dsdpcm.OutputRate(t.DSDLayout.Rate)
	}
	f := sourceFormat{pcm: chain.Format{SampleRate: rate, Channels: t.Channels, Layout: trackLayout(t)}, integer: integerSource(t)}
	if f.integer {
		f.bits = t.BitDepth
	}
	return f
}

func sameSourceFormat(a, b Track) bool {
	return effectiveSource(a) == effectiveSource(b)
}

func concreteFormat(spec zones.RenderSpec, source Track) (zones.StreamFormat, renderPlan, error) {
	f := spec.Format
	selected, ok := codecInfo(f.Codec, f.MimeType)
	if f.Delivery == "push" {
		if f.Codec != "alac" || f.MaxChannels != 2 || (f.MaxSampleRate != 44100 && f.MaxSampleRate != 48000) || (f.MaxBitDepth != 16 && f.MaxBitDepth != 24) {
			return f, renderPlan{}, ErrNoStreamFormat
		}
		selected = deliveryFormat{codec: f.Codec, mime: f.MimeType, delivery: f.Delivery, lossless: true}
	} else if !ok {
		return f, renderPlan{}, ErrNoStreamFormat
	}
	f.Delivery = selected.delivery
	lossless := selected.lossless
	f.Quality = "lossy"
	if lossless {
		f.Quality = "lossless"
	}
	maxChannels := f.MaxChannels
	if maxChannels < 1 || maxChannels > 32 {
		return f, renderPlan{}, ErrNoStreamFormat
	}
	// 压缩编码最多交付八声道；WAV 保留设备声明的上限。
	if f.Codec == "mp3" {
		maxChannels = min(maxChannels, 2)
	} else if f.Codec != "wav" {
		maxChannels = min(maxChannels, 8)
	}
	pcm := effectiveSource(source).pcm
	if pcm.SampleRate < 5 || pcm.Channels < 1 {
		return f, renderPlan{}, ErrNoStreamFormat
	}
	conversion, filter, err := chooseSampleRate(pcm.SampleRate, spec.Settings.SampleRate, f)
	if err != nil {
		return f, renderPlan{}, err
	}
	out := pcm
	out.SampleRate = conversion.To
	plan := renderPlan{sourcePCM: pcm, outputPCM: out, sampleRate: conversion, sampleRateFilter: filter}
	if err := plan.chooseChannels(spec.Settings.Downmix, maxChannels); err != nil {
		return f, renderPlan{}, err
	}
	if f.Delivery == "push" && plan.outputPCM.Channels == 1 {
		in := plan.outputPCM
		plan.outputPCM.Channels, plan.outputPCM.Layout = 2, chain.DefaultLayout(2)
		plan.tail = append(plan.tail, chain.Stage{In: in, Out: plan.outputPCM, Node: &monoStereo{}})
	}
	plan.peq, err = zones.DecodeSettingsDocument[zones.PEQSettings](spec.Settings.PEQ)
	if err != nil {
		return f, renderPlan{}, err
	}
	plan.convolution, err = spec.Settings.Convolution.DecodeConvolution()
	if err != nil {
		return f, renderPlan{}, err
	}
	out = plan.outputPCM
	plan.facts = sourceProcessing(source, spec.Start, spec.Settings, pcm)
	plan.facts.stages = len(conversion.Stages) > 0 || len(plan.stages) > 0 || plan.peq.Enabled || plan.convolution.Enabled && plan.convolution.IRID != nil
	f.Layout = out.Layout
	f.SampleRate, f.Channels, f.BitDepth = out.SampleRate, out.Channels, 0
	if lossless {
		f.BitDepth = 24
		if integerSource(source) && source.BitDepth > 0 && source.BitDepth <= 16 && !hasProcessing(plan.facts) {
			f.BitDepth = 16
		}
		if f.MaxBitDepth > 0 && f.MaxBitDepth < 24 {
			f.BitDepth = 16
		}
		if f.MaxBitDepth > 0 && f.MaxBitDepth < 16 {
			return f, renderPlan{}, ErrNoStreamFormat
		}
	}
	if f.Delivery == "push" {
		f.BitDepth = f.MaxBitDepth
	}
	return f, plan, nil
}

// chooseSampleRate 只规划，不生成核；有损交付约束在用户节点之前生效。
func chooseSampleRate(sourceRate int, document zones.SettingsDocument, f zones.StreamFormat) (src.Plan, string, error) {
	settings, err := zones.DecodeSettingsDocument[zones.SampleRateSettings](document)
	if err != nil {
		return src.Plan{}, "", err
	}
	maximum := 768000
	if f.MaxSampleRate > 0 {
		maximum = min(maximum, f.MaxSampleRate)
	}
	request := src.Request{SourceRate: sourceRate, Strategy: settings.Strategy, Filter: src.Filter(settings.Filter), MaxRate: maximum,
		Supported: func(rate int) bool { return rate > 0 && rate <= maximum },
	}
	if settings.CustomRate != nil {
		request.CustomRate = *settings.CustomRate
	}
	if f.Delivery == "push" {
		request.Strategy, request.CustomRate = "custom", f.MaxSampleRate
		plan, err := src.Choose(request)
		return plan, settings.Filter, err
	}
	var rates []int
	switch f.Codec {
	case "opus":
		rates = []int{48000}
	case "aac":
		rates = []int{48000, 44100}
	case "mp3":
		// 320 kb/s 使用 MPEG-1 的三个采样率，32 kHz 也是可明确协商的编码目标。
		rates = []int{48000, 44100, 32000}
		request.CompatibilityRates = rates
	}
	if len(rates) > 0 {
		request.Strategy = "none"
		request.MaxRate = min(maximum, rates[0])
		request.Supported = func(rate int) bool { return slices.Contains(rates, rate) }
	}
	plan, err := src.Choose(request)
	if errors.Is(err, src.ErrStrategyUnavailable) {
		request.Strategy = "none"
		plan, err = src.Choose(request)
	}
	if errors.Is(err, src.ErrNoSupportedRate) && f.MaxSampleRate > 0 && sourceRate > request.MaxRate {
		// 高于设备上限的音源可能需要同时降频和换族，例如 96 → 44.1 kHz。
		request.Strategy = "max_sample_rate"
		plan, err = src.Choose(request)
		plan.Compatibility = true
	}
	if err != nil {
		return src.Plan{}, "", errors.Join(ErrNoStreamFormat, err)
	}
	return plan, settings.Filter, nil
}
