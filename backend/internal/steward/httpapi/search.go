package httpapi

import (
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
	"net/http"
)

// WithWeb 共用工具与设置测试的搜索实现。
func (h *Handlers) WithWeb(service *web.Service) *Handlers { h.web = service; return h }

func (h *Handlers) searchTest(w http.ResponseWriter, r *http.Request, user identity.User) {
	httpserver.WriteJSON(w, 200, h.web.TestSearch(r.Context(), user))
}
