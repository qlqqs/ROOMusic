package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const (
	eventHeartbeatInterval  = 15 * time.Second
	credentialCheckInterval = 60 * time.Second
)

func (h *Handlers) sendMessage(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	var input struct {
		// Text 是消息文本。
		Text string `json:"text"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Text) == "" || !validText(input.Text) {
		invalidInput(w, r)
		return
	}
	h.startRun(w, r, user, id, input.Text, false)
}

func (h *Handlers) continueConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	h.startRun(w, r, user, id, "", true)
}

func (h *Handlers) startRun(w http.ResponseWriter, r *http.Request, user identity.User, id, text string, continuation bool) {
	detail, err := h.store.GetConversation(r.Context(), user.ID, id)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	if detail.Conversation.Status == "running" {
		writeProblem(w, r, busyProblem)
		return
	}
	settings, key, ok := h.configured(w, r, user)
	if !ok {
		return
	}
	client, err := h.outboundClient(r.Context(), user)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	defer client.CloseIdleConnections()
	if err := preflight(r.Context(), client, settings.Endpoint); err != nil {
		writeProblem(w, r, outboundProblem(err))
		return
	}
	model, err := modelProvider(client, settings.Endpoint, key, settings.Protocol)
	if err != nil {
		writeProblem(w, r, outboundProblem(err))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	run := runKey{user.ID, id}
	if !h.registerRun(run, cancel) {
		writeProblem(w, r, busyProblem)
		return
	}
	defer h.unregisterRun(run)
	modelName := detail.Conversation.Model
	if modelName == "" {
		modelName = settings.Model
	}
	h.streamRun(w, r, ctx, cancel, runtime.RunParams{Caller: tool.Caller{User: user, Source: "chat"},
		ConversationID: id, Text: text, Continue: continuation, DisplayName: settings.DisplayName,
		Provider: model, Model: modelName}, user)
}

type runResult struct {
	end *runtime.Event
	err error
}

func (h *Handlers) streamRun(w http.ResponseWriter, r *http.Request, ctx context.Context, cancel context.CancelFunc, params runtime.RunParams, user identity.User) {
	ready := make(chan struct{})
	events := make(chan runtime.Event, 1)
	result := make(chan runResult, 1)
	stopped := make(chan struct{})
	params.Started = func() { close(ready) }
	go func() {
		defer close(stopped)
		var end *runtime.Event
		defer func() {
			if recover() != nil {
				h.logger.ErrorContext(context.WithoutCancel(ctx), "管家运行异常", "event", "steward.run.panicked", "conversation_id", params.ConversationID, "stack", string(debug.Stack()))
				result <- runResult{end: &runtime.Event{Type: runtime.EventDone, End: &runtime.RunEnd{Reason: runtime.EndInternalError, Message: "管家运行发生内部错误"}}}
			}
		}()
		err := h.runner.Run(ctx, params, func(event runtime.Event) {
			if event.Type == runtime.EventDone {
				end = &event
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
			}
		})
		result <- runResult{end, err}
	}()
	defer func() { cancel(); <-stopped }()
	finish := responseLifetime(w, r)
	defer finish()

	// 原子占用或继续条件检查失败不会触发 Started，仍可写普通 JSON 错误。
	var completed *runResult
	cancelledByCheck := false
	select {
	case <-r.Context().Done():
		return
	case value := <-result:
		if value.err != nil && value.end == nil {
			h.writeStoreError(w, r, value.err)
			return
		}
		completed = &value
	case <-ready:
	}
	streamingHeaders(w)
	w.WriteHeader(http.StatusOK)
	if writeAndFlush(w, []byte(": ping\n\n")) != nil {
		return
	}
	heartbeat := time.NewTicker(eventHeartbeatInterval)
	credentials := time.NewTicker(credentialCheckInterval)
	defer heartbeat.Stop()
	defer credentials.Stop()
	for {
		// 运行结果使用独立通道；结束前先排空已按序提交的事件，避免 done 超前。
		if completed != nil {
			if cancelledByCheck {
				completed = &runResult{end: &runtime.Event{Type: runtime.EventDone, End: &runtime.RunEnd{Reason: runtime.EndCancelled}}}
				h.writeRunEnd(w, *completed)
				return
			}
			for {
				select {
				case event := <-events:
					if writeEvent(w, event) != nil {
						return
					}
				default:
					h.writeRunEnd(w, *completed)
					return
				}
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-credentials.C:
			valid, err := h.checkCredentials(r, user)
			if err != nil {
				continue
			}
			if valid && user.Role != "admin" {
				valid, err = h.store.GetMembersEnabled(r.Context())
				if err != nil {
					continue
				}
			}
			if !valid {
				cancelledByCheck = true
				credentials.Stop()
				cancel()
			}
		case <-heartbeat.C:
			if writeAndFlush(w, []byte(": ping\n\n")) != nil {
				return
			}
		case event := <-events:
			if cancelledByCheck {
				continue
			}
			if writeEvent(w, event) != nil {
				return
			}
		case value := <-result:
			completed = &value
		}
	}
}

func (h *Handlers) writeRunEnd(w http.ResponseWriter, result runResult) {
	if result.err != nil && (result.end == nil || result.end.End.Reason != runtime.EndInternalError) {
		p := storeProblem(result.err)
		// 已开始流后不能再写 JSON 信封；存储错误没有模型结束原因，不附带 reason。
		_ = writeEvent(w, mapEvent{Type: runtime.EventDone, End: endResponse{Reason: "", ErrorCode: p.code, Message: p.message}})
		return
	}
	if result.end != nil {
		end := result.end.End
		response := endResponse{Reason: end.Reason, ErrorKind: string(end.ErrorKind), Message: end.Message, StatusCode: end.StatusCode}
		if end.ErrorKind != "" {
			p := providerProblem(end.ErrorKind)
			response.ErrorCode = p.code
			if response.Message == "" {
				response.Message = p.message
			}
		}
		if end.Reason == runtime.EndInternalError {
			response.ErrorCode = "internal"
		}
		_ = writeEvent(w, mapEvent{Type: runtime.EventDone, Step: result.end.Step, End: response})
	}
}

type endResponse struct {
	// StatusCode 是模型端点返回的 HTTP 状态，无响应时省略。
	StatusCode int `json:"status_code,omitempty"`
	// Reason 是运行结束原因，存储失败时不伪造模型结束原因。
	Reason runtime.EndReason `json:"reason,omitempty"`
	// ErrorCode 是稳定错误码。
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorKind 是模型适配器的错误分类。
	ErrorKind string `json:"error_kind,omitempty"`
	// Message 是可展示且不含凭据的说明。
	Message string `json:"message,omitempty"`
}

type mapEvent struct {
	// Type 选择当前载荷的类型。
	Type runtime.EventType `json:"type"`
	// Step 是从一开始的模型调用序号。
	Step int `json:"step,omitempty"`
	// End 是运行的最终结果。
	End endResponse `json:"end"`
}

func writeEvent(w http.ResponseWriter, event any) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var kind runtime.EventType
	switch event := event.(type) {
	case runtime.Event:
		kind = event.Type
	case mapEvent:
		kind = event.Type
	}
	payload := make([]byte, 0, len(raw)+len(kind)+16)
	payload = append(payload, "event: "...)
	payload = append(payload, kind...)
	payload = append(payload, "\ndata: "...)
	payload = append(payload, raw...)
	payload = append(payload, '\n', '\n')
	return writeAndFlush(w, payload)
}
