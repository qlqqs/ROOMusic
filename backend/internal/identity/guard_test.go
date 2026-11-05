package identity

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
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
