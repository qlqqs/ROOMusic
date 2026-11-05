package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 只在真实worker的已提交日志处停顿；不打开事务、不取CATP，也不阻塞事务内的日志。
// 选择首个仍有pending的搜索批次，确保艺人profiles尚未被后台预先补全。
type artistOnlineBatchBarrier struct {
	entered chan struct{}
	release chan struct{}
	hit     sync.Once
	resume  sync.Once
}
type artistOnlineBarrierHandler struct {
	base    slog.Handler
	barrier *artistOnlineBatchBarrier
	attrs   []slog.Attr
}

func newArtistOnlineBatchBarrier() (*artistOnlineBatchBarrier, *slog.Logger) {
	barrier := &artistOnlineBatchBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	return barrier, slog.New(&artistOnlineBarrierHandler{base: slog.NewTextHandler(io.Discard, nil), barrier: barrier})
}
func (b *artistOnlineBatchBarrier) open() { b.resume.Do(func() { close(b.release) }) }
func (h *artistOnlineBarrierHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}
func (h *artistOnlineBarrierHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.base = h.base.WithAttrs(attrs)
	copy.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &copy
}
func (h *artistOnlineBarrierHandler) WithGroup(name string) slog.Handler {
	copy := *h
	copy.base = h.base.WithGroup(name)
	return &copy
}
func (h *artistOnlineBarrierHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.base.Handle(ctx, record); err != nil {
		return err
	}
	var event, capability string
	var completed, total int64
	read := func(attr slog.Attr) bool {
		switch attr.Key {
		case "event":
			event = attr.Value.String()
		case "capability":
			capability = attr.Value.String()
		case "completed":
			completed = attr.Value.Int64()
		case "total":
			total = attr.Value.Int64()
		}
		return true
	}
	for _, attr := range h.attrs {
		read(attr)
	}
	record.Attrs(read)
	if event == "catalog_projection_backfill_batch" && capability != "artists" && completed > 0 && completed < total {
		h.barrier.hit.Do(func() {
			close(h.barrier.entered)
			select {
			case <-h.barrier.release:
			case <-ctx.Done():
			}
		})
	}
	return nil
}

// 这里只建初始路由和名字，不写profile、effective credit、搜索或完成代次。
// 局部回归保留原故障的20k身份/24k名字维度；不能替代正式100k轨基准。
func seedArtistOnlineNames(t *testing.T, app *integrationTestApplication) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO artists(id,initial_name)
 SELECT ('b1000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,'Regression Artist '||lpad(i::text,5,'0')
 FROM generate_series(1,20000) i`,
		`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
 SELECT ('b2000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,
 lower(name),sha256(convert_to(lower(name),'UTF8')),
 ('b1000000-0000-4000-8000-'||lpad(artist::text,12,'0'))::uuid
 FROM (SELECT i,i AS artist,'Regression Artist '||lpad(i::text,5,'0') AS name FROM generate_series(1,20000) i
 UNION ALL SELECT 20000+i,i,'Regression Alias '||lpad(i::text,5,'0') FROM generate_series(1,4000) i) names`,
		`INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
 SELECT ('b3000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 ('b2000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,name,sha256(convert_to(name,'UTF8')),'seed'
 FROM (SELECT i,'Regression Artist '||lpad(i::text,5,'0') AS name FROM generate_series(1,20000) i
 UNION ALL SELECT 20000+i,'Regression Alias '||lpad(i::text,5,'0') FROM generate_series(1,4000) i) names`,
	} {
		mustExec(t, app, query)
	}
}

func artistOnlineRequest(t *testing.T, ctx context.Context, app *integrationTestApplication, user integrationTestUser, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Host = "roomusic.test"
	req.Header.Set("Origin", app.origin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "online-backfill-"+uuid.New())
	req.Header.Set("Idempotency-Key", uuid.New())
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, req)
	return response
}

func requireArtistOnlinePending(t *testing.T, app *integrationTestApplication) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var pending, gaps int64
	var state string
	err := app.database.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM catalog_projection_work w JOIN catalog_projection_state s ON s.build_id=w.build_id WHERE NOT w.completed),
 (SELECT count(*) FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id WHERE a.initial_name LIKE 'Regression Artist %' AND p.artist_id IS NULL),
 (SELECT status FROM catalog_projection_state WHERE capability='artists')`).Scan(&pending, &gaps, &state)
	if err != nil {
		t.Fatal(err)
	}
	if pending == 0 || gaps < 19000 || state == "ready" {
		t.Fatalf("在线断言必须发生在真实pending及大量profile缺口期间: pending=%d gaps=%d state=%s", pending, gaps, state)
	}
	t.Logf("后台尚未完成: pending=%d seeded_profile_gaps=%d artists_state=%s", pending, gaps, state)
}

func requireArtistOnlineGraph(t *testing.T, app *integrationTestApplication, releaseID, trackID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var corrected, track, composer bool
	err := app.database.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM artist_effective_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.release_id=$1::uuid AND c.role='album_artist' AND n.spelling='Online Corrected Artist'),
 EXISTS(SELECT 1 FROM artist_effective_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.track_id=$2::uuid AND c.role='track_artist' AND n.spelling='Regression Artist 00001'),
 EXISTS(SELECT 1 FROM artist_effective_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.track_id=$2::uuid AND c.role='composer' AND n.spelling='Regression Artist 00002')`, releaseID, trackID).Scan(&corrected, &track, &composer)
	if err != nil {
		t.Fatal(err)
	}
	if !corrected || !track || !composer {
		t.Fatalf("在线写入未同步更新graph: correction=%v track=%v composer=%v", corrected, track, composer)
	}
}

func TestArtistOnlineBarrierOnlyPausesCommittedPendingBatch(t *testing.T) {
	barrier, logger := newArtistOnlineBatchBarrier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 事务内/启动/发布以及没有剩余工作的记录都不允许挡住主线程。
	for _, item := range []struct {
		event, capability string
		completed, total  int
	}{
		{"catalog_projection_backfill_started", "", 0, 24},
		{"catalog_projection_backfill_batch", "artists", 10, 24},
		{"catalog_projection_backfill_batch", "", 24, 24},
	} {
		logger.InfoContext(ctx, "非barrier事件", "event", item.event, "capability", item.capability, "completed", item.completed, "total", item.total)
	}
	select {
	case <-barrier.entered:
		t.Fatal("错误地暂停非目标日志")
	default:
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		logger.InfoContext(ctx, "已提交批次", "event", "catalog_projection_backfill_batch", "completed", 10, "total", 24)
	}()
	select {
	case <-barrier.entered:
	case <-time.After(time.Second):
		t.Fatal("未捕获已提交批次")
	}
	select {
	case <-finished:
		t.Fatal("尚未放行就继续后台")
	default:
	}
	barrier.open()
	barrier.open()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("放行后未退出")
	}
	logger.InfoContext(ctx, "后续批次", "event", "catalog_projection_backfill_batch", "completed", 20, "total", 24)
}
