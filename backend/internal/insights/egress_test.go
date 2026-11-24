package insights

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

func TestInsightsOutboundPublicPolicy(t *testing.T) {
	var requests atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(local.Close)
	s := New(nil, Inputs{}, Options{Endpoint: local.URL, ExplicitURL: true})
	for _, endpoint := range []string{local.URL, "http://10.0.0.2/heartbeat", "http://[::ffff:127.0.0.1]/heartbeat", "http://[fd00::1]/heartbeat"} {
		err := egress.CheckEndpoint(t.Context(), s.client, endpoint)
		var rejected *egress.Error
		if !errors.As(err, &rejected) || rejected.Code != egress.CodeAddressNotAllowed {
			t.Errorf("%s 的地址检查错误 = %v", endpoint, err)
		}
	}
	if err := egress.CheckEndpoint(t.Context(), s.client, "https://8.8.8.8/heartbeat"); err != nil {
		t.Fatalf("公网地址检查失败：%v", err)
	}
	if got := s.send(t.Context(), []byte("{}")); got != "network" || requests.Load() != 0 {
		t.Fatalf("直连回环结果 = %q，回环服务收到 %d 次请求", got, requests.Load())
	}
}

func TestInsightsOutboundDoesNotFollowRedirect(t *testing.T) {
	type receivedRequest struct{ method, path, contentType, agent string }
	requests := make(chan receivedRequest, 2)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- receivedRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.UserAgent()}
		if r.URL.Path == "/insights/api/v1/heartbeat" {
			http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(mock.Close)
	s := New(nil, Inputs{}, Options{Endpoint: "https://insights.example.invalid/api/v1/heartbeat", Version: "1.2.3", MockURL: mock.URL})
	if got := s.send(t.Context(), []byte("{}")); got != "http_307" {
		t.Fatalf("发送结果 = %q，期望 http_307", got)
	}
	want := receivedRequest{http.MethodPost, "/insights/api/v1/heartbeat", "application/json", "ROOMusic/1.2.3"}
	select {
	case got := <-requests:
		if got != want {
			t.Errorf("收到 %+v，期望 %+v", got, want)
		}
	default:
		t.Fatal("mock 没有收到请求")
	}
	select {
	case got := <-requests:
		t.Errorf("客户端跟随了重定向，发出 %+v", got)
	default:
	}
}

type insightsWaitingTransport struct{}

func (insightsWaitingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestInsightsOutboundTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New(nil, Inputs{}, Options{Endpoint: "https://insights.example.invalid/api/v1/heartbeat"})
		s.client.Transport = insightsWaitingTransport{}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		start := time.Now()
		if got := s.send(ctx, []byte("{}")); got != "network" {
			t.Fatalf("超时结果 = %q，期望 network", got)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Fatalf("发送超时 = %v，期望 10 秒", elapsed)
		}
	})
}
