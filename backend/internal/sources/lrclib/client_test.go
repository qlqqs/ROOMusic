package lrclib

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const lyricRecord = `{"id":42,"name":"兼容别名","trackName":"合成曲目","artistName":"合成艺人","albumName":"合成专辑","duration":123.5,"instrumental":false,"plainLyrics":"第一行\n第二行","syncedLyrics":"[00:01.00]第一行","hasWordSync":false}`

var wantLyric = sources.Lyrics{
	ID: "42", Title: "合成曲目", Artist: "合成艺人", Album: "合成专辑", DurationSeconds: 123.5,
	Plain: "第一行\n第二行", Synced: "[00:01.00]第一行",
}

func lyricFixture(t *testing.T, handler http.HandlerFunc) Options {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.UserAgent() != sources.UserAgent() || r.Header.Get("Accept") != "application/json" {
			t.Errorf("请求 %s %s，头部 %v", r.Method, r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return Options{
		BaseURL: server.URL, Limiter: sources.NewLimiter(0, nil, nil),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
	}
}

func TestLyricsForParametersAndFields(t *testing.T) {
	for _, tc := range []struct {
		name, album, wantAlbum, wantDuration string
		duration                             int
	}{
		{name: "omitted", album: " \t "},
		{name: "negative", duration: -1},
		{name: "above_max", duration: 3601},
		{name: "minimum", duration: 1, wantDuration: "1"},
		{name: "with_album", album: "  合成 + 专辑 ", wantAlbum: "合成 + 专辑", duration: 123, wantDuration: "123"},
		{name: "maximum", duration: 3600, wantDuration: "3600"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := url.Values{"track_name": {"合成 & 曲目"}, "artist_name": {"合成/艺人"}}
			if tc.wantAlbum != "" {
				want.Set("album_name", tc.wantAlbum)
			}
			if tc.wantDuration != "" {
				want.Set("duration", tc.wantDuration)
			}
			var hits atomic.Int32
			opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.URL.Path != "/api/get" || !reflect.DeepEqual(r.URL.Query(), want) {
					t.Errorf("请求 %s，期望 /api/get?%s", r.URL, want.Encode())
				}
				io.WriteString(w, lyricRecord)
			})
			got, err := New(opts).LyricsFor(t.Context(), sources.LyricsQuery{
				Title: " 合成 & 曲目 ", Artist: "\t合成/艺人 ", Album: tc.album, DurationSeconds: tc.duration,
			})
			if err != nil || got != wantLyric || hits.Load() != 1 {
				t.Fatalf("精确查询得到 %#v，错误 %v，出站 %d 次，期望 %#v", got, err, hits.Load(), wantLyric)
			}
		})
	}
}

func TestLyricsForNullTextAndInstrumental(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       sources.Lyrics
	}{
		{"null_lyrics", `{"id":43,"trackName":"无文本样本","artistName":"合成艺人","albumName":"","duration":123.5,"instrumental":false,"plainLyrics":null,"syncedLyrics":null}`, sources.Lyrics{ID: "43", Title: "无文本样本", Artist: "合成艺人", DurationSeconds: 123.5}},
		{"instrumental", `{"id":44,"trackName":"纯音乐样本","artistName":"合成艺人","albumName":"合成专辑","duration":130,"instrumental":true,"plainLyrics":null,"syncedLyrics":null}`, sources.Lyrics{ID: "44", Title: "纯音乐样本", Artist: "合成艺人", Album: "合成专辑", DurationSeconds: 130, Instrumental: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) })
			got, err := New(opts).LyricsFor(t.Context(), sources.LyricsQuery{Title: tc.want.Title, Artist: tc.want.Artist})
			if err != nil || got != tc.want {
				t.Fatalf("歌词 %#v，错误 %v，期望 %#v", got, err, tc.want)
			}
		})
	}
}

func TestLyricsRejectsBlankRequiredFields(t *testing.T) {
	var hits atomic.Int32
	opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	})
	c := New(opts)
	for _, query := range []sources.LyricsQuery{{Artist: "合成艺人"}, {Title: "合成曲目", Artist: "  "}} {
		if got, err := c.LyricsFor(t.Context(), query); !errors.Is(err, sources.ErrInvalidQuery) || got != (sources.Lyrics{}) {
			t.Errorf("非法精确查询 %+v 得到 %#v，错误 %v", query, got, err)
		}
	}
	if got, err := c.SearchLyrics(t.Context(), sources.LyricsQuery{Title: " \t ", Artist: "合成艺人"}); !errors.Is(err, sources.ErrInvalidQuery) || got != nil {
		t.Errorf("空标题搜索得到 %#v，错误 %v", got, err)
	}
	if hits.Load() != 0 {
		t.Fatalf("非法查询出站 %d 次", hits.Load())
	}
}

func TestLyricsForCachesNotFound(t *testing.T) {
	var hits atomic.Int32
	opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"code":404,"name":"TrackNotFound","message":"Failed to find specified track"}`)
	})
	now := time.Unix(1700000000, 0)
	opts.Now = func() time.Time { return now }
	opts.DataDirectory = t.TempDir()
	c := New(opts)
	query := sources.LyricsQuery{Title: "缺少歌词的合成曲目", Artist: "合成艺人"}
	for _, step := range []struct {
		advance time.Duration
		hits    int32
	}{
		{0, 1},
		{23 * time.Hour, 1},
		{2 * time.Hour, 2},
	} {
		now = now.Add(step.advance)
		got, err := c.LyricsFor(t.Context(), query)
		if !errors.Is(err, sources.ErrNotFound) || got != (sources.Lyrics{}) || hits.Load() != step.hits {
			t.Fatalf("404 得到 %#v，错误 %v，出站 %d 次，期望 %d 次", got, err, hits.Load(), step.hits)
		}
	}
}

func TestSearchLyricsParametersAndResults(t *testing.T) {
	type candidate struct {
		ID       int     `json:"id"`
		Title    string  `json:"trackName"`
		Artist   string  `json:"artistName"`
		Album    string  `json:"albumName"`
		Duration float64 `json:"duration"`
	}
	fixture := make([]candidate, 22)
	want := make([]sources.Lyrics, 20)
	for i := range fixture {
		n := i + 1
		fixture[i] = candidate{n, fmt.Sprintf("曲目 %d", n), fmt.Sprintf("艺人 %d", n), fmt.Sprintf("专辑 %d", n), float64(n) + 0.25}
		if i < len(want) {
			item := fixture[i]
			want[i] = sources.Lyrics{ID: fmt.Sprint(item.ID), Title: item.Title, Artist: item.Artist, Album: item.Album, DurationSeconds: item.Duration}
		}
	}
	array, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, artist, album, body string
		params                    url.Values
		want                      []sources.Lyrics
		err                       error
	}{
		{name: "title_only_truncated", artist: "  ", body: string(array), params: url.Values{"track_name": {"合成曲目"}}, want: want},
		{name: "with_artist_and_album_empty_result", artist: " 合成艺人 ", album: " 合成专辑 ", body: "[]", params: url.Values{"track_name": {"合成曲目"}, "artist_name": {"合成艺人"}, "album_name": {"合成专辑"}}, want: []sources.Lyrics{}},
		{name: "missing_id", body: "[" + lyricRecord + `,{"trackName":"无编号"}]`, params: url.Values{"track_name": {"合成曲目"}}, err: sources.ErrUnavailable},
		{name: "missing_id_after_limit", body: string(array[:len(array)-1]) + `,{"trackName":"无编号"}]`, params: url.Values{"track_name": {"合成曲目"}}, err: sources.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.URL.Path != "/api/search" || !reflect.DeepEqual(r.URL.Query(), tc.params) {
					t.Errorf("请求 %s，期望 /api/search?%s", r.URL, tc.params.Encode())
				}
				io.WriteString(w, tc.body)
			})
			got, err := New(opts).SearchLyrics(t.Context(), sources.LyricsQuery{
				Title: " 合成曲目 ", Artist: tc.artist, Album: tc.album, DurationSeconds: 123,
			})
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.want) || hits.Load() != 1 {
				t.Fatalf("搜索得到 %#v，错误 %v，出站 %d 次，期望 %#v 和 %v", got, err, hits.Load(), tc.want, tc.err)
			}
		})
	}
}

func TestLyricsForRetriesBackground429And503(t *testing.T) {
	var hits atomic.Int32
	opts := lyricFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			io.WriteString(w, lyricRecord)
		}
	})
	now := time.Unix(1700000000, 0)
	opts.Now = func() time.Time { return now }
	var waits []time.Duration
	opts.After = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		now = now.Add(d)
		tick := make(chan time.Time, 1)
		tick <- now
		return tick
	}
	got, err := New(opts).LyricsFor(sources.WithBackground(t.Context()), sources.LyricsQuery{Title: "合成曲目", Artist: "合成艺人"})
	if err != nil || got != wantLyric || hits.Load() != 3 || !slices.Equal(waits, []time.Duration{5 * time.Second, 7 * time.Second}) {
		t.Fatalf("重试得到 %#v，错误 %v，出站 %d 次，等待 %v", got, err, hits.Load(), waits)
	}
}
