package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

type adapter struct {
	client   *http.Client
	endpoint string
	key      string
	protocol Protocol
}

// NewOpenAI 创建 Chat Completions 适配器，只使用传入的客户端。
func NewOpenAI(client *http.Client, endpoint, key string) (Provider, error) {
	return newAdapter(client, endpoint, key, ProtocolOpenAI)
}

// NewMessages 创建 Messages 适配器，使用调研核实的 2026-10-21 版本。
func NewMessages(client *http.Client, endpoint, key string) (Provider, error) {
	return newAdapter(client, endpoint, key, ProtocolMessages)
}

// NewResponses 创建 Responses 适配器，复用调用方提供的出站客户端。
func NewResponses(client *http.Client, endpoint, key string) (Provider, error) {
	return newAdapter(client, endpoint, key, ProtocolResponses)
}

func newAdapter(client *http.Client, endpoint, key string, protocol Protocol) (*adapter, error) {
	if client == nil {
		return nil, failure(ErrorOther, 0, "必须传入 HTTP 客户端", "")
	}
	if _, err := parseEndpoint(endpoint); err != nil {
		return nil, err
	}
	return &adapter{client: client, endpoint: endpoint, key: key, protocol: protocol}, nil
}

func (a *adapter) Stream(ctx context.Context, request Request) (Stream, error) {
	if request.Model == "" || request.MaxOutputTokens < 0 || (a.protocol == ProtocolMessages && request.MaxOutputTokens == 0) {
		return nil, failure(ErrorOther, 0, "模型名或最大输出 token 数无效", "")
	}
	var body any
	var err error
	var parser streamParser
	resource := "chat/completions"
	if a.protocol == ProtocolMessages {
		resource = "messages"
		body, err = messagesRequest(request)
		parser = newMessagesParser(request)
	} else if a.protocol == ProtocolResponses {
		resource = "responses"
		body, err = responsesRequest(request)
		parser = &responsesParser{}
	} else {
		body, err = a.openAIRequest(request)
		parser = &openAIParser{calls: make(map[int]*callFragments)}
	}
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, failure(ErrorOther, 0, "请求包含无效 JSON", "")
	}
	address, err := endpointURL(a.endpoint, a.protocol, resource)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	response, err := a.do(requestCtx, http.MethodPost, address, encoded)
	if err != nil {
		cancel()
		return nil, err
	}
	return newStream(ctx, requestCtx, cancel, response, parser, a.key), nil
}

func (a *adapter) do(ctx context.Context, method, address string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, failure(ErrorOther, 0, "无法构造端点请求", "")
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
	}
	if a.key != "" {
		// Messages 官方、Ollama 云端和 LM Studio 都接受 Bearer；DeepSeek
		// 的调研只核实了 x-api-key，因此只在该预设上使用它。
		if a.protocol == ProtocolMessages && request.URL.Hostname() == "api.deepseek.com" {
			request.Header.Set("x-api-key", a.key)
		} else {
			request.Header.Set("Authorization", "Bearer "+a.key)
		}
	}
	if a.protocol == ProtocolMessages {
		request.Header.Set("anthropic-version", "2026-10-21")
	}
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err(), 0)
		}
		if isTimeout(err) {
			return nil, failure(ErrorTimeout, 0, "模型端点请求超时", "")
		}
		// net/http 的原始错误可能包含 URL 或非法请求头的值，不能作为公开 cause。
		if errors.Is(err, egress.ErrDenied) {
			return nil, failure(ErrorEgressDenied, 0, "出站地址不允许", "")
		}
		return nil, failure(ErrorUnreachable, 0, "无法连接模型端点或建立传输", "")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err(), response.StatusCode)
		}
		if isTimeout(readErr) {
			return nil, failure(ErrorTimeout, response.StatusCode, "读取模型端点响应超时", "")
		}
		if readErr != nil && len(data) == 0 {
			data = []byte("无法读取端点错误响应")
		}
		return nil, responseFailure(response.StatusCode, data, a.key)
	}
	return response, nil
}

func normalizedArguments(raw string) (json.RawMessage, error) {
	if len(bytes.TrimSpace([]byte(raw))) == 0 {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return nil, failure(ErrorInvalidResponse, 0, "工具参数不是完整的合法 JSON", "")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		return nil, failure(ErrorInvalidResponse, 0, "工具参数无法编码", "")
	}
	return json.RawMessage(compact.Bytes()), nil
}

func finishEvent(protocol Protocol, raw string) Event {
	reason := FinishOther
	if protocol == ProtocolOpenAI {
		switch raw {
		case "stop":
			reason = FinishNormal
		case "tool_calls":
			reason = FinishToolCalls
		case "length":
			reason = FinishOutputLimit
		}
	} else {
		switch raw {
		case "end_turn", "stop_sequence":
			reason = FinishNormal
		case "pause_turn":
			reason = FinishPause
		case "tool_use":
			reason = FinishToolCalls
		case "max_tokens", "model_context_window_exceeded":
			reason = FinishOutputLimit
		}
	}
	return Event{Type: EventDone, Finish: &Finish{Reason: reason, RawReason: raw}}
}
