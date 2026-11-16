package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func c10Events(t *testing.T, w *httptest.ResponseRecorder) []runtime.Event {
	t.Helper()
	var events []runtime.Event
	var id uint64
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "id: ") {
			var err error
			id, err = strconv.ParseUint(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(line, "data: ") {
			var e runtime.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
				t.Fatal(err)
			}
			if e.Seq == 0 || e.Seq != id {
				t.Fatal("SSE id 与 seq 不一致", line)
			}
			events = append(events, e)
		}
	}
	return events
}
func c10Wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("等待合成运行超时")
	}
}
func c10Request(f *apiFixture, ctx context.Context, role, method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://roomusic.test/api/v1/"+path, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Origin", "http://roomusic.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.tokens[role]})
	return r
}
func TestPostgreSQLStewardC10DisconnectReconnectAndUnread(t *testing.T) {
	f := newAPIFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"前半段\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
			io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"后半段\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.configure(t, "admin", server.URL, "openai")
	id := f.conversation(t, "admin")
	path := "me/steward/conversations/" + id
	apiStatus(t, f.request("admin", "GET", path+"/events", nil), 204, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.ServeHTTP(w, c10Request(f, ctx, "admin", "POST", path+"/messages", `{"text":"断开仍继续"}`))
	}()
	c10Wait(t, started)
	// 等到事件发布后再断开，避免把网络调度顺序当作产品行为。
	deadline := time.Now().Add(3 * time.Second)
	var seq uint64
	for seq == 0 && time.Now().Before(deadline) {
		sub := f.api.supervisor.Subscribe(id, 0)
		if sub != nil {
			for _, e := range sub.Replay {
				if e.Type == runtime.EventText {
					seq = e.Seq
				}
			}
			sub.Close()
		}
		if seq == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if seq == 0 {
		t.Fatal("没有文本事件")
	}
	cancel()
	c10Wait(t, done)
	detail, err := f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil || detail.Conversation.Status != "running" {
		t.Fatal("请求取消导致运行停止", detail, err)
	}
	apiStatus(t, f.request("admin", "POST", path+"/read", nil), 200, "")
	close(release)
	response := f.request("admin", "GET", path+"/events?after="+strconv.FormatUint(seq, 10), nil)
	assertDone(t, response, runtime.EndCompleted)
	text := ""
	for _, e := range c10Events(t, response) {
		if e.Seq <= seq {
			t.Error("游标之前事件被重放")
		}
		if e.Type == runtime.EventText {
			text += e.Text
		}
	}
	if text != "后半段" {
		t.Fatal(text)
	}
	detail, err = f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil || detail.Conversation.Status != "idle" || !detail.Conversation.Unread || detail.Conversation.LastRunFinishedAt == nil {
		t.Fatal(detail, err)
	}
	raw, _ := json.Marshal(detail.Messages)
	if !bytes.Contains(raw, []byte("前半段后半段")) {
		t.Fatal("完整回答未落库", string(raw))
	}
	// 已结束且游标等于或超过 done 时返回 204，落后游标仍回放 done。
	completed := c10Events(t, response)
	last := completed[len(completed)-1].Seq
	for _, after := range []uint64{last, last + 1} {
		apiStatus(t, f.request("admin", "GET", path+"/events?after="+strconv.FormatUint(after, 10), nil), 204, "")
	}
	// 请求头优先于 query；保留日志允许结束后重放。
	r := c10Request(f, context.Background(), "admin", "GET", path+"/events?after=invalid", "")
	r.Header.Set("Last-Event-ID", strconv.FormatUint(seq, 10))
	response = httptest.NewRecorder()
	f.handler.ServeHTTP(response, r)
	assertDone(t, response, runtime.EndCompleted)
	for _, cursor := range []string{"-1", "1.5", "invalid", "18446744073709551616"} {
		apiStatus(t, f.request("admin", "GET", path+"/events?after="+cursor, nil), 400, "invalid_input")
	}
	r = c10Request(f, context.Background(), "admin", "GET", path+"/events", "")
	r.Header.Set("Last-Event-ID", "-1")
	response = httptest.NewRecorder()
	f.handler.ServeHTTP(response, r)
	apiStatus(t, response, 400, "invalid_input")
	for _, suffix := range []string{"/events", "/read"} {
		method := "GET"
		if suffix == "/read" {
			method = "POST"
		}
		apiStatus(t, f.request("other", method, path+suffix, nil), 404, "not_found")
	}
	response = f.request("admin", "GET", "me/steward/settings", nil)
	if !strings.Contains(response.Body.String(), `"unread_conversation_count":1`) {
		t.Fatal(response.Body)
	}
	response = f.request("admin", "GET", "me/steward/conversations", nil)
	if !strings.Contains(response.Body.String(), `"unread":true`) || !strings.Contains(response.Body.String(), `"run_status":"idle"`) {
		t.Fatal(response.Body)
	}
	before := detail.Conversation.UpdatedAt
	apiStatus(t, f.request("admin", "POST", path+"/read", nil), 200, "")
	detail, err = f.store.GetConversation(context.Background(), f.users["admin"], id)
	if err != nil || detail.Conversation.Unread || !detail.Conversation.UpdatedAt.Equal(before) {
		t.Fatal(detail, err)
	}
	response = f.request("admin", "GET", "me/steward/settings", nil)
	if !strings.Contains(response.Body.String(), `"unread_conversation_count":0`) {
		t.Fatal(response.Body)
	}
}
func TestPostgreSQLStewardC10StopFromAnotherConnection(t *testing.T) {
	f := newAPIFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	f.configure(t, "admin", server.URL, "openai")
	id := f.conversation(t, "admin")
	path := "me/steward/conversations/" + id
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		first <- f.request("admin", "POST", path+"/messages", map[string]string{"text": "等待停止"})
	}()
	c10Wait(t, started)
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- f.request("admin", "GET", path+"/events", nil) }()
	apiStatus(t, f.request("admin", "POST", path+"/cancel", nil), 200, "")
	for _, ch := range []chan *httptest.ResponseRecorder{first, second} {
		select {
		case w := <-ch:
			assertDone(t, w, runtime.EndCancelled)
		case <-time.After(3 * time.Second):
			t.Fatal("另一个连接未收到结束")
		}
	}
}
func TestPostgreSQLStewardC10RestartResetCanContinue(t *testing.T) {
	f := newAPIFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, apiModelResponse("openai", 3, false)) }))
	defer server.Close()
	f.configure(t, "admin", server.URL, "openai")
	id := f.conversation(t, "admin")
	ctx := context.Background()
	_, err := f.store.AppendMessage(ctx, f.users["admin"], id, runtime.MessageInput{Role: "user", Content: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"重启前的问题"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AcquireConversation(ctx, f.users["admin"], id); err != nil {
		t.Fatal(err)
	}
	count, err := f.store.ResetRunningConversations(ctx)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	path := "me/steward/conversations/" + id
	w := f.request("admin", "GET", path, nil)
	apiStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"status":"idle"`) || !strings.Contains(w.Body.String(), `"role":"user"`) {
		t.Fatal(w.Body)
	}
	apiStatus(t, f.request("admin", "GET", path+"/events", nil), 204, "")
	assertDone(t, f.request("admin", "POST", path+"/continue", nil), runtime.EndCompleted)
}
