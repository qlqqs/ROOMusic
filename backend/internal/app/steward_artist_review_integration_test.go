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

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type artistReviewRequest struct {
	Items []struct {
		ID         string `json:"item_id"`
		Tool       string `json:"tool"`
		Revertible bool   `json:"revertible"`
	} `json:"items"`
	Evidence []struct {
		ID    string `json:"item_id"`
		Facts struct {
			Targets []struct {
				Type     string `json:"type"`
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
				Source   string `json:"source"`
				Data     struct {
					Name struct {
						Value  string `json:"value"`
						Source string `json:"source"`
					} `json:"name"`
					Redirected   bool `json:"redirected"`
					ReleaseCount int  `json:"release_count"`
					TrackCount   int  `json:"track_count"`
				} `json:"data"`
			} `json:"targets"`
			External struct {
				Source  string `json:"source"`
				Artists []struct {
					ID    string            `json:"artist_id"`
					Links []json.RawMessage `json:"links"`
				} `json:"artists"`
			} `json:"external"`
		} `json:"facts"`
	} `json:"evidence"`
}

func TestPostgreSQLStewardReviewArtistTools(t *testing.T) {
	for _, tc := range []struct{ tool, kind string }{
		{"merge_artists", operations.TypeArtistMerge},
		{"rename_artist", operations.TypeArtistRename},
		{"ignore_artist_candidate", operations.TypeArtistCandidateIgnore},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			f := newArtistSamples(t)
			f.publish()
			input := map[string]any{
				"artist_id": f.primary, "expected_revision": f.revision(f.primary),
				"basis": map[string]string{"source": "local", "note": "ARTIST_REVIEW_PRIVATE_BASIS"},
			}
			targets := map[string]string{f.primary: "邓丽君"}
			switch tc.tool {
			case "merge_artists":
				input["source_artist_id"] = f.alias
				input["source_expected_revision"] = f.revision(f.alias)
				targets[f.alias] = "Teresa Teng"
			case "rename_artist":
				input["mode"], input["name"] = "set", "新人工主名"
			case "ignore_artist_candidate":
				input["other_artist_id"] = f.alias
				input["other_expected_revision"] = f.revision(f.alias)
				input["ignored"] = true
				left, right := "c0000000-0000-4000-8000-000000000001", "c0000000-0000-4000-8000-000000000002"
				mustExec(t, f.app, `INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,left_name,right_name)
 VALUES($1,$2,'album_track_one_to_one',$3,'邓丽君','Teresa Teng')`, left, right, f.releases[0].releaseID)
				input["key_pairs"] = []map[string]string{{"left_key_id": left, "right_key_id": right}}
				targets[f.alias] = "Teresa Teng"
			}
			revisions := map[string]int64{}
			for id := range targets {
				revisions[id] = int64(f.revision(id))
			}
			var mainHits, reviewHits atomic.Int32
			reviewRequests := make(chan artistReviewRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ToolChoice json.RawMessage `json:"tool_choice"`
					Messages   []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if len(request.ToolChoice) != 0 {
					if reviewHits.Add(1) != 1 || len(request.Messages) != 2 ||
						request.Messages[0].Content != runtime.ReviewSystemPrompt || request.Messages[1].Role != "user" {
						t.Error("独立审查请求的次数、消息或系统提示词错误")
						http.Error(w, "bad review", 400)
						return
					}
					content := request.Messages[1].Content
					if strings.Contains(content, "ARTIST_REVIEW_PRIVATE_") {
						t.Error("独立审查收到了主对话文字或原始依据")
					}
					var payload artistReviewRequest
					if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload.Items) != 1 {
						t.Errorf("审查方案项错误：%s，%v", content, err)
						http.Error(w, "bad items", 400)
						return
					}
					reviewRequests <- payload
					writeArtistReviewTool(w, "submit_review", map[string]any{"items": []any{map[string]any{
						"item_id": payload.Items[0].ID, "decision": "approved", "risk": "low", "reasons": []string{"艺人目标明确"},
					}}})
					return
				}
				if mainHits.Add(1) == 1 {
					writeArtistReviewTool(w, tc.tool, input)
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"完成\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()

			call := func(method, path string, body any, status int) *httptest.ResponseRecorder {
				t.Helper()
				w := f.call(method, "/api/v1/"+path, body, &f.admin)
				if w.Code != status {
					t.Fatalf("%s %s 返回 HTTP %d，期望 %d：%s", method, path, w.Code, status, w.Body)
				}
				return w
			}
			call("PUT", "me/steward/settings", map[string]string{
				"endpoint": server.URL, "model": "artist-main", "review_model": "artist-review", "protocol": "openai",
			}, 200)
			w := call("POST", "me/steward/conversations", map[string]string{"mode": "steward"}, 201)
			conversation, ok := decodeJSONMap(t, w)["id"].(string)
			if !ok || conversation == "" {
				t.Fatal(w.Body.String())
			}
			w = call("POST", "me/steward/conversations/"+conversation+"/messages", map[string]string{"text": "ARTIST_REVIEW_PRIVATE_USER"}, 200)
			if reviewHits.Load() != 1 || !strings.Contains(w.Body.String(), `"reason":"completed"`) {
				t.Fatalf("管家模式未完成独立审查，审查请求 %d 次：%s", reviewHits.Load(), w.Body)
			}
			assertArtistReviewTargets(t, <-reviewRequests, tc.tool, targets, revisions)
			ctx := context.Background()
			detail, err := runtime.NewStore(f.app.database).GetConversation(ctx, f.admin.id, conversation)
			if err != nil || len(detail.Plans) != 1 || len(detail.Plans[0].Items) != 1 || detail.Plans[0].Review == nil {
				t.Fatalf("审查方案为 %+v，错误 %v", detail.Plans, err)
			}
			plan := detail.Plans[0]
			item := plan.Items[0]
			if plan.Review.PolicyVersion != 3 || item.OperationID == nil || !item.Revertible {
				t.Fatalf("审查或执行结果错误：%+v，%+v", plan.Review, item)
			}
			record, err := operations.Load(ctx, f.app.database, *item.OperationID)
			if err != nil || record.Type != tc.kind || record.TargetID != f.primary {
				t.Fatalf("执行记录为 %+v，错误 %v", record, err)
			}
			var origin operations.Origin
			if err := json.Unmarshal(record.Origin, &origin); err != nil || origin.ConversationID != conversation ||
				origin.ReviewID != plan.Review.ID || origin.ReviewDecision != "approved" || origin.Mode != "steward" {
				t.Fatalf("执行来源 %+v，错误 %v", origin, err)
			}
			if tc.tool != "merge_artists" {
				return
			}
			w = call("GET", "operations?artist_id="+f.alias, nil, 200)
			items := artistSampleItems(t, decodeJSONMap(t, w), 1)
			listed := items[0].(map[string]any)
			if listed["id"] != record.ID || listed["revertible"] != true {
				t.Fatalf("操作列表中的合并记录无法撤销：%v", listed)
			}
			w = call("POST", "operations/"+record.ID+"/revert", nil, 200)
			undo := decodeJSONMap(t, w)
			if undo["reverts_operation_id"] != record.ID || undo["target_id"] != f.primary {
				t.Fatalf("撤销回执错误：%v", undo)
			}
			w = call("GET", "artists/"+f.alias, nil, 200)
			if artist := decodeJSONMap(t, w); artist["redirect_artist_id"] != nil {
				t.Fatalf("撤销后来源艺人仍被重定向：%v", artist)
			}
		})
	}
}

func writeArtistReviewTool(w io.Writer, name string, input any) {
	args, _ := json.Marshal(input)
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"index": 0, "finish_reason": "tool_calls",
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "artist-review-call", "type": "function",
			"function": map[string]any{"name": name, "arguments": string(args)},
		}}},
	}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
}

func assertArtistReviewTargets(t *testing.T, payload artistReviewRequest, tool string, targets map[string]string, revisions map[string]int64) {
	t.Helper()
	item := payload.Items[0]
	if item.ID == "" || item.Tool != tool || !item.Revertible || len(payload.Evidence) != 1 || payload.Evidence[0].ID != item.ID {
		t.Fatalf("审查收到的方案项和证据不匹配：%+v", payload)
	}
	facts := payload.Evidence[0].Facts
	if len(facts.Targets) != len(targets) {
		t.Fatalf("审查目标为 %+v，期望 %v", facts.Targets, targets)
	}
	seen := map[string]bool{}
	for _, target := range facts.Targets {
		if seen[target.ID] || targets[target.ID] == "" || target.Type != "artist" || target.Revision != revisions[target.ID] ||
			target.Source != "catalog_read_summary" || target.Data.Name.Value != targets[target.ID] ||
			target.Data.Name.Source != "catalog_effective_untrusted" || target.Data.Redirected ||
			target.Data.ReleaseCount != 1 || target.Data.TrackCount < 1 {
			t.Errorf("艺人目标证据错误：%+v", target)
		}
		seen[target.ID] = true
	}
	if facts.External.Source != "program_untrusted_external" || len(facts.External.Artists) != len(targets) {
		t.Fatalf("艺人外部证据的来源或覆盖范围错误：%+v", facts.External)
	}
	clear(seen)
	for _, target := range facts.External.Artists {
		if seen[target.ID] || targets[target.ID] == "" || target.Links == nil || len(target.Links) != 0 {
			t.Errorf("没有外部对应的艺人应带空数组：%+v", target)
		}
		seen[target.ID] = true
	}
}
