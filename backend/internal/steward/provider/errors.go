package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ErrorKind 是适配器失败的稳定分类。
type ErrorKind string

const (
	// ErrorTimeout 表示连接或读取模型响应超时。
	ErrorTimeout ErrorKind = "timeout"
	// ErrorEgressDenied 表示出站策略拒绝当前地址或重定向。
	ErrorEgressDenied ErrorKind = "egress_denied"
	// ErrorUnreachable 表示连接或传输建立失败。
	ErrorUnreachable ErrorKind = "unreachable"
	// ErrorAuthentication 表示认证失败。
	ErrorAuthentication ErrorKind = "authentication"
	// ErrorModelNotFound 表示明确的模型不存在错误。
	ErrorModelNotFound ErrorKind = "model_not_found"
	// ErrorToolUnsupported 表示端点明确拒绝工具调用能力。
	ErrorToolUnsupported ErrorKind = "tool_unsupported"
	// ErrorRateLimit 表示限流或额度耗尽，不承诺可重试。
	ErrorRateLimit ErrorKind = "rate_limit"
	// ErrorInvalidResponse 表示端点返回无法解析或不符合协议的内容。
	ErrorInvalidResponse ErrorKind = "invalid_response"
	// ErrorStreamInterrupted 表示未收到协议结束标志就断开。
	ErrorStreamInterrupted ErrorKind = "stream_interrupted"
	// ErrorOther 表示其他失败，包含未核实的错误格式。
	ErrorOther ErrorKind = "other"
)

// Error 是可用 errors.As 读取的失败，不保留密钥、请求头或原始请求。
type Error struct {
	// Kind 是稳定分类。
	Kind ErrorKind `json:"kind"`
	// StatusCode 是已收到的 HTTP 状态，没有响应时为零。
	StatusCode int `json:"status_code,omitempty"`
	// Message 是已去除密钥和敏感头、最多 500 个字符的原始错误信息。
	Message string `json:"message"`
}

// Error 返回可安全展示的分类信息。
func (err *Error) Error() string {
	return fmt.Sprintf("provider %s (HTTP %d): %s", err.Kind, err.StatusCode, err.Message)
}

var sensitiveHeader = regexp.MustCompile(`(?im)(?:authorization|proxy-authorization|x-api-key|api-key|cookie|set-cookie|anthropic-version|content-type|accept)\s*["']?\s*[:=][^\r\n]*`)

func failure(kind ErrorKind, status int, message, key string) *Error {
	if key != "" {
		message = strings.ReplaceAll(message, key, "[已隐藏]")
		encoded, _ := json.Marshal(key)
		message = strings.ReplaceAll(message, string(encoded[1:len(encoded)-1]), "[已隐藏]")
	}
	message = sensitiveHeader.ReplaceAllString(message, "[请求头已隐藏]")
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return &Error{Kind: kind, StatusCode: status, Message: message}
}

func responseFailure(status int, data []byte, key string) *Error {
	kind := ErrorOther
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	var detail struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	}
	message := string(data)
	if json.Unmarshal(data, &envelope) == nil && len(envelope.Error) > 0 {
		if json.Unmarshal(envelope.Error, &detail) == nil && detail.Message != "" {
			message = detail.Message
		} else {
			var plain string
			if json.Unmarshal(envelope.Error, &plain) == nil {
				message = plain
			}
		}
	} else {
		// Responses 的 error 流事件把 code/message 放在事件顶层。
		if json.Unmarshal(data, &detail) == nil && detail.Type == "error" && detail.Message != "" {
			message = detail.Message
		}
	}
	var code string
	_ = json.Unmarshal(detail.Code, &code)
	switch {
	case status == 408 || status == 504 || detail.Type == "timeout_error":
		kind = ErrorTimeout
	case status == 401 || detail.Type == "authentication_error":
		kind = ErrorAuthentication
	case status == 429 || detail.Type == "rate_limit_error" || code == "rate_limit_exceeded":
		kind = ErrorRateLimit
	case code == "model_not_found":
		kind = ErrorModelNotFound
	case status == 404 && strings.HasPrefix(message, "model \"") && strings.Contains(message, "not found"):
		// 调研核实的 Ollama 模型错误；不把所有 404 推定为模型不存在。
		kind = ErrorModelNotFound
	case code == "tool_unsupported" || strings.Contains(message, "does not support tools"):
		// 仅采用调研核实的能力拒绝文本，不把普通 400 当成不支持工具。
		kind = ErrorToolUnsupported
	}
	return failure(kind, status, message, key)
}

func hasError(data []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	return (len(object["error"]) > 0 && string(object["error"]) != "null") || string(object["type"]) == `"error"`
}

func isTimeout(err error) bool {
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout())
}

func contextFailure(err error, status int) error {
	if isTimeout(err) {
		return failure(ErrorTimeout, status, "模型端点请求超时", "")
	}
	return err
}
