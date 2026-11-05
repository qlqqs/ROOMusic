package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

const summaryTrackA = "abcdef01-0000-4000-8000-000000000001"
const summaryTrackB = "abcdef02-0000-4000-8000-000000000002"

func summaryIDsBody(ids []string) string {
	raw, err := json.Marshal(map[string]any{"track_ids": ids})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func summaryTestIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("abcdef00-0000-4000-8000-%012d", i)
	}
	return ids
}

func assertSummaryHTTPError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var payload httpserver.APIError
	decoder := json.NewDecoder(w.Body)
	if w.Code != status || decoder.Decode(&payload) != nil || payload.Error.Code != code {
		t.Fatalf("错误响应不符: status=%d, payload=%+v，预期 %d/%s", w.Code, payload, status, code)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("响应必须恰有一个 JSON 文档: %v", err)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("响应头不符: %v", w.Header())
	}
	if payload.RequestID != "summary-request" {
		t.Fatalf("关联标识丢失: %+v", payload)
	}
}

func TestDecodeTrackSummaryIDs(t *testing.T) {
	for _, test := range []struct {
		name, body, code string
		want             []string
	}{
		{"one", summaryIDsBody([]string{summaryTrackA}), "", []string{summaryTrackA}},
		{"order-and-case", summaryIDsBody([]string{strings.ToUpper(summaryTrackB), summaryTrackA, summaryTrackB, strings.ToUpper(summaryTrackA)}), "", []string{summaryTrackB, summaryTrackA}},
		{"1000", summaryIDsBody(summaryTestIDs(1000)), "", summaryTestIDs(1000)},
		{"1001-distinct", summaryIDsBody(summaryTestIDs(1001)), "invalid_input", nil},
		{"1001-with-duplicate", summaryIDsBody(append(summaryTestIDs(1000), strings.ToUpper(summaryTestIDs(1)[0]))), "", summaryTestIDs(1000)},
		{"empty", `{"track_ids":[]}`, "invalid_input", nil},
		{"missing", `{}`, "invalid_input", nil},
		{"null-object", `null`, "invalid_input", nil},
		{"array-object", `[]`, "invalid_input", nil},
		{"string-object", `"track_ids"`, "invalid_input", nil},
		{"null-list", `{"track_ids":null}`, "invalid_input", nil},
		{"string-list", `{"track_ids":"` + summaryTrackA + `"}`, "invalid_input", nil},
		{"object-list", `{"track_ids":{}}`, "invalid_input", nil},
		{"null-id", `{"track_ids":[null]}`, "invalid_input", nil},
		{"number-id", `{"track_ids":[1]}`, "invalid_input", nil},
		{"invalid-id", `{"track_ids":["invalid"]}`, "invalid_input", nil},
		{"space-id", summaryIDsBody([]string{" " + summaryTrackA}), "invalid_input", nil},
		{"valid-then-invalid", summaryIDsBody([]string{summaryTrackA, "invalid"}), "invalid_input", nil},
		{"unknown-field", `{"track_ids":["` + summaryTrackA + `"],"other":null}`, "invalid_input", nil},
		{"field-case", `{"TRACK_IDS":["` + summaryTrackA + `"]}`, "invalid_input", nil},
		{"duplicate-field", `{"track_ids":[],"track_ids":["` + summaryTrackA + `"]}`, "invalid_input", nil},
		{"escaped-duplicate", `{"track_ids":[],"track_\u0069ds":["` + summaryTrackA + `"]}`, "invalid_input", nil},
		{"malformed", `{"track_ids":`, "invalid_json", nil},
		{"trailing-object", `{"track_ids":[]} {}`, "invalid_json", nil},
		{"empty-body", ``, "invalid_json", nil},
		{"invalid-utf8", "{\"track_ids\":[\"\xff\"]}", "invalid_json", nil},
		{"body-limit", strings.Repeat(" ", 1<<20) + `{}`, "invalid_json", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("X-Request-ID", "summary-request")
			got, ok := decodeTrackSummaryIDs(w, httptest.NewRequest(http.MethodPost, "/api/v1/tracks/summaries", strings.NewReader(test.body)))
			if ok != (test.code == "") || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("解码结果: %v, %v，预期 %v；响应 %s", got, ok, test.want, w.Body.String())
			}
			if test.code != "" {
				assertSummaryHTTPError(t, w, 400, test.code)
			} else if w.Body.Len() != 0 {
				t.Fatal("成功解码不应写出响应")
			}
		})
	}
}

func TestTrackSummariesSecurityBeforeDecode(t *testing.T) {
	h := New(nil, identity.NewGuard(nil, false, "http://roomusic.test"), t.TempDir(), nil)
	for _, test := range []struct {
		origin, code string
		status       int
	}{
		{"", "origin_required", 403},
		{"http://foreign.test", "origin_forbidden", 403},
		{"http://roomusic.test", "unauthorized", 401},
	} {
		t.Run(test.code, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://roomusic.test/api/v1/tracks/summaries", strings.NewReader("invalid JSON"))
			r.Header.Set("Origin", test.origin)
			w := httptest.NewRecorder()
			w.Header().Set("X-Request-ID", "summary-request")
			h.trackSummaries(w, r)
			assertSummaryHTTPError(t, w, test.status, test.code)
		})
	}
}
