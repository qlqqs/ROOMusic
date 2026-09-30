package httpserver

import (
	"encoding/json"
	"net/http"
)

// APIError 是 REST 错误信封：稳定的机器可读 code、可展示的 message 与关联日志的 request_id。
type APIError struct {
	Error     APIErrorDetail `json:"error"`
	RequestID string         `json:"request_id"`
}

// APIErrorDetail 是 APIError 的错误详情。
type APIErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError 写出统一错误信封；request_id 取自中间件已回写的 X-Request-ID 响应头。
func WriteError(responseWriter http.ResponseWriter, request *http.Request, status int, code, message string) {
	requestID := responseWriter.Header().Get("X-Request-ID")
	WriteJSON(responseWriter, status, APIError{Error: APIErrorDetail{Code: code, Message: message}, RequestID: requestID})
}

// DecodeJSON 以 1 MiB 上限解码请求体；失败时写出 400 invalid_json 并返回 false。
func DecodeJSON(responseWriter http.ResponseWriter, request *http.Request, destination any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 1<<20)).Decode(destination); err != nil {
		WriteError(responseWriter, request, 400, "invalid_json", "请求格式无效")
		return false
	}
	return true
}

// WriteJSON 以 application/json 写出状态码和值。
func WriteJSON(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(value); err != nil {
		return
	}
}
