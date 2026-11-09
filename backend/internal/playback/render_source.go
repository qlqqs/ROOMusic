package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrNotRenderable 表示来源组合、PCM 子类型或所需解码器不在当前渲染能力内。
var ErrNotRenderable = errors.New("playback track is not renderable")

// RenderSource 是已安全打开、可交给解码器的原始来源；File 由调用方关闭。
// BitDepth 是整数源的有效位深；浮点 WAV 为 0，IsFloat 明确标识扩展 WAV 的浮点子类型。
// CUE 字段预留区间挂点，本阶段不接受虚拟轨，因此始终为 nil。
type RenderSource struct {
	File                           *os.File
	TrackID, SourceKind, Codec     string
	SampleRate, Channels, BitDepth int
	DurationSeconds                float64
	IsFloat                        bool
	CueStartFrame, CueEndFrame     *int64
}

// RenderError 提供无路径的失败分类，并保留原始原因供 errors.Is/As 使用。
// transient 仅供应用识别基础设施或取消错误，不应写入曲目级失败事件。
type RenderError struct {
	cause  error
	reason string
}

func (err *RenderError) Error() string { return "playback render source: " + err.reason }
func (err *RenderError) Unwrap() error { return err.cause }

// FailureReason 返回来源错误的稳定分类；不依赖渲染器或播放区包。
func (err *RenderError) FailureReason() string { return err.reason }

func classifyRenderError(err error) error {
	reason := "transient"
	switch {
	case errors.Is(err, ErrTrackNotFound):
		reason = "not_found"
	case errors.Is(err, ErrRootDisabled):
		reason = "root_disabled"
	case errors.Is(err, ErrNotRenderable), errors.Is(err, ErrNotDirectlyPlayable):
		reason = "unsupported_format"
	case errors.Is(err, ErrSourceUnavailable):
		reason = "source_unavailable"
	case errors.Is(err, ErrSourceChanged):
		reason = "source_changed"
	}
	return &RenderError{cause: err, reason: reason}
}

// OpenForRender 复用来源解析、安全打开与版本核对，始终交付源文件，不访问派生缓存。
// 调用者在进入此用例前完成鉴权；曲目缺失、停用目录和格式拒绝仍先于文件系统检查。
func (service *Service) OpenForRender(ctx context.Context, trackID string) (*RenderSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, classifyRenderError(err)
	}
	target, err := service.resolveFormat(ctx, trackID, service.renderFormat)
	if err != nil {
		return nil, classifyRenderError(err)
	}
	opened, err := target.openSource()
	if err != nil {
		return nil, classifyRenderError(err)
	}
	result := &RenderSource{
		File: opened.File, TrackID: target.source.TrackID,
		SourceKind: target.source.SourceKind, Codec: target.source.Codec,
		SampleRate: target.source.SampleRate, Channels: target.source.Channels, BitDepth: target.source.BitDepth,
	}
	if target.format.container == containerWAV {
		err = service.verifyRenderWAV(result, opened.Size)
	}
	if err == nil {
		result.DurationSeconds, err = service.sources.LoadTrackDuration(ctx, trackID)
		if err != nil {
			err = classifyLookup(err)
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = opened.File.Close()
		return nil, classifyRenderError(err)
	}
	return result, nil
}

func (service *Service) verifyRenderWAV(source *RenderSource, size int64) error {
	facts, matches, err := sniffRenderWAV(source.File, size)
	if err != nil {
		return fmt.Errorf("%w: read WAV format: %w", ErrSourceUnavailable, err)
	}
	if !matches || facts.codec != source.Codec ||
		(source.SampleRate != 0 && source.SampleRate != facts.sampleRate) ||
		(source.Channels != 0 && source.Channels != facts.channels) ||
		(source.BitDepth != 0 && source.BitDepth != facts.bits) {
		return fmt.Errorf("%w: WAV format differs from scan record", ErrSourceChanged)
	}
	if facts.decoder == "" || !service.renderDecoders.HasDecoder(facts.decoder) {
		return ErrNotRenderable
	}
	source.SampleRate, source.Channels = facts.sampleRate, facts.channels
	source.BitDepth, source.IsFloat = facts.validBits, facts.isFloat
	if facts.isFloat {
		source.BitDepth = 0
	}
	return nil
}
