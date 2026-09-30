package httpserver

import "net/http"

// HealthHandler 报告进程存活，不检查任何依赖。
func HealthHandler(responseWriter http.ResponseWriter, _ *http.Request) {
	WriteJSON(responseWriter, http.StatusOK, map[string]string{"status": "ok"})
}

// ReadyHandler 报告就绪状态：ready 返回 true 时 200 ready，否则 503 not_ready。
func ReadyHandler(ready func() bool) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, _ *http.Request) {
		if ready() {
			WriteJSON(responseWriter, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		WriteJSON(responseWriter, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
	}
}
