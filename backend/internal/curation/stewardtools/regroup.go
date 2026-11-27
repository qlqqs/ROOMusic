package stewardtools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// RegroupLocalFiles 供管理员在聊天与 MCP 中点名整理一个目录。
func RegroupLocalFiles(w *curation.Writer) tool.Definition {
	const position = `{"type":"integer","minimum":1}`
	const cueTrack = `{"type":"object","additionalProperties":false,"properties":{"track_id":` + idSchema + `,"disc":` + position + `,"track":` + position + `},"required":["track_id","disc","track"]}`
	const file = `{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","minLength":1},"disc":` + position + `,"track":` + position + `,"tracks":{"type":"array","items":` + cueTrack + `}},"required":["path"]}`
	const album = `{"type":"object","additionalProperties":false,"properties":{"files":{"type":"array","items":` + file + `}},"required":["files"]}`
	return tool.Definition{Name: "regroup_local_files", Description: "按文件重新归组连通目录中的发行。directory 和 path 均相对于曲库根。普通文件给 disc、track；CUE 父音频文件给 tracks，每项含 track_id、disc、track。",
		Scope: tool.ScopeShared, MinRole: tool.RoleAdmin, Revertible: true, Untrusted: true,
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"root_id":` + idSchema + `,"directory":{"type":"string"},"albums":{"type":"array","items":` + album + `}},"required":["root_id","directory","albums"]}`),
		Output: json.RawMessage(`{"type":"object"}`),
		Preview: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (tool.Preview, error) {
			var in curation.StewardGrouping
			if err := json.Unmarshal(raw, &in); err != nil {
				return tool.Preview{}, invalid()
			}
			before, err := w.PreviewStewardGrouping(ctx, in)
			if err != nil {
				return tool.Preview{}, toolError(err)
			}
			target := in.Directory
			if target == "" {
				target = "."
			}
			return tool.Preview{Target: target, Summary: fmt.Sprintf("将 %d 个现有发行的文件分为 %d 张专辑", len(before), len(in.Albums)), Changes: []tool.Change{{Field: "grouping", Before: before, After: in.Albums}}}, nil
		},
		Handler: func(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			var in curation.StewardGrouping
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, invalid()
			}
			origin := operations.Origin{Source: c.Source, ConversationID: c.ConversationID, Mode: c.Mode, PlanID: c.PlanID, TokenID: c.TokenID}
			result, err := w.SubmitStewardGrouping(ctx, in, c.User.ID, c.IdempotencyKey, c.RequestID, origin)
			return result.Response, toolError(err)
		},
	}
}
