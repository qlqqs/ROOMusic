package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// CreatePlan 在同一事务中保存方案并挂起其工具调用。
func (s *Store) CreatePlan(ctx context.Context, userID, conversationID string, items []PlanItem) (Plan, error) {
	digest, err := planDigest(items)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{ID: uuid.New(), Digest: digest, ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Items: items, Status: "pending"}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	if _, err = lockConversation(ctx, tx, userID, conversationID); err != nil {
		return Plan{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO steward_plans(id,conversation_id,user_id,digest,expires_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5)`, plan.ID, conversationID, userID, digest, plan.ExpiresAt)
	if err != nil {
		return Plan{}, err
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		item.ID = uuid.New()
		item.PlanID = plan.ID
		item.Decision = "pending"
		projectItem(item)
		preview, err := json.Marshal(item.Preview)
		if err != nil {
			return Plan{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO steward_plan_items(id,plan_id,tool_call_id,position,tool_name,input,preview,revertible,idempotency_key) SELECT $1::uuid,$2::uuid,t.id,$4,$5,$6::jsonb,$7::jsonb,$8,t.idempotency_key FROM steward_tool_calls t JOIN steward_messages m ON m.id=t.message_id WHERE t.id=$3::uuid AND m.conversation_id=$9::uuid`, item.ID, plan.ID, item.ToolCallID, item.Position, item.Tool, string(item.Input), string(preview), item.Revertible, conversationID)
		if err != nil {
			return Plan{}, err
		}
		if err = requireAffected(tx.ExecContext(ctx, `UPDATE steward_tool_calls SET status='awaiting_confirmation' WHERE id=$1::uuid AND status='running'`, item.ToolCallID)); err != nil {
			return Plan{}, err
		}
	}
	return plan, tx.Commit()
}

func readPlans(ctx context.Context, tx *sql.Tx, userID, conversationID string, pendingOnly bool) ([]Plan, error) {
	filter := ""
	if pendingOnly {
		filter = " AND status='pending'"
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,digest,expires_at,status FROM steward_plans WHERE user_id=$1::uuid AND conversation_id=$2::uuid`+filter+` ORDER BY created_at,id`, userID, conversationID)
	if err != nil {
		return nil, err
	}
	plans := []Plan{}
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Digest, &p.ExpiresAt, &p.Status); err != nil {
			rows.Close()
			return nil, err
		}
		p.ExpiresAt = p.ExpiresAt.UTC()
		plans = append(plans, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range plans {
		plans[i].Review, err = readLatestReview(ctx, tx, plans[i].ID)
		if err != nil {
			return nil, err
		}
		plans[i].Items, err = readPlanItems(ctx, tx, plans[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return plans, nil
}

func readPlanItems(ctx context.Context, tx *sql.Tx, planID string) ([]PlanItem, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.id::text,i.tool_call_id::text,i.position,i.tool_name,i.input,i.preview,i.revertible,i.idempotency_key::text,i.decision,i.result,i.operation_id::text,m.content::text,t.position,t.untrusted FROM steward_plan_items i JOIN steward_tool_calls t ON t.id=i.tool_call_id JOIN steward_messages m ON m.id=t.message_id WHERE i.plan_id=$1::uuid ORDER BY i.position`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlanItem{}
	for rows.Next() {
		item := PlanItem{PlanID: planID}
		var preview, content []byte
		var position int
		if err := rows.Scan(&item.ID, &item.ToolCallID, &item.Position, &item.Tool, (*[]byte)(&item.Input), &preview, &item.Revertible, &item.IdempotencyKey, &item.Decision, (*[]byte)(&item.Result), &item.OperationID, &content, &position, &item.Untrusted); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(preview, &item.Preview); err != nil {
			return nil, err
		}
		var message provider.Message
		if err := json.Unmarshal(content, &message); err != nil {
			return nil, err
		}
		index := 0
		for _, block := range message.Content {
			if block.Type == provider.BlockToolCall {
				if index == position {
					item.CallID = block.ToolCall.ID
					break
				}
				index++
			}
		}
		projectItem(&item)
		items = append(items, item)
	}
	return items, rows.Err()
}

// DecidePlan 按归属、状态、期限、摘要、空闲顺序检查，原子记录取舍并占用会话。
func (s *Store) DecidePlan(ctx context.Context, userID, conversationID string, decision PlanDecision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status, err := lockConversation(ctx, tx, userID, conversationID)
	if errors.Is(err, ErrNotFound) {
		return ErrPlanNotFound
	}
	if err != nil {
		return err
	}
	if len(decision.AutoApproveTools) > 0 {
		var mode string
		if err := tx.QueryRowContext(ctx, `SELECT mode FROM steward_conversations WHERE id=$1::uuid AND user_id=$2::uuid`, conversationID, userID).Scan(&mode); err != nil {
			return err
		}
		if mode == "steward" {
			return ErrInvalidInput
		}
	}
	var planStatus, digest string
	var expires time.Time
	err = tx.QueryRowContext(ctx, `SELECT status,digest,expires_at FROM steward_plans WHERE id=$1::uuid AND conversation_id=$2::uuid AND user_id=$3::uuid FOR UPDATE`, decision.PlanID, conversationID, userID).Scan(&planStatus, &digest, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlanNotFound
	}
	if err != nil {
		return err
	}
	if planStatus == "expired" {
		return ErrPlanExpired
	}
	if planStatus != "pending" {
		return ErrPlanNotFound
	}
	if !time.Now().Before(expires) {
		if err := s.resolvePendingPlans(ctx, tx, userID, conversationID, "expired", true); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrPlanExpired
	}
	if digest != decision.Digest {
		return ErrPlanDigestMismatch
	}
	if status == "running" {
		return ErrConversationBusy
	}
	items, err := readPlanItems(ctx, tx, decision.PlanID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	accepted := map[string]bool{}
	for _, item := range items {
		if item.Decision == "pending" {
			known[item.ID] = true
		}
	}
	for _, id := range decision.AcceptedItemIDs {
		if !known[id] || accepted[id] {
			return ErrInvalidInput
		}
		accepted[id] = true
	}
	tools := map[string]bool{}
	for _, item := range items {
		if accepted[item.ID] && item.Revertible {
			tools[item.Tool] = true
		}
	}
	seen := map[string]bool{}
	for _, name := range decision.AutoApproveTools {
		if !tools[name] || seen[name] {
			return ErrInvalidInput
		}
		seen[name] = true
	}
	for _, item := range items {
		if item.Decision != "pending" {
			continue
		}
		value := "declined"
		if accepted[item.ID] {
			value = "accepted"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steward_plan_items SET decision=$2 WHERE id=$1::uuid`, item.ID, value); err != nil {
			return err
		}
	}
	next := "declined"
	for _, item := range items {
		if item.Decision == "accepted" {
			next = "applied"
		}
	}
	if len(accepted) > 0 {
		next = "applied"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steward_plans SET status=$2,updated_at=clock_timestamp() WHERE id=$1::uuid`, decision.PlanID, next); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steward_conversations SET status='running',auto_approved_tools=ARRAY(SELECT DISTINCT unnest(auto_approved_tools || $3::text[])),updated_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID, decision.AutoApproveTools); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingExecutions 只返回用户已经取舍、但尚未落结果的项，供连接中断后的继续请求复用原幂等键。
func (s *Store) PendingExecutions(ctx context.Context, userID, conversationID string) ([]PlanItem, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plans, err := readPlans(ctx, tx, userID, conversationID, false)
	if err != nil {
		return nil, err
	}
	items := []PlanItem{}
	for _, plan := range plans {
		if plan.Status != "applied" && plan.Status != "declined" {
			continue
		}
		for _, item := range plan.Items {
			if item.Result == nil && item.Decision != "pending" {
				items = append(items, item)
			}
		}
	}
	return items, tx.Commit()
}

func finishPlanItem(ctx context.Context, tx *sql.Tx, userID, conversationID string, item PlanItem, result ToolCallResult, input MessageInput) error {
	if err := finishToolCall(ctx, tx, userID, conversationID, item.ToolCallID, result); err != nil {
		return err
	}
	if _, err := appendMessage(ctx, tx, userID, conversationID, input); err != nil {
		return err
	}
	output := struct {
		OperationID *string `json:"operation_id"`
	}{OperationID: item.OperationID}
	_ = json.Unmarshal(result.Output, &output)
	_, err := tx.ExecContext(ctx, `UPDATE steward_plan_items SET result=$2::jsonb,operation_id=$3::uuid,decision=$4 WHERE id=$1::uuid`, item.ID, string(result.Output), output.OperationID, item.Decision)
	return err
}

// FinishPlanItem 将方案结果、调用结果与模型工具消息一并保存。
func (s *Store) FinishPlanItem(ctx context.Context, userID, conversationID string, item PlanItem, result ToolCallResult, input MessageInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return err
	}
	if err := finishPlanItem(ctx, tx, userID, conversationID, item, result, input); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steward_plans SET status=CASE WHEN EXISTS (SELECT 1 FROM steward_plan_items WHERE plan_id=$1::uuid AND decision='accepted') THEN 'applied' ELSE 'declined' END,updated_at=clock_timestamp() WHERE id=$1::uuid AND status IN ('pending','applied','declined') AND NOT EXISTS (SELECT 1 FROM steward_plan_items WHERE plan_id=$1::uuid AND decision='pending')`, item.PlanID); err != nil {
		return err
	}
	return tx.Commit()
}

func planResultMessage(item PlanItem, output tool.Result, code string) (MessageInput, error) {
	return messageInput(messageContent{Message: provider.Message{Role: provider.RoleTool, Content: []provider.ContentBlock{{Type: provider.BlockToolResult, ToolResult: &provider.ToolResult{CallID: item.CallID, Content: string(output.Output), IsError: code != ""}}}}, Source: &toolSource{Name: item.Tool, Untrusted: item.Untrusted, Truncated: output.Truncated}}, provider.Usage{}, nil)
}

func (s *Store) resolvePendingPlans(ctx context.Context, tx *sql.Tx, userID, conversationID, status string, expiredOnly bool) error {
	plans, err := readPlans(ctx, tx, userID, conversationID, true)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		expired := !time.Now().Before(plan.ExpiresAt)
		if expiredOnly && !expired {
			continue
		}
		resolvedStatus := status
		if expired {
			resolvedStatus = "expired"
		}
		for _, item := range plan.Items {
			if item.Result != nil {
				continue
			}
			code := "not_executed"
			result := ToolCallResult{Status: "failed", Output: errorOutput(code, "方案已作废，工具未执行"), ErrorCode: &code}
			// 审查执行在结果提交前仍为 pending；失效时先对账，不能把已提交操作说成未执行。
			if plan.Review != nil {
				if s.operationLookup == nil {
					return errors.New("管家未装配操作结果查询")
				}
				recorded, err := s.operationLookup(ctx, tx, userID, item.IdempotencyKey)
				if err != nil {
					return err
				}
				if recorded.Output != nil {
					bounded := tool.BoundResult(recorded.Output, item.Untrusted)
					item.Decision, item.OperationID = "accepted", &recorded.ID
					code = ""
					result = ToolCallResult{Status: "succeeded", Output: bounded.Output, Truncated: bounded.Truncated}
				}
			}
			input, err := planResultMessage(item, tool.Result{Output: result.Output, Truncated: result.Truncated}, code)
			if err != nil {
				return err
			}
			if err := finishPlanItem(ctx, tx, userID, conversationID, item, result, input); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steward_plans SET status=$2,updated_at=clock_timestamp() WHERE id=$1::uuid`, plan.ID, resolvedStatus); err != nil {
			return err
		}
	}
	return nil
}
