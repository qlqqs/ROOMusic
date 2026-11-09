package playback

import "github.com/qlqq/roomusic/backend/internal/catalog"

// RenderDecoders 是渲染格式表需要的能力清单，由应用装配已自检的解码工具。
type RenderDecoders interface {
	HasDecoder(name string) bool
}

// WithRenderDecoders 启用渲染来源入口与公开格式表，不改变旧接口的直出限制。
// 未装配或传 nil 时，RenderFormats 为空，OpenForRender 拒绝所有格式。
func WithRenderDecoders(decoders RenderDecoders) ServiceOption {
	return func(service *Service) { service.renderDecoders = decoders }
}

// renderFormatTable 是直出六项之外的可渲染组合；具体解码器由 renderDecoderNames 指定。
var renderFormatTable = []Format{
	{SourceKind: "m4a", Codec: "alac", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerMP4},
	{SourceKind: "ape_apev2", Codec: "ape", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAPE},
	{SourceKind: "wavpack_apev2", Codec: "wavpack", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack},
	{SourceKind: "wavpack_apev2", Codec: "wavpack_hybrid", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack},
	{SourceKind: "aiff_id3", Codec: "aiff", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAIFF},
	{SourceKind: "wav_info", Codec: "ieee_float", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
	{SourceKind: "wav_info", Codec: "wav_extensible", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
	// CUE 沿用父音频编码与容器，只能经渲染截取后交付。
	{SourceKind: "cue_virtual", Codec: "flac", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerFLAC},
	{SourceKind: "cue_virtual", Codec: "mp3", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerMP3},
	{SourceKind: "cue_virtual", Codec: "pcm", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
	{SourceKind: "cue_virtual", Codec: "aac", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerMP4},
	{SourceKind: "cue_virtual", Codec: "vorbis", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerOgg},
	{SourceKind: "cue_virtual", Codec: "opus", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerOgg},
	{SourceKind: "cue_virtual", Codec: "alac", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerMP4},
	{SourceKind: "cue_virtual", Codec: "ape", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAPE},
	{SourceKind: "cue_virtual", Codec: "wavpack", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack},
	{SourceKind: "cue_virtual", Codec: "wavpack_hybrid", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack},
	{SourceKind: "cue_virtual", Codec: "aiff", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAIFF},
	{SourceKind: "cue_virtual", Codec: "ieee_float", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
	{SourceKind: "cue_virtual", Codec: "wav_extensible", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
}

// RenderFormats 返回可渲染组合的副本；只发布至少有一种对应解码器的组合。
// PCM 组合的具体位深与扩展 WAV 子类型在安全打开后再次核对。
// 保留原六项的表示字段；原派生项和新增 WAV 项仍以 derived 表示服务端处理交付。
func (service *Service) RenderFormats() []Format {
	formats := make([]Format, 0, len(formatTable)+len(renderFormatTable))
	if service.renderDecoders == nil {
		return formats
	}
	candidates := append([]Format(nil), formatTable...)
	candidates = append(candidates, renderFormatTable...)
	for _, format := range candidates {
		for _, decoder := range renderDecoderNames(format.Codec) {
			if service.renderDecoders.HasDecoder(decoder) {
				formats = append(formats, format)
				break
			}
		}
	}
	return formats
}

func (service *Service) renderFormat(source catalog.TrackSource) (Format, error) {
	for _, format := range service.RenderFormats() {
		if format.SourceKind == source.SourceKind && format.Codec == source.Codec {
			return format, nil
		}
	}
	return Format{}, ErrNotRenderable
}

func renderDecoderNames(codec string) []string {
	switch codec {
	case "flac", "aac", "alac", "ape", "wavpack":
		return []string{codec}
	case "mp3":
		return []string{"mp3", "mp3float"}
	case "vorbis":
		return []string{"vorbis", "libvorbis"}
	case "opus":
		return []string{"opus", "libopus"}
	case "wavpack_hybrid":
		return []string{"wavpack"}
	case "pcm":
		return []string{"pcm_u8", "pcm_s16le", "pcm_s24le", "pcm_s32le"}
	case "ieee_float":
		return []string{"pcm_f32le", "pcm_f64le"}
	case "wav_extensible":
		return []string{"pcm_u8", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_f64le"}
	case "aiff":
		return []string{"pcm_s8", "pcm_s16be", "pcm_s24be", "pcm_s32be", "pcm_s16le", "pcm_s24le", "pcm_s32le"}
	default:
		return nil
	}
}
