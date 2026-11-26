package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const patrolTextSchema = `{"type":"string","minLength":1,"maxLength":4096}`

func (a adapter) patrolTool(allowed availability) tool.Definition {
	return tool.Definition{
		Name: "submit_patrol", Description: "提交本块艺人规范身份和名录问题。专辑批的 results 为空；没有问题时 issues 为空。",
		Scope: tool.ScopeShared, MinRole: tool.RoleAdmin, Available: allowed,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["item_id","results","issues"],"properties":{
			"item_id":` + idSchema + `,
			"results":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","canonical","confidence"],"properties":{
				"id":{"type":"string"},"canonical":` + patrolTextSchema + `,"confidence":{"type":"string","enum":["high","low"]}}}},
			"issues":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","kind","suggested"],"properties":{
				"id":{"type":"string"},"kind":{"type":"string","enum":["typo","album_variant","artist_alias"]},"suggested":` + patrolTextSchema + `}}}}}`),
		Output: json.RawMessage(`{"type":"object"}`), Preview: a.submissionPreview("name_patrol", "提交名录巡检结果"), Handler: a.submitPatrol,
	}
}

func (a adapter) localCleanupTool(allowed availability) tool.Definition {
	return tool.Definition{
		Name: "submit_local_cleanup", Description: "提交本目录可核对的文字、编码、署名拆分和归组，无法判断时说明原因。",
		Scope: tool.ScopeShared, Revertible: true, MinRole: tool.RoleAdmin, Available: allowed,
		Input: json.RawMessage(`{"type":"object",
"properties":{"item_id":{"type":"string"},"decision":{"type":"string","enum":["apply","undetermined"]},"reason":{"type":"string","minLength":1,"maxLength":4096},"texts":{"type":"array","items":{"type":"object",
"properties":{"target_id":{"type":"string"},"field":{"type":"string"},"value":{"type":"string","minLength":1,"maxLength":4096}},"required":["target_id","field","value"],"additionalProperties":false}},"encodings":{"type":"array","items":{"type":"object",
"properties":{"release_id":{"type":"string"},"encoding":{"type":"string"}},"required":["release_id","encoding"],"additionalProperties":false}},"splits":{"type":"array","items":{"type":"object",
"properties":{"target_id":{"type":"string"},"role":{"type":"string"},"source_ref":{"type":"string"},"parts":{"type":"array","items":{"type":"string","minLength":1,"maxLength":4096}},"joins":{"type":"array","items":{"type":"string"}}},"required":["target_id","role","source_ref","parts","joins"],"additionalProperties":false}},"grouping":{"type":"object",
"properties":{"albums":{"type":"array","items":{"type":"object",
"properties":{"files":{"type":"array","items":{"type":"object",
"properties":{"file_id":{"type":"string"},"disc":{"type":"integer","minimum":1},"track":{"type":"integer","minimum":1},"tracks":{"type":"array","items":{"type":"object",
"properties":{"track_id":{"type":"string"},"disc":{"type":"integer","minimum":1},"track":{"type":"integer","minimum":1}},"required":["track_id","disc","track"],"additionalProperties":false}}},"required":["file_id"],"additionalProperties":false}}},"required":["files"],"additionalProperties":false}}},"required":["albums"],"additionalProperties":false}},"required":["item_id","decision"],"additionalProperties":false}`),
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
		_, item, err := a.patrolItem(ctx, c, in.ItemID, kind)
		if err != nil {
			return tool.Preview{}, err
		}
		return tool.Preview{Target: item.TargetID, Summary: summary, BackgroundDirect: true}, nil
	}
}

func (a adapter) patrolItem(ctx context.Context, c tool.Caller, id, kind string) (*curation.ModelRun, curation.PendingItem, error) {
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return nil, curation.PendingItem{}, err
	}
	item, err := selectItem(items, id, false)
	if err != nil {
		return nil, item, err
	}
	if run.TaskKind != "patrol_local" || item.Kind != kind {
		return nil, item, outOfBatch()
	}
	return run, item, nil
}

func (a adapter) submitPatrol(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in curation.PatrolSubmission
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	run, item, err := a.patrolItem(ctx, c, in.ItemID, "name_patrol")
	if err != nil {
		return nil, err
	}
	err = a.writer.SubmitPatrol(bindItem(ctx, run, c, item), item, in)
	if err != nil {
		return nil, toolError(err)
	}
	return json.RawMessage(`{"status":"handled"}`), nil
}

func (a adapter) submitLocalCleanup(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	var in curation.LocalInput
	if json.Unmarshal(raw, &in) != nil {
		return nil, invalid()
	}
	run, item, err := a.patrolItem(ctx, c, in.ItemID, "local_cleanup")
	if err != nil {
		return nil, err
	}
	if err = a.writer.SubmitLocal(bindItem(ctx, run, c, item), in); err != nil {
		return nil, toolError(err)
	}
	return json.RawMessage(`{"status":"handled"}`), nil
}
