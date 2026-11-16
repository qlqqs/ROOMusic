// Package stewardtools 提供安全的音频事实与分析进度，不返回媒体流或路径。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func StewardTools(service *playback.Service, store *catalog.Store, progress playback.AnalysisProgressProvider) []tool.Definition {
	return []tool.Definition{
		{Name: "get_track_audio", Description: "查看已存曲目音频格式、采样率、位深、声道、时长、大小和响度结果；直出格式另核对源文件，失败给出类别但保留已存事实。不含流地址和路径。", MinRole: tool.RoleMember, Scope: tool.ScopeRead,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["track_id"],"properties":{"track_id":{"type":"string","minLength":36,"maxLength":36}}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
				var p struct {
					TrackID string `json:"track_id"`
				}
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				if !uuid.Valid(p.TrackID) {
					return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "曲目标识必须是 UUID"}
				}
				p.TrackID = strings.ToLower(p.TrackID)
				source, err := store.LoadTrackSource(ctx, p.TrackID)
				if err != nil {
					return nil, failure(err)
				}
				duration, err := store.LoadTrackDuration(ctx, p.TrackID)
				if err != nil {
					return nil, failure(err)
				}
				loudness, err := store.LoadTrackAnalysis(ctx, p.TrackID, analysis.Version)
				if err != nil {
					return nil, failure(err)
				}
				var audioAnalysis any
				if a := loudness.Analysis; a != nil {
					audioAnalysis = map[string]any{"integrated_lufs": a.IntegratedLUFS, "true_peak_dbtp": a.TruePeakDBTP, "sample_peak_dbfs": a.SamplePeakDBFS, "loudness_range_lu": a.LoudnessRangeLU, "track_gain_db": a.TrackGainDB, "duration_ms": a.DurationMS, "leading_silence_ms": a.LeadingSilenceMS, "trailing_silence_ms": a.TrailingSilenceMS}
				}
				format := map[string]any{"source_kind": source.SourceKind, "codec": source.Codec}
				prepared, prepareErr := service.Prepare(ctx, p.TrackID)
				status := "verified"
				if prepareErr == nil {
					format["mime_type"] = prepared.Format.MIMEType
					if err := prepared.File.Close(); err != nil {
						return nil, &tool.Error{Code: "source_unavailable", Message: "无法完成源文件核对"}
					}
				} else {
					switch {
					case errors.Is(prepareErr, playback.ErrNotDirectlyPlayable):
						status = "not_directly_playable"
					case errors.Is(prepareErr, playback.ErrRootDisabled):
						status = "root_disabled"
					case errors.Is(prepareErr, playback.ErrSourceChanged):
						status = "source_changed"
					case errors.Is(prepareErr, playback.ErrSourceUnavailable):
						status = "source_unavailable"
					default:
						return nil, failure(prepareErr)
					}
				}
				return json.Marshal(map[string]any{"track_id": p.TrackID, "format": format, "sample_rate": source.SampleRate, "bit_depth": source.BitDepth, "channels": source.Channels, "duration_seconds": duration, "size_bytes": source.Size, "source_check": status, "analysis_status": loudness.Status, "analysis": audioAnalysis, "album": loudness.Album})
			}},
		{Name: "get_analysis_progress", Description: "查看音频分析总量、完成数、失败数与工作状态。", MinRole: tool.RoleAdmin, Scope: tool.ScopeRead, Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, _ tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
			out, err := progress.LoadProgress(ctx)
			if err != nil {
				return nil, failure(err)
			}
			return json.Marshal(out)
		}},
	}
}
func failure(err error) error {
	if errors.Is(err, catalog.ErrTrackNotFound) || errors.Is(err, catalog.ErrAnalysisTrackNotFound) || errors.Is(err, playback.ErrTrackNotFound) {
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "曲目不存在"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &tool.Error{Code: "database_unavailable", Message: "服务暂不可用"}
}
