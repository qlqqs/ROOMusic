package web

import (
	"github.com/qlqq/roomusic/backend/internal/steward/egress"
	"net/http"
)

// C8TestClient 仅在测试构建中提供外部 HTTP 整链的客户端注入点。
func C8TestClient(s *Service, factory func(egress.Options) (*http.Client, error)) {
	s.newClient = factory
}
