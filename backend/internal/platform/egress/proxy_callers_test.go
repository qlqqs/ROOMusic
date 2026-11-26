package egress_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	stewardapi "github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
)

func TestOutboundProxyBusinessCallers(t *testing.T) {
	t.Cleanup(func() { egress.SetProxy(nil) })
	const recording = "10000000-0000-4000-8000-000000000001"
	const sourceIP, modelIP, pageIP = "8.8.8.8", "1.1.1.1", "9.9.9.9"
	for _, scheme := range []string{"http", "socks5"} {
		for _, auth := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/auth=%t", scheme, auth), func(t *testing.T) {
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Proxy-Authorization") != "" {
						t.Error("业务目标收到代理鉴权头")
					}
					switch {
					case r.Host == sourceIP && r.Method == http.MethodGet && r.URL.Path == "/ws/2/recording/"+recording:
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"id":%q,"title":"合成录音"}`, recording)
					case r.Host == modelIP && r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"合成回答\"}}]}\n\n")
					case r.Host == pageIP && r.Method == http.MethodGet && r.URL.Path == "/fixture":
						w.Header().Set("Content-Type", "text/plain; charset=utf-8")
						io.WriteString(w, "合成网页")
					default:
						t.Errorf("收到意外业务请求 %s %s %s", r.Method, r.Host, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(origin.Close)
				proxy := egress.NewProxyFixture(t, scheme, auth, origin, nil)
				egress.SetProxy(&proxy.Config)
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				music := musicbrainz.New(musicbrainz.Options{
					BaseURL: "http://" + sourceIP + "/ws/2",
					Limiter: sources.NewLimiter(0, nil, nil),
				})
				entity, err := music.Lookup(ctx, "recording", recording, nil)
				if err != nil || entity.ID != recording || entity.Title != "合成录音" {
					t.Fatalf("来源返回 ID %q、标题 %q、错误 %v", entity.ID, entity.Title, err)
				}

				client, err := stewardapi.OutboundClient(ctx, identity.User{Role: "admin"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer client.CloseIdleConnections()
				model, err := provider.NewOpenAI(client, "http://"+modelIP+"/v1", "")
				if err != nil {
					t.Fatal(err)
				}
				stream, err := model.Stream(ctx, provider.Request{Model: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				event, err := stream.Next()
				stream.Close()
				if err != nil || event.Text != "合成回答" {
					t.Fatalf("模型返回文本 %q、错误 %v", event.Text, err)
				}

				page, err := web.New(nil, nil, nil, nil).FetchPage(ctx, "http://"+pageIP+"/fixture")
				if err != nil || page.Text != "合成网页" {
					t.Fatalf("网页返回文本 %q、错误 %v", page.Text, err)
				}
				want := []string{
					"GET http://" + sourceIP + "/ws/2/recording/" + recording + "?fmt=json",
					"POST http://" + modelIP + "/v1/chat/completions",
					"GET http://" + pageIP + "/fixture",
				}
				if scheme == "socks5" {
					want = []string{sourceIP + ":80", modelIP + ":80", pageIP + ":80"}
				}
				if got := proxy.Targets(); !reflect.DeepEqual(got, want) {
					t.Fatalf("代理收到 %v，期望 %v", got, want)
				}
			})
		}
	}
}
