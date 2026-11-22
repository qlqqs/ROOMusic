package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func (h *Handlers) outboundClient(ctx context.Context, user identity.User) (*http.Client, error) {
	return OutboundClient(ctx, user, h.store)
}

// OutboundClient 为模型端点和 SearXNG 复用同一出站策略。
func OutboundClient(ctx context.Context, user identity.User, store *runtime.Store) (*http.Client, error) {
	options := egress.Options{Policy: egress.Unrestricted, MaxRedirects: 0, Timeout: 5 * time.Minute}
	if user.Role != "admin" {
		entries, err := store.ListAllowlist(ctx)
		if err != nil {
			return nil, err
		}
		allowed := make(map[allowlistAddress]bool, len(entries))
		for _, entry := range entries {
			allowed[allowlistAddress{entry.Host, entry.Port}] = true
		}
		options.Policy = egress.PublicOrAllowlist
		options.Allowlist = func(host string, port int) bool { return allowed[allowlistAddress{normalizeHost(host), port}] }
	}
	client, err := egress.NewClient(options)
	if err != nil {
		return nil, err
	}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := checkRedirect(request, via); err != nil {
			// 模型端点不跟随任何重定向，避免转发 x-api-key；拒绝按连接失败分类。
			return errors.New("模型端点返回重定向，请检查接口地址")
		}
		return nil
	}
	return client, nil
}

func preflight(ctx context.Context, client *http.Client, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return egress.CheckEndpoint(ctx, client, endpoint)
}

func modelProvider(client *http.Client, endpoint, key, protocol string) (provider.Provider, error) {
	if protocol == string(provider.ProtocolResponses) {
		return provider.NewResponses(client, endpoint, key)
	}
	if protocol == string(provider.ProtocolMessages) {
		return provider.NewMessages(client, endpoint, key)
	}
	return provider.NewOpenAI(client, endpoint, key)
}
