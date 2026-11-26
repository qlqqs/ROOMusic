// Package stewardtools 提供后台整理会话专用的管家工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const idSchema = `{"type":"string","minLength":36,"maxLength":36}`
const noteSchema = `{"type":"string","minLength":1,"maxLength":500}`

type adapter struct {
	writer   *curation.Writer
	matcher  *program.ModelMatcher
	mockHost sources.MockHost
}

func StewardTools(w *curation.Writer, m *program.ModelMatcher, mockHost ...sources.MockHost) []tool.Definition {
	a := adapter{writer: w, matcher: m}
	if len(mockHost) > 0 {
		a.mockHost = mockHost[0]
	}
	available := func(ctx context.Context, c tool.Caller) bool { return c.Source == tool.SourceCuration }
	return []tool.Definition{
		a.patrolTool(available), a.localCleanupTool(available),
		a.backgroundMergeReleases(),
		a.verificationTool(available), a.identityTool(available), a.mediaTool(available),
		{Name: "read_pending_items", Description: "读取本次自动整理领取的待定项、冲突与候选资料", Scope: tool.ScopeRead, Untrusted: true, MinRole: tool.RoleAdmin, Available: available,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_ids":{"type":"array","items":` + idSchema + `}}}`), Output: json.RawMessage(`{"type":"array","items":{"type":"object"}}`), Handler: a.read},
		{Name: "resolve_release_match", Description: "按 MusicBrainz 编号核对待定项。同组多版结构吻合而无法区分时，提交任一吻合版本，后端只建立发行组级对应", Scope: tool.ScopeShared, Revertible: true, MinRole: tool.RoleAdmin, Available: available,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"external_id":` + idSchema + `,"basis":{"type":"object","additionalProperties":false,"properties":{"note":` + noteSchema + `},"required":["note"]}},"required":["item_id","external_id","basis"]}`), Output: json.RawMessage(`{"type":"object"}`), Handler: a.resolve, Preview: a.preview},
		{Name: "shelve_pending_item", Description: "放弃当前档并记录原因，后端按路线升级或结束当前项", Scope: tool.ScopePersonal, MinRole: tool.RoleAdmin, Available: available,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"reason":` + noteSchema + `,"fact":{"type":"object","additionalProperties":false,"properties":{"field":{"type":"string","enum":["year","label","catalog_number","barcode"]},"value":{"type":"string","minLength":1,"maxLength":4096}},"required":["field","value"]}},"required":["item_id","reason"]}`), Output: json.RawMessage(`{"type":"object"}`), Handler: a.shelve},
	}
}

func toolError(err error) error {
	var f *operations.Failure
	if errors.As(err, &f) {
		return &tool.Error{Code: tool.ErrorCode(f.Code), Message: f.Message}
	}
	if errors.Is(err, operations.ErrUnavailable) {
		return &tool.Error{Code: "database_unavailable", Message: "数据库暂不可用"}
	}
	return err
}
func invalid() error { return &tool.Error{Code: tool.CodeInvalidInput, Message: "输入参数无效"} }
func outOfBatch() error {
	return &tool.Error{Code: "out_of_batch", Message: "待定项不属于本次运行"}
}

func (a adapter) claimed(ctx context.Context, c tool.Caller) (*curation.ModelRun, []curation.PendingItem, error) {
	run, ok := curation.ModelRunFrom(ctx)
	if !ok || c.Source != tool.SourceCuration || (run.ActorID != "" && run.ActorID != c.User.ID) || !uuid.Valid(run.ConversationID) || (c.ConversationID != "" && c.ConversationID != run.ConversationID) {
		return nil, nil, &tool.Error{Code: tool.CodePermissionDenied, Message: "需要后台整理会话"}
	}
	settings, err := a.writer.Settings(ctx)
	if err != nil {
		return nil, nil, err
	}
	if artistActor := curation.ArtistWebActor(ctx); artistActor != c.User.ID && (!settings.Active || settings.EnabledBy.ID != c.User.ID) {
		return nil, nil, &tool.Error{Code: tool.CodePermissionDenied, Message: "当前操作者已不是自动整理开启者"}
	}
	items, err := a.writer.ClaimedItems(ctx, run.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	// 同一模型回答可依次提交多位同名候选，第一条建议已经结清本项。
	if len(items) == 0 && curation.ArtistWebActor(ctx) != "" && run.PendingItemID != "" {
		p, err := a.writer.PendingItem(ctx, run.PendingItemID)
		if err != nil {
			return nil, nil, err
		}
		if p.ArtistWebGeneration != nil && p.ConversationID == run.ConversationID && curation.SuggestionRecorded(p) {
			items = []curation.PendingItem{p}
		}
	}
	items = slices.DeleteFunc(items, func(p curation.PendingItem) bool {
		if run.PendingItemID != "" && p.ID != run.PendingItemID {
			return true
		}
		return run.ClaimDigests != nil && run.ClaimDigests[p.ID] != p.ModelClaimDigest()
	})
	if len(items) == 0 && run.ClaimDigests != nil {
		return nil, nil, toolError(operations.Conflict("item_stale", "领取时的证据已变化或已结清"))
	}
	return run, items, err
}
func selectItem(items []curation.PendingItem, id string, byRelease bool) (curation.PendingItem, error) {
	if !uuid.Valid(id) {
		return curation.PendingItem{}, invalid()
	}
	for _, p := range items {
		key := p.ID
		if byRelease {
			key = p.TargetID
		}
		if strings.EqualFold(id, key) {
			return p, nil
		}
	}
	return curation.PendingItem{}, outOfBatch()
}
func bindItem(ctx context.Context, run *curation.ModelRun, c tool.Caller, item curation.PendingItem) context.Context {
	bound := *run
	bound.ActorID, bound.PendingItemID = c.User.ID, item.ID
	return curation.WithModelRun(ctx, &bound)
}
func (a adapter) read(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		IDs []string `json:"item_ids"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	_, items, err := a.claimed(ctx, c)
	if err != nil {
		return nil, err
	}
	if in.IDs != nil {
		selected := make([]curation.PendingItem, 0, len(in.IDs))
		for _, id := range in.IDs {
			item, err := selectItem(items, id, false)
			if err != nil {
				return nil, err
			}
			selected = append(selected, item)
		}
		items = selected
	}
	if a.matcher != nil {
		if err := a.matcher.AnnotatePendingEvidence(ctx, items); err != nil {
			return nil, toolError(err)
		}
	}
	rows := make([]map[string]any, 0, len(items))
	for _, p := range items {
		if p.Kind == "artist_identity" {
			rows = append(rows, map[string]any{"item_id": p.ID, "kind": p.Kind, "artist_id": p.ArtistID, "evidence": p.Evidence})
			continue
		}
		rows = append(rows, map[string]any{"item_id": p.ID, "kind": p.Kind, "release_id": p.TargetID, "conflicts": p.Conflicts, "evidence": p.Evidence, "candidates": p.Candidates})
	}
	return json.Marshal(rows)
}

type matchInput struct {
	ItemID     string     `json:"item_id"`
	ExternalID string     `json:"external_id"`
	Basis      tool.Basis `json:"basis"`
}

func (a adapter) plan(ctx context.Context, c tool.Caller, in matchInput) (program.MatchPlan, *curation.ModelRun, curation.PendingItem, error) {
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return program.MatchPlan{}, nil, curation.PendingItem{}, err
	}
	item, err := selectItem(items, in.ItemID, false)
	if err != nil {
		return program.MatchPlan{}, nil, item, err
	}
	if item.TargetType != "release" {
		return program.MatchPlan{}, nil, item, invalid()
	}
	plan, err := a.matcher.Plan(ctx, item, in.ExternalID)
	return plan, run, item, toolError(err)
}
func (a adapter) preview(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
	var in matchInput
	if json.Unmarshal(raw, &in) != nil {
		return tool.Preview{}, invalid()
	}
	plan, _, _, err := a.plan(ctx, c, in)
	if err != nil {
		return tool.Preview{}, err
	}
	facts, err := json.Marshal(plan.Verification)
	if err != nil {
		return tool.Preview{}, err
	}
	p := tool.Preview{Target: plan.Target, Summary: "建立 MusicBrainz 对应", Verification: facts, BackgroundDirect: plan.Verification.Direct, Contradicted: plan.Verification.Contradicted}
	for _, v := range plan.Changes {
		p.Changes = append(p.Changes, tool.Change{Field: v.Field, Before: v.Before, After: v.After})
	}
	return p, nil
}
func modelSource(run *curation.ModelRun, note string) curation.Source {
	end := min(len(note), 500)
	for end < len(note) && !utf8.RuneStart(note[end]) {
		end--
	}
	return curation.Source{Kind: "model", Provider: curation.NormalizeProvider(run.Model), Basis: note[:end]}
}
func (a adapter) resolve(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in matchInput
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	plan, run, item, err := a.plan(ctx, c, in)
	if err != nil {
		return nil, err
	}
	if !plan.Verification.Direct && (c.ReviewID == "" || c.ReviewDecision != "approved") {
		return nil, &tool.Error{Code: tool.CodeApprovalRequired, Message: "此编号需要独立审查"}
	}
	ctx = bindItem(ctx, run, c, item)
	batch, err := run.Batch(ctx)
	if err != nil {
		return nil, toolError(err)
	}
	result, err := a.matcher.Apply(ctx, plan, program.ApplyOptions{Source: modelSource(run, in.Basis.Note), Key: c.IdempotencyKey, Batch: batch, PendingItemID: item.ID, ApprovedReviewID: c.ReviewID})
	if err != nil {
		return nil, toolError(err)
	}
	return json.Marshal(result)
}
func (a adapter) shelve(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		ID     string                      `json:"item_id"`
		Reason string                      `json:"reason"`
		Fact   *curation.FactualSuggestion `json:"fact,omitempty"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return nil, err
	}
	item, err := selectItem(items, in.ID, false)
	if err != nil {
		return nil, err
	}
	ctx = bindItem(ctx, run, c, item)
	if run.Abandon == nil {
		return nil, invalid()
	}
	if in.Fact != nil {
		if (run.TaskKind != "lookup" && run.TaskKind != "web") || (item.Kind != "release_match" && item.Kind != "recording_match") {
			return nil, invalid()
		}
		if err := a.writer.StageFactualSuggestion(ctx, item, *in.Fact, run.Model, in.Reason); err != nil {
			return nil, toolError(err)
		}
	}
	if err := run.Abandon(ctx, item.ID, in.Reason); err != nil {
		return nil, toolError(err)
	}
	return json.Marshal(map[string]string{"item_id": item.ID, "status": "finished_tier"})
}
