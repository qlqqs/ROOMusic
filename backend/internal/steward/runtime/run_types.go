package runtime

import (
	"encoding/json"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// Runner 执行一次请求内的多步对话，可并发用于不同会话。
type Runner struct {
	store    RunStore
	registry *tool.Registry
	logger   *slog.Logger
}

// NewRunner 注入存储、已完成注册的工具表和日志器，不执行任何 I/O。
func NewRunner(store RunStore, registry *tool.Registry, logger *slog.Logger) *Runner {
	return &Runner{store: store, registry: registry, logger: logger}
}

// RunParams 是入口已取得的调用者身份、会话及模型配置，不包含端点密钥。
type RunParams struct {
	Decision *PlanDecision
	// Caller 来自已认证入口。
	Caller tool.Caller
	// ConversationID 是当前用户的会话标识。
	ConversationID string
	// Text 是本次新发的用户消息。
	Text string
	// Context 是本次提供的界面状态。
	Context json.RawMessage
	// Continue 表示从已有历史继续，不追加用户消息或修改标题。
	Continue bool
	// DisplayName 是调用者当前设置中的管家名字。
	DisplayName string
	// Provider 是调用方按用户配置构造的适配器。
	Provider provider.Provider
	// Model 是本次使用的模型名。
	Model string
	// Started 在原子占用会话成功后、执行模型请求之前调用，供传输层开始响应。
	Started func()
}

// EventType 区分运行事件，不涉及任何传输协议。
type EventType string

const (
	EventPlan EventType = "plan"
	// EventText 表示助手文本增量。
	EventText EventType = "text"
	// EventToolStarted 表示开始执行工具。
	EventToolStarted EventType = "tool_started"
	// EventToolFinished 表示工具结果已落库。
	EventToolFinished EventType = "tool_finished"
	// EventUsage 表示当前模型步骤的最终用量快照。
	EventUsage EventType = "usage"
	// EventDone 表示运行结束且已尝试释放会话。
	EventDone EventType = "done"
)

// EndReason 是运行结束的稳定原因。
type EndReason string

const (
	EndAwaitingConfirmation EndReason = "awaiting_confirmation"
	// EndInternalError 表示运行发生内部异常。
	EndInternalError EndReason = "internal_error"
	// EndCompleted 表示模型给出了不含工具请求的回答。
	EndCompleted EndReason = "completed"
	// EndStepLimit 表示达到模型调用次数上限。
	EndStepLimit EndReason = "step_limit"
	// EndTokenLimit 表示达到运行用量或单次输出上限。
	EndTokenLimit EndReason = "token_limit"
	// EndCancelled 表示请求上下文已取消或超时。
	EndCancelled EndReason = "cancelled"
	// EndProviderError 表示模型调用失败或流未完整结束。
	EndProviderError EndReason = "provider_error"
)

// RunEnd 是运行结果，模型错误只携带可展示的信息。
type RunEnd struct {
	// StatusCode 是模型端点返回的 HTTP 状态，无响应时为零。
	StatusCode int `json:"status_code,omitempty"`
	// Reason 是结束原因。
	Reason EndReason `json:"reason"`
	// ErrorKind 仅在模型错误时提供。
	ErrorKind provider.ErrorKind `json:"error_kind,omitempty"`
	// Message 是模型适配器已脱敏的信息或固定说明。
	Message string `json:"message,omitempty"`
}

// ToolStarted 描述即将调用的工具。
type ToolStarted struct {
	// CallID 是模型给出的调用标识。
	CallID string `json:"call_id"`
	// Name 是请求的工具名。
	Name string `json:"name"`
	// Input 是完整工具参数。
	Input json.RawMessage `json:"input"`
}

// ToolFinished 描述一次已开始调用的持久化结果。
type ToolFinished struct {
	// CallID 是模型给出的调用标识。
	CallID string `json:"call_id"`
	// Status 是 succeeded 或 failed。
	Status string `json:"status"`
	// Output 是未添加来源标记的结果 JSON。
	Output json.RawMessage `json:"output"`
	// Truncated 表示工具输出已截断。
	Truncated bool `json:"truncated"`
	// Untrusted 表示输出含不可信资料。
	Untrusted bool `json:"untrusted"`
	// ErrorCode 是失败时的稳定错误码。
	ErrorCode string `json:"error_code,omitempty"`
}

// Event 由 Type 选择载荷；不向接收方暴露助手的不透明块。
type Event struct {
	Plan *Plan `json:"plan,omitempty"`
	// Type 是事件类型。
	Type EventType `json:"type"`
	// Step 是从一开始的模型调用序号。
	Step int `json:"step,omitempty"`
	// Text 是文本增量。
	Text string `json:"text,omitempty"`
	// ToolStarted 是开始载荷。
	ToolStarted *ToolStarted `json:"tool_started,omitempty"`
	// ToolFinished 是结果载荷。
	ToolFinished *ToolFinished `json:"tool_finished,omitempty"`
	// Usage 是当前步骤用量，未知的计数仍为 nil。
	Usage *provider.Usage `json:"usage,omitempty"`
	// End 是最终结果。
	End *RunEnd `json:"end,omitempty"`
}

// EventSink 同步、按序接收事件，不得阻塞等待运行结束或修改事件载荷。
// 传输失败时由接收方取消 Run 的上下文；接收方仍会收到最终 done 事件。
type EventSink func(Event)
