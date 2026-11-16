package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type c10CredentialContextKey struct{}

func TestC10F7CredentialRequestSnapshot(t *testing.T) {
	request := httptest.NewRequest("POST", "http://roomusic.test/messages", strings.NewReader("原始私有正文"))
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "synthetic-original"})
	request.Header.Set("Authorization", "synthetic-original-header")
	user := identity.User{ID: "synthetic-user", Role: "admin"}
	calls := 0
	h := &Handlers{checkCredentials: func(r *http.Request, got identity.User) (bool, error) {
		calls++
		cookie, err := r.Cookie(identity.SessionCookieName)
		if err != nil || cookie.Value != "synthetic-original" || got != user || r.Context().Value(c10CredentialContextKey{}) != calls || r.Body != nil || len(r.Header) != 1 || r == request {
			t.Error("复查持有原请求或丢失运行 context")
		}
		// 凭据失效分支无需存储；这里核对闭包输入，不替代真实数据库复查。
		return false, nil
	}}
	check := h.runCredentialCheck(request, user, runtime.UserSettings{})
	request.Header.Set("Cookie", identity.SessionCookieName+"=synthetic-mutated")
	request.Header.Set("Authorization", "synthetic-mutated-header")
	request.Body = io.NopCloser(strings.NewReader("修改后的私有正文"))
	for i := 1; i <= 2; i++ {
		if err := check(context.WithValue(context.Background(), c10CredentialContextKey{}, i)); !errors.Is(err, runtime.ErrRunInvalidated) {
			t.Fatal(err)
		}
	}
}

type c10StreamExecutor struct{}

func (c10StreamExecutor) Run(ctx context.Context, p runtime.RunParams, sink runtime.EventSink) error {
	p.Started()
	<-ctx.Done()
	sink(runtime.Event{Type: runtime.EventDone, End: &runtime.RunEnd{Reason: runtime.EndCancelled}})
	return nil
}
func TestC10F10StreamAtTail(t *testing.T) {
	s := runtime.NewSupervisor(context.Background(), c10StreamExecutor{})
	defer s.Shutdown(context.Background())
	run, err := s.Start(context.Background(), runtime.RunParams{ConversationID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "http://roomusic.test/events", nil).WithContext(ctx)
	h := &Handlers{}
	w := httptest.NewRecorder()
	h.streamSubscription(w, request, run.Subscribe(0))
	if w.Code != 200 {
		t.Fatal("运行中空日志未保持流", w.Code)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	sub := run.Subscribe(0)
	last := sub.Replay[len(sub.Replay)-1].Seq
	sub.Close()
	for _, after := range []uint64{last, last + 1} {
		w := httptest.NewRecorder()
		h.streamSubscription(w, request, run.Subscribe(after))
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatal(w.Code, w.Body)
		}
	}
}
