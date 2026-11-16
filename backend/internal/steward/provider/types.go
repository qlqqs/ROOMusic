// Package provider 提供与存储和工具执行无关的模型协议适配与端点探测。
package provider

import (
	"context"
	"encoding/json"
)

// Protocol 标记不透明内容所属的模型协议。
type Protocol string

const (
	// ProtocolOpenAI 表示兼容 Chat Completions 的协议。
	ProtocolOpenAI Protocol = "openai"
	// ProtocolResponses 表示 OpenAI Responses 协议。
	ProtocolResponses Protocol = "responses"
	// ProtocolMessages 表示兼容 Anthropic Messages 的协议。
	ProtocolMessages Protocol = "messages"
)

// Role 是运行时消息的角色，系统提示单独放在 Request 中。
type Role string

const (
	// RoleUser 表示用户消息。
	RoleUser Role = "user"
	// RoleAssistant 表示助手消息。
	RoleAssistant Role = "assistant"
	// RoleTool 表示工具执行结果。
	RoleTool Role = "tool"
)

// BlockType 区分内容块的载荷。
type BlockType string

const (
	// BlockText 表示文本。
	BlockText BlockType = "text"
	// BlockToolCall 表示完整工具调用。
	BlockToolCall BlockType = "tool_call"
	// BlockToolResult 表示工具结果。
	BlockToolResult BlockType = "tool_result"
	// BlockOpaque 表示协议专有内容。
	BlockOpaque BlockType = "opaque"
)

// Message 是可直接编码为 JSON 的运行时消息。
type Message struct {
	// ProviderBlocks 是完整的原协议内容；调用方先检查端点归属，再交给适配器回传。
	ProviderBlocks []OpaqueBlock `json:"-"`
	// Role 是消息角色。
	Role Role `json:"role"`
	// Content 按协议中的内容顺序保存，包含不透明块。
	Content []ContentBlock `json:"content"`
}

// ContentBlock 由 Type 选择载荷；除 Text 外的载荷使用指针区分缺失。
type ContentBlock struct {
	// Type 是内容类型。
	Type BlockType `json:"type"`
	// Text 是文本内容。
	Text string `json:"text,omitempty"`
	// ToolCall 是完整工具调用。
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	// ToolResult 是执行结果。
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	// Opaque 是只可回传同一协议的原始内容。
	Opaque *OpaqueBlock `json:"opaque,omitempty"`
}

// ToolCall 保留协议内调用标识与完整、合法的参数 JSON。
type ToolCall struct {
	// ID 是模型生成的调用标识，不限定前缀。
	ID string `json:"id"`
	// Name 是工具名。
	Name string `json:"name"`
	// Arguments 是完整 JSON；无参数调用规范成空对象。
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult 是提供给模型的工具结果；JSON 输出由调用方编码为 Content 文本。
type ToolResult struct {
	// CallID 对应 ToolCall.ID。
	CallID string `json:"call_id"`
	// Content 是结果或错误的文本。
	Content string `json:"content"`
	// IsError 标记执行失败，OpenAI 协议只能通过 Content 表达错误。
	IsError bool `json:"is_error"`
}

// OpaqueBlock 保存协议专有内容，不解释、不跨协议传递。
type OpaqueBlock struct {
	// Protocol 是原协议。
	Protocol Protocol `json:"protocol"`
	// Data 是原协议的完整 JSON 内容块；JSON 编码可改变空白但不改变值。
	Data json.RawMessage `json:"data"`
}

// ToolDefinition 是发给模型的工具定义，不包含执行与鉴权逻辑。
type ToolDefinition struct {
	// Name 是工具名。
	Name string `json:"name"`
	// Description 是说明。
	Description string `json:"description"`
	// InputSchema 是输入的 JSON Schema 对象。
	InputSchema json.RawMessage `json:"input_schema"`
}

// Request 是一次模型请求，不包含端点密钥或采样参数。
type Request struct {
	// NativeSearch 由调用者的搜索来源与生效能力共同决定。
	NativeSearch bool `json:"native_search,omitempty"`
	// ForceTool 指定唯一必须调用的工具；空值保留普通对话行为。
	ForceTool string `json:"force_tool,omitempty"`
	// Model 是调用方选择的模型。
	Model string `json:"model"`
	// System 是系统提示词。
	System string `json:"system,omitempty"`
	// Messages 是有序对话。
	Messages []Message `json:"messages"`
	// Tools 是本次允许的工具定义。
	Tools []ToolDefinition `json:"tools,omitempty"`
	// MaxOutputTokens 是输出上限；Messages 必须为正数，OpenAI 为零时省略。
	MaxOutputTokens int `json:"max_output_tokens"`
}

// Usage 是本次请求的累计用量；nil 表示未提供，不估算。
type Usage struct {
	// InputTokens 包含 Messages 的缓存创建和读取 token。
	InputTokens *int64 `json:"input_tokens,omitempty"`
	// OutputTokens 是累计输出 token。
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

// FinishReason 是跨协议结束原因。
type FinishReason string

const (
	// FinishNormal 表示正常结束。
	FinishNormal FinishReason = "normal"
	// FinishPause 要求原样回传助手块后继续当前轮次。
	FinishPause FinishReason = "pause_turn"
	// FinishToolCalls 表示请求执行工具。
	FinishToolCalls FinishReason = "tool_calls"
	// FinishOutputLimit 表示输出或上下文达到上限。
	FinishOutputLimit FinishReason = "output_limit"
	// FinishOther 保留其他或未知原因。
	FinishOther FinishReason = "other"
)

// Finish 同时保留归一化原因和协议原值。
type Finish struct {
	// Reason 是统一原因。
	Reason FinishReason `json:"reason"`
	// RawReason 是协议原始值，缺失时为空。
	RawReason string `json:"raw_reason"`
}

// EventType 是流事件类型。
type EventType string

const (
	// EventText 表示文本增量。
	EventText      EventType = "text"
	EventSearch    EventType = "search"
	EventCitations EventType = "citations"
	// EventToolCall 表示已拼接并校验的完整调用。
	EventToolCall EventType = "tool_call"
	// EventOpaque 表示完整不透明块。
	EventOpaque EventType = "opaque"
	// EventUsage 表示累计用量快照，调用方应替换而非累加。
	EventUsage EventType = "usage"
	// EventDone 表示收到了协议终止标志。
	EventDone EventType = "done"
)

// Event 由 Type 选择对应载荷，可直接编码为 JSON。
type Event struct {
	Search    *Search    `json:"search,omitempty"`
	Citations []Citation `json:"citations,omitempty"`
	// ProviderBlocks 仅在完整结束时提供，不进入界面或日志。
	ProviderBlocks []OpaqueBlock `json:"-"`
	// Type 是事件类型。
	Type EventType `json:"type"`
	// Text 是文本增量。
	Text string `json:"text,omitempty"`
	// ToolCall 是完整调用。
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	// Opaque 是完整协议内容块。
	Opaque *OpaqueBlock `json:"opaque,omitempty"`
	// Usage 是累计用量快照。
	Usage *Usage `json:"usage,omitempty"`
	// Finish 是结束原因。
	Finish *Finish `json:"finish,omitempty"`
}

// Provider 将统一请求转换为协议请求，HTTP 客户端由调用方注入。
type Provider interface {
	// Stream 发起流式请求，返回的流绑定 ctx 的生命周期。
	Stream(ctx context.Context, request Request) (Stream, error)
}

// Stream 采用同步拉取，不启动后台读取协程。调用方应 defer Close。
// ctx 取消会立即关闭响应体并中止 Next；Next 不可并发调用，Close 可并发调用。
type Stream interface {
	// Next 返回下一个事件，结束后返回 io.EOF；取消返回 context 的错误。
	Next() (Event, error)
	// Close 取消请求并释放连接，可重复调用。
	Close() error
}
