// Package stewardtools 将治理执行器适配成管家工具，审批由调用入口负责。
package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const idSchema = `{"type":"string","minLength":36,"maxLength":36}`
const revisionSchema = `{"type":"integer","minimum":1,"maximum":9007199254740990}`
const basisSchema = `{"type":"object","additionalProperties":false,"required":["source","note"],"properties":{"source":{"type":"string","enum":["local","external","mixed"]},"note":{"type":"string","maxLength":4096}}}`
const scopeProperties = `"mode":{"type":"string","enum":["aliases","albums"]},"name_key_ids":{"type":"array","items":` + idSchema + `},"group_ids":{"type":"array","items":` + idSchema + `}`

type adapter struct {
	db      *sql.DB
	reads   *governance.Store
	catalog *catalog.Store
	artists *governance.ArtistGovernance
}

type command struct {
	kind, fingerprint string
	execute           operations.Executor
}

type input struct {
	TrackID          string                                    `json:"track_id"`
	ReleaseID        string                                    `json:"release_id"`
	GroupID          string                                    `json:"group_id"`
	ArtistID         string                                    `json:"artist_id"`
	ExpectedRevision int64                                     `json:"expected_revision"`
	Changes          map[string]governance.RawCorrectionChange `json:"changes"`
	Action           string                                    `json:"action"`
	Issue            string                                    `json:"issue"`
	BasisToken       string                                    `json:"basis_token"`
	SourceReleaseID  string                                    `json:"source_release_id"`
	SourceRevision   int64                                     `json:"source_revision"`
	Preview          string                                    `json:"preview"`
}

// StewardTools 复用 HTTP 的请求语义及执行器；路径标识放入工具参数。
func StewardTools(db *sql.DB, resolution governance.ArtistResolutionRefresher) []tool.Definition {
	a := adapter{db, governance.NewStore(db), catalog.NewStore(db), governance.NewArtistGovernance(resolution)}
	release := `"release_id":` + idSchema + `,"expected_revision":` + revisionSchema
	artist := `"artist_id":` + idSchema + `,"expected_revision":` + revisionSchema
	changes := map[string]any{}
	for _, name := range []string{"title", "album_artist", "year", "genre", "label", "catalog_number", "edition", "barcode"} {
		changes[name] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"mode"}, "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"set", "null", "clear"}}, "value": map[string]any{}}}
	}
	changeSchema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": changes})
	const trackChange = `{"type":"object","additionalProperties":false,"required":["mode"],"properties":{"mode":{"type":"string","enum":["set","clear"]},"value":{"type":"string"}}}`
	trackChanges := `,"changes":{"type":"object","additionalProperties":false,"properties":{"title":` + trackChange + `,"artist":` + trackChange + `}}`
	definitions := []tool.Definition{
		a.write("correct_track", "修正曲目标题或艺人。set 需提供 value，clear 清除人工修正。expected_revision 来自 get_track。", release+`,"track_id":`+idSchema+trackChanges, `"release_id","track_id","expected_revision","changes"`, true),
		a.write("correct_release", "修正专辑字段。changes 中每个字段使用 set/value、null 或 clear；revision 来自 get_release。", release+`,"changes":`+string(changeSchema), `"release_id","expected_revision","changes"`, true),
		a.write("change_release_group", "调整版本归组。action=join 时提供 group_id；detach 时不提供 group_id。", release+`,"action":{"type":"string","enum":["join","detach"]},"group_id":`+idSchema, `"release_id","expected_revision","action"`, true),
		a.write("set_default_release", "设置专辑默认版本；release_id 为 null 时恢复自动选择，expected_revision 是组版本号。", `"group_id":`+idSchema+`,"expected_revision":`+revisionSchema+`,"release_id":{}`, `"group_id","expected_revision","release_id"`, true),
		a.write("review_release_issue", "核对或取消核对问题。review 必须提供 get_release_issues 返回的 basis 令牌到 basis_token；unreview 不提供令牌。", release+`,"issue":{"type":"string"},"action":{"type":"string","enum":["review","unreview"]},"basis_token":{"type":"string"}`, `"release_id","expected_revision","issue","action"`, true),
		a.write("claim_release", "把旧版本人工修改认领到当前版本。先用 preview_release_claim 取得 preview 令牌和双方 revision。", release+`,"source_release_id":`+idSchema+`,"source_revision":`+revisionSchema+`,"preview":{"type":"string","minLength":1}`, `"release_id","expected_revision","source_release_id","source_revision","preview"`, true),
		a.write("merge_artists", "把 source_artist_id 并入 artist_id，提供双方版本号。不可撤销。", artist+`,"source_artist_id":`+idSchema+`,"source_expected_revision":`+revisionSchema, `"artist_id","expected_revision","source_artist_id","source_expected_revision"`, false),
		a.write("split_artist", "按别名或专辑拆分艺人；先预览，把返回的 basis 令牌放入 basis_token。target=new 新建或 restore 恢复。不可撤销。", artist+`,`+scopeProperties+`,"basis_token":{"type":"string","minLength":64,"maxLength":64},"target":{"type":"object","additionalProperties":false,"required":["kind"],"properties":{"kind":{"type":"string","enum":["new","restore"]},"display_name":{"type":"string"},"artist_id":`+idSchema+`,"expected_revision":`+revisionSchema+`}}`, `"artist_id","expected_revision","mode","name_key_ids","basis_token","target"`, false),
		a.write("rename_artist", "修改艺人主名；mode=set 时提供 name，auto 时省略 name。不可撤销。", artist+`,"mode":{"type":"string","enum":["set","auto"]},"name":{"type":"string"}`, `"artist_id","expected_revision","mode"`, false),
		a.write("ignore_artist_candidate", "忽略或恢复艺人候选提示，提供双方版本号和明确的名字键对。不可撤销。", artist+`,"other_artist_id":`+idSchema+`,"other_expected_revision":`+revisionSchema+`,"ignored":{"type":"boolean"},"key_pairs":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["left_key_id","right_key_id"],"properties":{"left_key_id":`+idSchema+`,"right_key_id":`+idSchema+`}}}`, `"artist_id","expected_revision","other_artist_id","other_expected_revision","ignored","key_pairs"`, false),
	}
	definitions = append(definitions,
		a.read("preview_release_claim", "只读预览旧版本认领到新版本，返回迁移条目、preview 令牌和双方 revision。", `"release_id":`+idSchema+`,"source_release_id":`+idSchema, `"release_id","source_release_id"`),
		a.read("preview_artist_split", "只读预览艺人拆分范围，返回 basis 令牌；写工具使用 basis_token 传回。", `"artist_id":`+idSchema+`,`+scopeProperties, `"artist_id","mode","name_key_ids"`))
	return definitions
}

func schema(properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`)
}
func (a adapter) write(name, description, properties, required string, reversible bool) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: reversible, Untrusted: true,
		Input: schema(properties+`,"basis":`+basisSchema, required), Output: json.RawMessage(`{"type":"object"}`),
		Preview: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (tool.Preview, error) {
			if _, err := a.command(name, raw); err != nil {
				return tool.Preview{}, failure(err)
			}
			p, err := a.preview(ctx, name, raw)
			return p, failure(err)
		},
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			c, err := a.command(name, raw)
			if err != nil {
				return nil, failure(err)
			}
			result, err := operations.Run(ctx, a.db, operations.Spec{ActorID: caller.User.ID, Type: c.kind, Key: caller.IdempotencyKey, Fingerprint: c.fingerprint, RequestID: caller.RequestID,
				Origin: &operations.Origin{Source: caller.Source, ConversationID: caller.ConversationID, PlanID: caller.PlanID, Mode: caller.Mode, TokenID: caller.TokenID}}, c.execute)
			if err != nil {
				return nil, failure(err)
			}
			return result.Response, nil
		}}
}
func (a adapter) read(name, description, properties, required string) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleAdmin, Scope: tool.ScopeRead, Untrusted: true, Input: schema(properties, required), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
		var p input
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		var result any
		var err error
		if name == "preview_release_claim" {
			if !uuid.Valid(p.ReleaseID) || !uuid.Valid(p.SourceReleaseID) || strings.EqualFold(p.ReleaseID, p.SourceReleaseID) {
				return nil, invalid()
			}
			result, err = a.reads.PreviewClaim(ctx, strings.ToLower(p.SourceReleaseID), strings.ToLower(p.ReleaseID))
		} else {
			if !uuid.Valid(p.ArtistID) {
				return nil, invalid()
			}
			var scope governance.ArtistSplitScope
			_ = json.Unmarshal(raw, &scope)
			if err = scope.Normalize(); err == nil {
				result, err = governance.PreviewArtistSplit(ctx, a.db, strings.ToLower(p.ArtistID), scope)
			}
		}
		if err != nil {
			return nil, failure(err)
		}
		return json.Marshal(result)
	}}
}
func invalid() error { return &tool.Error{Code: tool.CodeInvalidInput, Message: "工具输入无效"} }
func failure(err error) error {
	if err == nil {
		return nil
	}
	var t *tool.Error
	if errors.As(err, &t) {
		return err
	}
	if errors.Is(err, catalog.ErrReleaseNotFound) || errors.Is(err, catalog.ErrReleaseGroupNotFound) || errors.Is(err, catalog.ErrArtistNotFound) {
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
	}
	if errors.Is(err, governance.ErrArtistPreviewPreparing) || errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		return &tool.Error{Code: "artist_catalog_preparing", Message: "艺人资料正在整理"}
	}
	_, code, message := operations.Problem(err)
	return &tool.Error{Code: tool.ErrorCode(code), Message: message}
}
