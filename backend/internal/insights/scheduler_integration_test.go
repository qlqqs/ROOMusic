package insights

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/insights/schema"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

type insightsCounts struct{}

func (insightsCounts) LibraryCounts(context.Context) (int, int, error)     { return 1000, 999, nil }
func (insightsCounts) HasConfiguredModel(context.Context) (bool, error)    { return true, nil }
func (insightsCounts) HasUnrevokedToken(context.Context) (bool, error)     { return true, nil }
func (insightsCounts) InsightSettings(context.Context) (bool, bool, error) { return true, false, nil }
func (insightsCounts) EnabledPluginCount(context.Context) (int, error)     { return 5, nil }
func (insightsCounts) DeviceUsage(context.Context, time.Time) (int, int, bool, error) {
	return 6, 1, true, nil
}

type insightsAccountsFunc func(context.Context) (int, int, error)

func (f insightsAccountsFunc) EnabledAccountCounts(ctx context.Context) (int, int, error) {
	return f(ctx)
}

func newInsightsScheduler(t *testing.T, handler http.HandlerFunc) (*sql.DB, Inputs, Options) {
	t.Helper()
	dsn := postgrestest.NewSchema(t, "insights_scheduler_")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock := httptest.NewServer(handler)
	t.Cleanup(mock.Close)
	counts := insightsCounts{}
	inputs := Inputs{
		Accounts: identity.NewGuard(db, false, ""), Library: counts, Models: counts, Tokens: counts,
		Curation: counts, Plugins: counts, Zones: counts,
		LANEnabled:    func(context.Context) (bool, error) { return true, nil },
		ScheduledScan: func(context.Context) (bool, error) { return false, nil },
	}
	return db, inputs, Options{Endpoint: "https://insights.example.invalid/api/v1/heartbeat", Version: "1.2.3", MockURL: mock.URL}
}

func execInsightsSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}

func makeInsightsDue(t *testing.T, db *sql.DB) {
	t.Helper()
	execInsightsSQL(t, db, `UPDATE instance_insights SET created_at=now()-interval '24 hours 1 minute' WHERE id`)
	execInsightsSQL(t, db, `INSERT INTO users(id,username,password_hash,role) VALUES(gen_random_uuid(),'insights-admin','synthetic','admin')`)
}

func readInsightsState(t *testing.T, s *Service) Status {
	t.Helper()
	status, err := s.state(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestPostgreSQLInsightsSchedulerEligibilityAndRestart(t *testing.T) {
	var requests atomic.Int32
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	makeInsightsDue(t, db)
	s := New(db, inputs, opts)
	assertHeld := func(stage string) {
		t.Helper()
		s.cycle(t.Context(), t.Context())
		status := readInsightsState(t, s)
		if status.LastAttemptAt != nil || requests.Load() != 0 {
			t.Fatalf("%s仍发送心跳，发送次数 %d，尝试时间 %v", stage, requests.Load(), status.LastAttemptAt)
		}
	}

	execInsightsSQL(t, db, `UPDATE instance_insights SET created_at=now()-interval '23 hours 59 minutes' WHERE id`)
	assertHeld("创建未满 24 小时")
	execInsightsSQL(t, db, `UPDATE instance_insights SET created_at=now()-interval '24 hours 1 minute' WHERE id`)
	execInsightsSQL(t, db, `UPDATE users SET disabled_at=now() WHERE username='insights-admin';
		INSERT INTO users(id,username,password_hash,role) VALUES(gen_random_uuid(),'insights-member','synthetic','user')`)
	assertHeld("只有启用的成员和停用的管理员")
	execInsightsSQL(t, db, `UPDATE users SET disabled_at=NULL WHERE username='insights-admin'`)
	if err := s.SetEnabled(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	assertHeld("关闭统计")
	if err := s.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	delay := s.cycle(t.Context(), t.Context())
	first := readInsightsState(t, s)
	if requests.Load() != 1 || first.LastAttemptAt == nil || first.LastSuccessAt == nil || first.LastError != nil {
		t.Fatalf("符合条件后应成功发送一次，发送次数 %d，状态 %+v", requests.Load(), first)
	}
	due := nextAttempt(first)
	if interval := due.Sub(*first.LastAttemptAt); interval < 24*time.Hour || interval >= 25*time.Hour {
		t.Errorf("两次尝试间隔 %v，期望 24 至 25 小时", interval)
	}
	if delta := time.Until(due) - delay; delta > 0 || delta < -5*time.Second {
		t.Errorf("调度等待 %v 与到期时刻 %v 不一致", delay, due)
	}
	reopened := New(db, inputs, opts)
	if got := nextAttempt(readInsightsState(t, reopened)); !got.Equal(due) {
		t.Errorf("重启后的到期时刻 %v，期望 %v", got, due)
	}
	reopened.cycle(t.Context(), t.Context())
	if got := readInsightsState(t, reopened); got.LastAttemptAt == nil || !got.LastAttemptAt.Equal(*first.LastAttemptAt) || requests.Load() != 1 {
		t.Errorf("重启后未到期却重新发送，发送次数 %d，尝试时间 %v", requests.Load(), got.LastAttemptAt)
	}
}

func TestPostgreSQLInsightsSchedulerDoesNotStart(t *testing.T) {
	var requests atomic.Int32
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	makeInsightsDue(t, db)
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"forced_off_despite_explicit_url", Options{Endpoint: opts.Endpoint, MockURL: opts.MockURL, Version: opts.Version, ForcedOff: true, ExplicitURL: true}},
		{"development_without_explicit_url", Options{Endpoint: opts.Endpoint, MockURL: opts.MockURL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(db, inputs, tc.opts)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			s.Run(ctx)
			if ctx.Err() != nil {
				t.Error("后台调度运行至上下文到期")
			}
			if got := readInsightsState(t, s); got.LastAttemptAt != nil || requests.Load() != 0 {
				t.Errorf("后台调度不应发送，发送次数 %d，尝试时间 %v", requests.Load(), got.LastAttemptAt)
			}
		})
	}
}

func TestPostgreSQLInsightsSchedulerRecordsOnlyFailureCode(t *testing.T) {
	var result atomic.Int32
	var requests atomic.Int32
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		code := int(result.Load())
		if code == 0 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if code != http.StatusNoContent {
			http.Error(w, "synthetic-upstream-error-body", code)
			return
		}
		w.WriteHeader(code)
	})
	makeInsightsDue(t, db)
	s := New(db, inputs, opts)
	result.Store(http.StatusNoContent)
	s.cycle(t.Context(), t.Context())
	success := readInsightsState(t, s).LastSuccessAt
	if success == nil {
		t.Fatal("第一次发送应成功")
	}
	for i, tc := range []struct {
		name   string
		status int32
		code   string
	}{
		{"upstream_error", http.StatusServiceUnavailable, "http_503"},
		{"unexpected_success_status", http.StatusOK, "invalid_response"},
		{"network_failure", 0, "network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result.Store(tc.status)
			execInsightsSQL(t, db, `UPDATE instance_insights SET last_attempt_at=now()-interval '25 hours' WHERE id`)
			s.cycle(t.Context(), t.Context())
			got := readInsightsState(t, s)
			if got.LastAttemptAt == nil || got.LastError == nil || *got.LastError != tc.code {
				t.Fatalf("失败记录 = %+v，期望错误码 %q", got, tc.code)
			}
			if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(*success) {
				t.Errorf("失败改写了上次成功时间：%v，期望 %v", got.LastSuccessAt, success)
			}
			s.cycle(t.Context(), t.Context())
			if n := requests.Load(); n != int32(i+2) {
				t.Errorf("失败后立刻重试，累计发送 %d 次，期望 %d 次", n, i+2)
			}
		})
	}
}

func TestPostgreSQLInsightsSchedulerConcurrentAttempts(t *testing.T) {
	var requests atomic.Int32
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	makeInsightsDue(t, db)
	execInsightsSQL(t, db, `UPDATE instance_insights SET last_attempt_at=now()-interval '25 hours 1 minute' WHERE id`)
	before := readInsightsState(t, New(db, inputs, opts))
	reached, release := make(chan struct{}, 2), make(chan struct{})
	inputs.Accounts = insightsAccountsFunc(func(ctx context.Context) (int, int, error) {
		reached <- struct{}{}
		select {
		case <-release:
			return 1, 1, nil
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	for range 2 {
		s := New(db, inputs, opts)
		go func() {
			s.cycle(ctx, ctx)
			done <- struct{}{}
		}()
	}
	// 两个调度实例都读取了同一个 last_attempt_at，再同时竞争写入。
	for range 2 {
		select {
		case <-reached:
		case <-ctx.Done():
			t.Fatal("两次竞争未到达同步点")
		}
	}
	close(release)
	for range 2 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("并发调度未完成")
		}
	}
	got := readInsightsState(t, New(db, inputs, opts))
	if requests.Load() != 1 || got.LastAttemptAt == nil || !got.LastAttemptAt.After(*before.LastAttemptAt) || got.LastSuccessAt == nil {
		t.Fatalf("并发发送次数 %d，状态 %+v，期望只发送一次", requests.Load(), got)
	}
}

func TestPostgreSQLInsightsClientPayloadDecodes(t *testing.T) {
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	makeInsightsDue(t, db)
	s := New(db, inputs, opts)
	status, err := s.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := schema.Decode(bytes.NewReader(status.Payload))
	if err != nil {
		t.Fatalf("统计端无法解码客户端的载荷：%v", err)
	}
	if got.InstanceID != status.InstanceID || got.Version != "1.2.3" || got.Members != "1" ||
		got.Library != (schema.Library{Tracks: "1k-10k", Releases: "1-999"}) ||
		got.Devices30d != (schema.Devices{Browser: "6+", UPnP: "1"}) ||
		got.Features != (schema.Features{AutoCuration: true, StewardModel: true, MCPToken: true, LANDevices: true, DSP: true, Plugins: "2-5"}) {
		t.Errorf("客户端载荷未按预期聚合：%+v", got)
	}
}

func TestPostgreSQLInsightsSchedulerCancelsInFlightOnDisable(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	db, inputs, opts := newInsightsScheduler(t, func(w http.ResponseWriter, r *http.Request) {
		// 先读完请求体，确保服务端能感知对端断开连接。
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	makeInsightsDue(t, db)
	s := New(db, inputs, opts)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("mock 未收到正在发送的心跳")
	}
	if err := s.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
		if ctx.Err() != nil {
			t.Fatal("在调度生命周期超时后才取消心跳")
		}
	case <-ctx.Done():
		t.Fatal("关闭统计没有取消正在发送的心跳")
	}
	cancel()
	<-done
	got := readInsightsState(t, s)
	if got.Enabled || got.LastAttemptAt == nil || got.LastSuccessAt != nil {
		t.Fatalf("关闭后状态 = %+v", got)
	}
}
