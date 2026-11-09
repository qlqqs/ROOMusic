package zones_test

import (
	"context"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestPostgreSQLZonesOutputReleaseAndTakeover(t *testing.T) {
	for _, tc := range []struct {
		name             string
		delay            time.Duration
		takeover, resume bool
	}{
		{"立即接手", 0, true, true}, {"十秒边界", 10 * time.Second, true, true}, {"超过十秒", 10*time.Second + time.Millisecond, true, false}, {"普通认领不恢复", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := newOutputFixture(t, 0)
			user := f.user(t, "user")
			claim, z := outputStart(t, f, user, f.tracks[:1])
			seq := z.Stream.Seq
			outputRelease(t, f, user, claim.SessionID, seq, 17.5)
			snapshot := f.snapshot(t, user)
			if snapshot.Devices[0].Online || snapshot.Devices[0].OutputSessionID != nil || snapshot.Zones[0].Intent != "pause" || snapshot.Zones[0].Stream != nil || snapshot.Zones[0].Position.Seconds != 17.5 || !r.Sessions()[0].Closed() {
				t.Fatalf("release 未立即保存位置、离线并暂停：%+v", snapshot)
			}
			controlError(t, f.request(t, user, "POST", "/outputs/"+claim.SessionID+"/reports", zones.ReportOutputInput{Kind: "status", Status: "playing", StreamSeq: &seq}, controlOrigin), 404, "output_unknown")
			f.clock.advance(tc.delay)
			next := outputClaim(t, f, user, claim.Device.DeviceID, tc.takeover, nil)
			if !next.Device.Online || next.SessionID == claim.SessionID {
				t.Fatal("认领未生成新的在线身份")
			}
			if tc.resume {
				if next.Zone.Intent != "play" || next.Zone.Stream == nil || next.Zone.Position.Seconds != 17.5 || r.Sessions()[1].Spec().StartSeconds != 17.5 {
					t.Fatalf("窗口内未接续：%+v", next.Zone)
				}
			} else if next.Zone.Intent != "pause" || next.Zone.Stream != nil {
				t.Fatalf("不满足接手条件却起播：%+v", next.Zone)
			}
		})
	}
}

func TestPostgreSQLZonesOutputLeaseAndSameSessionRecovery(t *testing.T) {
	f, r := newOutputFixture(t, 0)
	user := f.user(t, "user")
	claim, z := outputStart(t, f, user, f.tracks[:1])
	seq := z.Stream.Seq
	// 没有事件流、没有回报；45 秒整仍属于租约，下一次五秒扫描离线。
	f.clock.advance(45 * time.Second)
	if !f.snapshot(t, user).Devices[0].Online {
		t.Fatal("45 秒边界被提前判离线")
	}
	f.clock.advance(5 * time.Second)
	offline := f.snapshot(t, user)
	if offline.Devices[0].Online || offline.Zones[0].Intent != "pause" || offline.Zones[0].Stream != nil || !r.Sessions()[0].Closed() {
		t.Fatalf("租约过期未暂停：%+v", offline)
	}
	resumed := outputReport(t, f, user, claim.SessionID, seq, "status", "playing", 12)
	if !resumed.Device.Online || resumed.Zone.Intent != "play" || resumed.Zone.Stream == nil || resumed.Zone.Stream.Seq <= seq || resumed.Zone.Position.Seconds != 12 || r.Sessions()[1].Spec().StartSeconds != 12 {
		t.Fatalf("同一身份未在回报位置恢复：%+v", resumed)
	}
}

func TestPostgreSQLZonesOutputSupersededAndRestart(t *testing.T) {
	f, r := newOutputFixture(t, 0)
	user := f.user(t, "user")
	other := f.user(t, "admin")
	first, z := outputStart(t, f, user, f.tracks[:1])
	seq := z.Stream.Seq
	second := outputClaim(t, f, user, first.Device.DeviceID, false, nil)
	if second.Zone.Intent != "play" || second.Zone.Stream == nil || !r.Sessions()[0].Closed() {
		t.Fatal("认领取代未关闭旧流并继续意图")
	}
	in := zones.ReportOutputInput{Kind: "status", Status: "playing", StreamSeq: &seq}
	controlError(t, f.request(t, user, "POST", "/outputs/"+first.SessionID+"/reports", in, controlOrigin), 409, "output_superseded")
	controlError(t, f.request(t, other, "POST", "/outputs/"+second.SessionID+"/reports", in, controlOrigin), 404, "not_found")
	outputRelease(t, f, user, first.SessionID, seq, 99)
	if got := f.snapshot(t, user); !got.Devices[0].Online || got.Zones[0].Stream.Seq != second.Zone.Stream.Seq {
		t.Fatal("旧身份释放误停新流")
	}
	// 真正创建新 Service 读取同一数据库，不用篡改输出身份模拟重启。
	outputService(t, f, r)
	controlError(t, f.request(t, user, "POST", "/outputs/"+second.SessionID+"/reports", in, controlOrigin), 404, "output_unknown")
	loaded := f.snapshot(t, user)
	if loaded.Devices[0].Online || loaded.Zones[0].Intent != "pause" || loaded.Zones[0].Stream != nil {
		t.Fatalf("重启未按离线暂停加载：%+v", loaded)
	}
	restored := outputClaim(t, f, user, first.Device.DeviceID, false, &zones.PlayingOutput{EntryID: z.Current.EntryID, PositionSeconds: 23})
	if restored.Zone.Intent != "play" || restored.Zone.Stream == nil || restored.Zone.Stream.Seq <= second.Zone.Stream.Seq || restored.Zone.Position.Seconds != 23 {
		t.Fatalf("重启认领 playing 未接着放：%+v", restored.Zone)
	}
	sessions := r.Sessions()
	if spec := sessions[len(sessions)-1].Spec(); spec.Start.EntryID != z.Current.EntryID || spec.StartSeconds != 23 {
		t.Fatalf("重启开流参数：%+v", spec)
	}
}

func TestPostgreSQLZonesOutputStreamLostThreeFailedContinuations(t *testing.T) {
	for _, source := range []string{"Open失败", "新流再次报错"} {
		t.Run(source, func(t *testing.T) {
			f, r := newOutputFixture(t, 0)
			user := f.user(t, "user")
			claim, z := outputStart(t, f, user, f.tracks[:1])
			original := z.Stream.Seq
			if source == "Open失败" {
				r.failNextOpens(3)
			}
			result := outputReport(t, f, user, claim.SessionID, original, "fault", "loading", 14)
			if result.Zone.Intent != "play" || result.Zone.Fault != nil || !r.Sessions()[0].Closed() {
				t.Fatalf("原始断开不应直接暂停：%+v", result.Zone)
			}
			if source == "Open失败" {
				if r.openAttempts() != 2 {
					t.Fatal("应立即尝试第一次续流")
				}
				f.clock.advance(time.Second)
				result.Zone = f.snapshot(t, user).Zones[0]
				if result.Zone.Intent != "play" || result.Zone.Fault != nil || r.openAttempts() != 3 {
					t.Fatalf("两次续流失败就提前暂停：%+v", result.Zone)
				}
				f.clock.advance(time.Second)
				result.Zone = f.snapshot(t, user).Zones[0]
				if r.openAttempts() != 4 {
					t.Fatalf("开流尝试数 = %d，期望原流加三次续流", r.openAttempts())
				}
			} else {
				if result.Zone.Stream == nil || result.Zone.Stream.Seq == original || result.Zone.Position.Seconds != 14 || r.Sessions()[1].Spec().StartSeconds != 14 {
					t.Fatalf("断流没有从原位置接续：%+v", result.Zone)
				}
				for failures := 1; failures <= 3; failures++ {
					seq := result.Zone.Stream.Seq
					result = outputReport(t, f, user, claim.SessionID, seq, "fault", "loading", 0)
					if failures < 3 && (result.Zone.Intent != "play" || result.Zone.Stream == nil || result.Zone.Fault != nil) {
						t.Fatalf("第 %d 次续流失败提前停止：%+v", failures, result.Zone)
					}
				}
				if r.openAttempts() != 4 {
					t.Fatalf("第三次失败后不应再开流，尝试数 = %d", r.openAttempts())
				}
			}
			if result.Zone.Intent != "pause" || result.Zone.Stream != nil || result.Zone.Fault == nil || result.Zone.Fault.Kind != "transient" {
				t.Fatalf("三次续流失败没有暂停为 transient：%+v", result.Zone)
			}
		})
	}
}

func TestPostgreSQLZonesOutputRenderEvents(t *testing.T) {
	f, r := newOutputFixture(t, 0)
	user := f.user(t, "user")
	claim, z := outputStart(t, f, user, f.tracks[:3])
	entries := f.queue(t, user, z.ZoneID).Entries
	sub := outputSubscribe(t, f, user)
	session := r.Sessions()[0]
	outputEmit(t, session, zones.Event{Kind: "track_started", EntryID: entries[1].EntryID, StreamSeconds: 180, Transition: "gapless"})
	advanced := outputZone(t, sub, func(z zones.Zone) bool { return z.Current != nil && z.Current.EntryID == entries[1].EntryID })
	if advanced.Stream == nil || advanced.Stream.Seq != z.Stream.Seq || advanced.Stream.Segments[0].EntryID != entries[1].EntryID || advanced.Stream.Segments[0].StreamStartSeconds != 180 || advanced.Event == nil || advanced.Event.Kind != "track_started" {
		t.Fatalf("自然换曲未更新分段及公告：%+v", advanced)
	}
	persisted, err := zones.LoadUser(context.Background(), f.db, user.id)
	if err != nil {
		t.Fatal(err)
	}
	q := persisted.Zones[0].Queue
	if q.CurrentEntryID != entries[1].EntryID || len(q.History) != 1 || q.History[0] != entries[0].EntryID {
		t.Fatalf("自然推进未保留历史：%+v", q)
	}
	outputEmit(t, session, zones.Event{Kind: "format_boundary", NextEntryID: entries[2].EntryID, Reason: "format"})
	boundary := outputZone(t, sub, func(z zones.Zone) bool { return z.NextStream != nil })
	if boundary.Current.EntryID != entries[1].EntryID || boundary.Stream.Seq != z.Stream.Seq || boundary.NextStream.Segments[0].EntryID != entries[2].EntryID {
		t.Fatalf("格式边界提前换曲或选错下一首：%+v", boundary)
	}
	promoted := outputReport(t, f, user, claim.SessionID, boundary.NextStream.Seq, "status", "playing", 0)
	if promoted.Zone.Current.EntryID != entries[2].EntryID || promoted.Zone.Stream.Seq != boundary.NextStream.Seq || promoted.Zone.NextStream != nil || !session.Closed() {
		t.Fatalf("接续流 playing 未推进：%+v", promoted.Zone)
	}
	next := r.Sessions()[1]
	outputEmit(t, next, zones.Event{Kind: "stream_ended", EntryID: entries[2].EntryID})
	ended := outputZone(t, sub, func(z zones.Zone) bool { return z.Stream == nil && z.Intent == "pause" })
	if ended.Position.Seconds != 0 || ended.Current.EntryID != entries[2].EntryID || !next.Closed() {
		t.Fatalf("自然播完未保留末曲并归零：%+v", ended)
	}
}

func TestPostgreSQLZonesOutputPreloadedBoundaryDoesNotSkip(t *testing.T) {
	f, r := newOutputFixture(t, 0)
	user := f.user(t, "user")
	claim, z := outputStart(t, f, user, f.tracks[:3])
	entries := f.queue(t, user, z.ZoneID).Entries
	sub := outputSubscribe(t, f, user)
	outputEmit(t, r.Sessions()[0], zones.Event{Kind: "format_boundary", NextEntryID: entries[1].EntryID, Reason: "format"})
	b := outputZone(t, sub, func(z zones.Zone) bool { return z.NextStream != nil })
	outputEmit(t, r.Sessions()[1], zones.Event{Kind: "format_boundary", NextEntryID: entries[2].EntryID, Reason: "format"})
	// 该公告证明预载 B 的边界已经应用，不能用读取快照抢在消费者之前作否定断言。
	held := outputZone(t, sub, func(z zones.Zone) bool { return z.Revision > b.Revision })
	if held.Current.EntryID != entries[0].EntryID || held.Stream.Seq != z.Stream.Seq || held.NextStream == nil || held.NextStream.Seq != b.NextStream.Seq || len(r.Sessions()) != 2 {
		t.Fatalf("B 预载宣布 C 时跳过 B：%+v", held)
	}
	next := outputReport(t, f, user, claim.SessionID, b.NextStream.Seq, "status", "playing", 0)
	if next.Zone.Current.EntryID != entries[1].EntryID || next.Zone.Stream.Seq != b.NextStream.Seq || next.Zone.NextStream == nil || next.Zone.NextStream.Segments[0].EntryID != entries[2].EntryID || len(r.Sessions()) != 3 {
		t.Fatalf("B 晋升后未为 C 开接续：%+v", next.Zone)
	}
}

func TestPostgreSQLZonesOutputEmptyStreamEndedBoundaryOrders(t *testing.T) {
	for _, endedFirst := range []bool{true, false} {
		name := "boundary后ended"
		if endedFirst {
			name = "ended后boundary"
		}
		t.Run(name, func(t *testing.T) {
			f, r := newOutputFixture(t, 1)
			user := f.user(t, "user")
			ids := append(append([]string{}, f.tracks...), f.tracks[0])
			claim, z := outputStart(t, f, user, ids)
			entries := f.queue(t, user, z.ZoneID).Entries
			if z.Stream.Format != nil {
				t.Fatalf("首曲打不开必须输出 format:null：%+v", z.Stream.Format)
			}
			sub := outputSubscribe(t, f, user)
			empty := r.Sessions()[0]
			if endedFirst {
				outputReport(t, f, user, claim.SessionID, z.Stream.Seq, "ended", "paused", 0)
			}
			outputEmit(t, empty, zones.Event{Kind: "track_failed", EntryID: entries[0].EntryID, Reason: "decode_failed"})
			outputZone(t, sub, func(z zones.Zone) bool { return len(z.Failures) == 1 })
			outputEmit(t, empty, zones.Event{Kind: "format_boundary", NextEntryID: entries[1].EntryID, Reason: "format"})
			next := outputZone(t, sub, func(z zones.Zone) bool { return z.NextStream != nil })
			if !endedFirst {
				outputReport(t, f, user, claim.SessionID, z.Stream.Seq, "ended", "paused", 0)
			}
			if empty.Closed() || next.Intent != "play" || next.Current.EntryID != entries[0].EntryID {
				t.Fatal("空流 ended 提前销毁待派发的接续")
			}
			if _, err := f.service.TakeStream(context.Background(), user.id, z.ZoneID, next.NextStream.Seq); err != nil {
				t.Fatalf("空流后接续不可取：%v", err)
			}
			playing := outputReport(t, f, user, claim.SessionID, next.NextStream.Seq, "status", "playing", 0)
			if playing.Zone.Current.EntryID != entries[1].EntryID || playing.Zone.Stream.Seq != next.NextStream.Seq || !empty.Closed() {
				t.Fatalf("空流后接续 playing 没有推进：%+v", playing.Zone)
			}
			// 换上 B 不应清掉 A 的失败累计；再失败四项正好达到五次停止。
			for i := 1; i < 5; i++ {
				outputEmit(t, r.Sessions()[1], zones.Event{Kind: "track_failed", EntryID: entries[i].EntryID, Reason: "decode_failed"})
				state := outputZone(t, sub, func(z zones.Zone) bool { return len(z.Failures) == i+1 })
				if i < 4 && state.Intent != "play" {
					t.Fatal("五次前提前停止")
				}
				if i == 4 && (state.Intent != "pause" || state.Stream != nil || state.Event == nil || state.Event.Kind != "failures_stopped") {
					t.Fatalf("空流接续重置了失败累计：%+v", state)
				}
			}
		})
	}
}
