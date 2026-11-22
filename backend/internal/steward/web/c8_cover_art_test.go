package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const coverToolMBID = "abcdef01-0000-4000-8000-000000000001"

func coverToolFixture(t *testing.T, limit *sources.Limiter, handler http.HandlerFunc) *Service {
	t.Helper()
	client := New(nil, nil, nil, nil)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "musicbrainz.org":
			if r.URL.RequestURI() != "/ws/2/release/"+coverToolMBID+"?fmt=json" {
				t.Errorf("MusicBrainz 请求 %s", r.URL)
			}
			fmt.Fprintf(w, `{"id":%q,"title":"合成发行"}`, coverToolMBID)
		case "coverartarchive.org":
			handler(w, r)
		default:
			t.Errorf("意外主机 %s", r.Host)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	factory := func(o egress.Options) (*http.Client, error) {
		local := c8LocalClient(t, server, o)
		transport := local.Transport
		local.Transport = c8Transport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() == "coverartarchive.org" {
				want := http.Header{"Accept": {"application/json"}, "User-Agent": {sources.UserAgent()}}
				if r.Method != http.MethodGet || r.URL.String() != "https://coverartarchive.org/release/"+coverToolMBID+"/" || !reflect.DeepEqual(r.Header, want) {
					t.Errorf("封面请求 %s %s，头部 %v，期望 %v", r.Method, r.URL, r.Header, want)
				}
			}
			return transport.RoundTrip(r)
		})
		return local, nil
	}
	client.WithMusic(musicbrainz.New(musicbrainz.Options{NewClient: factory, Limiter: sources.NewLimiter(0, nil, nil)}))
	client.WithCovers(coverartarchive.New(coverartarchive.Options{NewClient: factory, ManifestLimiter: limit}))
	return client
}

func invokeCoverTool(t *testing.T, client *Service) json.RawMessage {
	t.Helper()
	definitions := client.StewardTools()
	i := slices.IndexFunc(definitions, func(d tool.Definition) bool { return d.Name == "get_music_metadata" })
	if i < 0 {
		t.Fatal("缺少 get_music_metadata")
	}
	raw := json.RawMessage(`{"entity":"release","mbid":"` + strings.ToUpper(coverToolMBID) + `","include":["cover-art"]}`)
	caller := tool.Caller{User: identity.User{ID: "cover-user-" + t.Name(), Role: "admin"}}
	out, err := definitions[i].Handler(t.Context(), caller, raw)
	if err != nil {
		t.Fatal(err)
	}
	var details MusicDetails
	if err := json.Unmarshal(out, &details); err != nil || details.Metadata.MBID != coverToolMBID || details.Metadata.Title != "合成发行" {
		t.Fatalf("音乐元数据 %s，解析错误 %v", out, err)
	}
	return out
}

func TestC8GetMusicCoverArtProjectionAndLimiter(t *testing.T) {
	var hits atomic.Int32
	permits := 0
	now := time.Unix(1700000000, 0)
	limiter := sources.NewLimiter(0, func() time.Time { return now }, func(d time.Duration) <-chan time.Time {
		if d != 10*time.Second {
			t.Errorf("对话限速计时 %v，期望 10 秒", d)
		}
		permits++
		return nil
	})
	const urlRoot = "http://coverartarchive.org/release/" + coverToolMBID + "/"
	client := coverToolFixture(t, limiter, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"images":[
			{"id":42,"image":"`+urlRoot+`42.png","front":true,"back":false,"thumbnails":{"500":"`+urlRoot+`42-500.jpg"},"comment":"内部清单字段"},
			{"id":"43","image":"`+urlRoot+`43.png","front":false,"back":true,"thumbnails":null}
		]}`)
	})
	out := invokeCoverTool(t, client)
	var result struct{ Covers []map[string]any }
	want := []map[string]any{
		{"url": urlRoot + "42.png", "front": true, "back": false, "thumbnails": map[string]any{"500": urlRoot + "42-500.jpg"}},
		{"url": urlRoot + "43.png", "front": false, "back": true, "thumbnails": nil},
	}
	if err := json.Unmarshal(out, &result); err != nil || !reflect.DeepEqual(result.Covers, want) || hits.Load() != 1 || permits != 1 {
		t.Fatalf("工具响应 %s，解析错误 %v，清单出站 %d 次，许可 %d，期望封面 %#v", out, err, hits.Load(), permits, want)
	}
}

func TestC8CoverArt404EmptyListAndOmittedToolField(t *testing.T) {
	var hits atomic.Int32
	client := coverToolFixture(t, sources.NewLimiter(0, nil, nil), func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	})
	covers, err := client.coverArt(t.Context(), coverToolMBID)
	if err != nil || covers == nil || len(covers) != 0 || hits.Load() != 1 {
		t.Fatalf("清单 404 时服务返回 %#v，错误 %v，出站 %d 次，期望一次出站并返回非 nil 空切片", covers, err, hits.Load())
	}

	// 工具 JSON 省略空的 covers 字段是 B2 之前的既有行为。
	out := invokeCoverTool(t, client)
	var result map[string]any
	want := map[string]any{"metadata": map[string]any{"mbid": coverToolMBID, "title": "合成发行"}}
	if err := json.Unmarshal(out, &result); err != nil || !reflect.DeepEqual(result, want) || hits.Load() != 2 {
		t.Fatalf("清单 404 时工具响应 %s，解析错误 %v，清单累计出站 %d 次，期望 %#v 且累计出站两次", out, err, hits.Load(), want)
	}
}
