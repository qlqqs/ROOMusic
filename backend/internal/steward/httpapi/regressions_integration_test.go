package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (f *apiFixture) allowEndpoint(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"host": u.Hostname(), "port": port}
	apiStatus(t, f.request("admin", "POST", "admin/steward/egress-allowlist", entry), 200, "")
	return entry
}

func assertDone(t *testing.T, w *httptest.ResponseRecorder, reason runtime.EndReason) endResponse {
	t.Helper()
	apiStatus(t, w, 200, "")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("不是 SSE：%s", w.Body)
	}
	var done []endResponse
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event mapEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == runtime.EventDone {
			done = append(done, event.End)
		}
	}
	if len(done) != 1 || done[0].Reason != reason {
		t.Fatalf("期望唯一 done(%s)，实际 %s", reason, w.Body)
	}
	return done[0]
}

// 按协议检查调用与结果的配对，而不是仅在请求文本里寻找工具名。
func assertWireHistory(t *testing.T, protocol string, raw []byte, callsWant int, texts ...string) {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Calls   []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			CallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	pending := map[string]bool{}
	calls, results := 0, 0
	var allText strings.Builder
	for _, m := range body.Messages {
		if protocol == "openai" {
			if m.Role == "tool" {
				if !pending[m.CallID] {
					t.Errorf("结果缺少对应调用：%s", m.CallID)
				}
				delete(pending, m.CallID)
				results++
			} else if len(pending) > 0 {
				t.Errorf("下一条 %s 消息之前尚有调用缺结果：%v", m.Role, pending)
			}
			for _, c := range m.Calls {
				pending[c.ID] = true
				calls++
			}
			var text string
			_ = json.Unmarshal(m.Content, &text)
			allText.WriteString(text)
		} else {
			var blocks []struct {
				Type    string          `json:"type"`
				ID      string          `json:"id"`
				CallID  string          `json:"tool_use_id"`
				Text    string          `json:"text"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(m.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			for _, b := range blocks {
				switch b.Type {
				case "tool_use":
					pending[b.ID] = true
					calls++
				case "tool_result":
					if !pending[b.CallID] {
						t.Errorf("结果缺少对应调用：%s", b.CallID)
					}
					delete(pending, b.CallID)
					results++
					allText.Write(b.Content)
				case "text":
					allText.WriteString(b.Text)
				}
			}
			if m.Role == "user" && len(pending) > 0 {
				t.Errorf("用户消息之后仍缺结果：%v", pending)
			}
		}
	}
	if len(pending) > 0 || calls != callsWant || results != callsWant {
		t.Errorf("调用=%d 结果=%d 未配对=%v，期望=%d", calls, results, pending, callsWant)
	}
	for _, text := range texts {
		if !strings.Contains(allText.String(), text) {
			t.Errorf("历史缺少 %q：%s", text, raw)
		}
	}
}

func TestPostgreSQLStewardHTTPMultiTurnHistory(t *testing.T) {
	for _, protocol := range []string{"openai", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			f := newAPIFixture(t)
			requests := make(chan []byte, 4)
			var n atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- raw
				step := 3
				if n.Add(1) == 1 {
					step = 1
				}
				_, _ = io.WriteString(w, apiModelResponse(protocol, step, false))
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, protocol)
			path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
			assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "第一轮合成问题"}), runtime.EndCompleted)
			<-requests
			<-requests
			assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "第二轮合成问题"}), runtime.EndCompleted)
			assertWireHistory(t, protocol, <-requests, 1, "第一轮合成问题", "第二轮合成问题", "synthetic-answer", "synthetic-tool-output")
			assertPrivateLogs(t, f)
		})
	}
}

func TestPostgreSQLStewardHTTPIncompleteHistoryGetsInterruptedResults(t *testing.T) {
	for _, protocol := range []string{"openai", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			f := newAPIFixture(t)
			received := make(chan []byte, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				received <- raw
				_, _ = io.WriteString(w, apiModelResponse(protocol, 3, false))
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, protocol)
			id := f.conversation(t, "admin")
			// 直接构造进程中断遗留的两次工具调用，故意按与 position 相反的顺序插入。
			messageID := uuid.New()
			content := `{"role":"assistant","content":[{"type":"tool_call","tool_call":{"id":"pending-a","name":"synthetic_lookup","arguments":{}}},{"type":"tool_call","tool_call":{"id":"pending-b","name":"synthetic_lookup","arguments":{}}}]}`
			if _, err := f.db.Exec(`INSERT INTO steward_messages(id,conversation_id,sequence,role,content) VALUES($1,$2,1,'assistant',$3)`, messageID, id, content); err != nil {
				t.Fatal(err)
			}
			for _, position := range []int{1, 0} {
				if _, err := f.db.Exec(`INSERT INTO steward_tool_calls(id,message_id,position,tool_name,input) VALUES($1,$2,$3,$4,'{}')`, uuid.New(), messageID, position, fmt.Sprintf("position-%d", position)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.Exec(`UPDATE steward_conversations SET status='running' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if count, err := f.store.ResetRunningConversations(context.Background()); err != nil || count != 1 {
				t.Fatalf("恢复=%d %v", count, err)
			}
			detail, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
			if err != nil {
				t.Fatal(err)
			}
			for i, c := range detail.Messages[0].ToolCalls {
				if c.Name != fmt.Sprintf("position-%d", i) || c.Status != "failed" || c.ErrorCode == nil || *c.ErrorCode != "interrupted" {
					t.Errorf("恢复或排序错误：%+v", c)
				}
			}
			assertDone(t, f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", map[string]string{"text": "继续合成对话"}), runtime.EndCompleted)
			assertWireHistory(t, protocol, <-received, 2, "interrupted", "继续合成对话")
		})
	}
}

func TestPostgreSQLStewardHTTPToolPanicEndsAndConversationRecovers(t *testing.T) {
	f := newAPIFixture(t)
	if err := f.registry.Register(tool.Definition{Name: "synthetic_panic", MinRole: tool.RoleMember, Scope: tool.ScopeRead, Input: json.RawMessage(`{"type":"object"}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) { panic(apiSyntheticKey) }}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, strings.ReplaceAll(apiModelResponse("openai", 1, false), "synthetic_lookup", "synthetic_panic"))
		} else {
			_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
		}
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	id := f.conversation(t, "admin")
	path := "me/steward/conversations/" + id
	end := assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "触发合成 panic"}), runtime.EndInternalError)
	if end.ErrorCode != "internal" {
		t.Errorf("错误码=%s", end.ErrorCode)
	}
	if !strings.Contains(f.logs.String(), "steward.run.panicked") {
		t.Error("缺少 panic 日志")
	}
	apiStatus(t, f.request("admin", "GET", path, nil), 200, "")
	assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "继续合成对话"}), runtime.EndCompleted)
	assertPrivateLogs(t, f)
}

func TestPostgreSQLStewardHTTPMemberAllowlistConversation(t *testing.T) {
	f := newAPIFixture(t)
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Error("无密钥端点收到认证头")
		}
		_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
	}))
	defer s.Close()
	settings := map[string]any{"endpoint": s.URL, "protocol": "openai", "model": "synthetic-model", "key": nil}
	apiStatus(t, f.request("member", "PUT", "me/steward/settings", settings), 403, "egress_denied")
	entry := f.allowEndpoint(t, s.URL)
	apiStatus(t, f.request("member", "PUT", "me/steward/settings", settings), 200, "")
	path := "me/steward/conversations/" + f.conversation(t, "member") + "/messages"
	assertDone(t, f.request("member", "POST", path, map[string]string{"text": "本地合成对话"}), runtime.EndCompleted)
	apiStatus(t, f.request("admin", "DELETE", "admin/steward/egress-allowlist", entry), 200, "")
	apiStatus(t, f.request("member", "POST", path, map[string]string{"text": "拒绝这次请求"}), 403, "egress_denied")
	if hits.Load() != 1 {
		t.Errorf("模型请求次数=%d", hits.Load())
	}
}

func TestPostgreSQLStewardHTTPRedirectDoesNotForwardKey(t *testing.T) {
	f := newAPIFixture(t)
	var targetHits, sourceHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		if r.Header.Get("X-Api-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("重定向目标收到密钥头")
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+apiSyntheticKey {
			t.Error("源端点未收到兼容协议密钥头")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	f.configure(t, "admin", source.URL, "messages")
	path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
	end := assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "合成重定向"}), runtime.EndProviderError)
	if end.ErrorCode != "provider_unreachable" || sourceHits.Load() != 1 || targetHits.Load() != 0 {
		t.Fatalf("end=%+v 源=%d 目标=%d", end, sourceHits.Load(), targetHits.Load())
	}
	assertPrivateLogs(t, f)
}

func TestPostgreSQLStewardHTTPAllowlistNormalization(t *testing.T) {
	f := newAPIFixture(t)
	for _, tc := range []struct {
		host, want string
		port       int
	}{{" Synthetic.INVALID. ", "synthetic.invalid", 1234}, {"::FFFF:127.0.0.1", "127.0.0.1", 1234}, {"2001:0DB8:0:0::1", "2001:db8::1", 65535}} {
		entry := map[string]any{"host": tc.host, "port": tc.port}
		w := f.request("admin", "POST", "admin/steward/egress-allowlist", entry)
		apiStatus(t, w, 200, "")
		var got allowlistResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Host != tc.want || got.Port != tc.port {
			t.Errorf("条目=%+v", got)
		}
		for _, port := range []int{tc.port, tc.port - 1} {
			allowed, err := f.store.IsEgressAllowed(context.Background(), tc.want, port)
			if err != nil || allowed != (port == tc.port) {
				t.Errorf("端口 %d: %v %v", port, allowed, err)
			}
		}
		apiStatus(t, f.request("admin", "DELETE", "admin/steward/egress-allowlist", map[string]any{"host": tc.want, "port": tc.port}), 200, "")
	}
	for _, entry := range []map[string]any{{"host": "a.invalid:80", "port": 80}, {"host": "a.invalid", "port": 0}, {"host": "a.invalid", "port": 65536}, {"host": "[::1]", "port": 80}} {
		apiStatus(t, f.request("admin", "POST", "admin/steward/egress-allowlist", entry), 400, "invalid_input")
	}
}

func TestPostgreSQLStewardHTTPStoredKeyAbsentFromAllColumnsAndLogs(t *testing.T) {
	f := newAPIFixture(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"message":"`+apiSyntheticKey+`"}}`)
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
	end := assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "synthetic-message-body"}), runtime.EndProviderError)
	if end.StatusCode != 401 || end.ErrorKind != "authentication" || strings.Contains(end.Message, apiSyntheticKey) || end.Message == "" {
		t.Errorf("脱敏结束事件=%+v", end)
	}
	rows, err := f.db.Query(`SELECT table_name,column_name,data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name LIKE 'steward\_%' ESCAPE '\' AND data_type IN ('text','character varying','bytea','json','jsonb') ORDER BY table_name,column_name`)
	if err != nil {
		t.Fatal(err)
	}
	type column struct{ table, name, kind string }
	var columns []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.table, &c.name, &c.kind); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(columns) == 0 {
		t.Fatal("未扫描任何列")
	}
	tables := map[string]bool{}
	for _, c := range columns {
		tables[c.table] = true
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		expression := "strpos(" + quote(c.name) + "::text,$1)>0"
		if c.kind == "bytea" {
			expression = "position(convert_to($1,'UTF8') in " + quote(c.name) + ")>0"
		}
		var found bool
		if err := f.db.QueryRow("SELECT EXISTS(SELECT 1 FROM "+quote(c.table)+" WHERE "+expression+")", apiSyntheticKey).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found {
			t.Errorf("密钥明文出现在 %s.%s", c.table, c.name)
		}
	}
	// 实例开关表只有整数和布尔列，其余五表均须被检索。
	if len(tables) != 5 {
		t.Errorf("检索表=%v", tables)
	}
	assertPrivateLogs(t, f)
}

func TestPostgreSQLStewardHTTPProbeFailurePreservesSettings(t *testing.T) {
	f := newAPIFixture(t)
	var fail, noKey atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if noKey.Load() && (r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "") {
			t.Error("清除密钥后仍发送认证头")
		}
		if fail.Load() {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic rejected"}}`)
			return
		}
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"data":[{"id":"synthetic-model"}]}`)
		} else {
			_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
		}
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	apiStatus(t, f.request("admin", "POST", "me/steward/settings/probe", map[string]any{}), 200, "")
	before := f.request("admin", "GET", "me/steward/settings", nil)
	apiStatus(t, before, 200, "")
	if !bytes.Contains(before.Body.Bytes(), []byte(`"tool_support":"unconfirmed"`)) {
		t.Fatal(before.Body.String())
	}
	fail.Store(true)
	apiStatus(t, f.request("admin", "POST", "me/steward/settings/probe", map[string]any{"endpoint": s.URL + "/changed", "key": nil, "model": "changed"}), 503, "provider_unreachable")
	after := f.request("admin", "GET", "me/steward/settings", nil)
	apiStatus(t, after, 200, "")
	if !bytes.Equal(before.Body.Bytes(), after.Body.Bytes()) {
		t.Fatalf("失败探测改变设置：之前=%s 之后=%s", before.Body, after.Body)
	}
	fail.Store(false)
	noKey.Store(true)
	apiStatus(t, f.request("admin", "POST", "me/steward/settings/probe", map[string]any{"key": nil}), 200, "")
	after = f.request("admin", "GET", "me/steward/settings", nil)
	var settings settingsResponse
	_ = json.Unmarshal(after.Body.Bytes(), &settings)
	if settings.KeySaved || settings.KeyLastFour != "" {
		t.Fatal("探测 key:null 没有清除密钥")
	}
	assertDone(t, f.request("admin", "POST", "me/steward/conversations/"+f.conversation(t, "admin")+"/messages", map[string]string{"text": "无密钥继续"}), runtime.EndCompleted)
}

func TestPostgreSQLStewardHTTPSettingsChangesClearProbe(t *testing.T) {
	for _, change := range []string{"endpoint", "protocol", "key", "clear_key", "authority", "authority_protocol", "host", "host_protocol"} {
		t.Run(change, func(t *testing.T) {
			f := newAPIFixture(t)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, "openai")
			if _, err := f.db.Exec(`UPDATE steward_user_settings SET probe_result='{"tool_support":"unsupported"}',probed_at=now(),overrides='{"protocol":"openai","model":"manual-model","tool_support":"unsupported"}' WHERE user_id=$1`, f.users["admin"]); err != nil {
				t.Fatal(err)
			}
			apiStatus(t, f.request("admin", "POST", "me/steward/conversations", map[string]any{}), 422, "provider_no_tool_support")
			patch := map[string]any{}
			switch change {
			case "endpoint":
				patch["endpoint"] = s.URL + "/new"
			case "protocol":
				patch["protocol"] = "messages"
			case "key":
				patch["key"] = "synthetic-replacement-key"
			case "clear_key":
				patch["key"] = nil
			case "authority", "authority_protocol":
				patch["endpoint"] = "http://127.0.0.1:1"
			case "host", "host_protocol":
				patch["endpoint"] = strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
			}
			if strings.HasSuffix(change, "_protocol") {
				patch["protocol"] = "messages"
			}
			w := f.request("admin", "PUT", "me/steward/settings", patch)
			apiStatus(t, w, 200, "")
			read := f.request("admin", "GET", "me/steward/settings", nil)
			apiStatus(t, read, 200, "")
			if !bytes.Equal(w.Body.Bytes(), read.Body.Bytes()) {
				t.Fatalf("保存与读取不一致：保存=%s 读取=%s", w.Body, read.Body)
			}
			var settings settingsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil {
				t.Fatal(err)
			}
			if string(settings.ProbeResult) != "null" || settings.ProbedAt != nil {
				t.Fatalf("旧探测未清除：%s", w.Body)
			}
			if string(settings.Overrides) != "{}" {
				t.Fatalf("未提交 overrides 时旧覆盖项未清除：%s", w.Body)
			}
			authorityChanged := strings.HasPrefix(change, "authority") || strings.HasPrefix(change, "host")
			if (authorityChanged || change == "clear_key") && (settings.KeySaved || settings.KeyLastFour != "") {
				t.Fatal("密钥未清除")
			}
			if authorityChanged {
				wantProtocol := ""
				if strings.HasSuffix(change, "_protocol") {
					wantProtocol = "messages"
				}
				if settings.Protocol != wantProtocol {
					t.Fatalf("更换主机或端口后协议=%q，期望=%q", settings.Protocol, wantProtocol)
				}
			}
			if change == "endpoint" || change == "clear_key" {
				assertDone(t, f.request("admin", "POST", "me/steward/conversations/"+f.conversation(t, "admin")+"/messages", map[string]string{"text": "更换端点后继续"}), runtime.EndCompleted)
			}
		})
	}
}

func TestPostgreSQLStewardHTTPPatchConversationTitleAndModel(t *testing.T) {
	f := newAPIFixture(t)
	models := make(chan string, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		models <- body.Model
		_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	path := "me/steward/conversations/" + f.conversation(t, "admin")
	apiStatus(t, f.request("admin", "PATCH", path, map[string]string{}), 400, "invalid_input")
	for _, tc := range []struct{ patch, want map[string]string }{{map[string]string{"title": "新标题"}, map[string]string{"title": "新标题", "model": "synthetic-model"}}, {map[string]string{"model": "override"}, map[string]string{"title": "新标题", "model": "override"}}, {map[string]string{"title": "最终标题", "model": ""}, map[string]string{"title": "最终标题", "model": ""}}} {
		tc.want["mode"] = "assistant"
		w := f.request("admin", "PATCH", path, tc.patch)
		apiStatus(t, w, 200, "")
		var got map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("PATCH=%s 期望=%v 错误=%v", w.Body, tc.want, err)
		}
	}
	assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "默认模型"}), runtime.EndCompleted)
	if got := <-models; got != "synthetic-model" {
		t.Errorf("空模型未回落默认：%s", got)
	}
}

func TestPostgreSQLStewardHTTPMemberDisabledDuringRunReceivesDone(t *testing.T) {
	f := newAPIFixture(t)
	started, released := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer s.Close()
	f.allowEndpoint(t, s.URL)
	f.configure(t, "member", s.URL, "openai")
	id := f.conversation(t, "member")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- f.requestTimeout("member", "POST", "me/steward/conversations/"+id+"/messages", map[string]string{"text": "等待成员开关"}, credentialCheckInterval+10*time.Second)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("模型没有启动")
	}
	apiStatus(t, f.request("admin", "PUT", "admin/steward/settings", map[string]bool{"members_enabled": false}), 200, "")
	select {
	case w := <-result:
		assertDone(t, w, runtime.EndCancelled)
	case <-time.After(credentialCheckInterval + 5*time.Second):
		t.Fatal("关闭成员开关后未结束")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("模型连接未释放")
	}
	d, err := f.store.GetConversation(context.Background(), f.users["member"], id)
	if err != nil || d.Conversation.Status != "idle" {
		t.Fatalf("会话未释放：%+v %v", d, err)
	}
	f.api.mu.Lock()
	left := len(f.api.running)
	f.api.mu.Unlock()
	if left != 0 {
		t.Errorf("运行登记残留=%d", left)
	}
}

func TestPostgreSQLStewardHTTPTimeoutResponses(t *testing.T) {
	f := newAPIFixture(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(504)
		_, _ = io.WriteString(w, `{"error":{"message":"synthetic model timeout"}}`)
	}))
	defer s.Close()
	f.configure(t, "admin", s.URL, "openai")
	w := f.request("admin", "POST", "me/steward/settings/probe", map[string]any{})
	apiStatus(t, w, 503, "provider_timeout")
	path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
	end := assertDone(t, f.request("admin", "POST", path, map[string]string{"text": "合成超时"}), runtime.EndProviderError)
	if end.ErrorKind != string(provider.ErrorTimeout) || end.ErrorCode != "provider_timeout" || end.StatusCode != 504 || end.Message != "synthetic model timeout" {
		t.Fatalf("超时结束事件=%+v", end)
	}
}

func TestPostgreSQLStewardHTTPOutboundRedirectDoesNotForwardCustomKey(t *testing.T) {
	f := newAPIFixture(t)
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-Api-Key") != "" {
			t.Error("目标收到自定义密钥头")
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != apiSyntheticKey {
			t.Error("源端点未收到自定义密钥头")
		}
		http.Redirect(w, r, target.URL, 307)
	}))
	defer source.Close()
	// 本地兼容端点使用 Bearer；自定义头通过 HTTP API 实际使用的出站客户端另行验证。
	client, err := f.api.outboundClient(context.Background(), identity.User{ID: f.users["admin"], Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodPost, source.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Api-Key", apiSyntheticKey)
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || hits.Load() != 0 {
		t.Fatalf("重定向结果=%v 目标请求=%d", err, hits.Load())
	}
}

func TestPostgreSQLStewardHTTPCredentialRecheckResults(t *testing.T) {
	for _, databaseError := range []bool{false, true} {
		t.Run(fmt.Sprintf("database_error_%v", databaseError), func(t *testing.T) {
			f := newAPIFixture(t)
			checked := make(chan struct{})
			f.api.WithCredentialCheck(func(*http.Request, identity.User) (bool, error) {
				close(checked)
				if databaseError {
					return false, fmt.Errorf("synthetic database unavailable")
				}
				return false, nil
			})
			started, released := make(chan struct{}), make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				close(started)
				if !databaseError {
					<-r.Context().Done()
					return
				}
				select {
				case <-checked:
				case <-r.Context().Done():
					return
				}
				select {
				case <-r.Context().Done():
					t.Error("数据库查询错误被当作凭据失效")
				case <-time.After(100 * time.Millisecond):
					_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
				}
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, "openai")
			path := "me/steward/conversations/" + f.conversation(t, "admin") + "/messages"
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				result <- f.requestTimeout("admin", "POST", path, map[string]string{"text": "等待凭据复查"}, credentialCheckInterval+10*time.Second)
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("模型没有启动")
			}
			select {
			case w := <-result:
				want := runtime.EndCancelled
				if databaseError {
					want = runtime.EndCompleted
				}
				assertDone(t, w, want)
			case <-time.After(credentialCheckInterval + 5*time.Second):
				t.Fatal("凭据复查后请求未结束")
			}
			select {
			case <-checked:
			default:
				t.Error("没有执行凭据复查")
			}
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Error("连接未释放")
			}
		})
	}
}
