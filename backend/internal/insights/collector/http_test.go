package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights/schema"
)

func insightsHeartbeat(id string) schema.Payload {
	return schema.Payload{
		Schema: 1, InstanceID: id, Version: "1.2.3", OS: "linux", Arch: "amd64", Container: true,
		Library: schema.Library{Tracks: "1k-10k", Releases: "1-999"}, Members: "2-5",
		Features:   schema.Features{AutoCuration: true, MCPToken: true, DSP: true, ScheduledScan: true, Plugins: "1"},
		Devices30d: schema.Devices{Browser: "2-5", UPnP: "0"},
	}
}

const collectorFixtureID = "a1000000-0000-4000-8000-000000000001"

func encodeInsightsHeartbeat(t *testing.T, payload schema.Payload) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func newInsightsCollector(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func postInsightsHeartbeat(h http.Handler, body, peer, forwarded, kind string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat", strings.NewReader(body))
	req.RemoteAddr = peer
	req.Header.Set("Content-Type", kind)
	if forwarded != "" {
		req.Header.Set("X-Real-IP", forwarded)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func getInsightsCollector(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestInsightsCollectorHeartbeatValidation(t *testing.T) {
	store := newInsightsCollector(t)
	handler := NewHandler(store)
	valid := encodeInsightsHeartbeat(t, insightsHeartbeat(collectorFixtureID))
	exactLimit := valid + strings.Repeat(" ", 4096-len(valid))
	for _, tc := range []struct {
		name, body, contentType string
		want                    int
	}{
		{"valid", valid, "application/json", http.StatusNoContent},
		{"exactly_4KiB", exactLimit, "application/json; charset=utf-8", http.StatusNoContent},
		{"over_4KiB", exactLimit + " ", "application/json", http.StatusBadRequest},
		{"unknown_field", strings.Replace(valid, `"schema":1`, `"schema":1,"client_ip":"203.0.113.20"`, 1), "application/json", http.StatusBadRequest},
		{"wrong_type", strings.Replace(valid, `"container":true`, `"container":"true"`, 1), "application/json", http.StatusBadRequest},
		{"wrong_content_type", valid, "text/plain", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := postInsightsHeartbeat(handler, tc.body, "203.0.113.20:20000", "", tc.contentType)
			if reply.Code != tc.want || reply.Body.Len() != 0 {
				t.Fatalf("心跳返回 %d %q，期望 %d 空正文", reply.Code, reply.Body, tc.want)
			}
		})
	}
}

func TestInsightsCollectorRateLimitPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name, peer, forwarded, samePrefix, sameForwarded, otherPrefix, otherForwarded string
	}{
		{"ipv4_32", "198.51.100.18:12000", "", "[::ffff:198.51.100.18]:12001", "", "198.51.100.19:12001", ""},
		{"ipv6_64", "[2001:db8:0:1::aaaa]:12000", "", "[2001:db8:0:1::bbbb]:12001", "", "[2001:db8:0:2::aaaa]:12001", ""},
		{"trusted_proxy", "127.0.0.1:12000", "198.51.100.18", "127.0.0.1:12001", "198.51.100.18", "127.0.0.1:12001", "198.51.100.19"},
		{"untrusted_proxy", "192.0.2.10:12000", "198.51.100.18", "192.0.2.10:12001", "198.51.100.19", "192.0.2.11:12001", "198.51.100.18"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(newInsightsCollector(t), netip.MustParseAddr("127.0.0.1"))
			body := encodeInsightsHeartbeat(t, insightsHeartbeat(collectorFixtureID))
			for i := range 30 {
				if r := postInsightsHeartbeat(h, body, tc.peer, tc.forwarded, "application/json"); r.Code != http.StatusNoContent {
					t.Fatalf("同组第 %d 次心跳返回 %d，期望 204", i+1, r.Code)
				}
			}
			if r := postInsightsHeartbeat(h, body, tc.samePrefix, tc.sameForwarded, "application/json"); r.Code != http.StatusTooManyRequests {
				t.Errorf("同组第 31 次心跳返回 %d，期望 429", r.Code)
			}
			if r := postInsightsHeartbeat(h, body, tc.otherPrefix, tc.otherForwarded, "application/json"); r.Code != http.StatusNoContent {
				t.Errorf("另一地址组首次心跳返回 %d，期望 204", r.Code)
			}
		})
	}
}

func TestInsightsCollectorSummaryAndPage(t *testing.T) {
	store := newInsightsCollector(t)
	now := time.Now()
	older := insightsHeartbeat("a1000000-0000-4000-8000-000000000002")
	older.Arch, older.Version, older.Container = "arm64", "2.0.0", false
	older.Features = schema.Features{StewardModel: true, LANDevices: true, Plugins: "0"}
	if err := store.put(older, now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store)
	newest := insightsHeartbeat(collectorFixtureID)
	previous := newest
	previous.Version = "1.1.0"
	newest.InstanceID = strings.ToUpper(newest.InstanceID)
	for _, p := range []schema.Payload{previous, newest} {
		body := encodeInsightsHeartbeat(t, p)
		if reply := postInsightsHeartbeat(h, body, "203.0.113.40:45000", "", "application/json"); reply.Code != http.StatusNoContent {
			t.Fatalf("写入心跳返回 %d", reply.Code)
		}
	}
	r := getInsightsCollector(h, "/api/v1/summary")
	if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("JSON 汇总响应 %d %v", r.Code, r.Header())
	}
	var got Summary
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GeneratedAt.Before(now) || got.GeneratedAt.After(time.Now()) {
		t.Errorf("汇总时间不在测试区间：%v", got.GeneratedAt)
	}
	got.GeneratedAt = time.Time{}
	want := Summary{
		Active7d: 1, Active30d: 2,
		Versions: distribution{"1.2.3": 1, "2.0.0": 1}, Arch: distribution{"amd64": 1, "arm64": 1}, Container: distribution{"true": 1, "false": 1},
		Library: map[string]distribution{"tracks": {"1k-10k": 2}, "releases": {"1-999": 2}},
		Members: distribution{"2-5": 2}, Features: FeatureSummary{AutoCuration: 1, StewardModel: 1, MCPToken: 1, LANDevices: 1, DSP: 1, ScheduledScan: 1, Plugins: distribution{"1": 1, "0": 1}},
		Devices30d: map[string]distribution{"browser": {"2-5": 2}, "upnp": {"0": 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("汇总结果 = %+v，期望 %+v", got, want)
	}
	if strings.Contains(strings.ToLower(r.Body.String()), collectorFixtureID) {
		t.Error("公开汇总暴露了实例 ID")
	}

	page := getInsightsCollector(h, "/")
	if page.Code != http.StatusOK || page.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("HTML 汇总响应 %d %v", page.Code, page.Header())
	}
	for _, fragment := range []string{
		"ROOMusic 匿名实例统计", "Instance insights", "最近 7 天<strong>1</strong>", "最近 30 天<strong>2</strong>",
		"自动整理</td><td>1</td><td>50.0%", "<td>1.2.3</td><td>1</td>", "<td>arm64</td><td>1</td>",
		"docs/insights.md", "收集了什么、怎么关闭", `href="/api/v1/summary"`,
	} {
		if !strings.Contains(page.Body.String(), fragment) {
			t.Errorf("HTML 汇总缺少 %q", fragment)
		}
	}
	if strings.Contains(strings.ToLower(page.Body.String()), collectorFixtureID) {
		t.Error("HTML 汇总暴露了实例 ID")
	}
}

func TestInsightsCollectorRateLimitRollingHour(t *testing.T) {
	limit := limiter{clients: make(map[netip.Prefix]*bucket)}
	addr := netip.MustParseAddr("203.0.113.30")
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	if !limit.allow(addr, start) {
		t.Fatal("首次心跳未获放行")
	}
	for i := range 29 {
		if !limit.allow(addr, start.Add(15*time.Minute)) {
			t.Fatalf("一小时内第 %d 次请求未获放行", i+2)
		}
	}
	if limit.allow(addr, start.Add(time.Hour-time.Nanosecond)) {
		t.Error("一小时内第 31 次请求获放行")
	}
	if !limit.allow(addr, start.Add(time.Hour)) {
		t.Error("满一小时后未归还第一个令牌")
	}
	if limit.allow(addr, start.Add(time.Hour)) {
		t.Error("满一小时后提前归还了其他令牌")
	}
	if !limit.allow(addr, start.Add(75*time.Minute)) {
		t.Error("后一批请求满一小时后未归还令牌")
	}
}
