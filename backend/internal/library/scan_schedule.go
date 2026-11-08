package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidScanSchedule 表示定时扫描的整点不在允许范围。
var ErrInvalidScanSchedule = errors.New("invalid scan schedule")

// ScanSchedule 保存每日扫描设置；更新时间只用于调度，不向客户端暴露。
type ScanSchedule struct {
	Enabled   bool      `json:"enabled"`
	Hour      int       `json:"hour"`
	UpdatedAt time.Time `json:"-"`
}

// ScheduledScanPlanInput 是触发计算的完整快照，不包含任何外部依赖。
type ScheduledScanPlanInput struct {
	Now             time.Time
	Location        *time.Location
	Settings        ScanSchedule
	LastStartedAt   time.Time
	LastHandledSlot time.Time
}

// ScheduledScanPlan 给出最近的计划时刻与严格晚于当前时间的下一个时刻。
type ScheduledScanPlan struct {
	Due  bool
	Slot time.Time
	Next time.Time
}

// PlanScheduledScan 按配置时区的日历整点计算，不用固定 24 小时跨日。
// 设置必须已通过 0–23 整点校验；夏令时钟点由 time.Date 规范化。
func PlanScheduledScan(in ScheduledScanPlanInput) ScheduledScanPlan {
	location := in.Location
	if location == nil {
		location = time.UTC
	}
	now := in.Now.In(location)
	slot := time.Date(now.Year(), now.Month(), now.Day(), in.Settings.Hour, 0, 0, 0, location)
	next := slot
	if now.Before(slot) {
		slot = time.Date(now.Year(), now.Month(), now.Day()-1, in.Settings.Hour, 0, 0, 0, location)
	} else {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, in.Settings.Hour, 0, 0, 0, location)
	}
	return ScheduledScanPlan{
		Slot: slot,
		Next: next,
		Due: in.Settings.Enabled && !slot.Before(in.Settings.UpdatedAt) &&
			in.LastStartedAt.Before(slot) && !slot.Equal(in.LastHandledSlot),
	}
}

// LoadScanSchedule 读取数据库中的单行设置，不回退到本地默认值。
func (service *Service) LoadScanSchedule(ctx context.Context) (ScanSchedule, error) {
	var settings ScanSchedule
	err := service.database.QueryRowContext(ctx, `SELECT enabled,hour,updated_at FROM scan_schedule WHERE id=1`).
		Scan(&settings.Enabled, &settings.Hour, &settings.UpdatedAt)
	if err != nil {
		return ScanSchedule{}, fmt.Errorf("load scan schedule: %w", err)
	}
	return settings, nil
}

// SaveScanSchedule 整份替换设置；重复保存相同值保留更新时间，提交后唤醒调度器。
func (service *Service) SaveScanSchedule(ctx context.Context, settings ScanSchedule) (ScanSchedule, error) {
	if settings.Hour < 0 || settings.Hour > 23 {
		return ScanSchedule{}, ErrInvalidScanSchedule
	}
	// 与本进程的读取及触发串行；保存成功后不能再用保存前的快照启动。
	service.scanScheduleMutex.Lock()
	defer service.scanScheduleMutex.Unlock()
	var saved ScanSchedule
	err := service.database.QueryRowContext(ctx, `UPDATE scan_schedule
		SET enabled=$1,hour=$2,updated_at=CASE WHEN enabled IS DISTINCT FROM $1 OR hour IS DISTINCT FROM $2 THEN NOW() ELSE updated_at END
		WHERE id=1 RETURNING enabled,hour,updated_at`, settings.Enabled, settings.Hour).
		Scan(&saved.Enabled, &saved.Hour, &saved.UpdatedAt)
	if err != nil {
		return ScanSchedule{}, fmt.Errorf("save scan schedule: %w", err)
	}
	select {
	case service.scanScheduleWake <- struct{}{}:
	default:
	}
	return saved, nil
}

// loadScanScheduleState 在同一快照中读取设置和任何发起方最近的扫描时间。
func (service *Service) loadScanScheduleState(ctx context.Context) (ScanSchedule, time.Time, error) {
	var settings ScanSchedule
	var lastStarted sql.NullTime
	err := service.database.QueryRowContext(ctx, `SELECT enabled,hour,updated_at,
		(SELECT MAX(started_at) FROM scan_runs) FROM scan_schedule WHERE id=1`).
		Scan(&settings.Enabled, &settings.Hour, &settings.UpdatedAt, &lastStarted)
	if err != nil {
		return ScanSchedule{}, time.Time{}, fmt.Errorf("load scan schedule state: %w", err)
	}
	return settings, lastStarted.Time, nil
}
