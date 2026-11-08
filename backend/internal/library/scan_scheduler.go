package library

import (
	"context"
	"errors"
	"time"
)

const scanSchedulePollInterval = 10 * time.Minute

// ScanSchedulerClock 允许测试同时控制当前时间和计时器，避免等待真实整点。
type ScanSchedulerClock interface {
	Now() time.Time
	NewTimer(time.Duration) ScanSchedulerTimer
}

// ScanSchedulerTimer 的计时器在保存设置或停机唤醒后必须停止。
type ScanSchedulerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type scanSchedulerClock struct{}

func (scanSchedulerClock) Now() time.Time { return time.Now() }
func (scanSchedulerClock) NewTimer(delay time.Duration) ScanSchedulerTimer {
	return scanSchedulerTimer{time.NewTimer(delay)}
}

type scanSchedulerTimer struct{ *time.Timer }

func (timer scanSchedulerTimer) C() <-chan time.Time { return timer.Timer.C }

// WithScanSchedulerClock 在启动调度器之前注入时钟，不应在运行中修改。
func (service *Service) WithScanSchedulerClock(clock ScanSchedulerClock) *Service {
	service.scanScheduleClock = clock
	return service
}

// RunScanScheduler 在应用生命周期内运行；每个 Service 仅由 app 启动一次。
// 触发无论成功、跳过还是失败都消费该时刻，不排队、不重试。
func (service *Service) RunScanScheduler(ctx context.Context, location *time.Location) {
	clock := service.scanScheduleClock
	if clock == nil {
		clock = scanSchedulerClock{}
	}
	var lastHandledSlot time.Time
	for ctx.Err() == nil {
		service.scanScheduleMutex.Lock()
		// 读取快照前合并旧唤醒；读取期间的新保存留给下一轮，不能丢弃。
		select {
		case <-service.scanScheduleWake:
		default:
		}
		delay := scanSchedulePollInterval
		settings, lastStarted, err := service.loadScanScheduleState(ctx)
		if err != nil {
			if service.logger != nil && ctx.Err() == nil {
				service.logger.Warn("无法读取定时扫描设置", "event", "library.scan.schedule_read_failed", "module", "library")
			}
		} else {
			plan := PlanScheduledScan(ScheduledScanPlanInput{
				Now: clock.Now(), Location: location, Settings: settings,
				LastStartedAt: lastStarted, LastHandledSlot: lastHandledSlot,
			})
			if plan.Due {
				lastHandledSlot = plan.Slot
				service.startScheduledScan(ctx, plan.Slot)
			}
			delay = plan.Next.Sub(clock.Now())
			if delay > scanSchedulePollInterval {
				delay = scanSchedulePollInterval
			}
			if delay < 0 {
				delay = 0
			}
		}
		service.scanScheduleMutex.Unlock()
		if ctx.Err() != nil {
			return
		}
		timer := clock.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-service.scanScheduleWake:
			timer.Stop()
		case <-timer.C():
		}
	}
}

func (service *Service) startScheduledScan(ctx context.Context, slot time.Time) {
	var activeRoots bool
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_roots WHERE status='active')`).Scan(&activeRoots); err != nil {
		service.logScheduledScanFailure(ctx, slot)
		return
	}
	if !activeRoots {
		service.logScheduledScanSkipped(slot, "no_active_roots")
		return
	}
	result, err := service.StartScan(ctx, StartOptions{Mode: ScanModeIncremental, Trigger: ScanTriggerScheduled, scheduledSlot: slot})
	switch {
	case errors.Is(err, ErrCoordinationUnavailable):
		service.logScheduledScanSkipped(slot, "coordination_unavailable")
	case err != nil:
		service.logScheduledScanFailure(ctx, slot)
	case !result.Started:
		service.logScheduledScanSkipped(slot, "scan_running")
	default:
		if service.logger != nil {
			service.logger.Info("定时扫描已启动", "event", "library.scan.scheduled_started", "module", "library", "scan_run_id", result.Scan.ID, "slot", slot)
		}
	}
}

func (service *Service) logScheduledScanSkipped(slot time.Time, reason string) {
	if service.logger != nil {
		service.logger.Info("本次定时扫描已跳过", "event", "library.scan.scheduled_skipped", "module", "library", "slot", slot, "reason", reason)
	}
}

func (service *Service) logScheduledScanFailure(ctx context.Context, slot time.Time) {
	if service.logger != nil && ctx.Err() == nil {
		service.logger.Warn("本次定时扫描未能启动", "event", "library.scan.scheduled_failed", "module", "library", "slot", slot, "reason", "database_unavailable")
	}
}
