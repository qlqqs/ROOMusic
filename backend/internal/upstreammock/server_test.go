package upstreammock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const releaseID = "30000000-0000-4000-8000-000000000001"
const doubleReleaseID = "30000000-0000-4000-8000-000000000002"
const groupID = "20000000-0000-4000-8000-000000000001"
const recordingID = "40000000-0000-4000-8000-000000000001"
const artistID = "10000000-0000-4000-8000-000000000001"

func newMock(t *testing.T, opts upstreammock.Options) http.Handler {
	t.Helper()
	h, err := upstreammock.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func callMock(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if path == "/acoustid/v2/lookup" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func requireStatus(t *testing.T, resp *httptest.ResponseRecorder, want int) {
	t.Helper()
	if resp.Code != want {
		t.Fatalf("HTTP %d，期望 %d，响应 %s", resp.Code, want, resp.Body)
	}
}

func decodeBody[T any](t *testing.T, resp *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("解码响应 %q: %v", resp.Body, err)
	}
	return result
}

func TestMockMusicBrainzRoutes(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for _, tc := range []struct {
		name, path string
		want       []string
	}{
		{"release", "release/" + doubleReleaseID + "?inc=recordings+media+isrcs+discids", []string{doubleReleaseID, `"position":2`, "QMZ9X2400003", "xiAfbTX2ldq_nND3B1cy9xbNvBA-"}},
		{"release_group", "release-group/" + groupID + "?inc=releases", []string{groupID, releaseID, doubleReleaseID}},
		{"artist", "artist/" + artistID + "?inc=aliases", []string{artistID, "Paper Islet"}},
		{"recording", "recording/" + recordingID + "?inc=isrcs+releases", []string{recordingID, "QMZ9X2400001", releaseID}},
		{"isrc", "isrc/QMZ9X2400001?inc=releases", []string{`"recordings":[{`, recordingID, releaseID}},
		{"disc", "discid/BCwmjDmGIsfPoenrs_aBAAmu1xU-", []string{`"id":"BCwmjDmGIsfPoenrs_aBAAmu1xU-"`, releaseID, doubleReleaseID}},
		{"barcode", "release?query=" + url.QueryEscape("barcode:900000000001 OR barcode:0900000000001"), []string{`"count":1`, releaseID}},
		{"catalog", "release?query=" + url.QueryEscape(`catno:"FOLD-001" AND label:"折线合成唱片"`), []string{`"count":1`, releaseID}},
		{"release_search", "release?query=" + url.QueryEscape(`release:"潮线以北" AND artist:"纸屿" AND tracks:3`), []string{`"count":1`, doubleReleaseID}},
		{"recording_search", "recording?query=" + url.QueryEscape(`recording:"潮汐信箱" AND artist:"纸屿" AND dur:[177000 TO 183000]`), []string{`"count":1`, recordingID}},
		{"artist_search", "artist?query=" + url.QueryEscape(`artist:"纸屿"`) + "&limit=1", []string{`"count":2`, artistID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callMock(h, http.MethodGet, "/musicbrainz/ws/2/"+tc.path, "")
			requireStatus(t, resp, 200)
			if !json.Valid(resp.Body.Bytes()) {
				t.Fatalf("无效 JSON: %q", resp.Body)
			}
			for _, want := range tc.want {
				if !strings.Contains(resp.Body.String(), want) {
					t.Errorf("响应缺少 %q: %s", want, resp.Body)
				}
			}
		})
	}
}

func TestMockSourceAndSearchRoutes(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for _, kind := range []string{"release/" + releaseID + "/", "release-group/" + groupID, "release-group/" + groupID + "/"} {
		t.Run(kind, func(t *testing.T) {
			resp := callMock(h, http.MethodGet, "/coverartarchive/"+kind, "")
			requireStatus(t, resp, 200)
			manifest := decodeBody[struct {
				Release string
				Images  []struct {
					ID         int
					Front      bool
					Thumbnails map[string]string
				}
			}](t, resp)
			base := "http://example.com/coverartarchive/release/" + releaseID
			if manifest.Release != base || len(manifest.Images) != 1 || !manifest.Images[0].Front || manifest.Images[0].ID != 1 || manifest.Images[0].Thumbnails["1200"] != base+"/1-1200" {
				t.Fatalf("封面清单错误: %+v", manifest)
			}
		})
	}
	for _, imageName := range []string{"1", "1-250", "1-500", "1-1200"} {
		resp := callMock(h, http.MethodGet, "/coverartarchive/release/"+releaseID+"/"+imageName, "")
		requireStatus(t, resp, 200)
		info, _, err := image.DecodeConfig(resp.Body)
		if err != nil || info.Width < 1 || info.Height < 1 {
			t.Errorf("封面 %s 无法解码: %+v, %v", imageName, info, err)
		}
	}

	lyrics := callMock(h, http.MethodGet, "/lrclib/api/get?"+url.Values{"track_name": {"潮汐信箱"}, "duration": {"180"}}.Encode(), "")
	requireStatus(t, lyrics, 200)
	exact := decodeBody[struct{ SyncedLyrics string }](t, lyrics)
	if exact.SyncedLyrics != "[00:00.00]把潮声折进信封" {
		t.Errorf("同步歌词 = %q", exact.SyncedLyrics)
	}
	lyrics = callMock(h, http.MethodGet, "/lrclib/api/search?"+url.Values{"track_name": {"码头"}}.Encode(), "")
	requireStatus(t, lyrics, 200)
	found := decodeBody[[]struct{ TrackName, PlainLyrics string }](t, lyrics)
	if len(found) != 1 || found[0].TrackName != "细雨码头" || found[0].PlainLyrics != "细雨盖住了脚步" {
		t.Errorf("搜索歌词 = %+v", found)
	}

	for _, tc := range []struct {
		fingerprint string
		count       int
	}{{"mock-tide", 1}, {"mock-ambiguous", 2}, {"mock-empty", 0}} {
		resp := callMock(h, http.MethodPost, "/acoustid/v2/lookup", "fingerprint="+tc.fingerprint+"&duration=180")
		requireStatus(t, resp, 200)
		match := decodeBody[struct {
			Status  string
			Results []struct{ Recordings []struct{ ID string } }
		}](t, resp)
		if match.Status != "ok" || len(match.Results) != tc.count {
			t.Errorf("指纹 %s = %+v", tc.fingerprint, match)
		}
		if tc.fingerprint == "mock-tide" && (len(match.Results) != 1 || len(match.Results[0].Recordings) != 1 || match.Results[0].Recordings[0].ID != recordingID) {
			t.Errorf("指纹未命中指定录音: %+v", match)
		}
	}

	for _, tc := range []struct {
		method, path, body string
		count              int
	}{
		{http.MethodGet, "/searxng/search?q=mock&format=json", "", 2},
		{http.MethodPost, "/tavily/search", `{"query":"mock","max_results":1}`, 1},
	} {
		resp := callMock(h, tc.method, tc.path, tc.body)
		requireStatus(t, resp, 200)
		page := decodeBody[struct {
			Results []struct{ Title, URL, Content string }
		}](t, resp)
		if len(page.Results) != tc.count || page.Results[0].Title != "纸屿的潮汐信箱" ||
			page.Results[0].URL != "https://catalog.example.invalid/paper-islet" || page.Results[0].Content == "" {
			t.Errorf("%s 的搜索结果 = %+v", tc.path, page)
		}
	}
}

func TestMockCoverArtArchiveThumbnailsAreJPEG(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for _, size := range []string{"250", "500", "1200"} {
		t.Run(size, func(t *testing.T) {
			resp := callMock(h, http.MethodGet, "/coverartarchive/release/"+releaseID+"/1-"+size, "")
			requireStatus(t, resp, 200)
			_, format, err := image.DecodeConfig(resp.Body)
			if mime := resp.Header().Get("Content-Type"); mime != "image/jpeg" || format != "jpeg" || err != nil {
				t.Fatalf("缩略图 Content-Type=%q，格式=%q，解码错误=%v，期望 JPEG", mime, format, err)
			}
		})
	}
}

func TestMockLRCLIBExactLookupDurationTolerance(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for _, tc := range []struct {
		duration string
		status   int
	}{{"177", 404}, {"178", 200}, {"182", 200}, {"183", 404}} {
		t.Run(tc.duration, func(t *testing.T) {
			query := url.Values{"track_name": {"潮汐信箱"}, "artist_name": {"纸屿"}, "duration": {tc.duration}}
			resp := callMock(h, http.MethodGet, "/lrclib/api/get?"+query.Encode(), "")
			requireStatus(t, resp, tc.status)
			if tc.status == 200 {
				got := decodeBody[struct {
					TrackName string
					Duration  int
				}](t, resp)
				if got.TrackName != "潮汐信箱" || got.Duration != 180 {
					t.Fatalf("歌词记录 = %+v，期望时长为 180 秒的《潮汐信箱》", got)
				}
			}
		})
	}
}

func TestMockModelRoutes(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for _, path := range []string{"/openai/v1/models", "/openai/chat/v1/models", "/anthropic/v1/models"} {
		resp := callMock(h, http.MethodGet, path, "")
		requireStatus(t, resp, 200)
		page := decodeBody[struct {
			Data    []struct{ ID string }
			HasMore *bool `json:"has_more"`
		}](t, resp)
		if len(page.Data) != 2 || page.Data[0].ID != "mock-model" || page.Data[1].ID != "mock-reviewer" {
			t.Errorf("%s 的模型列表 = %+v", path, page.Data)
		}
		if (strings.HasPrefix(path, "/anthropic/") && (page.HasMore == nil || *page.HasMore)) ||
			(strings.HasPrefix(path, "/openai/") && page.HasMore != nil) {
			t.Errorf("%s 的分页字段 = %v", path, page.HasMore)
		}
	}
	for _, tc := range []struct{ path, content, done string }{
		{"/openai/v1/responses", "event: response.output_text.delta", "event: response.completed"},
		{"/openai/v1/chat/completions", `"content":`, "data: [DONE]"},
		{"/openai/chat/v1/chat/completions", `"content":`, "data: [DONE]"},
		{"/anthropic/v1/messages", "event: content_block_delta", "event: message_stop"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp := callMock(h, http.MethodPost, tc.path, `{"model":"mock-model","stream":true}`)
			requireStatus(t, resp, 200)
			body := resp.Body.String()
			if resp.Header().Get("Content-Type") != "text/event-stream" ||
				!strings.Contains(body, tc.content) || !strings.Contains(body, tc.done) ||
				!strings.Contains(body, "合成回复") {
				t.Fatalf("SSE 响应错误: %s", body)
			}
		})
	}
	requireStatus(t, callMock(h, http.MethodPost, "/openai/chat/v1/responses", `{"model":"mock-model"}`), 404)
}

func TestMockControlAndScenarios(t *testing.T) {
	h := newMock(t, upstreammock.Options{Scenarios: map[string]string{"musicbrainz": "not_found"}})
	state := func() map[string]upstreammock.Scenario {
		resp := callMock(h, http.MethodGet, "/_mock/scenarios", "")
		requireStatus(t, resp, 200)
		return decodeBody[map[string]upstreammock.Scenario](t, resp)
	}
	defaultState := upstreammock.Scenario{Name: "ok", RetryAfter: 2, DelayMS: 3000}
	services := []string{"insights", "plugins", "musicbrainz", "coverartarchive", "lrclib", "acoustid", "wikidata", "commons", "wikipedia", "fanarttv", "theaudiodb", "openai", "anthropic", "searxng", "tavily"}
	initial := state()
	if len(initial) != len(services) {
		t.Fatalf("初始情景 = %+v", initial)
	}
	for _, service := range services {
		want := defaultState
		if service == "musicbrainz" {
			want.Name = "not_found"
		}
		if initial[service] != want {
			t.Errorf("%s 初始情景 = %+v，期望 %+v", service, initial[service], want)
		}
	}
	path := "/musicbrainz/ws/2/release/" + releaseID
	requireStatus(t, callMock(h, http.MethodGet, path, ""), 404)
	requireStatus(t, callMock(h, http.MethodPut, "/_mock/scenarios/no-service", `{"scenario":"ok"}`), 400)
	requireStatus(t, callMock(h, http.MethodPut, "/_mock/scenarios/musicbrainz", `{"scenario":"unknown"}`), 400)
	if !reflect.DeepEqual(initial, state()) {
		t.Fatal("无效设置改变了情景")
	}
	for _, tc := range []struct {
		scenario   string
		status     int
		retryAfter string
	}{
		{"ok", 200, ""}, {"not_found", 404, ""}, {"unavailable", 503, "7"},
		{"rate_limited", 503, "7"}, {"slow", 200, ""}, {"malformed", 200, ""},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			requireStatus(t, callMock(h, http.MethodPut, "/_mock/scenarios/musicbrainz",
				fmt.Sprintf(`{"scenario":%q,"retry_after":7,"delay_ms":25}`, tc.scenario)), 200)
			want := upstreammock.Scenario{Name: tc.scenario, RetryAfter: 7, DelayMS: 25}
			if got := state(); got["musicbrainz"] != want || got["tavily"] != defaultState {
				t.Fatalf("更新后情景 = %+v", got)
			}
			started := time.Now()
			resp := callMock(h, http.MethodGet, path, "")
			requireStatus(t, resp, tc.status)
			if got := resp.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After = %q，期望 %q", got, tc.retryAfter)
			}
			if tc.scenario == "slow" && time.Since(started) < 25*time.Millisecond {
				t.Error("slow 提前响应")
			}
			if tc.scenario == "ok" || tc.scenario == "slow" {
				if got := decodeBody[struct{ ID, Title string }](t, resp); got.ID != releaseID || got.Title != "潮线以北" {
					t.Errorf("发行 = %+v", got)
				}
			}
			if tc.scenario == "malformed" && json.Valid(resp.Body.Bytes()) {
				t.Errorf("malformed 返回了合法 JSON: %s", resp.Body)
			}
		})
	}
	requireStatus(t, callMock(h, http.MethodPut, "/_mock/scenarios/tavily", `{"scenario":"rate_limited"}`), 200)
	tavily := callMock(h, http.MethodPost, "/tavily/search", `{"query":"mock"}`)
	requireStatus(t, tavily, 429)
	if got := tavily.Header().Get("Retry-After"); got != "2" {
		t.Errorf("默认 Retry-After = %q", got)
	}
	requireStatus(t, callMock(h, http.MethodPost, "/_mock/reset", ""), 204)
	reset := state()
	for _, service := range services {
		if reset[service] != defaultState {
			t.Errorf("%s 重置后 = %+v", service, reset[service])
		}
	}
	requests := callMock(h, http.MethodGet, "/_mock/requests", "")
	requireStatus(t, requests, 200)
	if got := decodeBody[[]json.RawMessage](t, requests); got == nil || len(got) != 0 {
		t.Fatalf("重置后请求记录 = %s", requests.Body)
	}
}

func TestMockTimeoutAndDrop(t *testing.T) {
	path := "/musicbrainz/ws/2/release/" + releaseID
	for _, scenario := range []string{"timeout", "drop"} {
		t.Run(scenario, func(t *testing.T) {
			h := newMock(t, upstreammock.Options{Scenarios: map[string]string{"musicbrainz": scenario}})
			server := httptest.NewServer(h)
			defer server.Close()
			if scenario == "timeout" {
				ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := server.Client().Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) || resp != nil {
					t.Fatalf("timeout 返回响应 %v，错误 %v", resp, err)
				}
				return
			}
			resp, err := server.Client().Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 || len(body) == 0 || len(body) >= int(resp.ContentLength) || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("drop 响应状态 %d，正文 %q，声明长度 %d，错误 %v", resp.StatusCode, body, resp.ContentLength, err)
			}
		})
	}
	t.Run("malformed_image", func(t *testing.T) {
		h := newMock(t, upstreammock.Options{Scenarios: map[string]string{"coverartarchive": "malformed"}})
		resp := callMock(h, http.MethodGet, "/coverartarchive/release/"+releaseID+"/1-1200", "")
		requireStatus(t, resp, 200)
		if _, _, err := image.DecodeConfig(resp.Body); err == nil {
			t.Fatal("malformed 返回了有效图片")
		}
	})
}

func TestMockPostTimeoutReleasesHandlerWhenClientLeaves(t *testing.T) {
	h := newMock(t, upstreammock.Options{Scenarios: map[string]string{"acoustid": "timeout"}})
	started, returned := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		h.ServeHTTP(w, r)
		close(returned)
	}))
	serverCtx, stopServer := context.WithCancel(t.Context())
	server.Config.BaseContext = func(net.Listener) context.Context { return serverCtx }
	server.Start()
	defer func() {
		stopServer()
		server.Close()
	}()

	client := server.Client()
	client.Timeout = 250 * time.Millisecond
	resp, err := client.Post(server.URL+"/acoustid/v2/lookup", "application/x-www-form-urlencoded", strings.NewReader("fingerprint=mock-tide"))
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) || resp != nil {
		t.Fatalf("timeout 返回响应 %v，错误 %v", resp, err)
	}
	select {
	case <-started:
	default:
		t.Fatal("客户端超时前未进入服务端")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("客户端超时断开后，带请求体的 POST handler 仍未退出")
	}
}

type recordedRequest struct {
	Time                  time.Time
	Service, Method, Path string
	Query                 url.Values
	Status                int
}

func TestMockRequestLogRedactsSecrets(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	query := url.Values{"client": {"app-secret", "other-secret"}, "user": {"user-secret"}, "key": {"key-secret"},
		"api_key": {"api-secret"}, "TOKEN": {"token-secret"}, "duration": {"180"}}
	req := httptest.NewRequest(http.MethodPost, "/acoustid/v2/lookup?"+query.Encode(), strings.NewReader("fingerprint=mock-tide&user=body-secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer header-secret")
	req.Header.Set("X-API-Key", "model-secret")
	started := time.Now()
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	requireStatus(t, out, 200)
	requireStatus(t, callMock(h, http.MethodGet, "/searxng/search?q=mock", ""), 200)
	resp := callMock(h, http.MethodGet, "/_mock/requests?service=acoustid&limit=1", "")
	requireStatus(t, resp, 200)
	got := decodeBody[[]recordedRequest](t, resp)
	if len(got) != 1 || got[0].Method != http.MethodPost || got[0].Status != 200 ||
		got[0].Service != "acoustid" || got[0].Path != "/acoustid/v2/lookup" ||
		got[0].Time.Before(started) || got[0].Time.After(time.Now()) {
		t.Fatalf("请求记录 = %+v", got)
	}
	if bytes.Contains(resp.Body.Bytes(), []byte("secret")) {
		t.Fatal("请求记录泄露了合成密钥")
	}
	want := url.Values{"client": {"***"}, "user": {"***"}, "key": {"***"},
		"api_key": {"***"}, "TOKEN": {"***"}, "duration": {"180"}}
	if !reflect.DeepEqual(got[0].Query, want) {
		t.Errorf("脱敏查询 = %v，期望 %v", got[0].Query, want)
	}
}

func TestMockRequestLogKeepsLatest500(t *testing.T) {
	h := newMock(t, upstreammock.Options{})
	for i := 0; i < 502; i++ {
		requireStatus(t, callMock(h, http.MethodGet, fmt.Sprintf("/searxng/search?q=mock&n=%d", i), ""), 200)
	}
	resp := callMock(h, http.MethodGet, "/_mock/requests?limit=600", "")
	requireStatus(t, resp, 200)
	got := decodeBody[[]recordedRequest](t, resp)
	if len(got) != 500 || got[0].Query.Get("n") != "501" || got[499].Query.Get("n") != "2" {
		t.Fatalf("请求记录保留或排序错误，条数 %d", len(got))
	}
	resp = callMock(h, http.MethodGet, "/_mock/requests?limit=2", "")
	requireStatus(t, resp, 200)
	if got := decodeBody[[]recordedRequest](t, resp); len(got) != 2 || got[1].Query.Get("n") != "500" {
		t.Fatalf("limit=2 的结果 = %+v", got)
	}
}
