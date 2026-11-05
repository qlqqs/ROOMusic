package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type artistBatchDiagnosticKey struct{}
type artistBatchDiagnosticQuery struct {
	SQL             string    `json:"sql"`
	SHA256          string    `json:"sha256"`
	Category        string    `json:"category"`
	PID             uint32    `json:"pid"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	DurationMS      float64   `json:"duration_ms"`
	ArgumentSizes   []int     `json:"argument_sizes"`
	Rows            int64     `json:"rows"`
	Error           string    `json:"error,omitempty"`
	Arguments       []any     `json:"arguments,omitempty"`
	ArgumentsSHA256 string    `json:"arguments_sha256,omitempty"`
}

type artistBatchDiagnosticTrace struct {
	mu                   sync.Mutex
	active               bool
	captureProfileParams bool
	queries              []artistBatchDiagnosticQuery
}

// 驱动计时含结果消费，不冒充服务端执行时间；仅显式计划诊断保存合成夹具主名查询的身份参数。
func (tr *artistBatchDiagnosticTrace) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	tr.mu.Lock()
	active := tr.active
	tr.mu.Unlock()
	if !active {
		return ctx
	}
	q := &artistBatchDiagnosticQuery{SQL: data.SQL, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(data.SQL))), Category: artistBatchDiagnosticCategory(data.SQL), PID: conn.PgConn().PID(), Start: time.Now()}
	for _, arg := range data.Args {
		switch arg.(type) {
		case pgx.QueryExecMode, pgx.QueryResultFormats, pgx.QueryResultFormatsByOID:
			continue
		}
		size := 1
		v := reflect.ValueOf(arg)
		if v.IsValid() && (v.Kind() == reflect.Array || v.Kind() == reflect.Slice) {
			size = v.Len()
		}
		q.ArgumentSizes = append(q.ArgumentSizes, size)
		if tr.captureProfileParams && isArtistPlanDiagnosticSQL(data.SQL) {
			if ids, ok := arg.([]string); ok {
				arg = append([]string{}, ids...)
			}
			q.Arguments = append(q.Arguments, arg)
		}
	}
	if len(q.Arguments) > 0 {
		data, err := json.Marshal(q.Arguments)
		if err == nil {
			q.ArgumentsSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	return context.WithValue(ctx, artistBatchDiagnosticKey{}, q)
}
func (tr *artistBatchDiagnosticTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	q, ok := ctx.Value(artistBatchDiagnosticKey{}).(*artistBatchDiagnosticQuery)
	if !ok {
		return
	}
	q.End = time.Now()
	q.DurationMS = artistDiagnosticMS(q.End.Sub(q.Start))
	q.Rows = data.CommandTag.RowsAffected()
	if data.Err != nil {
		q.Error = data.Err.Error()
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.queries = append(tr.queries, *q)
}
func (tr *artistBatchDiagnosticTrace) start() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.queries = nil
	tr.active = true
}
func (tr *artistBatchDiagnosticTrace) stop() []artistBatchDiagnosticQuery {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.active = false
	return append([]artistBatchDiagnosticQuery(nil), tr.queries...)
}

func artistBatchDiagnosticCategory(sql string) string {
	q := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case q == "COMMIT":
		return "commit"
	case strings.HasPrefix(q, "BEGIN"):
		return "begin"
	case q == "ROLLBACK":
		return "rollback"
	case strings.Contains(q, "PG_ADVISORY_XACT_LOCK"):
		return "catp_wait"
	case sql == artistReleaseTrackCountSQL || sql == artistReleaseNameInputsSQL:
		return "selection"
	}
	write := strings.HasPrefix(q, "INSERT ") || strings.HasPrefix(q, "DELETE ") || strings.HasPrefix(q, "UPDATE ") || (strings.HasPrefix(q, "WITH ") && (strings.Contains(q, "INSERT INTO ") || strings.Contains(q, "DELETE FROM ") || strings.Contains(q, "UPDATE ")))
	if write {
		if strings.Contains(q, "CATALOG_PROJECTION_WORK") || strings.Contains(q, "CATALOG_PROJECTION_PHASES") || strings.Contains(q, "UPDATE CATALOG_PROJECTION_STATE") {
			return "progress_write"
		}
		return "graph_write"
	}
	if strings.Contains(q, "CATALOG_PROJECTION_PHASES") || strings.Contains(q, "CATALOG_PROJECTION_WORK") || strings.Contains(q, "FROM CATALOG_PROJECTION_STATE WHERE CAPABILITY='ARTISTS'") {
		return "selection"
	}
	if strings.HasPrefix(q, "SELECT EXISTS") && (strings.Contains(q, "ARTIST_PROJECTION_RELEASES") || strings.Contains(q, "ARTIST_PROFILES")) {
		return "marker_check"
	}
	return "graph_read"
}
func artistDiagnosticMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

type artistBatchDiagnosticTiming struct {
	CategoriesMS           map[string]float64 `json:"categories_ms"`
	DriverSQLMS            float64            `json:"driver_sql_ms"`
	GoSchedulingResidualMS float64            `json:"go_scheduling_residual_ms"`
	CATPHoldMS             float64            `json:"catp_hold_ms"`
	CATPIncludesCommit     bool               `json:"catp_includes_commit"`
}

func artistBatchDiagnosticTimings(queries []artistBatchDiagnosticQuery, elapsed time.Duration) artistBatchDiagnosticTiming {
	out := artistBatchDiagnosticTiming{CategoriesMS: map[string]float64{}}
	var locked, committed time.Time
	for _, q := range queries {
		out.CategoriesMS[q.Category] += q.DurationMS
		out.DriverSQLMS += q.DurationMS
		if q.Category == "catp_wait" && q.Error == "" {
			locked = q.End
		}
		if q.Category == "commit" && q.Error == "" {
			committed = q.End
		}
	}
	out.GoSchedulingResidualMS = artistDiagnosticMS(elapsed) - out.DriverSQLMS
	if !locked.IsZero() && !committed.IsZero() && committed.After(locked) {
		out.CATPHoldMS = artistDiagnosticMS(committed.Sub(locked))
		out.CATPIncludesCommit = true
	}
	return out
}

func TestArtistBatchDiagnosticClassification(t *testing.T) {
	for _, tc := range []struct{ sql, want string }{
		{"commit", "commit"}, {"begin isolation level read committed", "begin"}, {"SELECT pg_advisory_xact_lock($1,$2)", "catp_wait"},
		{artistReleaseTrackCountSQL, "selection"}, {artistReleaseNameInputsSQL, "selection"},
		{"SELECT phase FROM catalog_projection_phases", "selection"},
		{"WITH raw AS (SELECT 1) INSERT INTO artist_credits SELECT * FROM raw", "graph_write"},
		{"UPDATE catalog_projection_work SET completed=TRUE", "progress_write"},
		{"SELECT * FROM tracks", "graph_read"},
		{"SELECT EXISTS(SELECT 1 FROM artist_projection_releases)", "marker_check"},
	} {
		if got := artistBatchDiagnosticCategory(tc.sql); got != tc.want {
			t.Errorf("%q: %s want %s", tc.sql, got, tc.want)
		}
	}
}
func TestArtistBatchDiagnosticTimingIncludesCommitAndWaitSeparately(t *testing.T) {
	base := time.Unix(0, 0)
	qs := []artistBatchDiagnosticQuery{
		{Category: "catp_wait", Start: base, End: base.Add(5 * time.Millisecond), DurationMS: 5},
		{Category: "graph_write", Start: base.Add(6 * time.Millisecond), End: base.Add(16 * time.Millisecond), DurationMS: 10},
		{Category: "commit", Start: base.Add(20 * time.Millisecond), End: base.Add(30 * time.Millisecond), DurationMS: 10},
	}
	got := artistBatchDiagnosticTimings(qs, 32*time.Millisecond)
	if got.CATPHoldMS != 25 || got.DriverSQLMS != 25 || got.GoSchedulingResidualMS != 7 || !got.CATPIncludesCommit {
		t.Fatalf("计时错误：%+v", got)
	}
	qs[2].Error = "commit failed"
	if artistBatchDiagnosticTimings(qs, 32*time.Millisecond).CATPIncludesCommit {
		t.Fatal("失败提交不应成为成功持锁样本")
	}
}
