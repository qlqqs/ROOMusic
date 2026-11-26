package sources_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

type queueHTTPFixture struct {
	t        *testing.T
	mock     *upstreammock.Server
	now      time.Time
	policy   *sources.QueuePolicy
	requests map[string][]time.Time
	after    func(*http.Response)
}

func newQueueHTTPFixture(t *testing.T) *queueHTTPFixture {
	t.Helper()
	mock, err := upstreammock.New(upstreammock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := &queueHTTPFixture{t: t, mock: mock, now: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), requests: make(map[string][]time.Time)}
	f.policy = sources.NewQueuePolicy(func() time.Time { return f.now })
	return f
}

func (f *queueHTTPFixture) scenario(source, name string, seconds int) {
	f.t.Helper()
	body := fmt.Sprintf(`{"scenario":%q,"retry_after":%d}`, name, seconds)
	out := httptest.NewRecorder()
	f.mock.ServeHTTP(out, httptest.NewRequest(http.MethodPut, "/_mock/scenarios/"+source, strings.NewReader(body)))
	if out.Code != http.StatusOK {
		f.t.Fatalf("设置来源情景 HTTP %d：%s", out.Code, out.Body)
	}
}

func (f *queueHTTPFixture) requester(id string, interval time.Duration) *sources.Requester {
	now := func() time.Time { return f.now }
	after := func(d time.Duration) <-chan time.Time {
		// 界面调用的许可超时定时器独立于本测试主动推进的发送时钟。
		if d == 10*time.Second {
			return nil
		}
		f.now = f.now.Add(d)
		ch := make(chan time.Time, 1)
		ch <- f.now
		return ch
	}
	return sources.NewRequester(sources.Declaration{ID: id}, sources.Options{
		Now: now, After: after, Limiter: sources.NewLimiter(interval, now, after), DataDirectory: f.t.TempDir(),
		NewClient: func(egress.Options) (*http.Client, error) {
			return &http.Client{Transport: mockRoundTripper(func(r *http.Request) (*http.Response, error) {
				f.requests[id] = append(f.requests[id], f.now)
				out := httptest.NewRecorder()
				f.mock.ServeHTTP(out, r)
				resp := out.Result()
				if f.after != nil {
					f.after(resp)
				}
				return resp, nil
			})}, nil
		},
	})
}

func queueGET(ctx context.Context, r *sources.Requester, path string) error {
	return r.GetJSON(ctx, "http://mock.invalid"+path, func(body []byte) (bool, bool) {
		return false, len(body) > 0 && body[0] == '{'
	})
}

func requireQueuePause(t *testing.T, err error, id string, until time.Time) {
	t.Helper()
	var pause *sources.QueuePause
	if !errors.As(err, &pause) || pause.Source != id || pause.Reason != "rate_limited" || !pause.RetryAt.Equal(until) {
		t.Fatalf("来源 %s 应等至 %s，错误 %v，暂停 %+v", id, until, err, pause)
	}
}

func TestQueueHTTPBackoffAndRecovery(t *testing.T) {
	for _, tc := range []struct{ id, path, scenario string }{
		{"musicbrainz", "/musicbrainz/ws/2/release/" + mockReleaseID, "unavailable"},
		{"lrclib", "/lrclib/api/get?track_name=潮汐信箱&artist_name=纸屿&album_name=潮线以北&duration=180", "rate_limited"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			f := newQueueHTTPFixture(t)
			r := f.requester(tc.id, time.Second)
			ctx := f.policy.Context(t.Context(), sources.QueueNormal, nil)
			if err := queueGET(ctx, r, tc.path); err != nil {
				t.Fatal(err)
			}
			path := tc.path
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			if err := queueGET(ctx, r, path+separator+"fixture=baseline"); err != nil {
				t.Fatal(err)
			}
			success := f.requests[tc.id]
			if len(success) != 2 || success[1].Sub(success[0]) != time.Second {
				t.Fatalf("无故障时两次出站间隔应为 1s，实际 %v", success)
			}
			f.scenario(tc.id, tc.scenario, 0)
			path += separator + "fixture=uncached"
			for i, seconds := range []int{5, 10, 20, 40, 80, 160, 180, 180} {
				before := len(f.requests[tc.id])
				err := queueGET(ctx, r, path)
				until := f.now.Add(time.Duration(seconds) * time.Second)
				requireQueuePause(t, err, tc.id, until)
				sent := f.requests[tc.id][before:]
				if len(sent) != 5 {
					t.Fatalf("第 %d 轮出站 %d 次，期望 5 次", i+1, len(sent))
				}
				for j := 1; j < len(sent); j++ {
					if gap := sent[j].Sub(sent[j-1]); gap != time.Second {
						t.Fatalf("第 %d 轮第 %d 次相邻出站间隔 %v，期望 1s", i+1, j+1, gap)
					}
				}
				f.now = until.Add(-time.Nanosecond)
				requireQueuePause(t, queueGET(ctx, r, path), tc.id, until)
				if err := queueGET(ctx, r, tc.path); err != nil {
					t.Fatalf("暂停期间应可读缓存：%v", err)
				}
				if len(f.requests[tc.id]) != before+5 || !f.policy.HasFailure(tc.id) || len(f.policy.RecoveredSources()) != 0 {
					t.Fatal("暂停期间有额外出站，或缓存命中提前清除了暂停")
				}
				f.now = until
			}
			f.scenario(tc.id, "not_found", 0)
			before := len(f.requests[tc.id])
			if err := queueGET(ctx, r, path); !errors.Is(err, sources.ErrNotFound) {
				t.Fatalf("恢复探测应收到正常 404，实际 %v", err)
			}
			if len(f.requests[tc.id]) != before+1 || f.policy.HasFailure(tc.id) ||
				!slices.Equal(f.policy.RecoveredSources(), []string{tc.id}) {
				t.Fatal("真实 404 应清除失败级别并通知恢复")
			}
			f.scenario(tc.id, tc.scenario, 0)
			err := queueGET(ctx, r, path+"&fixture=after-recovery")
			requireQueuePause(t, err, tc.id, f.now.Add(5*time.Second))
		})
	}
}

func TestQueueHTTPRetryAfter(t *testing.T) {
	const id = "musicbrainz"
	const path = "/musicbrainz/ws/2/release/" + mockReleaseID
	for _, tc := range []struct {
		name, header string
		offset       time.Duration
		delays       []time.Duration
		pause        time.Duration
	}{
		{"missing", "", 0, []time.Duration{0, 300 * time.Millisecond, 900 * time.Millisecond, 2100 * time.Millisecond, 4500 * time.Millisecond}, 5 * time.Second},
		{"short", "2", 0, []time.Duration{0, 2 * time.Second, 4 * time.Second, 6 * time.Second, 8 * time.Second}, 5 * time.Second},
		{"ten-second-budget", "4", 0, []time.Duration{0, 4 * time.Second, 8 * time.Second}, 5 * time.Second},
		{"long-seconds", "241", 0, []time.Duration{0}, 241 * time.Second},
		{"long-date", "", 275 * time.Second, []time.Duration{0}, 275 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQueueHTTPFixture(t)
			start := f.now
			f.scenario(id, "unavailable", 0)
			f.after = func(resp *http.Response) {
				resp.Header.Del("Retry-After")
				if tc.header != "" {
					resp.Header.Set("Retry-After", tc.header)
				}
				if tc.offset > 0 {
					resp.Header.Set("Retry-After", start.Add(tc.offset).Format(http.TimeFormat))
				}
			}
			r := f.requester(id, 0)
			ctx := f.policy.Context(t.Context(), sources.QueueNormal, nil)
			err := queueGET(ctx, r, path)
			requireQueuePause(t, err, id, f.now.Add(tc.pause))
			var elapsed []time.Duration
			for _, at := range f.requests[id] {
				elapsed = append(elapsed, at.Sub(start))
			}
			if !slices.Equal(elapsed, tc.delays) {
				t.Fatalf("出站时刻 %v，期望 %v", elapsed, tc.delays)
			}
		})
	}
}

func TestQueueHTTPShortRetrySucceeds(t *testing.T) {
	const id = "musicbrainz"
	const path = "/musicbrainz/ws/2/release/" + mockReleaseID
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%t", background), func(t *testing.T) {
			f := newQueueHTTPFixture(t)
			f.scenario(id, "unavailable", 2)
			f.after = func(resp *http.Response) {
				if resp.StatusCode == http.StatusServiceUnavailable {
					f.scenario(id, "ok", 0)
				}
			}
			r := f.requester(id, time.Second)
			ctx := t.Context()
			if background {
				ctx = f.policy.Context(ctx, sources.QueueNormal, nil)
			}
			if err := queueGET(ctx, r, path); err != nil {
				t.Fatal(err)
			}
			got := f.requests[id]
			if len(got) != 2 || got[1].Sub(got[0]) != 2*time.Second || f.policy.HasFailure(id) {
				t.Fatalf("应按 Retry-After 等 2s 后单次重试成功，出站 %v，来源等待 %t", got, f.policy.HasFailure(id))
			}
		})
	}
}
