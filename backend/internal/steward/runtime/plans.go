package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

var (
	ErrPlanNotFound       = errors.New("方案不存在或已处理")
	ErrPlanExpired        = errors.New("方案已过期")
	ErrPlanDigestMismatch = errors.New("方案摘要不一致")
)

// Plan 是前端确认卡片与持久化方案的共同投影。
type Plan struct {
	ID        string     `json:"plan_id"`
	Digest    string     `json:"digest"`
	ExpiresAt time.Time  `json:"expires_at"`
	Items     []PlanItem `json:"items"`
	Status    string     `json:"status"`
}

// PlanItem 中执行输入与幂等键只保留在后端。
type PlanItem struct {
	ID             string          `json:"id"`
	Tool           string          `json:"tool"`
	Target         string          `json:"target"`
	Changes        []tool.Change   `json:"changes"`
	Revertible     bool            `json:"revertible"`
	Basis          *tool.Basis     `json:"basis"`
	Decision       string          `json:"decision"`
	Result         json.RawMessage `json:"result,omitempty"`
	OperationID    *string         `json:"operation_id,omitempty"`
	PlanID         string          `json:"-"`
	ToolCallID     string          `json:"-"`
	Position       int             `json:"-"`
	Input          json.RawMessage `json:"-"`
	Preview        tool.Preview    `json:"-"`
	IdempotencyKey string          `json:"-"`
	CallID         string          `json:"call_id"`
	Untrusted      bool            `json:"-"`
}

// PlanDecision 只能由已登录用户的确认接口创建，不来自模型参数。
type PlanDecision struct {
	PlanID           string   `json:"-"`
	Digest           string   `json:"digest"`
	AcceptedItemIDs  []string `json:"accepted_item_ids"`
	AutoApproveTools []string `json:"auto_approve_tools"`
}

// PlanStore 扩展运行存储，方案与工具结果均在所属会话锁内持久化。
type PlanStore interface {
	CreatePlan(context.Context, string, string, []PlanItem) (Plan, error)
	DecidePlan(context.Context, string, string, PlanDecision) error
	PendingExecutions(context.Context, string, string) ([]PlanItem, error)
	FinishPlanItem(context.Context, string, string, PlanItem, ToolCallResult, MessageInput) error
}

func planDigest(items []PlanItem) (string, error) {
	type entry struct {
		Tool     string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
		Position int             `json:"position"`
	}
	entries := make([]entry, 0, len(items))
	for _, item := range items {
		decoder := json.NewDecoder(bytes.NewReader(item.Input))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		normalized, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{item.Tool, normalized, item.Position})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func projectItem(item *PlanItem) {
	item.Target = item.Preview.Target
	item.Changes = item.Preview.Changes
	var input struct {
		Basis *tool.Basis `json:"basis"`
	}
	_ = json.Unmarshal(item.Input, &input)
	item.Basis = input.Basis
}

// CheckPlanDecision 在模型配置准备之前检查方案快照；取舍事务会在锁内再次检查。
func CheckPlanDecision(detail ConversationDetail, decision PlanDecision) error {
	for _, plan := range detail.Plans {
		if plan.ID != decision.PlanID {
			continue
		}
		if plan.Status == "expired" {
			return ErrPlanExpired
		}
		if plan.Status != "pending" {
			return ErrPlanNotFound
		}
		if !time.Now().Before(plan.ExpiresAt) {
			return ErrPlanExpired
		}
		if plan.Digest != decision.Digest {
			return ErrPlanDigestMismatch
		}
		if detail.Conversation.Status == "running" {
			return ErrConversationBusy
		}
		return nil
	}
	return ErrPlanNotFound
}
