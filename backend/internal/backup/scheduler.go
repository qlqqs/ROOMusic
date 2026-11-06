package backup

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Service) schedule(ctx context.Context, db *sql.DB) {
	for {
		if ctx.Err() != nil {
			return
		}
		// 在读取新快照前合并唤醒，之后到达的完成事件不能丢弃。
		select {
		case <-s.wake:
		default:
		}
		files, _ := s.listFiles()
		var last time.Time
		if len(files) > 0 {
			last = files[0].CreatedAt
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			return
		}
		plan := Plan(PlanInput{Now: s.now(), Location: s.settings.Location, Hour: s.settings.Hour, LastSuccess: last, RetryAt: s.retryAt, Startup: s.startup})
		s.startup = false
		s.mu.Unlock()
		delay := plan.Next.Sub(s.now())
		if plan.Due {
			_, err := s.runReserved(ctx, db, SourceScheduled, 0, false)
			if !errors.Is(err, ErrBusy) {
				continue
			}
			delay = 10 * time.Minute
		}
		if delay > 10*time.Minute {
			delay = 10 * time.Minute
		}
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
