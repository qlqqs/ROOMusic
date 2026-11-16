package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type conversationResponse struct {
	// ID 是资源标识。
	ID string `json:"id"`
	// Title 是会话标题。
	Title string `json:"title"`
	// Mode 是会话模式，支持 assistant、operator 与 steward。
	Mode string `json:"mode"`
	// Model 是对话模型名称。
	Model string `json:"model"`
	// Status 是当前持久化状态。
	Status string `json:"status"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是最近修改时间。
	UpdatedAt time.Time `json:"updated_at"`
}

func conversationView(c runtime.Conversation) conversationResponse {
	return conversationResponse{c.ID, c.Title, c.Mode, c.Model, c.Status, c.CreatedAt, c.UpdatedAt}
}

func (h *Handlers) listConversations(w http.ResponseWriter, r *http.Request, user identity.User) {
	page, size, err := httpserver.ParsePagination(r, 20, 100)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
		return
	}
	conversations, err := h.store.ListConversations(r.Context(), user.ID, runtime.ConversationPage{Number: page, Size: size})
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	total, err := h.store.CountConversations(r.Context(), user.ID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	type listItem struct {
		conversationResponse
		PendingItemCount int `json:"pending_item_count"`
	}
	items := make([]listItem, 0, len(conversations))
	for _, c := range conversations {
		items = append(items, listItem{conversationView(c), c.PendingItemCount})
	}
	httpserver.WriteJSON(w, 200, map[string]any{"items": items, "pagination": map[string]any{"page": page, "page_size": size, "total": total}})
}

func (h *Handlers) createConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	var input struct {
		// Title 是会话标题。
		Title string `json:"title"`
		// Mode 是会话模式，支持 assistant、operator 与 steward。
		Mode string `json:"mode"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	if input.Mode != "" && input.Mode != "assistant" && input.Mode != "operator" && input.Mode != "steward" {
		httpserver.WriteError(w, r, 400, "invalid_request", "会话模式无效")
		return
	}
	if !validText(input.Title) {
		invalidInput(w, r)
		return
	}
	settings, _, ok := h.configured(w, r, user)
	if !ok {
		return
	}
	conversation, err := h.store.CreateConversation(r.Context(), user.ID, strings.TrimSpace(input.Title), settings.Model, input.Mode)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 201, conversationView(conversation))
}

type messageResponse struct {
	// ID 是资源标识。
	ID string `json:"id"`
	// Sequence 是会话内递增的消息序号。
	Sequence int64 `json:"sequence"`
	// Role 是 user、assistant 或 tool。
	Role string `json:"role"`
	// Content 是过滤不透明内容后的消息块。
	Content []visibleBlock `json:"content"`
	// Context 是原用户消息的界面状态，不混入正文。
	Context json.RawMessage `json:"context,omitempty"`
	// InputTokens 是输入 token 用量，未知时为 null。
	InputTokens *int64 `json:"input_tokens"`
	// OutputTokens 是输出 token 用量，未知时为 null。
	OutputTokens *int64 `json:"output_tokens"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"created_at"`
	// ToolCalls 是消息关联的持久化工具调用。
	ToolCalls []toolCallResponse `json:"tool_calls"`
}

type visibleBlock struct {
	// Type 选择当前载荷的类型。
	Type provider.BlockType `json:"type"`
	// Text 是消息文本。
	Text string `json:"text,omitempty"`
	// ToolCall 是协议内的完整工具调用。
	ToolCall *provider.ToolCall `json:"tool_call,omitempty"`
	// ToolResult 是协议内的工具结果。
	ToolResult *provider.ToolResult `json:"tool_result,omitempty"`
}

type toolCallResponse struct {
	// ID 是资源标识。
	ID string `json:"id"`
	// Name 是工具名称。
	Name string `json:"name"`
	// Input 是工具输入 JSON。
	Input json.RawMessage `json:"input"`
	// Output 是工具输出 JSON，尚无结果时为 null。
	Output json.RawMessage `json:"output"`
	// Truncated 表示工具输出已经截断。
	Truncated bool `json:"truncated"`
	// Untrusted 表示输出含有不可信资料。
	Untrusted bool `json:"untrusted"`
	// Status 是当前持久化状态。
	Status string `json:"status"`
	// ErrorCode 是稳定错误码。
	ErrorCode *string `json:"error_code"`
	// DurationMS 是工具执行耗时，单位为毫秒。
	DurationMS *int64 `json:"duration_ms"`
}

func messageView(message runtime.Message) (messageResponse, error) {
	var content provider.Message
	if err := json.Unmarshal(message.Content, &content); err != nil {
		return messageResponse{}, err
	}
	result := messageResponse{ID: message.ID, Sequence: message.Sequence, Role: message.Role, Context: message.Context,
		InputTokens: message.InputTokens, OutputTokens: message.OutputTokens, CreatedAt: message.CreatedAt,
		Content: []visibleBlock{}, ToolCalls: []toolCallResponse{}}
	for _, block := range content.Content {
		// 白名单投影：不透明块及协议私有字段只留在数据库，不能发送到界面。
		switch block.Type {
		case provider.BlockText:
			result.Content = append(result.Content, visibleBlock{Type: block.Type, Text: block.Text})
		case provider.BlockToolCall:
			result.Content = append(result.Content, visibleBlock{Type: block.Type, ToolCall: block.ToolCall})
		case provider.BlockToolResult:
			result.Content = append(result.Content, visibleBlock{Type: block.Type, ToolResult: block.ToolResult})
		}
	}
	for _, call := range message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, toolCallResponse{call.ID, call.Name, call.Input, call.Output,
			call.Truncated, call.Untrusted, call.Status, call.ErrorCode, call.DurationMS})
	}
	return result, nil
}

func (h *Handlers) getConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	detail, err := h.store.GetConversation(r.Context(), user.ID, id)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	messages := make([]messageResponse, 0, len(detail.Messages))
	for _, stored := range detail.Messages {
		message, err := messageView(stored)
		if err != nil {
			h.logFailure(r, "internal")
			writeProblem(w, r, internalProblem)
			return
		}
		messages = append(messages, message)
	}
	var pending *runtime.Plan
	for i := range detail.Plans {
		if detail.Plans[i].Status == "pending" {
			pending = &detail.Plans[i]
			break
		}
	}
	httpserver.WriteJSON(w, 200, struct {
		conversationResponse
		// Messages 按会话内序号排列消息。
		Messages    []messageResponse `json:"messages"`
		Plans       []runtime.Plan    `json:"plans"`
		PendingPlan *runtime.Plan     `json:"pending_plan"`
	}{conversationView(detail.Conversation), messages, detail.Plans, pending})
}

func (h *Handlers) renameConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	var input struct {
		// Title 是会话标题，省略时保留。
		Title *string `json:"title"`
		// Model 是会话模型，空字符串表示使用默认模型。
		Model *string `json:"model"`
		Mode  *string `json:"mode"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	if input.Mode != nil && *input.Mode != "assistant" && *input.Mode != "operator" && *input.Mode != "steward" {
		httpserver.WriteError(w, r, 400, "invalid_request", "会话模式无效")
		return
	}
	if (input.Title == nil && input.Model == nil && input.Mode == nil) || (input.Title != nil && !validText(*input.Title)) || (input.Model != nil && !validText(*input.Model)) {
		invalidInput(w, r)
		return
	}
	if input.Title != nil {
		*input.Title = strings.TrimSpace(*input.Title)
	}
	if input.Model != nil {
		*input.Model = strings.TrimSpace(*input.Model)
	}
	conversation, err := h.store.UpdateConversation(r.Context(), user.ID, id, input.Title, input.Model, input.Mode)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]string{"title": conversation.Title, "model": conversation.Model, "mode": conversation.Mode})
}

func (h *Handlers) deleteConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteConversation(r.Context(), user.ID, id); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"deleted": true})
}

func (h *Handlers) cancelConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetConversation(r.Context(), user.ID, id); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	h.mu.Lock()
	cancel, running := h.running[runKey{user.ID, id}]
	if running {
		cancel()
	}
	h.mu.Unlock()
	httpserver.WriteJSON(w, 200, map[string]bool{"cancelled": running})
}
