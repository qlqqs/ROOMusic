package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func TestPostgreSQLStewardHTTPContinueReplaysHistory(t *testing.T) {
	for _, protocol := range []string{"openai", "messages"} {
		for _, lastRole := range []string{"user", "tool"} {
			t.Run(protocol+"/"+lastRole, func(t *testing.T) {
				f := newAPIFixture(t)
				var hits atomic.Int32
				requests := make(chan []byte, 8)
				failAt := int32(1)
				if lastRole == "tool" {
					failAt = 2
				}
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					requests <- raw
					n := hits.Add(1)
					if n == failAt {
						w.WriteHeader(http.StatusBadGateway)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic model failure"}}`)
						return
					}
					step := 1
					if n == failAt+2 {
						step = 3
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, apiModelResponse(protocol, step, false))
				}))
				defer s.Close()
				f.configure(t, "admin", s.URL, protocol)
				id := f.conversation(t, "admin")
				path := "me/steward/conversations/" + id
				assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "待重试的合成问题"}), runtime.EndProviderError)
				for i := int32(0); i < failAt; i++ {
					<-requests
				}
				// 空标题也应原样保留，继续运行不能再次自动命名。
				title := "保留的合成标题"
				if lastRole == "user" {
					title = ""
				}
				if err := f.store.RenameConversation(context.Background(), f.users["admin"], id, title); err != nil {
					t.Fatal(err)
				}
				before, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
				if err != nil {
					t.Fatal(err)
				}
				if len(before.Messages) == 0 || before.Messages[len(before.Messages)-1].Role != lastRole || before.Conversation.Status != "idle" {
					t.Fatalf("重试前历史=%+v", before)
				}
				// 初次失败后改名，继续请求必须使用当前设置。
				apiStatus(t, f.request("admin", "PUT", "me/steward/settings", map[string]string{"display_name": "「小\r\n曲」"}), 200, "")
				w := f.request("admin", "POST", path+"/continue", nil)
				assertDone(t, w, runtime.EndCompleted)
				assertContinueEvents(t, w)
				for i := 0; i < 2; i++ {
					raw := <-requests
					calls := i
					if lastRole == "tool" {
						calls++
					}
					assertWireHistory(t, protocol, raw, calls, "待重试的合成问题")
					assertContinueSystemName(t, protocol, raw)
				}
				after, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Messages) != len(before.Messages)+3 || after.Conversation.Title != title || after.Conversation.Status != "idle" {
					t.Fatalf("重试后历史=%+v", after)
				}
				if !reflect.DeepEqual(after.Messages[:len(before.Messages)], before.Messages) {
					t.Error("继续运行修改了已有消息")
				}
				users := 0
				for _, message := range after.Messages {
					if message.Role == "user" {
						users++
					}
				}
				if users != 1 || hits.Load() != failAt+2 {
					t.Errorf("用户消息=%d 模型请求=%d", users, hits.Load())
				}
				assertPrivateLogs(t, f)
			})
		}
	}
}

func assertContinueSystemName(t *testing.T, protocol string, raw []byte) {
	t.Helper()
	var request struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	// Messages 协议的 content 是块数组，只解码顶层 system。
	if protocol == "messages" {
		var body struct {
			System string `json:"system"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		request.System = body.System
	} else {
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) == 0 || request.Messages[0].Role != "system" {
			t.Fatalf("缺少系统消息：%s", raw)
		}
		request.System = request.Messages[0].Content
	}
	if !strings.HasSuffix(request.System, "用户称呼你为「小曲」。曲库修改直接调用对应的修改工具，不要先用文字征求确认。调用后系统会自动把它整理成方案，等用户在界面上确认后才执行；本会话已获用户免确认的工具会直接执行。") || strings.ContainsAny(request.System, "\r\n") {
		t.Errorf("继续请求的系统提示词=%q", request.System)
	}
}

func assertContinueEvents(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var kinds []runtime.EventType
	var eventName string
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event mapEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if eventName != string(event.Type) {
			t.Errorf("SSE 事件名=%q 数据类型=%q", eventName, event.Type)
		}
		kinds = append(kinds, event.Type)
	}
	want := []runtime.EventType{runtime.EventText, runtime.EventToolStarted, runtime.EventToolFinished, runtime.EventText, runtime.EventUsage, runtime.EventDone}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("继续事件=%v，期望=%v，响应=%s", kinds, want, w.Body)
	}
}

func TestPostgreSQLStewardHTTPContinueNothingReleasesConversation(t *testing.T) {
	for _, history := range []string{"empty", "assistant"} {
		t.Run(history, func(t *testing.T) {
			f := newAPIFixture(t)
			var hits atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.WriteString(w, apiModelResponse("openai", 3, false))
			}))
			defer s.Close()
			f.configure(t, "admin", s.URL, "openai")
			id := f.conversation(t, "admin")
			path := "me/steward/conversations/" + id
			if history == "assistant" {
				assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "已有最终回答"}), runtime.EndCompleted)
			}
			before, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
			if err != nil {
				t.Fatal(err)
			}
			beforeHits := hits.Load()
			for i := 0; i < 2; i++ {
				assertContinueProblem(t, f.request("admin", "POST", path+"/continue", nil), 409, "nothing_to_continue")
				after, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
				if err != nil || after.Conversation.Status != "idle" || after.Conversation.Title != before.Conversation.Title || after.Conversation.Unread != before.Conversation.Unread || !reflect.DeepEqual(after.Conversation.LastRunFinishedAt, before.Conversation.LastRunFinishedAt) || !reflect.DeepEqual(after.Messages, before.Messages) {
					t.Fatalf("拒绝继续后会话=%+v 错误=%v", after, err)
				}
			}
			if hits.Load() != beforeHits {
				t.Error("没有待继续内容时调用了模型")
			}
			// 普通发送仍能启动，证明数据库和本进程的占用均已释放。
			assertDone(t, f.request("admin", "POST", path+"/messages", map[string]string{"text": "拒绝继续后仍可发送"}), runtime.EndCompleted)
		})
	}
}

func assertContinueProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	apiStatus(t, w, status, code)
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("前置检查启动了 SSE：%s", w.Body)
	}
	var problem httpserver.APIError
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if requestID := w.Header().Get("X-Request-ID"); requestID == "" || problem.RequestID != requestID {
		t.Errorf("错误响应的请求关联标识不一致：%s", w.Body)
	}
}

func TestPostgreSQLStewardHTTPContinuePreconditionsMatchMessages(t *testing.T) {
	for _, tc := range []struct {
		name, role, code string
		status           int
	}{
		{"not_configured", "member", "steward_not_configured", 409},
		{"busy", "member", "conversation_busy", 409},
		{"disabled", "member", "steward_disabled", 403},
		{"foreign_member", "other", "not_found", 404},
		{"foreign_admin", "admin", "not_found", 404},
		{"unauthorized", "", "unauthorized", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			ctx := context.Background()
			conversation, err := f.store.CreateConversation(ctx, f.users["member"], "", "前置检查合成会话")
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "busy" {
				if err := f.store.AcquireConversation(ctx, f.users["member"], conversation.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "disabled" {
				apiStatus(t, f.request("admin", "PUT", "admin/steward/settings", map[string]bool{"members_enabled": false}), 200, "")
			}
			path := "me/steward/conversations/" + conversation.ID
			for _, suffix := range []string{"/continue", "/messages"} {
				var body any
				if suffix == "/messages" {
					body = map[string]string{"text": "合成问题"}
				}
				assertContinueProblem(t, f.request(tc.role, "POST", path+suffix, body), tc.status, tc.code)
			}
			detail, err := f.store.GetConversation(ctx, f.users["member"], conversation.ID)
			wantStatus := "idle"
			if tc.name == "busy" {
				wantStatus = "running"
			}
			if err != nil || len(detail.Messages) != 0 || detail.Conversation.Status != wantStatus {
				t.Fatalf("前置检查改变了会话：%+v 错误=%v", detail, err)
			}
		})
	}
}
