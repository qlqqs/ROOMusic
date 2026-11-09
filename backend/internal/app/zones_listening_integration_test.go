package app

import (
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestPostgreSQLZonesPlayHistoryOnce(t *testing.T) {
	f := newZonesSampleFixture(t)
	c := zonesSampleCalls{t: t, f: f}
	device := c.device("")
	output := c.claim("", device.Device.DeviceID, false, nil)
	events := f.events(t, output.SessionID)
	events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })
	result := c.command("", device.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:1], "start_index": 0})
	c.report("", output.SessionID, "status", "playing", &result.Zone.Stream.Seq, 0, nil)
	session := f.renderer.Sessions()[0]
	f.clock.Advance(11 * time.Second)
	f.emit(t, session, zones.Event{Kind: "time", EntryID: result.Zone.Current.EntryID, Seconds: 11, At: f.clock.Now()})
	events.zone(t, device.Zone.ZoneID, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "play_recorded" })
	assertOne := func() {
		t.Helper()
		var count int
		var seconds float64
		if err := f.database.QueryRow(`SELECT count(*),COALESCE(sum(played_seconds),0) FROM play_history WHERE user_id=$1 AND track_id=$2`, f.user.id, f.tracks[0]).Scan(&count, &seconds); err != nil {
			t.Fatal(err)
		}
		if count != 1 || seconds != 11 {
			t.Fatalf("过半访问的历史：行数=%d 秒数=%v，期望 1/11", count, seconds)
		}
	}
	assertOne()
	f.clock.Advance(time.Second)
	c.report("", output.SessionID, "status", "playing", &result.Zone.Stream.Seq, 12, nil)
	f.emit(t, session, zones.Event{Kind: "time", EntryID: result.Zone.Current.EntryID, Seconds: 12, At: f.clock.Now()})
	// 同一会话的 signal 是前面 time 已应用的顺序屏障，不用 sleep 或轮询数据库。
	f.emit(t, session, zones.Event{Kind: "signal", Signal: map[string]any{"test_barrier": true}})
	events.zone(t, device.Zone.ZoneID, func(z zones.Zone) bool { return z.Signal != nil })
	assertOne()
}
