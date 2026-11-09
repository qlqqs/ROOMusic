package playback

import "github.com/qlqq/roomusic/backend/internal/catalog"

// RenderDecoders 是渲染格式表需要的能力清单，由应用装配已自检的解码工具。
type RenderDecoders interface {
	HasDecoder(name string) bool
}

// WithRenderDecoders 启用渲染来源入口，不改变直出、派生与 Formats 的既有语义。
// 未装配或传 nil 时，RenderFormats 为空，OpenForRender 拒绝所有格式。
func WithRenderDecoders(decoders RenderDecoders) ServiceOption {
	return func(service *Service) { service.renderDecoders = decoders }
}

// RenderFormats 返回可渲染组合的副本；只发布至少有一种对应解码器的组合。
// PCM 组合的具体位深与扩展 WAV 子类型在安全打开后再次核对。
// 保留原六项的表示字段；原派生项和新增 WAV 项仍以 derived 表示服务端处理交付。
// 此入口不启用 CUE；旧 HTTP 格式表直到派生退役阶段才切换到这里。
func (service *Service) RenderFormats() []Format {
	formats := make([]Format, 0, len(formatTable)+len(derivedFormatTable)+2)
	if service.renderDecoders == nil {
		return formats
	}
	candidates := append([]Format(nil), formatTable...)
	candidates = append(candidates, derivedFormatTable...)
	candidates = append(candidates,
		Format{SourceKind: "wav_info", Codec: "ieee_float", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
		Format{SourceKind: "wav_info", Codec: "wav_extensible", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWAV},
	)
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
