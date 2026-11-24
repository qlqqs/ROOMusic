package upstreammock_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func TestMockInsightsHeartbeatScenariosAndRequestRecords(t *testing.T) {
	const path = "/insights/api/v1/heartbeat"
	const valid = `{"schema":3,"instance_id":"a1000000-0000-4000-8000-000000000001","version":"1.2.3","os":"linux","arch":"amd64","container":true,"library":{"tracks":"0","releases":"0"},"members":"1","features":{"auto_curation":false,"steward_model":false,"mcp_token":false,"lan_devices":false,"dsp":false,"scheduled_scan":false,"plugins":"0"},"devices_30d":{"browser":"0","upnp":"0","cast":"0","airplay":"1"}}`
	mock := newMock(t, upstreammock.Options{})
	requireStatus(t, callMock(mock, http.MethodPost, path, valid), http.StatusNoContent)
	requireStatus(t, callMock(mock, http.MethodPost, path, `{"unknown":true}`), http.StatusBadRequest)
	requireStatus(t, callMock(mock, http.MethodPut, "/_mock/scenarios/insights", `{"scenario":"unavailable","retry_after":5}`), http.StatusOK)
	failed := callMock(mock, http.MethodPost, path, valid)
	requireStatus(t, failed, http.StatusServiceUnavailable)
	if failed.Header().Get("Retry-After") != "5" {
		t.Errorf("mock Retry-After = %q，期望 5", failed.Header().Get("Retry-After"))
	}
	reply := callMock(mock, http.MethodGet, "/_mock/requests?service=insights", "")
	requireStatus(t, reply, http.StatusOK)
	records := decodeBody[[]struct {
		Service, Method, Path string
		Status                int
		Payload               json.RawMessage
	}](t, reply)
	wantStatuses := []int{http.StatusServiceUnavailable, http.StatusBadRequest, http.StatusNoContent}
	if len(records) != len(wantStatuses) {
		t.Fatalf("mock 返回 %d 条记录，期望 %d 条", len(records), len(wantStatuses))
	}
	for i, record := range records {
		if record.Service != "insights" || record.Method != http.MethodPost || record.Path != path || record.Status != wantStatuses[i] {
			t.Errorf("第 %d 条记录 = %+v，期望状态码 %d", i+1, record, wantStatuses[i])
		}
		if record.Status == http.StatusBadRequest {
			if len(record.Payload) != 0 {
				t.Error("拒绝的心跳出现在请求记录里")
			}
			continue
		}
		if !strings.Contains(string(record.Payload), `"instance_id":"a1000000-0000-4000-8000-000000000001"`) {
			t.Errorf("记录缺少已通过校验的载荷：%s", record.Payload)
		}
	}
}
