package app

import (
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

// 样本固定平台字段，避免生成机器的架构和容器环境进入前端合同。
func TestPostgreSQLInsightsAPISamples(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	recorder := newAPISampleRecorder(t, app)
	recorder.scope = "insights"
	headers := map[string]string{"X-Request-ID": sampleRequestID}
	const base = "/api/v1/admin/insights"
	recordStatus := func(name, method string, body any) {
		t.Helper()
		r := app.request(t, method, base, body, &admin, headers)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
		var status map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		payload := status["payload"].(map[string]any)
		payload["arch"] = "amd64"
		payload["container"] = false
		r.Body.Reset()
		if err := json.NewEncoder(r.Body).Encode(status); err != nil {
			t.Fatal(err)
		}
		recorder.record("insights/"+name, r, 200)
	}
	recorder.record("insights/about", app.request(t, "GET", "/api/v1/system/about", nil, nil, headers), 200)
	recordStatus("status", "GET", nil)
	recordStatus("disabled", "PUT", map[string]bool{"enabled": false})
	recorder.record("insights/notice-acknowledgement", app.request(t, "POST", base+"/notice-acknowledgement", nil, &admin, headers), 204)
	forced := newConfiguredIntegrationTestApplication(t, nil, config.Server{InsightsDisabled: true})
	forcedAdmin := forced.createUser(t, "admin")
	recorder.record("insights/forced-off", forced.request(t, "PUT", base, map[string]bool{"enabled": true}, &forcedAdmin, headers), 409)
	recorder.finish()
}
