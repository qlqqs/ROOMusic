package render

import (
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/leveling"
)

// signalPath 的路径字段按曲目确定，监测字段由派发器补齐。
type signalPath struct {
	Quality         string             `json:"quality"`
	Steps           []chain.SignalStep `json:"steps"`
	Clipping        *bool              `json:"clipping,omitempty"`
	ProcessingSpeed *float64           `json:"processing_speed,omitempty"`
}

func (path *signalPath) append(step chain.SignalStep) {
	path.Steps = append(path.Steps, step)
	rank := func(quality string) int {
		switch quality {
		case "enhanced":
			return 1
		case "high":
			return 2
		case "low":
			return 3
		default:
			return 0
		}
	}
	if rank(step.Quality) > rank(path.Quality) {
		path.Quality = step.Quality
	}
}

func (s *session) segmentSignal(d *decoder) signalPath {
	path := signalPath{Quality: "lossless", Steps: []chain.SignalStep{}}
	track := d.track
	source := chain.SignalStep{Kind: "source", Quality: "lossless", Params: map[string]any{"codec": track.Codec, "sample_rate": track.SampleRate, "channels": track.Channels}}
	if track.BitDepth > 0 {
		source.Params["bit_depth"] = track.BitDepth
	}
	switch strings.ToLower(track.Codec) {
	case "mp3", "aac", "vorbis", "opus", "wavpack_hybrid":
		source.Quality, source.Lossy = "low", true
	}
	path.append(source)
	if track.SampleRate != s.pcm.SampleRate {
		path.append(rateStep(track.SampleRate, s.pcm.SampleRate))
	}
	if track.Channels != s.pcm.Channels {
		path.append(chain.SignalStep{Kind: "downmix", Quality: "high", Compat: true, Params: map[string]any{"from_channels": track.Channels, "to_channels": s.pcm.Channels, "by": "ffmpeg"}})
	}
	gainDB, basis, enabled := leveling.Choose(leveling.Mode(s.spec.Settings.Leveling), track.TrackGainDB, track.AlbumGainDB, d.entry.AlbumContext)
	if enabled {
		path.append(chain.SignalStep{Kind: "leveling", Quality: "enhanced", Params: map[string]any{"gain_db": gainDB, "basis": basis}})
	}
	for _, step := range s.chain.Steps() {
		path.append(step)
	}
	if step, ok := s.ditherStep(d); ok {
		path.append(step)
	}
	// Opus 固定 48 kHz 属于有损编码本身；来源与编码采样率已给出转换两端。
	if s.pcm.SampleRate != s.format.SampleRate && s.format.Codec != "opus" {
		path.append(rateStep(s.pcm.SampleRate, s.format.SampleRate))
	}
	encode := chain.SignalStep{Kind: "encode", Quality: "lossless", Params: map[string]any{"codec": s.format.Codec, "sample_rate": s.format.SampleRate}}
	if s.format.BitDepth > 0 {
		encode.Params["bit_depth"] = s.format.BitDepth
	} else {
		encode.Quality, encode.Lossy = "low", true
		if s.format.Codec == "opus" {
			encode.Params["bitrate_kbps"] = 128
		} else {
			encode.Params["bitrate_kbps"] = 256
		}
	}
	path.append(encode)
	return path
}

func rateStep(from, to int) chain.SignalStep {
	return chain.SignalStep{Kind: "convert", Quality: "high", Compat: true, Params: map[string]any{"from_rate": from, "to_rate": to, "by": "ffmpeg"}}
}

// ditherStep 只描述整首已确定的量化处理，不展示瞬时交叉淡化。
func (s *session) ditherStep(d *decoder) (chain.SignalStep, bool) {
	bits := s.format.BitDepth
	if bits == 0 {
		return chain.SignalStep{}, false
	}
	passthrough := d.exact && (!d.leveling || d.gain == 1) && !s.spec.Settings.Headroom.Enabled && d.sourceBits > 0 && d.sourceBits <= bits
	if passthrough {
		return chain.SignalStep{}, false
	}
	step := chain.SignalStep{Kind: "dither", Quality: "enhanced", Params: map[string]any{"bits": bits, "kind": "tpdf"}}
	if d.integer && d.sourceBits > bits {
		step.Compat, step.Quality = true, "high"
	}
	return step, true
}
