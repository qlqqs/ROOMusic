package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func requireCurationToolCode(t *testing.T, err error, code tool.ErrorCode) {
	t.Helper()
	var failure *tool.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("工具错误=%v，期望 %s", err, code)
	}
}

func modelToolInput(t *testing.T, values map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(values)
	requireCurationOK(t, err)
	return raw
}

func TestPostgreSQLCurationToolsPending(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	ctx := t.Context()
	conversation, otherConversation := modelConversation(t, f), modelConversation(t, f)
	own := modelClaim(t, f, conversation, modelPending(t, f, f.a, "release_match", "own"))
	foreign := modelClaim(t, f, otherConversation, modelPending(t, f, f.b, "release_match", "foreign"))
	endedRelease, _ := insertGroupedRelease(t, f.application, f.rootID,
		groupedRelease{title: "已处理发行", artist: "合成艺人", tracks: []string{"present"}})
	ended := modelClaim(t, f, conversation, modelPending(t, f, endedRelease, "release_match", "ended"))
	requireCurationOK(t, f.writer.ShelvePendingItem(ctx, ended.ID, conversation, "已搁置合成项"))

	caller := tool.Caller{Source: tool.SourceCuration, ConversationID: conversation,
		User: identity.User{ID: f.admin.id, Role: "admin"}, IdempotencyKey: uuid.New()}
	wrongCaller := caller
	wrongCaller.User.ID = f.application.createUser(t, "admin").id
	var abandonedReason string
	runCtx := curation.WithModelRun(ctx, &curation.ModelRun{ConversationID: conversation, Model: "synthetic",
		Abandon: func(ctx context.Context, id, reason string) error {
			if id != own.ID {
				t.Fatalf("放弃工具传递了错误的待定项 %s", id)
			}
			abandonedReason = reason
			return f.writer.FinishModelItem(ctx, own, conversation, "unlisted")
		}})

	input := func(t *testing.T, toolName, itemID string) json.RawMessage {
		t.Helper()
		switch toolName {
		case "read_pending_items":
			return modelToolInput(t, map[string]any{"item_ids": []string{itemID}})
		case "resolve_release_match":
			return modelToolInput(t, map[string]any{"item_id": itemID, "external_id": uuid.New(), "basis": tool.Basis{Note: "合成比对依据"}})
		case "shelve_pending_item":
			return modelToolInput(t, map[string]any{"item_id": itemID, "reason": "候选无法区分"})
		default:
			t.Fatalf("未知工具 %q", toolName)
			return nil
		}
	}
	definitions := make(map[string]tool.Definition)
	for _, definition := range stewardtools.StewardTools(f.writer, nil) {
		definitions[definition.Name] = definition
	}
	for _, name := range []string{"read_pending_items", "resolve_release_match", "shelve_pending_item"} {
		definition, ok := definitions[name]
		if !ok {
			t.Fatalf("缺少工具 %s", name)
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				ctx    context.Context
				caller tool.Caller
				id     string
				code   tool.ErrorCode
			}{
				{"missing_model_run", ctx, caller, own.ID, tool.CodePermissionDenied},
				{"not_enabler", runCtx, wrongCaller, own.ID, tool.CodePermissionDenied},
				{"other_conversation", runCtx, caller, foreign.ID, "out_of_batch"},
				{"not_in_progress", runCtx, caller, ended.ID, "out_of_batch"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := input(t, name, tc.id)
					_, err := definition.Handler(tc.ctx, tc.caller, args)
					requireCurationToolCode(t, err, tc.code)
					if name == "resolve_release_match" {
						_, err = definition.Preview(tc.ctx, tc.caller, args)
						requireCurationToolCode(t, err, tc.code)
					}
				})
			}
		})
	}

	read := definitions["read_pending_items"]
	if !read.Untrusted {
		t.Fatal("待定项外部资料应标为不可信")
	}
	rows, err := read.Handler(runCtx, caller, json.RawMessage(`{}`))
	requireCurationOK(t, err)
	requireExternalJSON(t, rows, []map[string]any{{"item_id": own.ID, "kind": own.Kind, "release_id": own.TargetID,
		"conflicts": own.Conflicts, "evidence": own.Evidence, "candidates": own.Candidates}})
	shelve := definitions["shelve_pending_item"]
	if shelve.Scope != tool.ScopePersonal {
		t.Fatalf("搁置工具范围为 %s，期望 personal", shelve.Scope)
	}
	result, err := shelve.Handler(runCtx, caller, input(t, shelve.Name, own.ID))
	requireCurationOK(t, err)
	requireExternalJSON(t, result, map[string]string{"item_id": own.ID, "status": "finished_tier"})
	stored, err := f.writer.PendingItem(ctx, own.ID)
	requireCurationOK(t, err)
	if stored.Status != "resolved" || stored.Resolution == nil || *stored.Resolution != "unlisted" || abandonedReason != "候选无法区分" {
		t.Fatalf("放弃理由 %q，结清后的待定项 %+v", abandonedReason, stored)
	}
	rows, err = read.Handler(runCtx, caller, json.RawMessage(`{}`))
	requireCurationOK(t, err)
	requireExternalJSON(t, rows, []any{})
}

func TestPostgreSQLCurationToolsAutoCorrectRelease(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	ctx := t.Context()
	conversation := modelConversation(t, f)
	modelClaim(t, f, conversation, modelPending(t, f, f.a, "release_match", "correction"))
	_, revision := f.correct(t, f.a, 1, map[string]any{"label": setChange("人工厂牌")})
	batch := f.batch(t, "model")
	runCtx := curation.WithModelRun(ctx, &curation.ModelRun{ConversationID: conversation, Model: "ACME/Model: X",
		Batch: func(context.Context) (string, error) { return batch, nil }})
	caller := tool.Caller{Source: tool.SourceCuration, ConversationID: conversation, User: identity.User{ID: f.admin.id, Role: "admin"}}
	handler := stewardtools.AutoCorrectRelease(f.writer)
	basis := strings.Repeat("a", 497) + "证"
	for _, tc := range []struct {
		name, target, field string
		change              any
		code                tool.ErrorCode
	}{
		{"clear", f.a, "edition", clearChange, "invalid_correction"},
		{"out_of_batch", f.b, "edition", setChange("其他发行"), "out_of_batch"},
		{"manual_override", f.a, "label", setChange("模型厂牌"), "manual_override"},
		{"set", f.a, "edition", setChange("模型版本"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller.IdempotencyKey = uuid.New()
			args := modelToolInput(t, map[string]any{"release_id": tc.target, "expected_revision": revision,
				"changes": map[string]any{tc.field: tc.change}, "basis": tool.Basis{Note: basis + "据"}})
			out, err := handler(runCtx, caller, args)
			if tc.code != "" {
				requireCurationToolCode(t, err, tc.code)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE batch_id=$1`, batch)
				return
			}
			requireCurationOK(t, err)
			var result curation.Applied
			requireCurationOK(t, json.Unmarshal(out, &result))
			f.textOwner(t, "edition", "模型版本", result.OperationID)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1 AND batch_id=$2 AND actor_id=$3
				AND origin->>'conversation_id'=$4 AND origin->>'provider_kind'='model'
				AND origin->>'provider'='acme-model--x' AND origin->>'basis'=$5 AND octet_length(origin->>'basis')=500`,
				result.OperationID, batch, f.admin.id, conversation, basis)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM releases WHERE id=$1 AND edit_revision=$2`, f.a, revision)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_corrections WHERE release_id=$1 AND field='edition'`, f.a)
		})
	}
}
