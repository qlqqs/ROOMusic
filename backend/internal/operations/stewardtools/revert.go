package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// RevertTools 使用已登记类型的撤销执行器，幂等键由入口保存并传入。
func RevertTools(db *sql.DB, kinds ...operations.Kind) []tool.Definition {
	registered := map[string]operations.Kind{}
	for _, kind := range kinds {
		registered[kind.Type()] = kind
	}
	load := func(ctx context.Context, raw json.RawMessage) (operations.Record, operations.Kind, error) {
		var p struct {
			OperationID string `json:"operation_id"`
		}
		if json.Unmarshal(raw, &p) != nil || !uuid.Valid(p.OperationID) {
			return operations.Record{}, nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "操作标识必须是 UUID"}
		}
		record, err := operations.Load(ctx, db, strings.ToLower(p.OperationID))
		if errors.Is(err, operations.ErrNotFound) {
			return record, nil, &tool.Error{Code: tool.CodeTargetNotFound, Message: "操作不存在"}
		}
		if err != nil {
			return record, nil, err
		}
		kind, ok := registered[record.Type]
		if !ok {
			return record, nil, &tool.Error{Code: "not_revertible", Message: "这类操作不支持撤销"}
		}
		return record, kind, nil
	}
	return []tool.Definition{{Name: "revert_operation", Description: "撤销一条可撤销的曲库操作。operation_id 来自操作结果或 list_operations；撤销本身不能再撤销。", MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["operation_id"],"properties":{"operation_id":{"type":"string","minLength":36,"maxLength":36},"basis":{"type":"object","additionalProperties":false,"required":["source","note"],"properties":{"source":{"type":"string","enum":["local","external","mixed"]},"note":{"type":"string","maxLength":4096}}}}}`), Output: json.RawMessage(`{"type":"object"}`),
		Preview: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (tool.Preview, error) {
			record, kind, err := load(ctx, raw)
			if err != nil {
				return tool.Preview{}, err
			}
			original := record
			record.Before, record.After = record.After, record.Before
			descriptions, err := kind.Describe(ctx, db, []operations.Record{record})
			if err != nil {
				return tool.Preview{}, err
			}
			d := descriptions[record.ID]
			p := tool.Preview{Summary: "撤销操作", Changes: []tool.Change{}}
			if d.Release != nil {
				p.Target = d.Release.Title
			}
			if d.Album != nil {
				p.Target = d.Album.Title
			}
			if d.Artist != nil {
				p.Target = d.Artist.Name
			}
			if previewer, ok := kind.(operations.RevertPreviewer); ok {
				changes, err := previewer.PreviewRevert(ctx, db, original)
				if err != nil {
					return p, err
				}
				for _, change := range changes {
					p.Changes = append(p.Changes, tool.Change{Field: change.Field, Before: change.Before, After: change.After})
				}
				return p, nil
			}
			encoded, err := json.Marshal(d.Changes)
			if err != nil {
				return p, err
			}
			var changes map[string]any
			_ = json.Unmarshal(encoded, &changes)
			if fields, ok := changes["fields"].([]any); ok {
				for _, value := range fields {
					field, ok := value.(map[string]any)
					if !ok {
						continue
					}
					name, _ := field["field"].(string)
					p.Changes = append(p.Changes, tool.Change{Field: name, Before: field["before"], After: field["after"]})
				}
			} else {
				p.Changes = append(p.Changes, tool.Change{Field: record.Type, Before: changes["from"], After: changes["to"]})
			}
			return p, nil
		}, Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			record, kind, err := load(ctx, raw)
			if err != nil {
				return nil, err
			}
			result, err := operations.Run(ctx, db, operations.Spec{ActorID: caller.User.ID, Type: operations.TypeRevert, Key: caller.IdempotencyKey, RequestID: caller.RequestID,
				Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": record.ID}), Origin: &operations.Origin{Source: caller.Source, ConversationID: caller.ConversationID, PlanID: caller.PlanID, Mode: caller.Mode, TokenID: caller.TokenID}}, operations.RevertExecutor(kind, record.ID))
			if err != nil {
				_, code, message := operations.Problem(err)
				return nil, &tool.Error{Code: tool.ErrorCode(code), Message: message}
			}
			return result.Response, nil
		}}}
}
