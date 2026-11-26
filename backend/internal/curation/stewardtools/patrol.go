package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const localTextSchema = `{"type":"string","minLength":1,"maxLength":4096}`

func (a adapter) localCleanupTool(allowed availability) tool.Definition {
	return tool.Definition{
		Name: "submit_local_cleanup", Description: "提交从本目录文字或字段的候选解码中提取的专辑标题、艺人或曲名，仅填写当前待整理字段。",
		Scope: tool.ScopeShared, Revertible: true, MinRole: tool.RoleAdmin, Available: allowed,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["item_id"],"properties":{
			"item_id":` + idSchema + `,"title":` + localTextSchema + `,"artist":` + localTextSchema + `,
			"tracks":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["track_id","title"],"properties":{
				"track_id":` + idSchema + `,"title":` + localTextSchema + `}}},
			"track_artists":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["track_id","artist"],"properties":{
				"track_id":` + idSchema + `,"artist":` + localTextSchema + `}}}}}`),
		Output:  json.RawMessage(`{"type":"object"}`),
		Preview: a.submissionPreview("local_cleanup", "按本地文字整理"),
		Handler: a.submitLocalCleanup,
	}
}

// 两类提交均由后端核对批次，实际写入再校验当前数据和字段来源。
func (a adapter) submissionPreview(kind, summary string) func(context.Context, tool.Caller, json.RawMessage) (tool.Preview, error) {
	return func(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
		var in struct {
			ItemID string `json:"item_id"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return tool.Preview{}, invalid()
		}
		_, item, err := a.localItem(ctx, c, in.ItemID, kind)
		if err != nil {
			return tool.Preview{}, err
		}
		return tool.Preview{Target: item.TargetID, Summary: summary, BackgroundDirect: true}, nil
	}
}

func (a adapter) localItem(ctx context.Context, c tool.Caller, id, kind string) (*curation.ModelRun, curation.PendingItem, error) {
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return nil, curation.PendingItem{}, err
	}
	item, err := selectItem(items, id, false)
	if err != nil {
		return nil, item, err
	}
	if run.TaskKind != "local" || item.Kind != kind {
		return nil, item, outOfBatch()
	}
	return run, item, nil
}

func (a adapter) submitLocalCleanup(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in curation.LocalInput
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	run, item, err := a.localItem(ctx, c, in.ItemID, "local_cleanup")
	if err != nil {
		return nil, err
	}
	if err = a.writer.SubmitLocal(bindItem(ctx, run, c, item), in); err != nil {
		return nil, toolError(err)
	}
	return json.RawMessage(`{"status":"handled"}`), nil
}
