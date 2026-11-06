package backup

import "time"

type PlanInput struct {
	Now         time.Time
	Location    *time.Location
	Hour        int
	LastSuccess time.Time
	RetryAt     time.Time
	Startup     bool
}
type PlanResult struct {
	Due  bool
	Next time.Time
}

// Plan 仅计算计划，不读文件、不启动计时器。失败后的重试时间限制本轮到点判断。
func Plan(in PlanInput) PlanResult {
	due := PlanResult{Due: true, Next: in.Now}
	if in.Startup && (in.LastSuccess.IsZero() || in.Now.Sub(in.LastSuccess) >= 24*time.Hour) {
		return due
	}
	if !in.RetryAt.IsZero() {
		if !in.Now.Before(in.RetryAt) {
			return due
		}
		return PlanResult{Next: in.RetryAt}
	}
	location := in.Location
	if location == nil {
		location = time.UTC
	}
	now := in.Now.In(location)
	success := in.LastSuccess.In(location)
	today := !in.LastSuccess.IsZero() && now.Year() == success.Year() && now.YearDay() == success.YearDay()
	at := time.Date(now.Year(), now.Month(), now.Day(), in.Hour, 0, 0, 0, location)
	if !today && !now.Before(at) {
		return due
	}
	if today {
		at = time.Date(now.Year(), now.Month(), now.Day()+1, in.Hour, 0, 0, 0, location)
	}
	return PlanResult{Next: at}
}
