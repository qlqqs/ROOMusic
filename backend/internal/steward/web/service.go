// Package web 提供管家的搜索、网页正文与音乐元数据工具。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/egress"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const maxBodyBytes = 2 << 20
const requestTimeout = 20 * time.Second
const genericUserAgent = "Mozilla/5.0 (compatible; WebReader/1.0)"

// SettingsReader 只读取调用者本人的搜索设置。
type SettingsReader interface {
	GetUserSettings(context.Context, string) (runtime.UserSettings, error)
}

// EndpointClient 复用模型端点的管理员和成员出站策略。
type EndpointClient func(context.Context, identity.User) (*http.Client, error)

// Service 的客户端和时钟可在包内替换；不向用户暴露源站配置。
type Service struct {
	settings       SettingsReader
	secrets        *secret.Store
	endpointClient EndpointClient
	logger         *slog.Logger
	newClient      func(egress.Options) (*http.Client, error)
	musicLimiter   *requestLimiter
}

// New 装配依赖，不发出请求。
func New(settings SettingsReader, secrets *secret.Store, endpointClient EndpointClient, logger *slog.Logger) *Service {
	return &Service{settings: settings, secrets: secrets, endpointClient: endpointClient, logger: logger,
		newClient: egress.NewClient, musicLimiter: globalMusicLimiter}
}

func failure(code string) *tool.Error {
	messages := map[string]string{
		"search_not_configured":    "尚未配置外部搜索",
		"searxng_json_disabled":    "这个实例没有开启 JSON 输出",
		"search_key_invalid":       "搜索密钥无效或需要重新填写",
		"search_quota_exceeded":    "搜索服务额度已用完或达到套餐限制",
		"search_unreachable":       "搜索服务暂时无法访问",
		"egress_denied":            "出站地址不允许访问",
		"fetch_blocked_address":    "只能读取不含用户名和密码的公网 HTTP(S) 地址，地址最多 2048 字节",
		"fetch_blocked_port":       "网页读取只允许 80 和 443 端口",
		"fetch_unsupported_type":   "网页类型或字符编码不受支持",
		"fetch_too_many_redirects": "网页重定向超过 5 次",
		"fetch_timeout":            "网页读取超过 20 秒",
		"fetch_failed":             "网页读取失败",
		"rate_limited":             "外部资料请求达到频率上限或排队超时，请稍后重试",
		"source_unavailable":       "音乐数据源暂时不可用，请稍后重试",
	}
	return &tool.Error{Code: tool.ErrorCode(code), Message: messages[code]}
}

func errorCode(err error) string {
	var typed *tool.Error
	if errors.As(err, &typed) {
		return string(typed.Code)
	}
	return "source_unavailable"
}

func (s *Service) logRequest(ctx context.Context, source, address string, started time.Time, count int, err error) {
	if s.logger == nil {
		return
	}
	domain := ""
	if u, parseErr := url.Parse(address); parseErr == nil {
		domain = u.Hostname()
	}
	code := ""
	if err != nil {
		code = errorCode(err)
	}
	s.logger.InfoContext(ctx, "外部资料请求结束", "event", "steward.web.request.finished", "message", "外部资料请求结束", "module", "steward",
		"source", source, "domain", domain, "duration_ms", time.Since(started).Milliseconds(), "result_count", count, "error_code", code)
}

func request(ctx context.Context, method, address string, body io.Reader, agent string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func readJSON(response *http.Response, target any) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBodyBytes {
		return errors.New("外部响应超过读取上限")
	}
	return json.Unmarshal(data, target)
}
