package stewardtools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// SettingsTools 只读取或修改调用者本人的分区；复用 set_settings 的文档校验与事务。
func SettingsTools(service *zones.Service, logger *slog.Logger) []tool.Definition {
	read := tool.Definition{Name: "get_zone_settings", Description: "读取本人指定分区的完整播放与音效设置。先用 get_playback 取得 zone_id。", MinRole: tool.RoleMember, Scope: tool.ScopeRead, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["zone_id"],"properties":{` + zoneProperty + `}}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			id, _, err := settingsInput(raw, false)
			if err != nil {
				return nil, err
			}
			snapshot, err := service.Snapshot(ctx, caller.User.ID)
			if err != nil {
				return nil, mapError(err)
			}
			for _, zone := range snapshot.Zones {
				if zone.ZoneID == id {
					return json.Marshal(map[string]any{"zone_id": id, "settings": zone.Settings})
				}
			}
			return nil, mapError(zones.ErrNotFound)
		}}
	write := tool.Definition{Name: "set_zone_settings", Description: "修改本人分区的播放与音效设置，省略项保持原值；headroom、sample_rate、downmix、peq、convolution 提供完整对象。先读取 get_zone_settings，以现有文档为基础修改。", MinRole: tool.RoleMember, Scope: tool.ScopePersonal, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["zone_id","settings"],"properties":{` + zoneProperty + `,"settings":` + settingsSchema + `}}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			id, patch, err := settingsInput(raw, true)
			if err != nil {
				return nil, err
			}
			result, err := service.Command(ctx, caller.User.ID, id, zones.Command{Command: "set_settings", Settings: patch})
			if err != nil {
				return nil, mapError(err)
			}
			tool.LogConfigChanged(ctx, logger, caller, "set_zone_settings", id)
			return json.Marshal(map[string]any{"zone_id": id, "settings": result.Zone.Settings, "server_time": result.ServerTime})
		}}
	return []tool.Definition{read, write}
}
func settingsInput(raw json.RawMessage, write bool) (string, zones.SettingsPatch, error) {
	var p struct {
		ZoneID   string          `json:"zone_id"`
		Settings json.RawMessage `json:"settings"`
	}
	var patch zones.SettingsPatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", patch, invalid("设置输入无效")
	}
	if !uuid.Valid(p.ZoneID) {
		return "", patch, invalid("分区标识必须是 UUID")
	}
	if write {
		if zones.ValidateSettingsShape(p.Settings) != nil || json.Unmarshal(p.Settings, &patch) != nil || zones.ValidateSettingsPatch(patch) != nil {
			return "", patch, invalid("播放或音效设置无效")
		}
	}
	return strings.ToLower(p.ZoneID), patch, nil
}

const settingsSchema = `{"type":"object","additionalProperties":false,"properties":{
 "leveling":{"type":"string","enum":["off","auto","track","album"]},
 "gapless":{"type":"boolean"},"crossfade":{"type":"boolean"},
 "crossfade_seconds":{"type":"integer","enum":[3,5,8,12]},"quality":{"type":"string","enum":["lossless","lossy"]},
 "headroom":{"type":"object","additionalProperties":false,"required":["enabled","db","clip_indicator"],"properties":{"enabled":{"type":"boolean"},"db":{"type":"number","minimum":-30,"maximum":0},"clip_indicator":{"type":"boolean"}}},
 "sample_rate":{"type":"object","additionalProperties":false,"required":["strategy","custom_rate","filter"],"properties":{"strategy":{"type":"string","enum":["none","max_sample_rate","power_of_2","dsd","custom"]},"custom_rate":{},"filter":{"type":"string","enum":["linear_precise","minimum_precise","linear_smooth","minimum_smooth"]}}},
 "downmix":{"type":"object","additionalProperties":false,"required":["enabled"],"properties":{"enabled":{"type":"boolean"}}},
 "peq":{"type":"object","additionalProperties":false,"required":["enabled","preamp_db","bands"],"properties":{"enabled":{"type":"boolean"},"preamp_db":{"type":"number","minimum":-30,"maximum":30},"bands":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["type","freq_hz","channels","enabled"],"properties":{"type":{"type":"string","enum":["peaking","low_shelf","high_shelf","low_pass","high_pass","band_pass","notch","all_pass"]},"freq_hz":{"type":"number","minimum":10,"maximum":40000},"channels":{},"enabled":{"type":"boolean"},"gain_db":{"type":"number","minimum":-30,"maximum":30},"q":{"type":"number","minimum":0.1,"maximum":30},"slope_db":{"type":"integer","enum":[6,12,18,24,30,36]}}}},"source":{"type":"object"}}},
 "convolution":{"type":"object","additionalProperties":false,"required":["enabled","ir_id","before_peq"],"properties":{"enabled":{"type":"boolean"},"ir_id":{},"before_peq":{"type":"boolean"}}}
}}`
