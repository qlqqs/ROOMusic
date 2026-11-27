package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func MatchReleaseExternal(m *program.ModelMatcher) tool.Definition {
	return tool.Definition{
		Name: "match_release_external", Description: "核对本地发行和 MusicBrainz 编号并建立可撤销的对应。专辑使用 release，单轨散曲使用 recording。部分对应或无法区分版本时只认到发行组。",
		MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: true, Untrusted: true,
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"release_id":` + idSchema + `,"mbid":` + idSchema + `,"entity":{"type":"string","enum":["release","recording"]},"basis":{"type":"object","additionalProperties":false,"required":["source","note"],"properties":{"source":{"type":"string","enum":["local","external","mixed"]},"note":{"type":"string","maxLength":4096}}}},"required":["release_id","entity","mbid"]}`),
		Output: json.RawMessage(`{"type":"object"}`),
		Preview: func(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
			var in program.ExternalMatchInput
			if err := json.Unmarshal(raw, &in); err != nil {
				return tool.Preview{}, invalid()
			}
			p, err := m.PreviewExternal(ctx, in, c.User.ID)
			if err != nil {
				return tool.Preview{}, toolError(err)
			}
			level := map[string]string{"release": "版本", "release_group": "发行组", "recording": "录音"}[p.Level]
			out := tool.Preview{Target: p.Target, Summary: "将《" + p.Target + "》对应到 MusicBrainz " + level + "《" + p.Edition.Title + "》", Changes: []tool.Change{
				{Field: "musicbrainz", After: p.Edition}, {Field: "matched_mbid", After: p.MatchedMBID}, {Field: "level", After: p.Level},
			}}
			for _, c := range p.Changes {
				out.Changes = append(out.Changes, tool.Change{Field: c.Field, Before: c.Before, After: c.After})
			}
			return out, nil
		},
		Handler: func(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			var in program.ExternalMatchInput
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, invalid()
			}
			origin := operations.Origin{Source: c.Source, ConversationID: c.ConversationID, PlanID: c.PlanID, Mode: c.Mode, TokenID: c.TokenID}
			out, err := m.SelectExternal(ctx, in, c.User.ID, c.IdempotencyKey, origin, c.RequestID)
			if err != nil {
				return nil, toolError(err)
			}
			return json.Marshal(out)
		},
	}
}
