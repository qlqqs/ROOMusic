package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
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
		Text    string          `json:"text"`
		Context json.RawMessage `json:"context"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Text) == "" || !validText(input.Text) {
		invalidInput(w, r)
		return
	}
	normalized, err := runtime.NormalizeContext(input.Context)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_request", "页面上下文无效")
		return
	}
	h.startRun(w, r, user, id, input.Text, false, normalized)
}

func (h *Handlers) continueConversation(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	// 保留旧客户端无请求体的继续调用。
	if r.Body != nil && r.Body != http.NoBody {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
			return
		}
		if len(strings.TrimSpace(string(body))) != 0 {
			if !utf8.Valid(body) || !json.Valid(body) {
				httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
				return
			}
			var input struct{}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if bytes.TrimSpace(body)[0] != '{' || decoder.Decode(&input) != nil {
				httpserver.WriteError(w, r, 400, "invalid_request", "继续请求不接受任何字段")
				return
			}
		}
	}
	h.startRun(w, r, user, id, "", true, nil)
}

func (h *Handlers) startRun(w http.ResponseWriter, r *http.Request, user identity.User, id, text string, continuation bool, messageContext json.RawMessage, decisions ...*runtime.PlanDecision) {
	var decision *runtime.PlanDecision
	if len(decisions) > 0 {
		decision = decisions[0]
	}
	reviewPlanID := ""
	if strings.HasSuffix(r.URL.Path, "/review") {
		reviewPlanID = strings.ToLower(r.PathValue("plan_id"))
	}
	detail, err := h.store.GetConversation(r.Context(), user.ID, id)
	if (decision != nil || reviewPlanID != "") && errors.Is(err, runtime.ErrNotFound) {
		err = runtime.ErrPlanNotFound
	}
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	if decision != nil {
		if err := runtime.CheckPlanDecision(detail, *decision); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if reviewPlanID != "" {
		if detail.Conversation.Mode != "steward" {
			h.writeStoreError(w, r, runtime.ErrReviewNotApplicable)
			return
		}
		found := false
		for _, plan := range detail.Plans {
			if plan.ID == reviewPlanID {
				found = true
				err = runtime.CheckPlanDecision(detail, runtime.PlanDecision{PlanID: plan.ID, Digest: plan.Digest})
				break
			}
		}
		if !found {
			err = runtime.ErrPlanNotFound
		}
		if err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if detail.Conversation.Status == "running" {
		writeProblem(w, r, busyProblem)
		return
	}
	settings, key, ok := h.configured(w, r, user)
	if !ok {
		return
	}
	model, closeClient, err := h.openModel(r.Context(), user, settings, key)
	if err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	modelName := detail.Conversation.Model
	if modelName == "" {
		modelName = settings.Model
	}
	run, err := h.supervisor.Start(r.Context(), runtime.RunParams{
		Finished: closeClient,
		Check:    h.runCredentialCheck(r, user, settings), Caller: tool.Caller{User: user, Source: "chat", RequestID: r.Header.Get("X-Request-ID")},
		ReviewPlanID: reviewPlanID, ReviewModel: settings.ReviewModel, Decision: decision, ConversationID: id, Text: text, Context: messageContext, Continue: continuation, DisplayName: settings.DisplayName,
		Provider: model, Model: modelName, Protocol: provider.Protocol(settings.Protocol), Endpoint: settings.Endpoint,
		NativeSearch: settings.SearchSource == "native" && effectiveNativeSearch(settings, settingsUpdate(settings)) == provider.NativeSearchSupported})
	if err != nil {
		closeClient()
		h.writeStoreError(w, r, err)
		return
	}
	h.streamSubscription(w, r, run.Subscribe(0))
}

// runCredentialCheck 只捕获会话标识与设置快照，不保留原始请求。
func (h *Handlers) runCredentialCheck(request *http.Request, user identity.User, expected runtime.UserSettings) func(context.Context) error {
	sessionToken := ""
	if cookie, err := request.Cookie(identity.SessionCookieName); err == nil {
		sessionToken = strings.Clone(cookie.Value)
	}
	return func(ctx context.Context) error {
		checkRequest := (&http.Request{Header: make(http.Header)}).WithContext(ctx)
		if sessionToken != "" {
			checkRequest.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: sessionToken})
		}
		valid, err := h.checkCredentials(checkRequest, user)
		if err != nil {
			return err
		}
		if !valid {
			return runtime.ErrRunInvalidated
		}
		if user.Role != "admin" {
			enabled, err := h.store.GetMembersEnabled(ctx)
			if err != nil {
				return err
			}
			if !enabled {
				return runtime.ErrRunInvalidated
			}
		}
		return h.checkModelKey(ctx, user.ID, expected)
	}
}

func (h *Handlers) checkModelKey(ctx context.Context, userID string, expected runtime.UserSettings) error {
	current, err := h.store.GetUserSettings(ctx, userID)
	if err != nil {
		return err
	}
	if current.Endpoint != expected.Endpoint || !sameModelKey(current.Key, expected.Key) {
		return runtime.ErrRunInvalidated
	}
	return nil
}

func sameModelKey(a, b *runtime.EncryptedKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.Ciphertext, b.Ciphertext)
}

func (h *Handlers) conversationEvents(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetConversation(r.Context(), user.ID, id); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("after")
	}
	var after uint64
	if cursor != "" {
		var err error
		after, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			invalidInput(w, r)
			return
		}
	}
	h.streamSubscription(w, r, h.supervisor.Subscribe(id, after))
}

func (h *Handlers) streamSubscription(w http.ResponseWriter, r *http.Request, subscription *runtime.Subscription) {
	if subscription == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer subscription.Close()
	finish := responseLifetime(w, r)
	defer finish()
	streamingHeaders(w)
	w.WriteHeader(http.StatusOK)
	if writeAndFlush(w, []byte(": ping\n\n")) != nil {
		return
	}
	for _, event := range subscription.Replay {
		if h.writeRunEvent(w, event) != nil {
			return
		}
	}
	heartbeat := time.NewTicker(eventHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if writeAndFlush(w, []byte(": ping\n\n")) != nil {
				return
			}
		case event, ok := <-subscription.Events:
			if !ok {
				return
			}
			if h.writeRunEvent(w, event) != nil {
				return
			}
		}
	}
}

func (h *Handlers) writeRunEvent(w http.ResponseWriter, event runtime.Event) error {
	if event.Type != runtime.EventDone {
		return writeEvent(w, event)
	}
	response := endResponse{}
	if event.Failure != nil && (event.End == nil || event.End.Reason != runtime.EndInternalError) {
		p := storeProblem(event.Failure)
		response.ErrorCode, response.Message = p.code, p.message
	} else if end := event.End; end != nil {
		response = endResponse{Reason: end.Reason, ErrorKind: string(end.ErrorKind), Message: end.Message, StatusCode: end.StatusCode}
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
	}
	return writeEvent(w, mapEvent{Seq: event.Seq, Type: runtime.EventDone, Step: event.Step, End: response})
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
	Seq uint64 `json:"seq"`
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
	var seq uint64
	switch event := event.(type) {
	case runtime.Event:
		kind = event.Type
		seq = event.Seq
	case mapEvent:
		kind = event.Type
		seq = event.Seq
	}
	payload := make([]byte, 0, len(raw)+len(kind)+16)
	payload = append(payload, "id: "...)
	payload = strconv.AppendUint(payload, seq, 10)
	payload = append(payload, "\nevent: "...)
	payload = append(payload, kind...)
	payload = append(payload, "\ndata: "...)
	payload = append(payload, raw...)
	payload = append(payload, '\n', '\n')
	return writeAndFlush(w, payload)
}
