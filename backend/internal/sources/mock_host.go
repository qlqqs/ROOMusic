package sources

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

// MockHost 只为实例配置的 mock 主机和有效端口额外放行，零值沿用公网策略。
type MockHost struct {
	host string
	port int
}

func NewMockHost(base string) MockHost {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return MockHost{}
	}
	return MockHost{u.Hostname(), effectivePort(u)}
}

func effectivePort(u *url.URL) int {
	if u.Port() != "" {
		p, _ := strconv.Atoi(u.Port())
		return p
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

func (m MockHost) allows(host string, port int) bool {
	return m.host != "" && strings.EqualFold(host, m.host) && port == m.port
}

func (m MockHost) Matches(u *url.URL) bool {
	return m.allows(u.Hostname(), effectivePort(u))
}

func (m MockHost) EgressOptions(opts egress.Options) egress.Options {
	if m.host != "" {
		opts.Policy, opts.Allowlist = egress.PublicOrAllowlist, m.allows
	}
	return opts
}
