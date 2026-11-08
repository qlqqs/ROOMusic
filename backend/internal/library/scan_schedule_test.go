package library

import (
	"testing"
	"time"
)

func TestPlanScheduledScan(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	parse := func(value string) time.Time {
		if value == "" {
			return time.Time{}
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, tc := range []struct {
		name, now, updated, started, handled, slot, next string
		enabled, due                                     bool
		location                                         *time.Location
	}{
		{name: "四点到点", now: "2026-11-08T04:00:00+08:00", enabled: true, due: true},
		{name: "在跑时跳过后当天不补", now: "2026-11-08T09:00:00+08:00", handled: "2026-11-08T04:00:00+08:00", started: "2026-11-08T03:50:00+08:00", enabled: true},
		{name: "四点前手动扫过仍触发", now: "2026-11-08T04:00:00+08:00", started: "2026-11-08T03:59:00+08:00", enabled: true, due: true},
		{name: "九点启动补扫", now: "2026-11-08T09:00:00+08:00", enabled: true, due: true},
		{name: "九点重启自动扫描已跑过", now: "2026-11-08T09:00:00+08:00", started: "2026-11-08T04:00:00+08:00", enabled: true},
		{name: "九点重启手动扫描已跑过", now: "2026-11-08T09:00:00+08:00", started: "2026-11-08T08:00:00+08:00", enabled: true},
		{name: "十五点开启当天不触发", now: "2026-11-08T15:00:00+08:00", updated: "2026-11-08T15:00:00+08:00", enabled: true},
		{name: "十五点开启次日触发", now: "2026-11-09T04:00:00+08:00", updated: "2026-11-08T15:00:00+08:00", slot: "2026-11-09T04:00:00+08:00", next: "2026-11-10T04:00:00+08:00", enabled: true, due: true},
		{name: "升级当天不补扫", now: "2026-11-08T09:00:00+08:00", updated: "2026-11-08T08:00:00+08:00", enabled: true},
		{name: "三点五十九改到四点", now: "2026-11-08T04:00:00+08:00", updated: "2026-11-08T03:59:00+08:00", enabled: true, due: true},
		{name: "关闭不触发", now: "2026-11-08T04:00:00+08:00"},
		{name: "跨日沿用昨天已处理时刻", now: "2026-11-09T00:00:00+08:00", handled: "2026-11-08T04:00:00+08:00", enabled: true},
		{name: "跨月下一时刻", now: "2026-11-30T23:59:00+08:00", slot: "2026-11-30T04:00:00+08:00", next: "2026-12-01T04:00:00+08:00", enabled: true, due: true},
		{name: "UTC输入按上海四点触发", now: "2026-11-07T20:00:00Z", slot: "2026-11-07T20:00:00Z", next: "2026-11-08T20:00:00Z", enabled: true, due: true},
		{name: "UTC时区四点触发", now: "2026-11-08T04:00:00Z", slot: "2026-11-08T04:00:00Z", next: "2026-11-09T04:00:00Z", location: time.UTC, enabled: true, due: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location := tc.location
			if location == nil {
				location = shanghai
			}
			slot, next := tc.slot, tc.next
			if slot == "" {
				slot = "2026-11-08T04:00:00+08:00"
			}
			if next == "" {
				next = "2026-11-09T04:00:00+08:00"
			}
			plan := PlanScheduledScan(ScheduledScanPlanInput{
				Now: parse(tc.now), Location: location,
				Settings:      ScanSchedule{Enabled: tc.enabled, Hour: 4, UpdatedAt: parse(tc.updated)},
				LastStartedAt: parse(tc.started), LastHandledSlot: parse(tc.handled),
			})
			if plan.Due != tc.due || !plan.Slot.Equal(parse(slot)) || !plan.Next.Equal(parse(next)) {
				t.Fatalf("计划 = %+v，期望 Due=%v Slot=%s Next=%s", plan, tc.due, slot, next)
			}
			if !plan.Next.After(parse(tc.now)) {
				t.Fatalf("下一时刻必须严格晚于当前时间：%v", plan.Next)
			}
		})
	}
}
