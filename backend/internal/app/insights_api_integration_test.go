package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/insights"
	"github.com/qlqq/roomusic/backend/internal/insights/schema"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const insightsAdminPath = "/api/v1/admin/insights"
const insightsAckPath = insightsAdminPath + "/notice-acknowledgement"

func TestPostgreSQLInsightsAdminAPI(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, insightsAdminPath, nil},
		{http.MethodPut, insightsAdminPath, map[string]bool{"enabled": false}},
		{http.MethodPost, insightsAckPath, nil},
	} {
		requireAPIError(t, app.request(t, tc.method, tc.path, tc.body, nil, nil), http.StatusUnauthorized, "unauthorized")
		requireAPIError(t, app.request(t, tc.method, tc.path, tc.body, &member, nil), http.StatusForbidden, "forbidden")
	}

	original := systemDecode[insights.Status](t, app.request(t, http.MethodGet, insightsAdminPath, nil, &admin, nil))
	var createdAt time.Time
	if err := app.database.QueryRowContext(t.Context(), `SELECT created_at FROM instance_insights WHERE id`).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if !original.Enabled || original.ForcedOff || original.NoticeAcknowledged || original.LastAttemptAt != nil ||
		original.LastSuccessAt != nil || original.LastError != nil || !original.FirstSendAfter.Equal(createdAt.Add(24*time.Hour)) ||
		original.EndpointHost != "insights.roomusic.org" || original.PublicPageURL != "https://insights.roomusic.org/" {
		t.Fatalf("管理员初始状态不符合同：%+v", original)
	}
	p, err := schema.Decode(bytes.NewReader(original.Payload))
	if err != nil || p.InstanceID != original.InstanceID || p.Members != "2-5" {
		t.Fatalf("管理员初始预览错误：%+v，错误 %v", p, err)
	}
	for _, enabled := range []bool{false, true} {
		response := app.request(t, http.MethodPut, insightsAdminPath, map[string]bool{"enabled": enabled}, &admin, nil)
		written := systemDecode[insights.Status](t, response)
		read := systemDecode[insights.Status](t, app.request(t, http.MethodGet, insightsAdminPath, nil, &admin, nil))
		if written.Enabled != enabled || read.Enabled != enabled || written.InstanceID != original.InstanceID || read.InstanceID != original.InstanceID {
			t.Errorf("开关更新为 %t 后，写入状态 %+v，读取状态 %+v", enabled, written, read)
		}
	}

	var acknowledgedAt time.Time
	for i := range 2 {
		resp := app.request(t, http.MethodPost, insightsAckPath, nil, &admin, nil)
		if resp.Code != http.StatusNoContent || resp.Body.Len() != 0 {
			t.Fatalf("第 %d 次说明确认返回 %d %s", i+1, resp.Code, resp.Body)
		}
		var got time.Time
		if err := app.database.QueryRowContext(t.Context(), `SELECT notice_acknowledged_at FROM instance_insights WHERE id`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if i == 1 && !got.Equal(acknowledgedAt) {
			t.Errorf("重复确认改变了时间：首次 %v，本次 %v", acknowledgedAt, got)
		}
		acknowledgedAt = got
	}
	read := systemDecode[insights.Status](t, app.request(t, http.MethodGet, insightsAdminPath, nil, &admin, nil))
	if !read.NoticeAcknowledged {
		t.Error("读取状态没有反映已确认")
	}
}

func TestPostgreSQLInsightsForcedOffAPI(t *testing.T) {
	app := newConfiguredIntegrationTestApplication(t, nil, config.Server{InsightsDisabled: true})
	admin := app.createUser(t, "admin")
	status := systemDecode[insights.Status](t, app.request(t, http.MethodGet, insightsAdminPath, nil, &admin, nil))
	if status.Enabled || !status.ForcedOff {
		t.Fatalf("部署层关闭时的状态不符合同：%+v", status)
	}
	for _, enabled := range []bool{true, false} {
		resp := app.request(t, http.MethodPut, insightsAdminPath, map[string]bool{"enabled": enabled}, &admin, nil)
		requireAPIError(t, resp, http.StatusConflict, "insights_forced_off")
	}
	var saved bool
	if err := app.database.QueryRowContext(t.Context(), `SELECT enabled FROM instance_insights WHERE id`).Scan(&saved); err != nil || !saved {
		t.Fatalf("部署层关闭覆盖了保存的实例开关：enabled=%t，错误 %v", saved, err)
	}
}

func TestPostgreSQLSystemAboutPublicWithoutVersion(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"default", "", "https://github.com/qlqqs/ROOMusic"},
		{"fork", "https://source.example.invalid/roomusic-fork", "https://source.example.invalid/roomusic-fork"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newConfiguredIntegrationTestApplication(t, nil, config.Server{SourceURL: tc.source})
			r := app.request(t, http.MethodGet, "/api/v1/system/about", nil, nil, nil)
			got := systemDecode[map[string]string](t, r)
			if len(got) != 2 || got["license"] != "AGPL-3.0-only" || got["source_url"] != tc.want {
				t.Fatalf("匿名访问关于接口返回 %v，期望仅含许可和源码地址 %q", got, tc.want)
			}
		})
	}
}

func TestPostgreSQLInsightsHeartbeatMatchesAdminPreview(t *testing.T) {
	postgresURL := "postgres://synthetic:synthetic@example.invalid/roomusic"
	t.Setenv("ROOMUSIC_DATABASE_URL", postgresURL)
	t.Setenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS", t.TempDir())
	t.Setenv("ROOMUSIC_ENV", "development")
	t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", "")
	t.Setenv("ROOMUSIC_SECURE_COOKIES", "")
	t.Setenv("ROOMUSIC_PUBLIC_URL", "")
	t.Setenv("ROOMUSIC_INSIGHTS_DISABLED", "")
	t.Setenv("ROOMUSIC_INSIGHTS_URL", "https://insights.example.invalid/api/v1/heartbeat")
	t.Setenv("ROOMUSIC_SOURCE_URL", "")

	mock, err := upstreammock.New(upstreammock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	type heartbeat struct {
		body  []byte
		agent string
		code  int
		err   error
	}
	received := make(chan heartbeat, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/insights/api/v1/heartbeat" {
			mock.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		r.Body = io.NopCloser(bytes.NewReader(raw))
		reply := httptest.NewRecorder()
		mock.ServeHTTP(reply, r)
		w.WriteHeader(reply.Code)
		received <- heartbeat{body: raw, agent: r.UserAgent(), code: reply.Code, err: err}
	}))
	t.Cleanup(server.Close)
	t.Setenv("ROOMUSIC_UPSTREAM_MOCK_URL", server.URL)
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredIntegrationTestApplication(t, nil, settings)
	admin := app.createUser(t, "admin")
	preview := systemDecode[insights.Status](t, app.request(t, http.MethodGet, insightsAdminPath, nil, &admin, nil))
	if preview.EndpointHost != strings.TrimPrefix(server.URL, "http://") || preview.PublicPageURL != "https://insights.example.invalid/" {
		t.Fatalf("mock 模式端点或公开页错误：%+v", preview)
	}
	// 先取得预览，再回拨实例年龄，并通过设置接口唤醒真实调度循环。
	if _, err := app.database.ExecContext(t.Context(), `UPDATE instance_insights SET created_at=now()-interval '25 hours' WHERE id`); err != nil {
		t.Fatal(err)
	}
	systemDecode[insights.Status](t, app.request(t, http.MethodPut, insightsAdminPath, map[string]bool{"enabled": true}, &admin, nil))

	select {
	case got := <-received:
		if got.err != nil || got.code != http.StatusNoContent {
			t.Fatalf("mock 接收心跳返回 %d，读取错误 %v，载荷 %q", got.code, got.err, got.body)
		}
		if !bytes.Equal(got.body, preview.Payload) {
			t.Fatalf("请求体与管理员 payload 字节不同\n请求 %s\n预览 %s", got.body, preview.Payload)
		}
		version := buildinfo.Current().Version
		if version == "" {
			version = "dev"
		}
		if got.agent != "ROOMusic/"+version {
			t.Errorf("User-Agent = %q", got.agent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mock 未收到调度发送的心跳")
	}
	requests := httptest.NewRecorder()
	mock.ServeHTTP(requests, httptest.NewRequest(http.MethodGet, "/_mock/requests?service=insights", nil))
	records := systemDecode[[]struct {
		Status  int             `json:"status"`
		Payload json.RawMessage `json:"payload"`
	}](t, requests)
	if len(records) != 1 || records[0].Status != http.StatusNoContent || !bytes.Equal(records[0].Payload, preview.Payload) {
		t.Fatalf("mock 未记录匹配的心跳：%s", requests.Body)
	}
}
