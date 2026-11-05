package identity

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func requireOriginDecision(t *testing.T, guard *Guard, host, origin string, headers map[string]string, wantCode string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Host = host
	request.Header.Set("Origin", origin)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	response.Header().Set("X-Request-ID", "origin-unit-test")
	allowed := guard.RequireSameOrigin(response, request)
	if allowed != (wantCode == "") {
		t.Fatalf("来源 %q（Host=%q）allowed=%v，期望错误码 %q", origin, host, allowed, wantCode)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("来源校验不应写 Cookie")
	}
	if wantCode == "" {
		if response.Code != http.StatusOK || response.Body.Len() != 0 {
			t.Fatalf("允许的来源不应写响应：HTTP %d %s", response.Code, response.Body.String())
		}
		return
	}
	if response.Code != http.StatusForbidden || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("来源错误响应不符：HTTP %d headers=%v", response.Code, response.Header())
	}
	message := "请求来源不受信任"
	if wantCode == "origin_required" {
		message = "请求缺少来源校验"
	}
	want := map[string]any{"error": map[string]any{"code": wantCode, "message": message}, "request_id": "origin-unit-test"}
	var body map[string]any
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&body); err != nil || !reflect.DeepEqual(body, want) {
		t.Fatalf("来源错误信封 = %v, %v，期望 %v", body, err, want)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("错误响应必须恰好包含一个 JSON 信封：%v", err)
	}
}

func TestGuardAllowedOrigins(t *testing.T) {
	guard := NewGuardWithAllowedOrigins(nil, []string{"http://192.168.50.206:8080", "https://music.example.com"})
	for _, tc := range []struct {
		name, host, origin, wantCode string
		headers                      map[string]string
	}{
		{"http", "backend.internal:8080", "http://192.168.50.206:8080", "", nil},
		{"https", "backend.internal:8080", "https://music.example.com", "", nil},
		{"normalized", "evil.example", "HTTPS://Music.Example.COM:443", "", nil},
		{"http scheme mismatch", "music.example.com", "http://music.example.com", "origin_forbidden", nil},
		{"https scheme mismatch", "192.168.50.206:8080", "https://192.168.50.206:8080", "origin_forbidden", nil},
		{"other host matches Host", "other.example", "https://other.example", "origin_forbidden", nil},
		{"wrong port", "music.example.com:8443", "https://music.example.com:8443", "origin_forbidden", nil},
		{"path", "music.example.com", "https://music.example.com/app", "origin_forbidden", nil},
		{"trailing slash", "music.example.com", "https://music.example.com/", "origin_forbidden", nil},
		{"credentials", "music.example.com", "https://user@music.example.com", "origin_forbidden", nil},
		{"query", "music.example.com", "https://music.example.com?x=1", "origin_forbidden", nil},
		{"fragment", "music.example.com", "https://music.example.com#x", "origin_forbidden", nil},
		{"null", "music.example.com", "null", "origin_forbidden", nil},
		{"file", "music.example.com", "file:///music", "origin_forbidden", nil},
		{"missing", "music.example.com", "", "origin_required", nil},
		{"forwarded headers cannot veto", "evil.example", "https://music.example.com", "", map[string]string{"X-Forwarded-Host": "evil.example", "X-Forwarded-Proto": "http", "Forwarded": "host=evil.example;proto=http"}},
		{"forwarded headers cannot allow", "music.example.com", "http://evil.example", "origin_forbidden", map[string]string{"X-Forwarded-Host": "music.example.com", "X-Forwarded-Proto": "https", "Forwarded": "host=music.example.com;proto=https"}},
		{"forwarded proto cannot fix mismatch", "music.example.com", "http://music.example.com", "origin_forbidden", map[string]string{"X-Forwarded-Proto": "https"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireOriginDecision(t, guard, tc.host, tc.origin, tc.headers, tc.wantCode)
		})
	}
}

func TestGuardAllowedOriginsRequiresNonemptyConfiguration(t *testing.T) {
	for _, origins := range [][]string{nil, {}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("空清单属于装配错误，必须 panic")
				}
			}()
			NewGuardWithAllowedOrigins(nil, origins)
		}()
	}
}

func TestGuardLegacySameOrigin(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		secure                        bool
		publicURL, host, origin, code string
	}{
		{"http scheme allowed", false, "", "roomusic.test", "http://roomusic.test", ""},
		{"http scheme rejects https", false, "", "roomusic.test", "https://roomusic.test", "origin_forbidden"},
		{"https scheme allowed", true, "", "roomusic.test", "https://roomusic.test", ""},
		{"https scheme rejects http", true, "", "roomusic.test", "http://roomusic.test", "origin_forbidden"},
		{"public host overrides Host", false, "http://localhost:5173", "localhost:8080", "http://localhost:5173", ""},
		{"public host rejects request host", false, "http://localhost:5173", "localhost:8080", "http://localhost:8080", "origin_forbidden"},
		{"public scheme does not override switch", false, "https://music.example.com", "backend.internal", "http://music.example.com", ""},
		{"public scheme still follows switch", false, "https://music.example.com", "backend.internal", "https://music.example.com", "origin_forbidden"},
		{"request host allowed", false, "", "localhost:5173", "http://localhost:5173", ""},
		{"request host rejects another port", false, "", "localhost:5173", "http://localhost:8080", "origin_forbidden"},
		{"request host rejects another host", false, "", "roomusic.test", "http://evil.example", "origin_forbidden"},
		{"missing", false, "", "roomusic.test", "", "origin_required"},
		{"path", false, "", "roomusic.test", "http://roomusic.test/", "origin_forbidden"},
		{"credentials", false, "", "roomusic.test", "http://user@roomusic.test", "origin_forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireOriginDecision(t, NewGuard(nil, tc.secure, tc.publicURL), tc.host, tc.origin, nil, tc.code)
		})
	}
}

func TestGuardSessionCookies(t *testing.T) {
	allowed := NewGuardWithAllowedOrigins(nil, []string{"http://192.168.50.206:8080", "https://music.example.com"})
	for _, tc := range []struct {
		name, origin string
		guard        *Guard
		secure       bool
	}{
		{"allowed http", "http://192.168.50.206:8080", allowed, false},
		{"allowed https", "https://music.example.com", allowed, true},
		{"normalized https", "HTTPS://Music.Example.com:443", allowed, true},
		{"legacy insecure ignores origin", "https://roomusic.test", NewGuard(nil, false, ""), false},
		{"legacy secure ignores origin", "http://roomusic.test", NewGuard(nil, true, ""), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, clear := range []bool{false, true} {
				name := "set"
				if clear {
					name = "clear"
				}
				t.Run(name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "/", nil)
					request.Host = "evil.example"
					request.Header.Set("Origin", tc.origin)
					// 故意伪造相反的转发协议：Cookie 不得依赖这些头或 request.TLS。
					forwardedProto := "https"
					if tc.secure {
						forwardedProto = "http"
					}
					request.Header.Set("X-Forwarded-Proto", forwardedProto)
					request.Header.Set("X-Forwarded-Host", "evil.example")
					request.Header.Set("Forwarded", "proto="+forwardedProto+";host=evil.example")
					response := httptest.NewRecorder()
					before := time.Now().Add(24*time.Hour - time.Second)
					if clear {
						tc.guard.ClearSessionCookie(response, request)
					} else {
						tc.guard.SetSessionCookie(response, request, "test-session-token")
					}
					cookies := response.Result().Cookies()
					if len(cookies) != 1 {
						t.Fatalf("期望一个会话 Cookie，得到 %d", len(cookies))
					}
					cookie := cookies[0]
					if cookie.Name != SessionCookieName || cookie.Secure != tc.secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
						t.Fatalf("Cookie 属性不符：name=%q secure=%v（期望 %v）httpOnly=%v sameSite=%v path=%q domain=%q", cookie.Name, cookie.Secure, tc.secure, cookie.HttpOnly, cookie.SameSite, cookie.Path, cookie.Domain)
					}
					if clear {
						if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.Expires.IsZero() {
							t.Fatal("退出 Cookie 必须清空值、MaxAge=-1 且不设置 Expires")
						}
					} else if cookie.Value != "test-session-token" || cookie.MaxAge != 0 || cookie.Expires.Before(before) || cookie.Expires.After(time.Now().Add(24*time.Hour)) {
						t.Fatal("登录 Cookie 必须保留令牌、24 小时 Expires 与默认 MaxAge")
					}
				})
			}
		})
	}
}

func TestGuardSameHost(t *testing.T) {
	guard := NewGuardWithSameHost(nil).WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, tc := range []struct {
		name, host, origin, code string
	}{
		{"LAN http", "192.168.50.206:18090", "http://192.168.50.206:18090", ""},
		{"public https", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888", ""},
		{"https on LAN port", "192.168.50.206:18090", "https://192.168.50.206:18090", ""},
		{"uppercase Host", "MUSIC.QLQQS.COM:8888", "https://music.qlqqs.com:8888", ""},
		{"uppercase Origin", "music.qlqqs.com:8888", "HTTPS://Music.Qlqqs.COM:8888", ""},
		{"https default port", "music.example.com:443", "https://music.example.com", ""},
		{"http default port", "music.example.com:80", "http://music.example.com", ""},
		{"Origin default port", "music.example.com", "https://music.example.com:443", ""},
		{"IPv6", "[fd00::1]:8090", "http://[fd00::1]:8090", ""},
		{"IPv6 uppercase and default port", "[FD00::AB]:443", "https://[fd00::ab]", ""},
		{"other host", "10.0.0.1:8080", "https://music.qlqqs.com:8888", "origin_forbidden"},
		{"lost port", "music.qlqqs.com", "https://music.qlqqs.com:8888", "origin_forbidden"},
		{"http does not discard 443", "x:443", "http://x", "origin_forbidden"},
		{"https does not discard 80", "x:80", "https://x", "origin_forbidden"},
		{"missing Host", "", "https://music.qlqqs.com:8888", "origin_forbidden"},
		{"null Origin", "music.qlqqs.com:8888", "null", "origin_forbidden"},
		{"path", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888/app", "origin_forbidden"},
		{"trailing slash", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888/", "origin_forbidden"},
		{"credentials", "music.qlqqs.com:8888", "https://user@music.qlqqs.com:8888", "origin_forbidden"},
		{"query", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888?x=1", "origin_forbidden"},
		{"fragment", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888#x", "origin_forbidden"},
		{"file scheme", "music.qlqqs.com:8888", "file://music.qlqqs.com:8888", "origin_forbidden"},
		{"non-ASCII host", "音乐.example", "https://音乐.example", "origin_forbidden"},
		{"wildcard host", "*.example.com", "https://*.example.com", "origin_forbidden"},
		{"bare IPv6 Host", "fd00::1:8090", "http://[fd00::1]:8090", "origin_forbidden"},
		{"IPv6 zone", "[fd00::1%eth0]:8090", "http://[fd00::1]:8090", "origin_forbidden"},
		{"trailing dot is distinct", "music.example.com.", "https://music.example.com", "origin_forbidden"},
		{"IPv6 text is distinct", "[fd00:0:0:0:0:0:0:1]", "http://[fd00::1]", "origin_forbidden"},
		{"missing Origin", "music.qlqqs.com:8888", "", "origin_required"},
		{"missing Origin and Host", "", "", "origin_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireOriginDecision(t, guard, tc.host, tc.origin, nil, tc.code)
		})
	}
}

func TestGuardSameHostIgnoresForwardedHeaders(t *testing.T) {
	guard := NewGuardWithSameHost(nil).WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, tc := range []struct {
		name, host, code, forwardedHost, forwardedProto string
	}{
		{"mismatch cannot be repaired", "10.0.0.1:8080", "origin_forbidden", "music.qlqqs.com:8888", "https"},
		{"missing Host cannot be supplied", "", "origin_forbidden", "music.qlqqs.com:8888", "https"},
		{"match cannot be vetoed", "music.qlqqs.com:8888", "", "evil.example:80", "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, headers := range map[string]map[string]string{
				"X-Forwarded-Host":  {"X-Forwarded-Host": tc.forwardedHost},
				"X-Forwarded-Proto": {"X-Forwarded-Proto": tc.forwardedProto},
				"X-Forwarded-For":   {"X-Forwarded-For": "192.168.50.206"},
				"Forwarded":         {"Forwarded": "host=" + tc.forwardedHost + ";proto=" + tc.forwardedProto},
				"all":               {"X-Forwarded-Host": tc.forwardedHost, "X-Forwarded-Proto": tc.forwardedProto, "X-Forwarded-For": "192.168.50.206", "Forwarded": "host=" + tc.forwardedHost + ";proto=" + tc.forwardedProto},
				// Go 已解析的 request.Host 才是权威，不另读 Header 映射中的 Host。
				"Host header map": {"Host": tc.forwardedHost},
			} {
				t.Run(name, func(t *testing.T) {
					requireOriginDecision(t, guard, tc.host, "https://music.qlqqs.com:8888", headers, tc.code)
				})
			}
		})
	}
}

func TestGuardSameHostSessionCookies(t *testing.T) {
	guard := NewGuardWithSameHost(nil)
	for _, tc := range []struct {
		name, host, origin string
		secure             bool
	}{
		{"http", "192.168.50.206:18090", "http://192.168.50.206:18090", false},
		{"https", "music.qlqqs.com:8888", "https://music.qlqqs.com:8888", true},
		{"normalized https", "Music.Example.COM:443", "HTTPS://music.example.com", true},
		{"IPv6 http", "[fd00::1]:8090", "http://[fd00::1]:8090", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, clear := range []bool{false, true} {
				name := "set"
				if clear {
					name = "clear"
				}
				t.Run(name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "/", nil)
					request.Host = tc.host
					request.Header.Set("Origin", tc.origin)
					forwardedProto := "http"
					if !tc.secure {
						forwardedProto = "https"
						// 即使上游连接带 TLS，也只按浏览器 Origin 决定 Secure。
						request.TLS = &tls.ConnectionState{}
					}
					request.Header.Set("X-Forwarded-Proto", forwardedProto)
					request.Header.Set("X-Forwarded-Host", "evil.example")
					request.Header.Set("Forwarded", "host=evil.example;proto="+forwardedProto)
					response := httptest.NewRecorder()
					if !guard.RequireSameOrigin(response, request) {
						t.Fatalf("可信来源被拒绝：%s", response.Body.String())
					}
					before := time.Now().Add(24*time.Hour - time.Second)
					if clear {
						guard.ClearSessionCookie(response, request)
					} else {
						guard.SetSessionCookie(response, request, "same-host-session-token")
					}
					cookies := response.Result().Cookies()
					if len(cookies) != 1 {
						t.Fatalf("期望一个会话 Cookie，得到 %d", len(cookies))
					}
					cookie := cookies[0]
					if cookie.Name != SessionCookieName || cookie.Secure != tc.secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
						t.Fatalf("Cookie 属性不符：%+v，期望 Secure=%v", cookie, tc.secure)
					}
					if clear {
						if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.Expires.IsZero() {
							t.Fatal("退出 Cookie 必须清空值、MaxAge=-1 且不设置 Expires")
						}
					} else if cookie.Value != "same-host-session-token" || cookie.MaxAge != 0 || cookie.Expires.Before(before) || cookie.Expires.After(time.Now().Add(24*time.Hour)) {
						t.Fatal("登录 Cookie 必须保留令牌、24 小时 Expires 与默认 MaxAge")
					}
				})
			}
		})
	}
}

func TestGuardSameHostCookieSecureRejectsUntrustedOrigins(t *testing.T) {
	guard := NewGuardWithSameHost(nil)
	for _, tc := range []struct{ name, host, origin string }{
		{"mismatched https", "backend.internal", "https://music.example.com"},
		{"missing Host", "", "https://music.example.com"},
		{"invalid Host", "music.example.com:0", "https://music.example.com"},
		{"path", "music.example.com", "https://music.example.com/"},
		{"null", "music.example.com", "null"},
		{"missing Origin", "music.example.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Host = tc.host
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("X-Forwarded-Proto", "https")
			if guard.cookieSecure(request) {
				t.Fatal("不可信来源的 Cookie Secure 计算必须返回 false")
			}
		})
	}
}

func TestGuardOriginRejectionLogs(t *testing.T) {
	for _, mode := range []string{"same-host", "allow-list"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct{ name, origin, host, wantOrigin, wantHost string }{
				{"raw fields", "https://Other.Example:8888", "Backend.Internal:8080", "https://Other.Example:8888", "Backend.Internal:8080"},
				{"256 byte boundary", strings.Repeat("o", 256), strings.Repeat("h", 256), strings.Repeat("o", 256), strings.Repeat("h", 256)},
				{"long ASCII fields", "https://" + strings.Repeat("o", 300), strings.Repeat("h", 300), "https://" + strings.Repeat("o", 248), strings.Repeat("h", 256)},
				{"UTF-8 cut at boundary", "https://" + strings.Repeat("o", 247) + "中尾", strings.Repeat("h", 255) + "中尾", "https://" + strings.Repeat("o", 247), strings.Repeat("h", 255)},
				{"structured control characters", "https://evil.example\n\"event\":\"forged\"", "backend\r\ninternal", "https://evil.example\n\"event\":\"forged\"", "backend\r\ninternal"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var output bytes.Buffer
					logger := slog.New(slog.NewJSONHandler(&output, nil))
					guard := NewGuardWithSameHost(nil)
					if mode == "allow-list" {
						guard = NewGuardWithAllowedOrigins(nil, []string{"https://music.example.com"})
					}
					guard.WithLogger(logger)
					requireOriginDecision(t, guard, tc.host, tc.origin, map[string]string{"X-Request-ID": "origin-log-test"}, "origin_forbidden")
					request := httptest.NewRequest(http.MethodPost, "/", nil)
					request.Host = tc.host
					request.Header.Set("Origin", tc.origin)
					// Cookie 的共用判定不得再写拒绝事件。
					if guard.cookieSecure(request) || guard.cookieSecure(request) {
						t.Fatal("拒绝的来源不应计算为可信 https")
					}
					decoder := json.NewDecoder(&output)
					var record map[string]any
					if err := decoder.Decode(&record); err != nil {
						t.Fatalf("解码来源拒绝日志：%v", err)
					}
					for key, want := range map[string]string{"event": "identity.origin.rejected", "level": "WARN", "module": "identity", "request_id": "origin-log-test", "mode": mode, "origin": tc.wantOrigin, "host": tc.wantHost} {
						if record[key] != want {
							t.Errorf("日志 %s=%q，期望 %q", key, record[key], want)
						}
					}
					for _, key := range []string{"mode", "origin", "host"} {
						value, ok := record[key].(string)
						if !ok || len(value) > 256 || !utf8.ValidString(value) {
							t.Errorf("日志字段 %s 必须是不超过 256 字节的合法 UTF-8", key)
						}
					}
					if err := decoder.Decode(new(any)); err != io.EOF {
						t.Fatalf("一次拒绝必须恰好记录一条来源事件：%v", err)
					}
				})
			}
		})
	}
}

func TestGuardOriginLoggingSkipsAcceptedMissingAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		name, host, origin, code string
		guard                    *Guard
	}{
		{"same-host allowed", "music.example.com", "https://music.example.com", "", NewGuardWithSameHost(nil)},
		{"allow-list allowed", "backend.internal", "https://music.example.com", "", NewGuardWithAllowedOrigins(nil, []string{"https://music.example.com"})},
		{"same-host missing Origin", "music.example.com", "", "origin_required", NewGuardWithSameHost(nil)},
		{"allow-list missing Origin", "music.example.com", "", "origin_required", NewGuardWithAllowedOrigins(nil, []string{"https://music.example.com"})},
		{"legacy allowed", "music.example.com", "http://music.example.com", "", NewGuard(nil, false, "")},
		{"legacy rejected", "music.example.com", "http://evil.example", "origin_forbidden", NewGuard(nil, false, "")},
		{"legacy missing Origin", "music.example.com", "", "origin_required", NewGuard(nil, false, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			tc.guard.WithLogger(slog.New(slog.NewJSONHandler(&output, nil)))
			requireOriginDecision(t, tc.guard, tc.host, tc.origin, nil, tc.code)
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Host = tc.host
			request.Header.Set("Origin", tc.origin)
			tc.guard.cookieSecure(request)
			if output.Len() != 0 {
				t.Fatalf("通过、缺 Origin、旧模式与 Cookie 判定不应记录来源拒绝日志：%s", output.String())
			}
		})
	}
}

func TestGuardOriginRejectionLogUsesMiddlewareRequestID(t *testing.T) {
	for _, requestID := range []string{"origin-correlation-test", "invalid request id"} {
		t.Run(requestID, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			guard := NewGuardWithSameHost(nil).WithLogger(logger)
			handler := httpserver.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				guard.RequireSameOrigin(w, r)
			}))
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Host = "backend.internal"
			request.Header.Set("Origin", "https://music.example.com")
			request.Header.Set("X-Request-ID", requestID)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("期望来源拒绝，得到 HTTP %d", response.Code)
			}
			id := response.Header().Get("X-Request-ID")
			if id == "" || (requestID == "origin-correlation-test" && id != requestID) || (requestID != "origin-correlation-test" && id == requestID) {
				t.Fatalf("中间件请求 ID 未按合同保留或替换：%q", id)
			}
			decoder := json.NewDecoder(&output)
			rejected := 0
			for {
				var record map[string]any
				if err := decoder.Decode(&record); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("解码中间件日志：%v", err)
				}
				if record["event"] == "identity.origin.rejected" {
					rejected++
					if record["request_id"] != id {
						t.Fatalf("拒绝日志 request_id=%q，与响应 %q 不一致", record["request_id"], id)
					}
				}
			}
			if rejected != 1 {
				t.Fatalf("期望恰好一条来源拒绝事件，得到 %d", rejected)
			}
		})
	}
}
