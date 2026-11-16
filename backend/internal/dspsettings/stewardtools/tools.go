// Package stewardtools 提供本人 PEQ 预设的读取与套用。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func StewardTools(service *dspsettings.Service, playback *zones.Service, logger *slog.Logger) []tool.Definition {
	return []tool.Definition{
		{Name: "list_peq_presets", Description: "列出本人的 PEQ 预设，含 preset_id、名称、完整 PEQ 文档与创建/更新时间。", MinRole: tool.RoleMember, Scope: tool.ScopeRead, Untrusted: true,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, caller tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
				items, err := service.ListPresets(ctx, caller.User.ID)
				if err != nil {
					return nil, failure(err)
				}
				return json.Marshal(map[string]any{"items": items})
			}},
		{Name: "apply_peq_preset", Description: "把本人预设的完整 PEQ 文档套用到本人分区，保留预设的启用状态，其它播放设置不变。", MinRole: tool.RoleMember, Scope: tool.ScopePersonal, Untrusted: true,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["preset_id","zone_id"],"properties":{"preset_id":{"type":"string","minLength":36,"maxLength":36},"zone_id":{"type":"string","minLength":36,"maxLength":36}}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
				var p struct {
					PresetID string `json:"preset_id"`
					ZoneID   string `json:"zone_id"`
				}
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				if !uuid.Valid(p.PresetID) || !uuid.Valid(p.ZoneID) {
					return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "预设与分区标识必须是 UUID"}
				}
				p.PresetID = strings.ToLower(p.PresetID)
				p.ZoneID = strings.ToLower(p.ZoneID)
				presets, err := service.ListPresets(ctx, caller.User.ID)
				if err != nil {
					return nil, failure(err)
				}
				for _, preset := range presets {
					if preset.ID != p.PresetID {
						continue
					}
					settings, err := zones.DecodeSettingsDocument[zones.PEQSettings](preset.PEQ)
					if err != nil {
						return nil, failure(err)
					}
					settings.Source = &zones.PEQSource{Kind: "preset", PresetID: preset.ID, Name: preset.Name}
					document, err := zones.NewPEQDocument(settings)
					if err != nil {
						return nil, failure(err)
					}
					out, err := playback.Command(ctx, caller.User.ID, p.ZoneID, zones.Command{Command: "set_settings", Settings: zones.SettingsPatch{PEQ: &document}})
					if err != nil {
						return nil, failure(err)
					}
					tool.LogConfigChanged(ctx, logger, caller, "apply_peq_preset", p.ZoneID)
					return json.Marshal(map[string]any{"zone_id": p.ZoneID, "preset_id": preset.ID, "peq": out.Zone.Settings.PEQ, "server_time": out.ServerTime})
				}
				return nil, &tool.Error{Code: tool.CodeTargetNotFound, Message: "预设不存在"}
			}},
	}
}
func failure(err error) error {
	switch {
	case errors.Is(err, dspsettings.ErrNotFound), errors.Is(err, zones.ErrNotFound):
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
	case errors.Is(err, dspsettings.ErrInvalidInput), errors.Is(err, zones.ErrInvalidInput):
		return &tool.Error{Code: tool.CodeInvalidInput, Message: "音效设置无效"}
	case errors.Is(err, zones.ErrZoneOffline):
		return &tool.Error{Code: "zone_offline", Message: "分区离线"}
	case errors.Is(err, zones.ErrRendererUnavailable):
		return &tool.Error{Code: "renderer_unavailable", Message: "没有可用的音频渲染器"}
	default:
		return err
	}
}
