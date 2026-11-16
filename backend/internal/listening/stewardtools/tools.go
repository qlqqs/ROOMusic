// Package stewardtools 将本人的播放历史适配为管家工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type input struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	TrackID  string `json:"track_id"`
}

func definition(name, description, properties, required string, scope tool.Scope, untrusted bool, run func(context.Context, string, input) (any, error)) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleMember, Scope: scope, Untrusted: untrusted,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			if p.TrackID != "" && !uuid.Valid(p.TrackID) {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "曲目标识必须是 UUID"}
			}
			p.TrackID = strings.ToLower(p.TrackID)
			result, err := run(ctx, caller.User.ID, p)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}}
}

// StewardTools 只访问调用者本人的历史，不提供清空全部历史的工具。
func StewardTools(service *listening.Service) []tool.Definition {
	return []tool.Definition{
		definition("list_play_history", "查看本人的播放历史，按曲目汇总次数与最近播放时间。page 默认 1；page_size 默认 20、最大 50。返回 items、pagination、has_history。", `"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50}`, "", tool.ScopeRead, true,
			func(ctx context.Context, user string, p input) (any, error) {
				return service.Tracks(ctx, user, p.Page, p.PageSize)
			}),
		definition("list_recent_releases", "查看本人最近播放的专辑版本，最多 8 个；返回 items 与 has_history。", "", "", tool.ScopeRead, true,
			func(ctx context.Context, user string, _ input) (any, error) { return service.RecentReleases(ctx, user) }),
		definition("delete_play_history_item", "移除本人历史中的一首曲目，删除该曲目的全部播放记录。track_id 来自历史列表；返回 deleted 记录数，无记录时为 0。", `"track_id":{"type":"string","minLength":36,"maxLength":36}`, `"track_id"`, tool.ScopePersonal, false,
			func(ctx context.Context, user string, p input) (any, error) {
				n, err := service.DeleteTrack(ctx, user, p.TrackID)
				return map[string]any{"deleted": n}, err
			}),
	}
}
