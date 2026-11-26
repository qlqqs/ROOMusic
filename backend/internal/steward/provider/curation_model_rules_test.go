package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func curationRuleProvider(t *testing.T, protocol Protocol, client *http.Client, endpoint string) Provider {
	t.Helper()
	if protocol != ProtocolResponses {
		return makeProvider(t, protocol, client, endpoint)
	}
	p, err := NewResponses(client, endpoint, syntheticKey)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func curationRuleRead(t *testing.T, p Provider) *Error {
	t.Helper()
	stream, err := p.Stream(SingleAttempt(t.Context()), testRequest())
	if err == nil {
		defer stream.Close()
		for err == nil {
			_, err = stream.Next()
		}
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("应返回结构化供应方错误，实际 %v", err)
	}
	if strings.Contains(failure.Error(), syntheticKey) || strings.Contains(failure.Error(), "synthetic-private-header") {
		t.Fatalf("错误暴露了合成凭据：%+v", failure)
	}
	return failure
}

func TestCurationModelRulesHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name                string
		status              int
		code, message, want string
	}{
		{"401", 401, "invalid_api_key", "", "configuration"},
		{"403", 403, "permission_error", "", "configuration"},
		{"普通404", 404, "", "", "request_rejected"},
		{"模型不存在", 404, "model_not_found", "", "configuration"},
		{"Ollama模型不存在", 404, "", `model "synthetic" not found`, "configuration"},
		{"普通400", 400, "invalid_request_error", "", "request_rejected"},
		{"工具拒绝400", 400, "tool_unsupported", "", "configuration"},
		{"402", 402, "", "", "provider_quota"},
		{"余额400", 400, "insufficient_balance", "", "provider_quota"},
		{"余额429", 429, "insufficient_quota", "", "provider_quota"},
		{"中文余额429", 429, "", "余额不足", "provider_quota"},
		{"普通429", 429, "rate_limit_exceeded", "quota rate limit", "temporary"},
		{"408", 408, "", "", "temporary"},
		{"500", 500, "", "", "temporary"},
		{"502", 502, "", "", "temporary"},
		{"503", 503, "", "", "temporary"},
		{"504", 504, "", "", "temporary"},
		{"529", 529, "", "", "temporary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, tc.code,
					tc.message+" "+syntheticKey+" Authorization: synthetic-private-header")
			}))
			defer server.Close()
			got := curationRuleRead(t, curationRuleProvider(t, ProtocolOpenAI, server.Client(), server.URL))
			if got.BackgroundClass() != tc.want || got.Code != tc.code || got.StatusCode != tc.status {
				t.Fatalf("HTTP %d：分类=%s 稳定码=%q，期望 %s/%q，错误=%+v", got.StatusCode, got.BackgroundClass(), got.Code, tc.want, tc.code, got)
			}
		})
	}
}

func TestCurationModelRulesStreamFailures(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages, ProtocolResponses} {
		t.Run(string(protocol), func(t *testing.T) {
			type scenario struct {
				name, wire, class, code string
				kind                    ErrorKind
				recovery                bool
			}
			scenarios := []scenario{
				{"余额事件", namedFrame("error", `{"error":{"code":"insufficient_quota","message":"`+syntheticKey+` Authorization: synthetic-private-header"}}`), "provider_quota", "insufficient_quota", ErrorOther, false},
				{"配置事件", namedFrame("error", `{"error":{"type":"authentication_error","message":"`+syntheticKey+`"}}`), "configuration", "authentication_error", ErrorAuthentication, false},
				{"限流事件", namedFrame("error", `{"type":"error","code":"rate_limit_exceeded","message":"稍后重试"}`), "temporary", "rate_limit_exceeded", ErrorRateLimit, true},
				{"半个SSE后断开", "data: {\"choices\":[", "temporary", "", ErrorStreamInterrupted, true},
				{"无效JSON", dataFrame("{]"), "empty_reply", "", ErrorInvalidResponse, false},
			}
			if protocol == ProtocolResponses {
				scenarios = append(scenarios, scenario{"嵌套失败事件",
					namedFrame("response.failed", `{"type":"response.failed","response":{"error":{"code":"insufficient_balance","message":"`+syntheticKey+`"}}}`),
					"provider_quota", "insufficient_balance", ErrorOther, false})
			}
			for _, tc := range scenarios {
				t.Run(tc.name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						w.Header().Set("Retry-After", "120")
						w.Header().Set("x-ratelimit-reset-tokens", "10m")
						if tc.name == "半个SSE后断开" {
							w.Header().Set("Content-Length", "999")
						}
						io.WriteString(w, tc.wire)
					}))
					defer server.Close()
					got := curationRuleRead(t, curationRuleProvider(t, protocol, server.Client(), server.URL))
					if got.StatusCode != 200 || got.BackgroundClass() != tc.class || got.Code != tc.code || got.Kind != tc.kind {
						t.Errorf("流错误分类=%s 稳定码=%q 错误=%+v，期望 %s/%q/%s", got.BackgroundClass(), got.Code, got, tc.class, tc.code, tc.kind)
					}
					if tc.recovery {
						if got.RetryDelay == nil {
							t.Fatal("缺少上游重置时间")
						}
						if delay := *got.RetryDelay; delay <= 599*time.Second || delay > 600*time.Second {
							t.Errorf("应保留较晚的 600 秒重置时刻，实际 %v", delay)
						}
					}
				})
			}
		})
	}
}

type curationRuleTransport func(*http.Request) (*http.Response, error)

func (fn curationRuleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestCurationModelRulesTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"连接失败", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic refused")}, ErrorUnreachable},
		{"超时", context.DeadlineExceeded, ErrorTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: curationRuleTransport(func(*http.Request) (*http.Response, error) {
				return nil, tc.err
			})}
			got := curationRuleRead(t, curationRuleProvider(t, ProtocolOpenAI, client, "http://127.0.0.1"))
			if got.Kind != tc.want || got.BackgroundClass() != "temporary" || got.StatusCode != 0 {
				t.Fatalf("期望 %s/temporary，无 HTTP 状态，实际 %+v", tc.want, got)
			}
		})
	}
}

func TestCurationModelRulesRecoveryHeaders(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, retry, openAI, messages string
		want                          time.Duration
	}{
		{"秒数", "600", "", "", 600 * time.Second},
		{"HTTP日期", now.Add(time.Minute).Format(http.TimeFormat), "", "", time.Minute},
		{"取最晚时刻", "5", "1m10s", now.Add(95 * time.Second).Format(time.RFC3339), 95 * time.Second},
		{"无效值", "invalid", "500", "invalid", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tc.retry)
			h.Set("X-Ratelimit-Reset-Requests", tc.openAI)
			h.Set("Anthropic-Ratelimit-Tokens-Reset", tc.messages)
			got := upstreamDelay(h, now)
			if got == nil && tc.want == -1 {
				return
			}
			if got == nil || *got != tc.want {
				t.Fatalf("恢复时长 %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestCurationModelRulesRequestEstimate(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolMessages, ProtocolResponses} {
		t.Run(string(protocol), func(t *testing.T) {
			estimate := func(r Request) int64 {
				t.Helper()
				size, err := RequestTokens(protocol, "http://127.0.0.1", r)
				if err != nil {
					t.Fatal(err)
				}
				return size
			}
			r := testRequest()
			base := estimate(r)
			// 两段 ASCII 分别计 2 和 1，中日韩文各计 2，JSON 转义各计 2。
			r.Messages[0].Content[0].Text += "abcde:7中日한\"\\\n"
			const extra = 2 + 1 + 1 + 6 + 6
			if got := estimate(r); got != base+extra {
				t.Fatalf("中日韩文和转义后的估算增量 %d，期望 %d", got-base, extra)
			}
			withTool := estimate(r)
			r.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"曲名":{"type":"string"}}}`)
			withSchema := estimate(r)
			r.Tools = nil
			if withSchema <= withTool || estimate(r) >= withTool {
				t.Fatal("完整协议预算应计入工具及工具 schema")
			}
			withoutTool := estimate(r)
			r.MaxOutputTokens += 7
			if got := estimate(r); got != withoutTool+7 {
				t.Fatalf("预留输出应增加 7，实际增加 %d", got-withoutTool)
			}
		})
	}
}
