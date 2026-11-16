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

// OperationResultReader 按原幂等键只读对账，不会执行或重试操作。
type OperationResultReader interface {
	LookupOperationResult(context.Context, string, string) (RecordedOperation, error)
}

func (s *Store) LookupOperationResult(ctx context.Context, userID, key string) (RecordedOperation, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RecordedOperation{}, err
	}
	defer tx.Rollback()
	result, err := s.lookupOperationResult(ctx, tx, userID, key)
	if err != nil {
		return RecordedOperation{}, err
	}
	return result, tx.Commit()
}

func (s *Store) lookupOperationResult(ctx context.Context, tx *sql.Tx, userID, key string) (RecordedOperation, error) {
	if s.operationLookup == nil {
		return RecordedOperation{}, errors.New("管家未装配操作结果查询")
	}
	return s.operationLookup(ctx, tx, userID, key)
}

// WithOperationLookup 由装配层接入操作记录查询，运行时不访问其他能力的私有表。
func (s *Store) WithOperationLookup(lookup OperationLookup) *Store {
	s.operationLookup = lookup
	return s
}

// resolveDecidedPlans 在新消息之前收尾已取舍但未完成的项，只对账，不恢复执行。
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
				code = "not_executed"
				output.Output = errorOutput(code, "用户已发送新消息，工具未执行")
			} else if item.Decision == "rejected_by_review" {
				code = "rejected_by_review"
				output.Output, _ = json.Marshal(map[string]any{"code": code, "reasons": item.ReviewReasons, "review_id": item.ReviewID})
			}
			recorded, err := s.lookupOperationResult(ctx, tx, userID, item.IdempotencyKey)
			if err != nil {
				return err
			}
			if recorded.Output != nil {
				code = ""
				output = tool.BoundResult(recorded.Output, item.Untrusted)
				item.Decision, item.OperationID = "accepted", &recorded.ID
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
			if recorded.Output != nil && plan.Status == "declined" {
				if _, err := tx.ExecContext(ctx, `UPDATE steward_plans SET status='applied',updated_at=clock_timestamp() WHERE id=$1::uuid`, plan.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
