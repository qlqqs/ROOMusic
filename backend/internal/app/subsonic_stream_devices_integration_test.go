package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func requireStreamDevice(t *testing.T, device zones.Device) {
	t.Helper()
	if device.Kind != "subsonic" || device.DeviceID == "" || device.ZoneID == "" || device.Online || device.OutputSessionID != nil || len(device.Capabilities.StreamFormats) != 0 {
		t.Fatalf("客户端设备的身份或输出能力有误：%+v", device)
	}
}

func onlyStreamDevice(t *testing.T, c zonesSampleCalls) zones.Device {
	t.Helper()
	snap := jukeboxSnapshot(c)
	if len(snap.Devices) != 1 || len(snap.Zones) != 1 || snap.Devices[0].ZoneID != snap.Zones[0].ZoneID || snap.Devices[0].DeviceID != snap.Zones[0].DeviceID {
		t.Fatalf("应有一台客户端设备和对应的播放区：%+v", snap)
	}
	requireStreamDevice(t, snap.Devices[0])
	return snap.Devices[0]
}

func waitStreamDevice(t *testing.T, events *zonesSampleEvents, id string) zones.Device {
	t.Helper()
	var payload struct{ Device zones.Device }
	events.wait(t, func(frame zonesSampleFrame) bool {
		if frame.Event != "device" {
			return false
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Device.DeviceID == id
	})
	requireStreamDevice(t, payload.Device)
	return payload.Device
}

func TestPostgreSQLSubsonicStreamDeviceLifecycle(t *testing.T) {
	f := newSubsonicStreamFixture(t)
	c := zonesSampleCalls{t: t, f: f.zonesSampleFixture}
	source := streamFixtureBytes(t, "flac-16-44100.flac")
	id := f.track(t, playbackTrackSpec{relative: "phone.flac", content: source, bitDepth: 16, sampleRate: 44100, channels: 2}, 3)
	events := f.events(t, "")
	defer events.close()
	events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })
	requireSubsonicOK(t, f.get("ping", id))
	device := onlyStreamDevice(t, c)
	first := waitStreamDevice(t, events, device.DeviceID)
	if !first.LastSeenAt.Equal(device.LastSeenAt.Time) {
		t.Fatal("登记 SSE 与快照的最近使用时间不同")
	}
	defaults := jukeboxSnapshotZone(c, device.ZoneID).Settings
	if defaults.Leveling != "off" || defaults.Quality != "lossless" || defaults.Crossfade || defaults.Gapless || defaults.Headroom.Enabled {
		t.Fatalf("新设备默认设置有误：%+v", defaults)
	}
	events.zone(t, device.ZoneID, func(z zones.Zone) bool { return z.Settings == defaults })
	changed := c.command("", device.ZoneID, map[string]any{"command": "set_settings", "settings": map[string]any{"quality": "lossy", "leveling": "album"}})
	events.zone(t, device.ZoneID, func(z zones.Zone) bool { return z.Settings.Quality == "lossy" && z.Settings.Leveling == "album" })
	if jukeboxSnapshotZone(c, device.ZoneID).Settings != changed.Zone.Settings || changed.Zone.Settings == defaults {
		t.Fatal("刷新快照后设备设置有误")
	}
	for _, cmd := range []map[string]any{{"command": "play"}, {"command": "seek", "position_seconds": 1}, {"command": "enqueue", "track_ids": []string{id}}} {
		response := c.call("", "POST", "/zones/"+device.ZoneID+"/commands", cmd, 409)
		requireAPIError(t, response, 409, "device_unsupported")
	}
	browser := c.device("")
	for _, pair := range [][2]string{{device.ZoneID, browser.Zone.ZoneID}, {browser.Zone.ZoneID, device.ZoneID}} {
		response := c.call("", "POST", "/zones/"+pair[0]+"/commands", map[string]any{"command": "transfer", "target_zone_id": pair[1]}, 409)
		requireAPIError(t, response, 409, "device_unsupported")
	}
	c.call("", "DELETE", "/devices/"+browser.Device.DeviceID, nil, 204)
	requireAPIError(t, f.request(t, "PUT", subsonicMePath, map[string]string{"jukebox_zone_id": device.ZoneID}, &f.user, nil), 404, "not_found")
	requireSubsonicOK(t, f.get("ping", id))
	download := f.get("download", id)
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), source) {
		t.Fatal("下载原文件失败")
	}
	if !onlyStreamDevice(t, c).LastSeenAt.Equal(device.LastSeenAt.Time) {
		t.Fatal("控制、ping 或 download 更新了最近使用时间")
	}
	played := f.get("stream", id, "format", "raw")
	if played.Code != 200 || !bytes.Equal(played.Body.Bytes(), source) {
		t.Fatal("客户端 raw 取流失败")
	}
	touched := waitStreamDevice(t, events, device.DeviceID)
	requireSubsonicOK(t, f.get("ping", id))
	if !touched.LastSeenAt.After(device.LastSeenAt.Time) || !touched.LastSeenAt.Equal(onlyStreamDevice(t, c).LastSeenAt.Time) {
		t.Fatalf("最近使用时间：登记 %v，取流 %v", device.LastSeenAt, touched.LastSeenAt)
	}
	c.call("", "DELETE", "/devices/"+device.DeviceID, nil, 204)
	if s := jukeboxSnapshot(c); len(s.Devices) != 0 || len(s.Zones) != 0 {
		t.Fatalf("删除后快照仍有设备或播放区：%+v", s)
	}
	requireSubsonicOK(t, f.get("ping", id))
	newDevice := onlyStreamDevice(t, c)
	if newDevice.DeviceID == device.DeviceID || newDevice.ZoneID == device.ZoneID || jukeboxSnapshotZone(c, newDevice.ZoneID).Settings != defaults {
		t.Fatal("同名客户端重新登记后未取得新设备、播放区和默认设置")
	}
}

func TestPostgreSQLSubsonicStreamClientNames(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	c := zonesSampleCalls{t: t, f: &zonesSampleFixture{integrationTestApplication: a, user: admin}}
	other := createSubsonicMember(t, a, &admin, "subsonic-name-listener")
	for i, pair := range [][2]string{
		{strings.Repeat("音", 65), strings.Repeat("音", 64)},
		{"  音\x00乐\u0085播放器\n ", "音乐播放器"},
		{"合成\xff设备", "合成\uFFFD设备"},
		{" \t\u3000 ", "Subsonic 客户端"},
	} {
		var first string
		for _, raw := range pair {
			p := subsonicParameters(subsonicAdmin, subsonicPassword, "token")
			p.Set("c", raw)
			requireSubsonicOK(t, requestSubsonic(a, "GET", "ping", p))
			s := jukeboxSnapshot(c)
			if len(s.Devices) != i+1 || len(s.Zones) != i+1 {
				t.Fatalf("登记 %q 后：设备数 %d，播放区数 %d", raw, len(s.Devices), len(s.Zones))
			}
			var id string
			for _, d := range s.Devices {
				if d.Name == pair[1] {
					requireStreamDevice(t, d)
					id = d.DeviceID
				}
			}
			if id == "" || (first != "" && first != id) {
				t.Fatalf("%q 与规范名 %q 未命中相同设备", raw, pair[1])
			}
			first = id
		}
	}
	p := subsonicParameters("subsonic-name-listener", subsonicPassword, "token")
	p.Set("c", "音乐播放器")
	requireSubsonicOK(t, requestSubsonic(a, "GET", "ping", p))
	if countRows(t, a, "SELECT count(*) FROM playback_devices WHERE name=$1 AND user_id=$2", "音乐播放器", other) != 1 || len(jukeboxSnapshot(c).Devices) != 4 {
		t.Fatal("成员间同名设备未独立登记或暴露在其他成员快照中")
	}
}

func TestPostgreSQLSubsonicStreamScrobble(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	const viewer = "stream-history-viewer"
	createSubsonicMember(t, f.app, &f.admin, viewer)
	member := loginPasswordUser(t, f.app, viewer, subsonicPassword, "user")
	id := f.tracks["Overture"]
	const duration = 123.5
	mustExec(t, f.app, "UPDATE tracks SET duration_seconds=$1 WHERE id=$2", duration, id)
	putSubsonicBrowseRating(t, f, &member, "tracks", id, 4)
	call := func(user, method string, fields ...string) *httptest.ResponseRecorder {
		params := f.params("json", fields...)
		params.Set("u", user)
		params.Set("c", "合成客户端")
		return requestSubsonic(f.app, http.MethodGet, method, params)
	}
	now := func(user string) []protocol.NowPlayingEntry {
		return subsonicBrowseJSON[protocol.NowPlaying](t, call(user, "getNowPlaying"), "nowPlaying").Entries
	}
	nowAt := time.Now().UTC().Truncate(time.Millisecond)
	// 两名成员的上报时间落在过期边界两侧，无需等待十分钟。
	for _, x := range []struct {
		user string
		age  time.Duration
	}{{viewer, 10*time.Minute + time.Second}, {subsonicAdmin, 9 * time.Minute}} {
		requireSubsonicOK(t, call(x.user, "scrobble", "id", id, "submission", "false", "time", strconv.FormatInt(nowAt.Add(-x.age).UnixMilli(), 10)))
	}
	for _, actor := range []string{viewer, subsonicAdmin} {
		entries := now(actor)
		if len(entries) != 1 || entries[0].ID != id || entries[0].Username != subsonicAdmin || entries[0].PlayerName != "合成客户端" || entries[0].MinutesAgo != 9 {
			t.Fatalf("%s 查到的正在播放为 %+v", actor, entries)
		}
		wantRating := 0
		if actor == viewer {
			wantRating = 4
		}
		requireSubsonicEntityRating(t, id, entries[0].UserRating, wantRating)
	}
	if countRows(t, f.app, "SELECT count(*) FROM play_history") != 0 {
		t.Fatal("submission=false 产生了历史记录")
	}
	at := strconv.FormatInt(nowAt.UnixMilli(), 10)
	responses := make(chan *httptest.ResponseRecorder, 8)
	start := make(chan struct{})
	for range cap(responses) {
		go func() { <-start; responses <- call(subsonicAdmin, "scrobble", "id", id, "time", at) }()
	}
	close(start)
	for range cap(responses) {
		requireSubsonicOK(t, <-responses)
	}
	requireSubsonicOK(t, call(subsonicAdmin, "scrobble", "id", id, "submission", "true", "time", at))
	var playedAt time.Time
	var seconds float64
	if err := f.app.database.QueryRow("SELECT played_at,played_seconds FROM play_history WHERE user_id=$1 AND track_id=$2", f.admin.id, id).Scan(&playedAt, &seconds); err != nil {
		t.Fatal(err)
	}
	if !playedAt.Equal(nowAt) || seconds != duration || countRows(t, f.app, "SELECT count(*) FROM play_history") != 1 {
		t.Fatalf("重复或并发提交后的记录不符：%v、%v 秒", playedAt, seconds)
	}
}
