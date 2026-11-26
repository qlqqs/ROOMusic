package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRootPollAndSettings(t *testing.T) {
	clock := &scheduledScanTestClock{now: time.Now().UTC().Truncate(time.Microsecond), timers: make(chan *scheduledScanTestTimer, 64)}
	dailyClock := &scheduledScanTestClock{now: clock.now, timers: make(chan *scheduledScanTestTimer, 8)}
	app := newIntegrationTestApplication(t, WithDirectoryWatchClock(clock), WithScanSchedulerClock(dailyClock))
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	rootDir := createLibraryDirectory(t, app, "离线轮询根")
	root := createRoot(t, app, admin, rootDir, "create-poll-root")
	otherDir := createLibraryDirectory(t, app, "待扫描根")
	other := createRoot(t, app, admin, otherDir, "create-pending-root")
	const base = "/api/v1/library-roots"
	path := base + "/" + root + "/scan-interval"

	readRoot := func(id string) map[string]any {
		t.Helper()
		response := app.request(t, http.MethodGet, base, nil, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("目录列表 HTTP %d：%s", response.Code, response.Body.String())
		}
		for _, item := range decodeJSONMap(t, response)["items"].([]any) {
			dto := item.(map[string]any)
			if dto["id"] == id {
				return dto
			}
		}
		t.Fatalf("目录列表缺少 %s", id)
		return nil
	}
	requireRoot := func(id string, mode any, minutes, revision int) {
		t.Helper()
		dto := readRoot(id)
		gotMode, present := dto["scan_watch_mode"]
		if !present || gotMode != mode || dto["poll_interval_minutes"] != float64(minutes) || dto["revision"] != float64(revision) {
			t.Fatalf("目录状态 %+v，期望 mode=%v interval=%d revision=%d", dto, mode, minutes, revision)
		}
	}
	save := func(minutes int) {
		t.Helper()
		body := map[string]any{"poll_interval_minutes": minutes}
		response := app.request(t, http.MethodPut, path, body, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("保存 %d 分钟 HTTP %d：%s", minutes, response.Code, response.Body.String())
		}
		if want := map[string]any{"poll_interval_minutes": float64(minutes)}; !reflect.DeepEqual(decodeJSONMap(t, response), want) {
			t.Fatalf("保存响应内容不符：%s", response.Body.String())
		}
	}
	requireRoot(root, nil, 60, 1)
	requireRoot(other, nil, 60, 1)
	for _, minutes := range []int{15, 60, 240, 720, 1440} {
		t.Run(fmt.Sprintf("%d分钟", minutes), func(t *testing.T) {
			save(minutes)
			requireRoot(root, nil, minutes, 1)
			requireRoot(other, nil, 60, 1)
		})
	}
	for _, body := range []string{
		`{}`, `{"poll_interval_minutes":14}`, `{"poll_interval_minutes":null}`, `{"poll_interval_minutes":"15"}`,
		`{"poll_interval_minutes":15,"poll_interval_minutes":60}`, `{"poll_interval_minutes":15,"extra":0}`,
		`{"poll_interval_minutes":15}{}`,
	} {
		t.Run(body, func(t *testing.T) {
			requireAPIError(t, personalRaw(app, http.MethodPut, path, body, &admin, nil), http.StatusBadRequest, "invalid_input")
		})
	}
	valid := json.RawMessage(`{"poll_interval_minutes":15}`)
	requireAPIError(t, app.request(t, http.MethodPut, base+"/"+uuid.New()+"/scan-interval", valid, &admin, nil), http.StatusNotFound, "not_found")
	requireAPIError(t, app.request(t, http.MethodPut, base+"/bad-id/scan-interval", valid, &admin, nil), http.StatusBadRequest, "invalid_id")
	requireAPIError(t, app.request(t, http.MethodPut, path, valid, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, app.request(t, http.MethodPut, path, valid, &member, nil), http.StatusForbidden, "forbidden")
	requireAPIError(t, app.request(t, http.MethodPut, path, valid, &admin, map[string]string{"Origin": "http://untrusted.example"}), http.StatusForbidden, "origin_forbidden")
	requireAPIError(t, app.request(t, http.MethodGet, base, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, app.request(t, http.MethodGet, base, nil, &member, nil), http.StatusForbidden, "forbidden")
	requireRoot(root, nil, 1440, 1)

	// 模拟曾完成整根扫描的根现已离线。注册会降级，另一个根仍待首次扫描。
	if err := os.Remove(rootDir); err != nil {
		t.Fatal(err)
	}
	mustExec(t, app, `UPDATE library_roots SET watch_baseline_at=now() WHERE id=$1`, root)
	mustExec(t, app, `UPDATE scan_schedule SET enabled=false WHERE id=1`)
	save(15) // 唤醒后台读取新的根配置。
	var anchor time.Time
	for i := 0; i < cap(clock.timers) && anchor.IsZero(); i++ {
		clock.waitTimer(t)
		if err := app.database.QueryRowContext(t.Context(), `SELECT coalesce(last_poll_started_at,'0001-01-01'::timestamptz) FROM library_roots WHERE id=$1`, root).Scan(&anchor); err != nil {
			t.Fatal(err)
		}
	}
	if anchor.IsZero() {
		t.Fatal("目录未建立轮询起点")
	}
	requireRoot(root, "scheduled", 15, 1)
	requireRoot(other, nil, 60, 1)
	save(15)
	var repeated time.Time
	if err := app.database.QueryRowContext(t.Context(), `SELECT last_poll_started_at FROM library_roots WHERE id=$1`, root).Scan(&repeated); err != nil {
		t.Fatal(err)
	}
	if !repeated.Equal(anchor) {
		t.Fatalf("重复保存改动轮询起点：%v → %v", anchor, repeated)
	}
	clock.fire(clock.waitTimer(t), anchor.Add(15*time.Minute))
	for range cap(clock.timers) {
		timer := clock.waitTimer(t)
		var scanID string
		if err := app.database.QueryRowContext(t.Context(), `SELECT coalesce((SELECT id::text FROM scan_runs LIMIT 1),'')`).Scan(&scanID); err != nil {
			t.Fatal(err)
		}
		if scanID != "" {
			if status := waitForScanToFinish(t, app, scanID); status != "incomplete" {
				t.Fatalf("仅扫描离线根，终态应为 incomplete，实际 %s", status)
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM scan_runs WHERE roots_total=1 AND NOT whole_library AND trigger_kind='scheduled' AND scan_mode='incremental'`)
			return
		}
		clock.fire(timer, anchor.Add(15*time.Minute))
	}
	t.Fatal("推进轮询时钟后未启动扫描")
}
