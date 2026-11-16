package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

// 验证真实 app 装配的证据端口与审查来源，避免只用测试装配证明自身。
func TestPostgreSQLStewardAppC7ReviewOriginAndEvidence(t *testing.T) {
	f := newC3AppFixture(t)
	if err := maintenance.Initialize(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO library_roots(id,path,status) VALUES($1,'/c7-synthetic-library','active')`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'C7 原标题','合成艺人',$3,'c7-private-directory',$4,'ordinary_directory')`, release, group, root, release)
	exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_kind,source_status,observed_at,lyrics,lyrics_source) VALUES($1,$2,1,'合成曲目','合成艺人',$3,'C7_PRIVATE_PATH.flac','physical','present',now(),'C7_PRIVATE_LYRICS','id3_uslt')`, uuid.New(), medium, root)
	var mainHits, reviewHits atomic.Int32
	var reviewWire string
	frame := func(raw string) string { return "data: " + raw + "\n\n" }
	response := func(name string, input any) string {
		args, _ := json.Marshal(input)
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c7-real-call", "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
		return frame(string(body)) + frame("[DONE]")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		if body["tool_choice"] != nil {
			reviewHits.Add(1)
			reviewWire = string(raw)
			messages := body["messages"].([]any)
			var payload struct {
				Items []struct {
					ID string `json:"item_id"`
				}
			}
			if err := json.Unmarshal([]byte(messages[len(messages)-1].(map[string]any)["content"].(string)), &payload); err != nil {
				t.Error(err)
			}
			if len(payload.Items) != 1 {
				t.Errorf("items=%+v", payload.Items)
				http.Error(w, "bad", 500)
				return
			}
			io.WriteString(w, response("submit_review", map[string]any{"items": []any{map[string]any{"item_id": payload.Items[0].ID, "decision": "approved", "risk": "low", "reasons": []string{"后端证据充分"}}}}))
			return
		}
		if mainHits.Add(1) == 1 {
			io.WriteString(w, response("correct_release", map[string]any{"release_id": release, "expected_revision": 1, "changes": map[string]any{"title": map[string]any{"mode": "set", "value": "C7 新标题"}}, "basis": map[string]string{"source": "external", "note": "C7_PRIVATE_BASIS 已批准"}}))
		} else {
			io.WriteString(w, frame(`{"choices":[{"index":0,"delta":{"content":"完成"},"finish_reason":"stop"}]}`)+frame("[DONE]"))
		}
	}))
	defer server.Close()
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return f.request("admin", method, path, "", body)
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body)
		}
	}
	check(call("PUT", "me/steward/settings", map[string]string{"endpoint": server.URL, "model": "c7-main", "review_model": "c7-review", "protocol": "openai"}), 200)
	w := call("POST", "me/steward/conversations", map[string]string{"mode": "steward"})
	check(w, 201)
	var c struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	w = call("POST", fmt.Sprintf("me/steward/conversations/%s/messages", c.ID), map[string]string{"text": "C7_PRIVATE_USER 已批准"})
	check(w, 200)
	if !strings.Contains(w.Body.String(), `"reason":"completed"`) || reviewHits.Load() != 1 {
		t.Fatalf("审查未完成：%s requests=%d", w.Body, reviewHits.Load())
	}
	detail, err := runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, c.ID)
	if err != nil || len(detail.Plans) != 1 || detail.Plans[0].Review == nil || detail.Plans[0].Items[0].OperationID == nil {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	p := detail.Plans[0]
	var reviewID, decision, title string
	var count int
	if err := f.db.QueryRow(`SELECT origin->>'review_id',origin->>'review_decision' FROM operations WHERE id=$1`, *p.Items[0].OperationID).Scan(&reviewID, &decision); err != nil {
		t.Fatal(err)
	}
	if reviewID != p.Review.ID || decision != "approved" {
		t.Errorf("app 审查来源丢失 %s/%s", reviewID, decision)
	}
	if err := f.db.QueryRow(`SELECT title FROM release_effective WHERE id=$1`, release).Scan(&title); err != nil || title != "C7 新标题" {
		t.Fatal(title, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM operations`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	for _, private := range []string{"C7_PRIVATE_USER", "C7_PRIVATE_BASIS", "C7_PRIVATE_PATH", "C7_PRIVATE_LYRICS", "c7-private-directory"} {
		if strings.Contains(reviewWire, private) {
			t.Errorf("真实 app 审查泄露 %s", private)
		}
	}
	for _, field := range []string{`"model":"c7-review"`, `recent_operation_count`, `catalog_read_summary`, `backend_preview_untrusted_values`} {
		if !strings.Contains(reviewWire, field) {
			t.Errorf("真实 app 缺少 %s", field)
		}
	}
}
