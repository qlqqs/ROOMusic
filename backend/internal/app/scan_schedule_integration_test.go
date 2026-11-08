package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library"
)

func TestPostgreSQLScanScheduleSettings(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin, member := application.createUser(t, "admin"), application.createUser(t, "user")
	const path = "/api/v1/scan-schedule"
	readUpdatedAt := func() time.Time {
		t.Helper()
		var updated time.Time
		if err := application.database.QueryRow(`SELECT updated_at FROM scan_schedule WHERE id=1`).Scan(&updated); err != nil {
			t.Fatal(err)
		}
		return updated
	}
	assertSettings := func(method string, body any, enabled bool, hour int) {
		t.Helper()
		response := application.request(t, method, path, body, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s 设置：HTTP %d：%s", method, response.Code, response.Body.String())
		}
		want := map[string]any{"enabled": enabled, "hour": float64(hour)}
		if got := decodeJSONMap(t, response); !reflect.DeepEqual(got, want) {
			t.Fatalf("设置响应 = %v，期望 %v（不暴露 updated_at）", got, want)
		}
	}
	assertSettings(http.MethodGet, nil, true, 4)
	before := readUpdatedAt()
	assertSettings(http.MethodPut, map[string]any{"enabled": true, "hour": 4}, true, 4)
	if after := readUpdatedAt(); !after.Equal(before) {
		t.Fatalf("重复保存改变 updated_at：%v → %v", before, after)
	}
	assertSettings(http.MethodPut, map[string]any{"enabled": false, "hour": 5}, false, 5)
	assertSettings(http.MethodGet, nil, false, 5)
	changed := readUpdatedAt()
	if !changed.After(before) {
		t.Fatalf("修改设置没有更新 updated_at：%v → %v", before, changed)
	}
	assertSettings(http.MethodPut, map[string]any{"enabled": false, "hour": 5}, false, 5)
	if after := readUpdatedAt(); !after.Equal(changed) {
		t.Fatalf("重复保存非默认设置改变 updated_at：%v → %v", changed, after)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"整点小于零", `{"enabled":true,"hour":-1}`},
		{"整点大于二十三", `{"enabled":true,"hour":24}`},
		{"缺少开关", `{"hour":4}`},
		{"缺少整点", `{"enabled":true}`},
		{"未知字段", `{"enabled":true,"hour":4,"extra":1}`},
		{"字段类型错误", `{"enabled":true,"hour":"4"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := application.request(t, http.MethodPut, path, json.RawMessage(tc.body), &admin, nil)
			requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
		})
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		body := map[string]any{"enabled": true, "hour": 4}
		requireAPIError(t, application.request(t, method, path, body, nil, nil), http.StatusUnauthorized, "unauthorized")
		requireAPIError(t, application.request(t, method, path, body, &member, nil), http.StatusForbidden, "forbidden")
	}
	// 被拒绝的保存不能改变设置或影响当天的触发资格。
	assertSettings(http.MethodGet, nil, false, 5)
	if after := readUpdatedAt(); !after.Equal(changed) {
		t.Fatalf("被拒绝的请求改变 updated_at：%v → %v", changed, after)
	}
}

func TestPostgreSQLScheduledScan(t *testing.T) {
	for _, busy := range []bool{false, true} {
		name := "整点发起一次增量扫描"
		if busy {
			name = "手动扫描在跑时跳过且当天不补"
		}
		t.Run(name, func(t *testing.T) {
			clock := &scheduledScanTestClock{now: time.Now().UTC(), timers: make(chan *scheduledScanTestTimer, 8)}
			// 从下一天 3:59 开始，确保迁移的更新时间早于待触发整点。
			clock.now = time.Date(clock.now.Year(), clock.now.Month(), clock.now.Day()+1, 3, 59, 0, 0, time.UTC)
			application := newIntegrationTestApplication(t, WithScanSchedulerClock(clock))
			timer := clock.waitTimer(t)
			if timer.delay != time.Minute {
				t.Fatalf("离四点只剩一分钟，等待时间 = %v", timer.delay)
			}
			admin, member := application.createUser(t, "admin"), application.createUser(t, "user")
			root := createLibraryDirectory(t, application, "scheduled")
			writeSyntheticTrack(t, filepath.Join(root, "Album", "01.flac"), 1, "TITLE=定时曲目", "ARTIST=合成艺人", "ALBUM=定时专辑", "TRACKNUMBER=1")
			createRoot(t, application, admin, root, "create-scheduled")
			gate := newScanFeedbackGate(t, application, "tracks", "INSERT", "NEW.source_root_id IS NOT NULL")
			var scanID string
			trigger := "scheduled"
			if busy {
				started := application.request(t, http.MethodPost, "/api/v1/scans", nil, &admin, nil)
				if started.Code != http.StatusAccepted {
					t.Fatalf("手动启动：HTTP %d：%s", started.Code, started.Body.String())
				}
				dto := decodeJSONMap(t, started)
				scanID = requireScanModeDTO(t, dto, "incremental")
				if dto["trigger"] != "manual" {
					t.Fatalf("手动扫描 trigger = %v", dto["trigger"])
				}
				trigger = "manual"
				waitScanFeedbackBlockedBy(t, application.database, gate.pid)
			}
			clock.fire(timer, clock.Now().Add(time.Minute))
			timer = clock.waitTimer(t) // 循环已完成本次触发或跳过，才建立下一计时器。
			if !busy {
				waitScanFeedbackBlockedBy(t, application.database, gate.pid)
			}
			var count int
			if err := application.database.QueryRow(`SELECT COUNT(*) FROM scan_runs`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("到点后扫描数 = %d，期望 1；err=%v", count, err)
			}
			var storedID, mode, storedTrigger string
			if err := application.database.QueryRow(`SELECT id::text,scan_mode,trigger_kind FROM scan_runs`).Scan(&storedID, &mode, &storedTrigger); err != nil {
				t.Fatal(err)
			}
			if mode != "incremental" || storedTrigger != trigger || (busy && storedID != scanID) {
				t.Fatalf("持久化扫描 = %s/%s/%s，期望原扫描且 incremental/%s", storedID, mode, storedTrigger, trigger)
			}
			scanID = storedID
			for _, user := range []*integrationTestUser{&admin, &member} {
				response := application.request(t, http.MethodGet, "/api/v1/scans/"+scanID, nil, user, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("读取扫描：HTTP %d：%s", response.Code, response.Body.String())
				}
				if dto := decodeJSONMap(t, response); dto["trigger"] != trigger || requireScanModeDTO(t, dto, "incremental") != scanID {
					t.Fatalf("扫描 DTO 发起方或模式不符：%v", dto)
				}
			}
			gate.release(t)
			if status := waitForScanToFinish(t, application, scanID); status != "succeeded" {
				t.Fatalf("扫描终态 = %s", status)
			}
			clock.fire(timer, clock.Now().Add(10*time.Minute))
			clock.waitTimer(t)
			if err := application.database.QueryRow(`SELECT COUNT(*) FROM scan_runs`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("同一计划时刻被重复触发：count=%d err=%v", count, err)
			}
			application.shutdown()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := application.app.WaitForScanScheduler(ctx); err != nil {
				t.Fatalf("生命周期取消后调度器未退出：%v", err)
			}
		})
	}
}

// 只有测试显式推进时才到点；新计时器作为一轮数据库操作完成的同步边界。
type scheduledScanTestClock struct {
	mutex  sync.Mutex
	now    time.Time
	timers chan *scheduledScanTestTimer
}

func (clock *scheduledScanTestClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *scheduledScanTestClock) NewTimer(delay time.Duration) library.ScanSchedulerTimer {
	timer := &scheduledScanTestTimer{ticks: make(chan time.Time, 1), delay: delay}
	clock.timers <- timer
	return timer
}

func (clock *scheduledScanTestClock) fire(timer *scheduledScanTestTimer, now time.Time) {
	clock.mutex.Lock()
	clock.now = now
	clock.mutex.Unlock()
	timer.ticks <- now
}

func (clock *scheduledScanTestClock) waitTimer(t *testing.T) *scheduledScanTestTimer {
	t.Helper()
	select {
	case timer := <-clock.timers:
		return timer
	case <-time.After(10 * time.Second):
		t.Fatal("调度器未进入等待")
		return nil
	}
}

type scheduledScanTestTimer struct {
	ticks chan time.Time
	delay time.Duration
}

func (timer *scheduledScanTestTimer) C() <-chan time.Time { return timer.ticks }
func (timer *scheduledScanTestTimer) Stop() bool          { return true }
