package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// RecordedOperation 是受治理写入提供的只读结果；不存在时 Output 为 nil。
type RecordedOperation struct {
	ID     string
	Output json.RawMessage
}

// OperationLookup 在调用方的消息追加事务中按原幂等键查询，不得执行写操作。
type OperationLookup func(context.Context, *sql.Tx, string, string) (RecordedOperation, error)

// WithOperationLookup 由装配层接入操作记录查询，运行时不访问其他能力的私有表。
func (s *Store) WithOperationLookup(lookup OperationLookup) *Store {
	s.operationLookup = lookup
	return s
}

// resolveDecidedPlans 在新消息之前收尾已批准但未完成的项，只对账，不恢复执行。
func (s *Store) resolveDecidedPlans(ctx context.Context, tx *sql.Tx, userID, conversationID string) error {
	plans, err := readPlans(ctx, tx, userID, conversationID, false)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.Status != "applied" && plan.Status != "declined" {
			continue
		}
		for _, item := range plan.Items {
			if item.Result != nil || item.Decision == "pending" {
				continue
			}
			code := "declined_by_user"
			output := tool.Result{Output: errorOutput(code, "用户未选择这项修改")}
			if item.Decision == "accepted" {
				if s.operationLookup == nil {
					return errors.New("管家未装配操作结果查询")
				}
				recorded, err := s.operationLookup(ctx, tx, userID, item.IdempotencyKey)
				if err != nil {
					return err
				}
				code = "not_executed"
				output.Output = errorOutput(code, "用户已发送新消息，工具未执行")
				if recorded.Output != nil {
					code = ""
					output = tool.BoundResult(recorded.Output, item.Untrusted)
					item.OperationID = &recorded.ID
				}
			}
			result := ToolCallResult{Output: output.Output, Truncated: output.Truncated, Status: "succeeded"}
			if code != "" {
				result.Status, result.ErrorCode = "failed", &code
			}
			message, err := planResultMessage(item, output, code)
			if err != nil {
				return err
			}
			if err := finishPlanItem(ctx, tx, userID, conversationID, item, result, message); err != nil {
				return err
			}
		}
	}
	return nil
}
