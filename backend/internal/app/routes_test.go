package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

// TestRoutesResolveToRegisteredPatterns 固定 REST 路由表：每个“方法 + 路径”都解析到原来的注册模式。
// 完成日志的 route_template 就是这个模式，改动它会破坏日志合同；缺失的路由会落入 /api 兜底。
func TestRoutesResolveToRegisteredPatterns(t *testing.T) {
	application := New(nil, config.Server{}, nil, testFrontend(), nil)
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	for _, route := range []struct {
		method, path, pattern string
	}{
		{http.MethodGet, "/healthz", "GET /healthz"},
		{http.MethodGet, "/readyz", "GET /readyz"},
		{http.MethodGet, "/api/v1/setup/status", "GET /api/v1/setup/status"},
		{http.MethodPost, "/api/v1/setup/admin", "POST /api/v1/setup/admin"},
		{http.MethodPost, "/api/v1/auth/login", "POST /api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout", "POST /api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/auth/me", "GET /api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/password", "POST /api/v1/auth/password"},
		{http.MethodGet, "/api/v1/users", "GET /api/v1/users"},
		{http.MethodPost, "/api/v1/users", "POST /api/v1/users"},
		{http.MethodPatch, "/api/v1/users/" + id, "PATCH /api/v1/users/{id}"},
		{http.MethodPost, "/api/v1/users/" + id + "/password", "POST /api/v1/users/{id}/password"},
		{http.MethodPost, "/api/v1/users/" + id + "/sessions/revoke", "POST /api/v1/users/{id}/sessions/revoke"},
		{http.MethodGet, "/api/v1/library-roots", "GET /api/v1/library-roots"},
		{http.MethodPost, "/api/v1/library-roots", "POST /api/v1/library-roots"},
		{http.MethodPatch, "/api/v1/library-roots/" + id, "PATCH /api/v1/library-roots/{id}"},
		{http.MethodPost, "/api/v1/library-roots/" + id + "/restore", "POST /api/v1/library-roots/{id}/restore"},
		{http.MethodGet, "/api/v1/library-root-operations", "GET /api/v1/library-root-operations"},
		{http.MethodGet, "/api/v1/scans", "GET /api/v1/scans"},
		{http.MethodPost, "/api/v1/scans", "POST /api/v1/scans"},
		{http.MethodGet, "/api/v1/scans/active", "GET /api/v1/scans/active"},
		{http.MethodGet, "/api/v1/scans/" + id, "GET /api/v1/scans/{id}"},
		{http.MethodPost, "/api/v1/scans/" + id + "/cancel", "POST /api/v1/scans/{id}/cancel"},
		{http.MethodGet, "/api/v1/scans/" + id + "/diagnostics", "GET /api/v1/scans/{id}/diagnostics"},
		{http.MethodGet, "/api/v1/operations", "GET /api/v1/operations"},
		{http.MethodPost, "/api/v1/operations/" + id + "/revert", "POST /api/v1/operations/{id}/revert"},
		{http.MethodGet, "/api/v1/catalog/status", "GET /api/v1/catalog/status"},
		{http.MethodGet, "/api/v1/artists/" + id + "/merged-sources", "GET /api/v1/artists/{id}/merged-sources"},
		{http.MethodPost, "/api/v1/tracks/summaries", "POST /api/v1/tracks/summaries"},
		{http.MethodGet, "/api/v1/search", "GET /api/v1/search"},
		{http.MethodGet, "/api/v1/search/suggestions", "GET /api/v1/search/suggestions"},
		{http.MethodGet, "/api/v1/releases", "GET /api/v1/releases"},
		{http.MethodGet, "/api/v1/releases/" + id + "/evidence", "GET /api/v1/releases/{id}/evidence"},
		{http.MethodGet, "/api/v1/releases/" + id + "/corrections", "GET /api/v1/releases/{id}/corrections"},
		{http.MethodPatch, "/api/v1/releases/" + id + "/corrections", "PATCH /api/v1/releases/{id}/corrections"},
		{http.MethodPost, "/api/v1/releases/" + id + "/group", "POST /api/v1/releases/{id}/group"},
		{http.MethodGet, "/api/v1/releases/" + id, "GET /api/v1/releases/{id}"},
		{http.MethodGet, "/api/v1/release-groups", "GET /api/v1/release-groups"},
		{http.MethodGet, "/api/v1/release-groups/" + id, "GET /api/v1/release-groups/{id}"},
		{http.MethodGet, "/api/v1/artworks/" + id, "GET /api/v1/artworks/{id}"},
		{http.MethodPost, "/api/v1/me/plays", "POST /api/v1/me/plays"},
		{http.MethodGet, "/api/v1/me/plays/recent-releases", "GET /api/v1/me/plays/recent-releases"},
		{http.MethodDelete, "/api/v1/me/plays", "DELETE /api/v1/me/plays"},
		{http.MethodGet, "/api/v1/me/plays/tracks", "GET /api/v1/me/plays/tracks"},
		{http.MethodGet, "/api/v1/me/plays/queue", "GET /api/v1/me/plays/queue"},
		{http.MethodDelete, "/api/v1/me/plays/tracks/" + id, "DELETE /api/v1/me/plays/tracks/{track_id}"},
		{http.MethodGet, "/some/page", "/"},
		{http.MethodGet, "/api/v1/not-registered", "/api/"},
		{http.MethodGet, "/api", "/api"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, pattern := application.mux.Handler(request); pattern != route.pattern {
			t.Errorf("%s %s resolved to %q, want %q", route.method, route.path, pattern, route.pattern)
		}
	}
}
