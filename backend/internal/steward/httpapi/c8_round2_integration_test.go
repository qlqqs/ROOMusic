package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostgreSQLC8SearXNGPreflightOnlyWhenRelevant(t *testing.T) {
	for _, tc := range []struct {
		name         string
		storedSource string
		patch        map[string]any
		status       int
	}{
		{"rename_none", "none", map[string]any{"display_name": "新名称"}, 200},
		{"rename_tavily", "tavily", map[string]any{"display_name": "新名称"}, 200},
		{"rename_native", "native", map[string]any{"display_name": "新名称"}, 200},
		{"explicit_url", "none", map[string]any{"searxng_url": "http://127.0.0.1"}, 403},
		{"merged_searxng", "searxng", map[string]any{"display_name": "新名称"}, 403},
		{"switch_to_searxng", "none", map[string]any{"search_source": "searxng"}, 403},
		{"switch_from_searxng", "searxng", map[string]any{"search_source": "none"}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			// 合成历史设置模拟放行名单被撤销后的地址；只检查预检，不建立出站连接。
			if _, err := f.store.SaveUserSettings(t.Context(), f.users["member"], runtime.UserSettingsInput{SearchSource: tc.storedSource, SearXNGURL: "http://127.0.0.1"}); err != nil {
				t.Fatal(err)
			}
			code := ""
			if tc.status == 403 {
				code = "egress_denied"
			}
			apiStatus(t, f.request("member", "PUT", "me/steward/settings", tc.patch), tc.status, code)
		})
	}
}

func TestPostgreSQLC8OversizedProviderBlocksTextOnly(t *testing.T) {
	f := newAPIFixture(t)
	requests := 0
	secretBlock := strings.Repeat("opaque-content-", 21000)
	frame := func(kind string, body any) string {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return "event: " + kind + "\ndata: " + string(raw) + "\n\n"
	}
	stream := frame("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "web_search_tool_result", "tool_use_id": "s1", "content": []any{map[string]any{"type": "web_search_result", "url": "https://example.org", "title": "来源", "encrypted_content": secretBlock}}}}) +
		frame("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) +
		frame("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": "保留文字"}}) +
		frame("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}) +
		frame("message_delta", map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "end_turn"}}) +
		frame("message_stop", map[string]string{"type": "message_stop"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if requests == 2 && (!bytes.Contains(raw, []byte("保留文字")) || bytes.Contains(raw, []byte("opaque-content-"))) {
			t.Error("超限历史没有退为仅文字")
		}
		io.WriteString(w, stream)
	}))
	defer server.Close()
	f.configure(t, "admin", server.URL, "messages")
	id := f.conversation(t, "admin")
	for i := 0; i < 2; i++ {
		assertDone(t, f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", map[string]string{"text": "合成问题"}), runtime.EndCompleted)
	}
	var assistants, missing int
	if err := f.db.QueryRow(`SELECT count(*),count(*) FILTER (WHERE provider_blocks IS NULL) FROM steward_messages WHERE conversation_id=$1 AND role='assistant'`, id).Scan(&assistants, &missing); err != nil {
		t.Fatal(err)
	}
	if assistants != 2 || missing != 2 {
		t.Fatalf("原始块超限落库：assistant=%d NULL=%d", assistants, missing)
	}
}
