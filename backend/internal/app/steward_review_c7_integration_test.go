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
	checkC7ReviewOriginAndEvidence(t, false)
}

func TestPostgreSQLStewardReviewPolicyV2Evidence(t *testing.T) {
	checkC7ReviewOriginAndEvidence(t, true)
}

func checkC7ReviewOriginAndEvidence(t *testing.T, external bool) {
	t.Helper()
	f := newC3AppFixture(t)
	if err := maintenance.Initialize(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	root, group, release, medium, track := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
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
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_kind,source_status,observed_at,lyrics,lyrics_source) VALUES($1,$2,1,'合成曲目','合成艺人',$3,'C7_PRIVATE_PATH.flac','physical','present',now(),'C7_PRIVATE_LYRICS','id3_uslt')`, track, medium, root)
	if external {
		exec(`INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
			VALUES(gen_random_uuid(),'musicbrainz','release',$1,'synthetic-release','tag','exact'),
			(gen_random_uuid(),'musicbrainz','release_group',$2,'synthetic-group','barcode','high'),
			(gen_random_uuid(),'musicbrainz','track',$3,'synthetic-recording','isrc','high')`, release, group, track)
		exec(`INSERT INTO curation_pending_items(target_type,target_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts,resolution,resolved_at)
			VALUES('release',$1,'release_match','waiting_model',1,repeat('a',64),'{"private":"C7_PRIVATE_PROGRAM_EVIDENCE"}',
			'[{"external_id":"open-candidate","title":"合成外部标题","artist":"合成外部艺人","date":"2026","country":"JP","track_count":1,"method":"search","score":90}]',
			'["ambiguous_match"]',NULL,NULL),
			('release',$1,'release_match','resolved',1,repeat('b',64),'{}',
			'[{"external_id":"resolved-candidate"}]','[]','program_matched',now())`, release)
	}
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
	if external {
		if runtime.ReviewPolicyVersion != 2 || p.Review.PolicyVersion != 2 {
			t.Errorf("审查策略当前=%d 保存=%d，期望 2", runtime.ReviewPolicyVersion, p.Review.PolicyVersion)
		}
		assertC7ExternalReviewEvidence(t, reviewWire, release)
	}
}

func assertC7ExternalReviewEvidence(t *testing.T, wire, releaseID string) {
	t.Helper()
	var request struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(wire), &request); err != nil || len(request.Messages) == 0 {
		t.Fatalf("审查请求无法解码：%v", err)
	}
	type externalLink struct {
		ObjectType string `json:"object_type"`
		ExternalID string `json:"external_id"`
		Method     string `json:"method"`
		Confidence string `json:"confidence"`
	}
	type candidate struct {
		ExternalID string `json:"external_id"`
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Date       string `json:"date"`
		Country    string `json:"country"`
		TrackCount int    `json:"track_count"`
		Method     string `json:"method"`
		Score      int    `json:"score"`
	}
	var payload struct {
		Evidence []struct {
			Facts struct {
				External struct {
					Source string `json:"source"`
					Data   []struct {
						ReleaseID string         `json:"release_id"`
						Links     []externalLink `json:"links"`
						Pending   []struct {
							Kind       string      `json:"kind"`
							Conflicts  []string    `json:"conflicts"`
							Candidates []candidate `json:"candidates"`
						} `json:"pending"`
					} `json:"data"`
				} `json:"external"`
			} `json:"facts"`
		} `json:"evidence"`
	}
	content := request.Messages[len(request.Messages)-1].Content
	if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload.Evidence) != 1 {
		t.Fatalf("审查证据=%s err=%v", content, err)
	}
	external := payload.Evidence[0].Facts.External
	if external.Source != "program_untrusted_external" || len(external.Data) != 1 || external.Data[0].ReleaseID != releaseID {
		t.Fatalf("外部证据的来源或发行不正确：%+v", external)
	}
	data := external.Data[0]
	links := make(map[string]externalLink, len(data.Links))
	for _, link := range data.Links {
		links[link.ObjectType] = link
	}
	if len(data.Links) != 3 ||
		links["release"] != (externalLink{"release", "synthetic-release", "tag", "exact"}) ||
		links["release_group"] != (externalLink{"release_group", "synthetic-group", "barcode", "high"}) ||
		links["track"] != (externalLink{"track", "synthetic-recording", "isrc", "high"}) {
		t.Errorf("外部对应=%+v", data.Links)
	}
	wantCandidate := candidate{"open-candidate", "合成外部标题", "合成外部艺人", "2026", "JP", 1, "search", 90}
	if len(data.Pending) != 1 || data.Pending[0].Kind != "release_match" ||
		len(data.Pending[0].Conflicts) != 1 || data.Pending[0].Conflicts[0] != "ambiguous_match" ||
		len(data.Pending[0].Candidates) != 1 || data.Pending[0].Candidates[0] != wantCandidate {
		t.Errorf("未结束待定项=%+v", data.Pending)
	}
	if strings.Contains(content, "C7_PRIVATE_PROGRAM_EVIDENCE") || strings.Contains(content, "resolved-candidate") {
		t.Error("审查收到了原始证据或已结束待定项")
	}
}
