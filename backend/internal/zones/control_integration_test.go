package zones_test

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func TestPostgreSQLZonesControlCommands(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	// transfer 与 import 在各自用例里覆盖；其余命令各取一条真正改变状态的成功路径。
	for _, tc := range []struct {
		name    string
		order   []int
		current int
		intent  string
		opens   int
	}{
		{"play_tracks", []int{3}, 3, "play", 1},
		{"play_next", []int{0, 1, 3, 2}, 1, "play", 0},
		{"enqueue", []int{0, 1, 2, 3}, 1, "play", 0},
		{"play_entry", []int{0, 1, 2}, 0, "play", 1},
		{"remove_entry", []int{0, 2}, 2, "play", 1},
		{"move_entry", []int{0, 2, 1}, 1, "play", 0},
		{"clear_queue", []int{}, -1, "pause", 0},
		{"set_mode", []int{0, 1, 2}, 1, "play", 0},
		{"play", []int{0, 1, 2}, 1, "play", 0},
		{"pause", []int{0, 1, 2}, 1, "pause", 0},
		{"stop", []int{0, 1, 2}, 1, "pause", 0},
		{"next", []int{0, 1, 2}, 2, "play", 1},
		{"previous", []int{0, 1, 2}, 0, "play", 1},
		{"seek", []int{0, 1, 2}, 1, "play", 0},
		{"set_settings", []int{0, 1, 2}, 1, "play", 0},
		{"retry", []int{0, 1, 2}, 1, "play", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := f.device(t, user, true)
			id := device.Zone.ZoneID
			if tc.name == "retry" {
				f.renderer.failNext = true
			}
			initial := f.command(t, user, id, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:3], "start_index": 1})
			if tc.name == "retry" && (initial.Zone.Fault == nil || initial.Zone.Fault.Kind != "transient" || initial.Zone.Intent != "pause") {
				t.Fatalf("retry 前置条件未形成 transient 故障：%+v", initial.Zone)
			}
			entries := f.queue(t, user, id).Entries
			if tc.name == "play" {
				f.command(t, user, id, map[string]any{"command": "pause"})
			}
			if tc.name == "stop" {
				f.command(t, user, id, map[string]any{"command": "seek", "position_seconds": 42})
			}
			body := map[string]any{"command": tc.name}
			switch tc.name {
			case "play_tracks":
				body["track_ids"], body["start_index"] = f.tracks[3:], 0
			case "play_next", "enqueue":
				body["track_ids"] = f.tracks[3:]
			case "play_entry":
				body["entry_id"] = entries[0].EntryID
			case "remove_entry":
				body["entry_id"] = entries[1].EntryID
			case "move_entry":
				body["entry_id"], body["delta"] = entries[2].EntryID, -1
			case "set_mode":
				body["mode"] = "repeat_all"
			case "seek":
				body["position_seconds"] = 42
			case "set_settings":
				body["settings"] = map[string]any{"leveling": "track", "crossfade": true}
			}
			before := f.renderer.Sessions()
			result := f.command(t, user, id, body)
			gotQueue := f.queue(t, user, id)
			wantTracks := make([]string, len(tc.order))
			for i, index := range tc.order {
				wantTracks[i] = f.tracks[index]
			}
			gotTracks := make([]string, len(gotQueue.Entries))
			for i, entry := range gotQueue.Entries {
				gotTracks[i] = entry.Track.TrackID
			}
			if !slices.Equal(gotTracks, wantTracks) || result.Zone.QueueLength != len(tc.order) {
				t.Fatalf("命令后的队列 = %v，期望 %v；长度 = %d", gotTracks, wantTracks, result.Zone.QueueLength)
			}
			if tc.current < 0 {
				if result.Zone.Current != nil {
					t.Fatalf("清空后仍有当前项：%+v", result.Zone.Current)
				}
			} else if result.Zone.Current == nil || result.Zone.Current.Track.TrackID != f.tracks[tc.current] {
				t.Fatalf("当前曲目不符合命令：%+v", result.Zone.Current)
			}
			if result.Zone.Intent != tc.intent || result.Zone.Fault != nil {
				t.Fatalf("意图或故障错误：%+v", result.Zone)
			}
			sessions := f.renderer.Sessions()
			if len(sessions)-len(before) != tc.opens {
				t.Fatalf("新增会话 = %d，期望 %d", len(sessions)-len(before), tc.opens)
			}
			if tc.opens == 1 {
				latest := sessions[len(sessions)-1]
				if result.Zone.Stream == nil || latest.Closed() || latest.Spec().Start.TrackID != f.tracks[tc.current] || latest.Spec().ZoneID != id {
					t.Fatalf("命令未在目标曲目开流：%+v", latest.Spec())
				}
			}
			switch tc.name {
			case "clear_queue", "stop":
				if result.Zone.Stream != nil || result.Zone.Position.Seconds != 0 || !before[len(before)-1].Closed() {
					t.Fatal("清空/停止未归零并关闭原会话")
				}
			case "pause", "play":
				if result.Zone.Stream == nil || before[len(before)-1].Closed() {
					t.Fatal("立即暂停/恢复不应关闭会话")
				}
			case "set_mode":
				if result.Zone.Mode != queue.ModeRepeatAll {
					t.Fatalf("模式 = %q", result.Zone.Mode)
				}
			case "seek":
				if result.Zone.Position.Seconds != 42 {
					t.Fatalf("定位 = %v", result.Zone.Position.Seconds)
				}
			case "set_settings":
				if result.Zone.Settings.Leveling != "track" || !result.Zone.Settings.Crossfade {
					t.Fatalf("设置未生效：%+v", result.Zone.Settings)
				}
			}
		})
	}
}

func TestPostgreSQLZonesControlErrors(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	for _, code := range []string{"queue_full", "track_not_found", "zone_offline", "device_online", "volume_fixed"} {
		t.Run(code, func(t *testing.T) {
			device := f.device(t, user, code == "device_online")
			id := device.Zone.ZoneID
			method, path := "POST", "/zones/"+id+"/commands"
			body := map[string]any{"command": "enqueue", "track_ids": f.tracks[:1]}
			status := http.StatusConflict
			switch code {
			case "queue_full":
				ids := make([]string, 10000)
				for i := range ids {
					ids[i] = f.tracks[0]
				}
				f.command(t, user, id, map[string]any{"command": "enqueue", "track_ids": ids})
			case "track_not_found":
				body["track_ids"] = []string{f.tracks[0], uuid.New()}
				status = http.StatusNotFound
			case "zone_offline":
				body["command"], body["start_index"] = "play_tracks", 0
			case "device_online":
				method, path, body = "DELETE", "/devices/"+device.Device.DeviceID, nil
			case "volume_fixed":
				caps := zones.Capabilities{StreamFormats: []zones.StreamFormatCapability{}, MaxChannels: 2, DSD: "none", Volume: "fixed"}
				f.output(t, user, device.Device.DeviceID, false, &caps)
				method, path, body = "PUT", "/devices/"+device.Device.DeviceID+"/volume", map[string]any{"volume": 0.8, "muted": true}
			}
			before := f.queue(t, user, id)
			controlError(t, f.request(t, user, method, path, body, controlOrigin), status, code)
			if after := f.queue(t, user, id); !reflect.DeepEqual(after, before) {
				t.Fatal("拒绝的命令改变了队列")
			}
		})
	}
}

func TestPostgreSQLZonesControlTransfer(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	source, target := f.device(t, user, true), f.device(t, user, true)
	sourceID, targetID := source.Zone.ZoneID, target.Zone.ZoneID
	f.command(t, user, sourceID, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:3], "start_index": 1})
	f.command(t, user, sourceID, map[string]any{"command": "seek", "position_seconds": 37.5})
	f.command(t, user, sourceID, map[string]any{"command": "pause"})
	sourceQueue := f.queue(t, user, sourceID)
	old := f.renderer.Sessions()[0]
	result := f.command(t, user, sourceID, map[string]any{"command": "transfer", "target_zone_id": targetID})
	if result.Zone.QueueLength != 0 || result.Zone.Current != nil || result.Zone.Stream != nil || result.Zone.Intent != "pause" || !old.Closed() {
		t.Fatalf("源区未清空并关闭会话：%+v", result.Zone)
	}
	if got := f.queue(t, user, sourceID); len(got.Entries) != 0 {
		t.Fatal("源队列未清空")
	}
	if got := f.queue(t, user, targetID); !reflect.DeepEqual(got.Entries, sourceQueue.Entries) {
		t.Fatal("目标未得到原队列与条目身份")
	}
	z := result.TargetZone
	if z == nil || z.Current == nil || z.Current.EntryID != sourceQueue.Entries[1].EntryID || z.Position.Seconds != 37.5 || z.Intent != "play" || z.Stream == nil {
		t.Fatalf("目标未在原当前位置起播：%+v", z)
	}
	sessions := f.renderer.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("转移后会话数 = %d，期望 2", len(sessions))
	}
	spec := sessions[1].Spec()
	if sessions[1].Closed() || spec.ZoneID != targetID || spec.Start.EntryID != sourceQueue.Entries[1].EntryID || spec.StartSeconds != 37.5 {
		t.Fatalf("转移开流参数错误：%+v", spec)
	}
	persisted, err := zones.LoadUser(context.Background(), f.db, user.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range persisted.Zones {
		switch zone.ZoneID {
		case sourceID:
			if len(zone.Queue.Entries) != 0 || zone.Queue.CurrentEntryID != "" {
				t.Fatal("源清空未持久化")
			}
		case targetID:
			if len(zone.Queue.Entries) != 3 || zone.Queue.CurrentEntryID != spec.Start.EntryID || zone.PositionSeconds != 37.5 {
				t.Fatal("目标队列/位置未持久化")
			}
		}
	}
}

func TestPostgreSQLZonesControlImport(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	id := f.device(t, user, false).Zone.ZoneID
	body := map[string]any{
		"command": "import", "entries": []zones.ImportEntry{
			{EntryID: "before", TrackID: f.tracks[0]}, {EntryID: "missing-current", TrackID: uuid.New()}, {EntryID: "successor", TrackID: f.tracks[1]},
		},
		"current_entry_id": "missing-current", "mode": "repeat_all", "position_seconds": 67, "history": []string{"before", "missing-current"},
	}
	result := f.command(t, user, id, body)
	entries := f.queue(t, user, id).Entries
	if result.Imported == nil || !*result.Imported || len(entries) != 2 || entries[0].Track.TrackID != f.tracks[0] || entries[1].Track.TrackID != f.tracks[1] {
		t.Fatalf("导入未丢弃不存在的曲目：%+v", result)
	}
	if result.Zone.Current == nil || result.Zone.Current.EntryID != entries[1].EntryID || result.Zone.Position.Seconds != 0 || result.Zone.Intent != "pause" {
		t.Fatalf("丢失当前项后未取后继并归零：%+v", result.Zone)
	}
	if !uuid.Valid(entries[0].EntryID) || !uuid.Valid(entries[1].EntryID) {
		t.Fatal("旧字符串 ID 未映射为 UUID")
	}
	repeated := f.command(t, user, id, body)
	if repeated.Imported == nil || *repeated.Imported || repeated.QueueChange != nil || !reflect.DeepEqual(repeated.Zone, result.Zone) {
		t.Fatalf("重复导入不是无操作：%+v", repeated)
	}
	// 清空后再导入，验证一次性来自持久标记，而不只是“当前队列非空”。
	f.command(t, user, id, map[string]any{"command": "clear_queue"})
	repeated = f.command(t, user, id, body)
	if repeated.Imported == nil || *repeated.Imported || repeated.Zone.QueueLength != 0 {
		t.Fatal("清空队列后错误地允许再次导入")
	}
}

func TestPostgreSQLZonesControlDebounce(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	id := f.device(t, user, true).Zone.ZoneID
	f.command(t, user, id, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:1], "start_index": 0})
	original := f.renderer.Sessions()[0]
	for _, body := range []map[string]any{
		{"command": "seek", "position_seconds": 10},
		{"command": "set_settings", "settings": map[string]any{"leveling": "track"}},
		{"command": "seek", "position_seconds": 40},
		{"command": "set_settings", "settings": map[string]any{"leveling": "album", "gapless": false}},
	} {
		f.command(t, user, id, body)
		f.clock.advance(50 * time.Millisecond)
	}
	// 最后一次命令在 150 ms；449 ms 时尚未到最后一次命令的 300 ms 期限。
	f.clock.advance(249 * time.Millisecond)
	if len(f.renderer.Sessions()) != 1 || original.Closed() {
		t.Fatal("去抖期限前提前重建了流")
	}
	f.clock.advance(time.Millisecond)
	sessions := f.renderer.Sessions()
	if len(sessions) != 2 || !original.Closed() || sessions[1].Closed() {
		t.Fatalf("多次定位/设置后应只重建一次，累计会话 = %d", len(sessions))
	}
	spec := sessions[1].Spec()
	if spec.StartSeconds != 40 || spec.Settings.Leveling != "album" || spec.Settings.Gapless {
		t.Fatalf("重建没有使用最后的位置与设置：%+v", spec)
	}
	f.clock.advance(300 * time.Millisecond)
	if len(f.renderer.Sessions()) != 2 {
		t.Fatal("残留定时器又开了流")
	}
}

func TestPostgreSQLZonesControlIsolation(t *testing.T) {
	f := newControlFixture(t)
	users := []controlUser{f.user(t, "user"), f.user(t, "user"), f.user(t, "admin")}
	devices := make([]zones.RegisterDeviceResult, len(users))
	for i, user := range users {
		devices[i] = f.device(t, user, true)
	}
	for i, actor := range users {
		snapshot := f.snapshot(t, actor)
		if len(snapshot.Devices) != 1 || len(snapshot.Zones) != 1 || snapshot.Devices[0].DeviceID != devices[i].Device.DeviceID || snapshot.Zones[0].ZoneID != devices[i].Zone.ZoneID {
			t.Fatal("快照泄漏其他用户设备或播放区")
		}
		for j, target := range devices {
			if i == j {
				continue
			}
			for _, request := range []struct {
				method, path string
				body         any
			}{
				{"GET", "/zones/" + target.Zone.ZoneID + "/queue", nil},
				{"POST", "/zones/" + target.Zone.ZoneID + "/commands", map[string]any{"command": "pause"}},
				{"POST", "/zones/" + devices[i].Zone.ZoneID + "/commands", map[string]any{"command": "transfer", "target_zone_id": target.Zone.ZoneID}},
				{"PATCH", "/devices/" + target.Device.DeviceID, map[string]any{"name": "越权改名"}},
				{"PUT", "/devices/" + target.Device.DeviceID + "/volume", map[string]any{"volume": 0.8, "muted": true}},
				{"DELETE", "/devices/" + target.Device.DeviceID, nil},
			} {
				controlError(t, f.request(t, actor, request.method, request.path, request.body, controlOrigin), http.StatusNotFound, "not_found")
			}
		}
	}
}

func TestPostgreSQLZonesControlOriginBeforeAuthentication(t *testing.T) {
	f := newControlFixture(t)
	id := uuid.New()
	for _, route := range []struct{ method, path string }{
		{"POST", "/devices"}, {"PATCH", "/devices/" + id}, {"DELETE", "/devices/" + id},
		{"PUT", "/devices/" + id + "/volume"}, {"POST", "/zones/" + id + "/commands"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			for _, origin := range []struct {
				value  string
				status int
				code   string
			}{
				{"", 403, "origin_required"}, {"http://foreign.test", 403, "origin_forbidden"}, {controlOrigin, 401, "unauthorized"},
			} {
				controlError(t, f.request(t, controlUser{}, route.method, route.path, map[string]any{}, origin.value), origin.status, origin.code)
			}
		})
	}
}

func TestPostgreSQLZonesControlCleanup(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	// 从 31 天前开始登记，在线设备也保持旧 last_seen_at，避免只测 SQL 期限过滤。
	cleanupAt := f.clock.current().Add(time.Hour)
	f.clock.set(cleanupAt.Add(-31 * 24 * time.Hour))
	oldOffline := f.device(t, user, false)
	oldOnline := f.device(t, user, true)
	f.clock.set(cleanupAt.Add(-30 * 24 * time.Hour))
	boundary := f.device(t, user, false)
	f.clock.set(cleanupAt.Add(-24 * time.Hour))
	recent := f.device(t, user, false)
	f.clock.set(cleanupAt.Add(-time.Hour))
	// 推进至注册于服务启动时的一小时清理回调；不直接调用 Cleanup 代替调度。
	f.clock.advance(time.Hour)
	snapshot := f.snapshot(t, user)
	got := make([]string, 0, len(snapshot.Devices))
	for _, device := range snapshot.Devices {
		got = append(got, device.DeviceID)
	}
	want := []string{oldOnline.Device.DeviceID, boundary.Device.DeviceID, recent.Device.DeviceID}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("应只删除超期且离线的设备，剩余 = %v，期望 %v", got, want)
	}
	controlError(t, f.request(t, user, "GET", "/zones/"+oldOffline.Zone.ZoneID+"/queue", nil, ""), 404, "not_found")
	persisted, err := zones.LoadUser(context.Background(), f.db, user.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Devices) != 3 || len(persisted.Zones) != 3 {
		t.Fatalf("设备清理未持久化：%+v", persisted)
	}
}
