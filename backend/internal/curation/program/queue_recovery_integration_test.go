package program

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQLQueueRecoveryRacesFailureCommit(t *testing.T) {
	for _, newFailure := range []bool{false, true} {
		name := "恢复先于等待提交"
		if newFailure {
			name = "恢复后又进入更长暂停"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := newPendingRefreshFixture(t)
			_, err := f.writer.SetSettings(ctx, f.adminID, true)
			requirePendingRefreshOK(t, err)
			release, _ := f.release(t, "合成专辑", "合成艺人")
			now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			entered, resume := make(chan struct{}), make(chan struct{})
			var hold atomic.Bool
			w := NewWorker(f.db, f.writer, curation.NewSources(f.db), func(ctx context.Context) (bool, error) {
				if hold.Swap(false) {
					close(entered)
					select {
					case <-resume:
					case <-ctx.Done():
						return false, ctx.Err()
					}
				}
				return false, nil
			}, nil, Options{Now: func() time.Time { return now }})
			requirePendingRefreshOK(t, w.refreshChanged(ctx))
			item := requireQueueItem(t, w, "release")
			before := testQueueWorkState(t, w, "release", release)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/first":
					w.Header().Set("Retry-After", "245")
					w.WriteHeader(http.StatusServiceUnavailable)
				case "/second":
					w.Header().Set("Retry-After", "300")
					w.WriteHeader(http.StatusServiceUnavailable)
				case "/ui":
					io.WriteString(w, `{"ok":true}`)
				default:
					http.NotFound(w, r)
				}
				calls.Add(1)
			}))
			t.Cleanup(server.Close)
			requester := sources.NewRequester(sources.Declaration{ID: "musicbrainz"}, sources.Options{
				Now: w.opts.Now, Limiter: sources.NewLimiter(0, nil, nil),
				NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
			})
			read := func(ctx context.Context, path string) error {
				return requester.GetJSON(ctx, server.URL+path, func(body []byte) (bool, bool) {
					return false, string(body) == `{"ok":true}`
				})
			}
			bg := w.policy.Context(ctx, sources.QueueNormal, nil)
			var pause *sources.QueuePause
			if err := read(bg, "/first"); !errors.As(err, &pause) || !pause.RetryAt.Equal(now.Add(245*time.Second)) {
				t.Fatalf("首次故障应暂停 245s，实际 %v / %+v", err, pause)
			}
			state := before
			state.IdentityDigest, state.MatchRuleVersion = item.InputDigest, MatchRuleVersion
			failure := &stepFailure{retry: &matchRetry{Step: "tag_id"}, err: pause}
			attempt := context.WithValue(bg, attemptKey{}, &queueAttempt{item: item, worker: w})
			result := make(chan error, 1)
			hold.Store(true)
			go func() { result <- w.saveQueryFailure(attempt, state, before, failure) }()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("保存等待尚未到达闸门就退出：%v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("保存等待未到达闸门")
			}

			requirePendingRefreshOK(t, read(ctx, "/ui"))
			want := now
			if newFailure {
				var newer *sources.QueuePause
				want = now.Add(300 * time.Second)
				if err := read(bg, "/second"); !errors.As(err, &newer) || !newer.RetryAt.Equal(want) {
					t.Fatalf("新的故障应暂停至 %s，实际 %v / %+v", want, err, newer)
				}
			}
			// 让领取循环先处理恢复事件，再让失败结果完成写入。
			requirePendingRefreshOK(t, w.drainQueues(ctx, nil))
			close(resume)
			select {
			case err := <-result:
				var returned *sources.QueuePause
				if newFailure && !errors.As(err, &returned) || !newFailure && !errors.Is(err, errAlbumWaiting) {
					t.Fatalf("提交等待后的控制流：%v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("失败结果尚未提交")
			}

			wait := testQueueWorkState(t, w, "release", release)
			_, ready, _, err := w.queueCounts(ctx, "release")
			requirePendingRefreshOK(t, err)
			wantCalls, wantReady := int32(2), int64(1)
			if newFailure {
				wantCalls, wantReady = 3, 0
			}
			if wait.Status != "try_later" || wait.RetrySource != "musicbrainz" || wait.NextAttemptAt == nil || !wait.NextAttemptAt.Equal(want) ||
				wait.RetryCount != 0 || wait.Attempts != 0 || wait.Generation != before.Generation || ready != wantReady || calls.Load() != wantCalls {
				t.Fatalf("恢复与结果交错后应等待至 %s、就绪 %d 项、出站 %d 次，实际期限 %v、就绪 %d 项、出站 %d 次，状态 %s，来源 %s，失败计次 %d，结论次数 %d，代次 %d",
					want, wantReady, wantCalls, wait.NextAttemptAt, ready, calls.Load(), wait.Status, wait.RetrySource,
					wait.RetryCount, wait.Attempts, wait.Generation)
			}
		})
	}
}
