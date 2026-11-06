package backup

import (
	"testing"
	"time"
)

func instant(s string) time.Time {
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return at
}

func TestPlan(t *testing.T) {
	for _, tc := range []struct {
		name, now, last, retry, zone, next string
		hour                               int
		startup, due                       bool
	}{
		{name: "到点", now: "2026-11-06T03:00:00+08:00", last: "2026-11-05T03:00:00+08:00", zone: "Asia/Shanghai", hour: 3, due: true},
		{name: "当天手动成功不重复", now: "2026-11-06T04:00:00+08:00", last: "2026-11-06T01:00:00+08:00", zone: "Asia/Shanghai", hour: 3, next: "2026-11-07T03:00:00+08:00"},
		{name: "启动无备份", now: "2026-11-06T01:00:00+08:00", zone: "Asia/Shanghai", hour: 3, startup: true, due: true},
		{name: "启动25小时", now: "2026-11-06T01:00:00+08:00", last: "2026-11-05T00:00:00+08:00", zone: "Asia/Shanghai", hour: 3, startup: true, due: true},
		{name: "启动24小时边界", now: "2026-11-06T01:00:00+08:00", last: "2026-11-05T01:00:00+08:00", zone: "Asia/Shanghai", hour: 3, startup: true, due: true},
		{name: "启动23小时", now: "2026-11-06T01:00:00+08:00", last: "2026-11-05T02:00:00+08:00", zone: "Asia/Shanghai", hour: 3, startup: true, next: "2026-11-06T03:00:00+08:00"},
		{name: "到点失败仍等一小时", now: "2026-11-06T04:30:00+08:00", retry: "2026-11-06T05:00:00+08:00", zone: "Asia/Shanghai", hour: 3, next: "2026-11-06T05:00:00+08:00"},
		{name: "未到点重试已到", now: "2026-11-06T02:00:00+08:00", retry: "2026-11-06T02:00:00+08:00", zone: "Asia/Shanghai", hour: 3, due: true},
		{name: "未到点重试未到", now: "2026-11-06T01:30:00+08:00", retry: "2026-11-06T02:00:00+08:00", zone: "Asia/Shanghai", hour: 3, next: "2026-11-06T02:00:00+08:00"},
		{name: "跨午夜仍等待重试", now: "2026-11-07T00:15:00+08:00", retry: "2026-11-07T00:30:00+08:00", zone: "Asia/Shanghai", hour: 0, next: "2026-11-07T00:30:00+08:00"},
		{name: "UTC日期不同但本地已成功", now: "2026-11-06T00:30:00Z", last: "2026-11-05T18:00:00Z", zone: "Asia/Shanghai", hour: 3, next: "2026-11-06T19:00:00Z"},
		{name: "纽约夏令时缺口之前", now: "2026-03-08T00:00:00-05:00", zone: "America/New_York", hour: 2, next: "2026-03-08T01:00:00-05:00"},
		{name: "纽约夏令时缺口之后", now: "2026-03-08T03:00:00-04:00", last: "2026-03-07T02:00:00-05:00", zone: "America/New_York", hour: 2, due: true},
		{name: "纽约夏令时当天已成功", now: "2026-03-08T04:00:00-04:00", last: "2026-03-08T00:30:00-05:00", zone: "America/New_York", hour: 2, next: "2026-03-09T02:00:00-04:00"},
		{name: "纽约冬令时重复小时", now: "2026-11-01T01:30:00-05:00", last: "2026-11-01T01:00:00-04:00", zone: "America/New_York", hour: 1, next: "2026-11-02T01:00:00-05:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			in := PlanInput{Now: instant(tc.now), Location: loc, Hour: tc.hour, Startup: tc.startup}
			if tc.last != "" {
				in.LastSuccess = instant(tc.last)
			}
			if tc.retry != "" {
				in.RetryAt = instant(tc.retry)
			}
			next := in.Now
			if tc.next != "" {
				next = instant(tc.next)
			}
			got := Plan(in)
			if got.Due != tc.due || !got.Next.Equal(next) {
				t.Fatalf("Plan = %+v，期望 due=%v next=%v", got, tc.due, next)
			}
		})
	}
}
