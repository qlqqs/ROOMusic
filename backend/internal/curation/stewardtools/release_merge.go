package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// 后台并入的目标与来源始终来自后台已领取的待定项。
func (a adapter) backgroundMergeReleases() tool.Definition {
	return tool.Definition{Name: "merge_releases", Description: "将待定项的候选发行整张并入目标发行", Scope: tool.ScopeShared, Revertible: true, MinRole: tool.RoleAdmin,
		Available: func(_ context.Context, c tool.Caller) bool { return c.Source == tool.SourceCuration },
		Input:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"basis":{"type":"object","additionalProperties":false,"properties":{"note":` + noteSchema + `},"required":["note"]}},"required":["item_id","basis"]}`),
		Output:    json.RawMessage(`{"type":"object"}`), Preview: a.previewMerge, Handler: a.mergeReleases}
}

func (a adapter) mergeItem(ctx context.Context, c tool.Caller, raw json.RawMessage) (matchInput, *curation.ModelRun, curation.PendingItem, error) {
	var in matchInput
	if json.Unmarshal(raw, &in) != nil {
		return in, nil, curation.PendingItem{}, invalid()
	}
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return in, nil, curation.PendingItem{}, err
	}
	item, err := selectItem(items, in.ItemID, false)
	return in, run, item, err
}
func (a adapter) previewMerge(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
	_, _, item, err := a.mergeItem(ctx, c, raw)
	if err != nil {
		return tool.Preview{}, err
	}
	plan, err := a.writer.PreviewReleaseMerge(ctx, item)
	if err != nil {
		return tool.Preview{}, toolError(err)
	}
	p := tool.Preview{BackgroundDirect: true, Target: plan.Target.Title, Summary: "整张并入来源发行"}
	for _, change := range program.ReleaseMergeChanges(plan.Source, plan.Target, plan.Token, plan.Hidden, plan.FavoritesMoved) {
		p.Changes = append(p.Changes, tool.Change{Field: change.Field, Before: change.Before, After: change.After})
	}
	return p, nil
}

func (a adapter) mergeReleases(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	in, run, item, err := a.mergeItem(ctx, c, raw)
	if err != nil {
		return nil, err
	}
	preview, err := a.writer.PreviewReleaseMerge(ctx, item)
	if err != nil {
		return nil, toolError(err)
	}
	ctx = bindItem(ctx, run, c, item)
	batch, err := run.Batch(ctx)
	if err != nil {
		return nil, toolError(err)
	}
	result, err := a.writer.MergeReleases(ctx, curation.ReleaseAutoMerge{ItemID: item.ID, Token: preview.Token, Source: modelSource(run, in.Basis.Note), Key: c.IdempotencyKey, BatchID: batch})
	if err != nil {
		return nil, toolError(err)
	}
	return json.Marshal(result)
}
