package render

import (
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
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
	if t.IsFloat {
		return false
	}
	switch strings.ToLower(t.Codec) {
	case "flac", "alac", "ape", "wavpack", "wavpack_hybrid", "pcm", "aiff", "wav_extensible":
		return true
	}
	return strings.HasPrefix(strings.ToLower(t.Codec), "pcm_s") || strings.HasPrefix(strings.ToLower(t.Codec), "pcm_u")
}

func sameSourceFormat(a, b Track) bool {
	return a.SampleRate == b.SampleRate && a.Channels == b.Channels && a.BitDepth == b.BitDepth && integerSource(a) == integerSource(b) && a.IsFloat == b.IsFloat
}

func concreteFormat(spec zones.RenderSpec, source Track) (zones.StreamFormat, chain.Format, error) {
	f := spec.Format
	selected, ok := codecInfo(f.Codec, f.MimeType)
	if !ok {
		return f, chain.Format{}, ErrNoStreamFormat
	}
	f.Delivery = selected.delivery
	lossless := selected.lossless
	f.Quality = "lossy"
	if lossless {
		f.Quality = "lossless"
	}
	maxChannels := f.MaxChannels
	if maxChannels == 0 {
		maxChannels = 2
	}
	if maxChannels < 1 || maxChannels > 32 {
		return f, chain.Format{}, ErrNoStreamFormat
	}
	channels := min(source.Channels, maxChannels)
	// 第一批压缩编码使用最多八声道的标准布局；WAV 保留设备声明的声道数。
	if f.Codec != "wav" {
		channels = min(channels, 8)
	}
	rate := source.SampleRate
	if lossless && f.MaxSampleRate > 0 {
		for rate > f.MaxSampleRate {
			rate /= 2
		}
	}
	if rate < 5 || channels < 1 {
		return f, chain.Format{}, ErrNoStreamFormat
	}
	f.SampleRate, f.Channels, f.BitDepth = rate, channels, 0
	if lossless {
		f.BitDepth = 24
		if integerSource(source) && source.BitDepth > 0 && source.BitDepth <= 16 && spec.Settings.Leveling == "off" && !spec.Settings.Headroom.Enabled {
			f.BitDepth = 16
		}
		if f.MaxBitDepth > 0 && f.MaxBitDepth < 24 {
			f.BitDepth = 16
		}
		if f.MaxBitDepth > 0 && f.MaxBitDepth < 16 {
			return f, chain.Format{}, ErrNoStreamFormat
		}
	} else if f.Codec == "opus" {
		f.SampleRate = 48000
		if f.MaxSampleRate > 0 && f.MaxSampleRate < 48000 {
			return f, chain.Format{}, ErrNoStreamFormat
		}
	} else {
		maximum := 96000
		if f.MaxSampleRate > 0 {
			maximum = min(maximum, f.MaxSampleRate)
		}
		for f.SampleRate > maximum {
			f.SampleRate /= 2
		}
		if f.SampleRate < 7350 {
			return f, chain.Format{}, ErrNoStreamFormat
		}
	}
	return f, chain.Format{SampleRate: rate, Channels: channels}, nil
}
