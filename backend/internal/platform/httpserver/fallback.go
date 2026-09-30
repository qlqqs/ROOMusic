package httpserver

import (
	"net/http"
	"strings"
)

// API 兜底模式比 "/" 更具体：未注册的 /api 请求返回 JSON 错误，不落入前端页面。
const (
	apiFallbackPattern     = "/api/"
	apiRootFallbackPattern = "/api"
)

var apiProbeMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// RegisterAPIFallback 在 /api 与 /api/ 上注册兜底 handler。兜底模式比 "/" 更具体，所以未注册的 API 请求不会落入前端页面。
func RegisterAPIFallback(mux *http.ServeMux, handler http.Handler) {
	mux.Handle(apiFallbackPattern, handler)
	mux.Handle(apiRootFallbackPattern, handler)
}

func isAPIFallbackPattern(pattern string) bool {
	return pattern == apiFallbackPattern || pattern == apiRootFallbackPattern
}

// APINotFoundHandler 在路径已注册但方法不符时返回 405 与 Allow，否则返回 404。
func APINotFoundHandler(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if allowed := allowedAPIMethods(mux, request); len(allowed) > 0 {
			responseWriter.Header().Set("Allow", strings.Join(allowed, ", "))
			WriteError(responseWriter, request, http.StatusMethodNotAllowed, "method_not_allowed", "接口不支持该请求方法")
			return
		}
		WriteError(responseWriter, request, http.StatusNotFound, "not_found", "接口不存在")
	})
}

func allowedAPIMethods(mux *http.ServeMux, request *http.Request) []string {
	var allowed []string
	for _, method := range apiProbeMethods {
		probe := request.WithContext(request.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" && !isAPIFallbackPattern(pattern) {
			allowed = append(allowed, method)
		}
	}
	return allowed
}
