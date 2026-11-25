package app

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
	subsonicauth "github.com/qlqq/roomusic/backend/internal/subsonic/auth"
	subsonicapi "github.com/qlqq/roomusic/backend/internal/subsonic/httpapi"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

const jukeboxMember = "subsonic-jukebox-listener"

func newJukeboxPlaybackFixture(t *testing.T) *zonesSampleFixture {
	t.Helper()
	f := newZonesSampleFixture(t)
	hash, err := identity.HashPassword(subsonicPassword)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE users SET username=$1,password_hash=$2 WHERE id=$3", jukeboxMember, hash, f.user.id)
	f.exec(t, "UPDATE tracks SET duration_seconds=120")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := subsonicauth.New(f.database, secret.New(filepath.Join(t.TempDir(), "instance.key")), identity.NewLoginThrottle(f.clock.Now), logger)
	subsonicapi.Register(f.mux, service, identity.NewGuard(f.database, false, f.origin), buildinfo.Current().Version, subsonicapi.Browse{
		Catalog: catalog.NewStore(f.database), Personal: personal.NewService(f.database), Zones: f.service,
	})
	return f
}

func jukeboxRequest(f *zonesSampleFixture, user, format, action string, options url.Values) *httptest.ResponseRecorder {
	params := subsonicParameters(user, subsonicPassword, "plain")
	params.Set("action", action)
	params.Set("f", format)
	for key, values := range options {
		params[key] = values
	}
	return requestSubsonic(f.integrationTestApplication, http.MethodGet, "jukeboxControl.view", params)
}

func selectJukeboxZone(t *testing.T, f *zonesSampleFixture, zoneID string) {
	t.Helper()
	response := f.request(t, http.MethodPut, subsonicMePath, map[string]string{"jukebox_zone_id": zoneID}, &f.user, nil)
	requireSubsonicWebJSON(t, response, map[string]any{
		"enabled": true, "ready": false, "username": jukeboxMember, "jukebox_zone_id": zoneID,
	})
}

func jukeboxSnapshot(c zonesSampleCalls) zones.Snapshot {
	c.t.Helper()
	return zonesSampleDecode[zones.Snapshot](c.t, c.call("", http.MethodGet, "", nil, 200), 200)
}

func jukeboxSnapshotZone(c zonesSampleCalls, id string) zones.Zone {
	c.t.Helper()
	for _, zone := range jukeboxSnapshot(c).Zones {
		if zone.ZoneID == id {
			return zone
		}
	}
	c.t.Fatalf("网页快照缺少播放区 %s", id)
	return zones.Zone{}
}

func jukeboxQueue(c zonesSampleCalls, zoneID string) zones.QueueResult {
	c.t.Helper()
	return zonesSampleDecode[zones.QueueResult](c.t, c.call("", http.MethodGet, "/zones/"+zoneID+"/queue", nil, 200), 200)
}

func requireJukeboxStatus(t *testing.T, body map[string]any, index int, playing bool, gain float64, position int) {
	t.Helper()
	got, ok := body["jukeboxStatus"].(map[string]any)
	if !ok || len(got) != 4 || got["currentIndex"] != float64(index) || got["playing"] != playing || got["gain"] != gain || got["position"] != float64(position) {
		t.Fatalf("jukeboxStatus = %v，期望 index=%d playing=%t gain=%v position=%d", got, index, playing, gain, position)
	}
	if _, exists := body["jukeboxPlaylist"]; exists {
		t.Fatal("状态响应包含队列")
	}
}

func requireJukeboxQueue(t *testing.T, c zonesSampleCalls, zoneID string, want []string) zones.QueueResult {
	t.Helper()
	web := jukeboxQueue(c, zoneID)
	webIDs := make([]string, 0, len(web.Entries))
	for _, entry := range web.Entries {
		webIDs = append(webIDs, entry.Track.TrackID)
	}
	if web.ZoneID != zoneID || !slices.Equal(webIDs, want) {
		t.Fatalf("网页队列 %s %v，期望 %s %v", web.ZoneID, webIDs, zoneID, want)
	}
	body := requireSubsonicOK(t, jukeboxRequest(c.f, jukeboxMember, "json", "get", nil))
	playlist, _ := body["jukeboxPlaylist"].(map[string]any)
	entries, ok := playlist["entry"].([]any)
	if !ok || len(entries) != len(want) {
		t.Fatalf("点唱机队列条目为 %v，期望 %v", playlist["entry"], want)
	}
	for index, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry["id"] != webIDs[index] || entry["title"] != web.Entries[index].Track.Title {
			t.Fatalf("点唱机队列第 %d 项为 %v，网页条目为 %+v", index, entry, web.Entries[index])
		}
	}
	return web
}

type jukeboxQueueEvent struct {
	ZoneID string            `json:"zone_id"`
	Change zones.QueueChange `json:"change"`
}

func requireJukeboxQueueEvent(t *testing.T, stream *zonesSampleEvents, zoneID, kind string, revision int64) {
	t.Helper()
	var event jukeboxQueueEvent
	stream.wait(t, func(frame zonesSampleFrame) bool {
		if frame.Event != "queue" {
			return false
		}
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			t.Fatal(err)
		}
		return event.ZoneID == zoneID
	})
	if event.Change.Kind != kind || event.Change.QueueRevision != revision {
		t.Fatalf("队列事件为 %+v，期望 kind=%s revision=%d", event, kind, revision)
	}
}

func requireJukeboxVolumeEvent(t *testing.T, stream *zonesSampleEvents, deviceID string, volume float64) {
	t.Helper()
	var event struct{ Device zones.Device }
	stream.wait(t, func(frame zonesSampleFrame) bool {
		if frame.Event != "device" {
			return false
		}
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			t.Fatal(err)
		}
		return event.Device.DeviceID == deviceID
	})
	if event.Device.Volume != volume {
		t.Fatalf("设备 %s 的 SSE 音量为 %v，期望 %v", deviceID, event.Device.Volume, volume)
	}
}

func TestPostgreSQLSubsonicJukeboxPlaybackAndQueue(t *testing.T) {
	f := newJukeboxPlaybackFixture(t)
	c := zonesSampleCalls{t: t, f: f}
	device := registerJukeboxDevice(t, f.integrationTestApplication, &f.user)
	id := device.Zone.ZoneID
	output := c.claim("", device.Device.DeviceID, false, nil)
	events := f.events(t, output.SessionID)
	events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })
	selectJukeboxZone(t, f, id)
	call := func(action string, params url.Values) map[string]any {
		t.Helper()
		return requireSubsonicOK(t, jukeboxRequest(f, jukeboxMember, "json", action, params))
	}

	// 先从网页加入一项，验证 set 以一个队列版本替换旧队列。
	old := c.command("", id, map[string]any{"command": "enqueue", "track_ids": f.tracks[4:]})
	requireJukeboxQueueEvent(t, events, id, "insert", old.Zone.QueueRevision)
	requireJukeboxStatus(t, call("set", url.Values{"id": f.tracks[:3]}), 0, false, 0.75, 0)
	web := requireJukeboxQueue(t, c, id, f.tracks[:3])
	if web.QueueRevision != old.Zone.QueueRevision+1 {
		t.Fatalf("替换队列版本 = %d，替换前 = %d", web.QueueRevision, old.Zone.QueueRevision)
	}
	requireJukeboxQueueEvent(t, events, id, "replace", web.QueueRevision)

	requireJukeboxStatus(t, call("start", nil), 0, true, 0.75, 0)
	requireJukeboxRenderStart(t, f, id, f.tracks[0], 0)
	started := jukeboxSnapshotZone(c, id)
	if started.Stream == nil {
		t.Fatalf("起播后网页快照缺少输出流：%+v", started)
	}
	c.report("", output.SessionID, "status", "playing", &started.Stream.Seq, 0, nil)
	events.zone(t, id, func(z zones.Zone) bool {
		return z.Status == "playing" && z.Current != nil && z.Current.EntryID == web.Entries[0].EntryID
	})
	started = jukeboxSnapshotZone(c, id)
	if started.Status != "playing" || started.Current == nil || started.Current.EntryID != web.Entries[0].EntryID {
		t.Fatalf("起播后的网页快照为 %+v", started)
	}

	requireJukeboxStatus(t, call("skip", url.Values{"index": {"2"}, "offset": {"30"}}), 2, true, 0.75, 30)
	requireJukeboxRenderStart(t, f, id, f.tracks[2], 30)
	skipped := jukeboxSnapshotZone(c, id)
	if skipped.Stream == nil {
		t.Fatalf("跳曲后网页快照缺少输出流：%+v", skipped)
	}
	c.report("", output.SessionID, "status", "playing", &skipped.Stream.Seq, 0, nil)
	events.zone(t, id, func(z zones.Zone) bool {
		return z.Status == "playing" && z.Current != nil && z.Current.EntryID == web.Entries[2].EntryID && z.Position.Seconds == 30
	})
	requireJukeboxStatus(t, call("status", nil), 2, true, 0.75, 30)

	requireJukeboxStatus(t, call("setGain", url.Values{"gain": {"0.5"}}), 2, true, 0.5, 30)
	requireJukeboxVolumeEvent(t, events, device.Device.DeviceID, 0.5)
	requireJukeboxDeviceVolume(t, c, device.Device.DeviceID, 0.5)
	requireJukeboxStatus(t, call("stop", nil), 2, false, 0.5, 30)
	events.zone(t, id, func(z zones.Zone) bool { return z.Intent == "pause" && z.Status == "paused" })

	call("remove", url.Values{"index": {"1"}})
	web = requireJukeboxQueue(t, c, id, []string{f.tracks[0], f.tracks[2]})
	requireJukeboxQueueEvent(t, events, id, "remove", web.QueueRevision)

	call("add", url.Values{"id": {f.tracks[0], f.tracks[3]}})
	beforeShuffle := requireJukeboxQueue(t, c, id, []string{f.tracks[0], f.tracks[2], f.tracks[0], f.tracks[3]})
	requireJukeboxQueueEvent(t, events, id, "insert", beforeShuffle.QueueRevision)

	// 固定随机源使顺序变为 [1,2,3,0]，同时检查重号歌曲的条目身份。
	requireJukeboxStatus(t, call("shuffle", nil), 0, false, 0.5, 30)
	shuffledIDs := []string{f.tracks[2], f.tracks[0], f.tracks[3], f.tracks[0]}
	web = requireJukeboxQueue(t, c, id, shuffledIDs)
	requireJukeboxQueueEvent(t, events, id, "replace", web.QueueRevision)
	for i, entry := range web.Entries {
		if entry.EntryID != beforeShuffle.Entries[(i+1)%len(web.Entries)].EntryID {
			t.Fatalf("打乱后条目 %d 的身份变更：%+v", i, entry)
		}
	}
	requireJukeboxXMLQueue(t, f, shuffledIDs)

	// 网页写入也应立即体现在点唱机队列。
	inserted := c.command("", id, map[string]any{"command": "enqueue", "track_ids": f.tracks[4:]})
	requireJukeboxQueueEvent(t, events, id, "insert", inserted.Zone.QueueRevision)
	requireJukeboxQueue(t, c, id, append(shuffledIDs, f.tracks[4]))
	requireJukeboxStatus(t, call("clear", nil), -1, false, 0.5, 0)
	web = requireJukeboxQueue(t, c, id, nil)
	requireJukeboxQueueEvent(t, events, id, "clear", web.QueueRevision)
	requireJukeboxXMLQueue(t, f, nil)
}

func requireJukeboxRenderStart(t *testing.T, f *zonesSampleFixture, zoneID, trackID string, seconds float64) {
	t.Helper()
	sessions := f.renderer.Sessions()
	if len(sessions) == 0 {
		t.Fatal("替身没有收到渲染会话")
	}
	spec := sessions[len(sessions)-1].Spec()
	if spec.ZoneID != zoneID || spec.Start.TrackID != trackID || spec.StartSeconds != seconds {
		t.Fatalf("替身起播请求为 %+v，期望播放区 %s、曲目 %s、位置 %v", spec, zoneID, trackID, seconds)
	}
}

func requireJukeboxDeviceVolume(t *testing.T, c zonesSampleCalls, id string, volume float64) {
	t.Helper()
	for _, device := range jukeboxSnapshot(c).Devices {
		if device.DeviceID == id {
			if device.Volume != volume {
				t.Fatalf("设备 %s 的网页快照音量为 %v，期望 %v", id, device.Volume, volume)
			}
			return
		}
	}
	t.Fatalf("网页快照缺少设备 %s", id)
}

func requireJukeboxXMLQueue(t *testing.T, f *zonesSampleFixture, want []string) {
	t.Helper()
	response := jukeboxRequest(f, jukeboxMember, "xml", "get", nil)
	requireSubsonicHTTP(t, response, http.StatusOK, "application/xml; charset=utf-8")
	var root struct {
		XMLName  xml.Name `xml:"http://subsonic.org/restapi subsonic-response"`
		Status   string   `xml:"status,attr"`
		Playlist *struct {
			Entries []struct {
				ID string `xml:"id,attr"`
			} `xml:"entry"`
		} `xml:"jukeboxPlaylist"`
	}
	if err := xml.Unmarshal(response.Body.Bytes(), &root); err != nil || root.Status != "ok" || root.Playlist == nil {
		t.Fatalf("XML 点唱机队列无法读取：%v，%s", err, response.Body)
	}
	ids := make([]string, 0, len(root.Playlist.Entries))
	for _, entry := range root.Playlist.Entries {
		ids = append(ids, entry.ID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("XML 点唱机队列为 %v，期望 %v", ids, want)
	}
}

func TestPostgreSQLSubsonicJukeboxGroupGain(t *testing.T) {
	f := newJukeboxPlaybackFixture(t)
	c := zonesSampleCalls{t: t, f: f}
	volumes := []float64{0.75, 0.375}
	outputs := &zonestest.GroupOutputs{Service: f.service}
	for i, id := range []string{"slim:02:00:00:00:00:01", "slim:02:00:00:00:00:02"} {
		outputs.Devices = append(outputs.Devices, zones.ManagedDevice{
			Kind: "slimproto", UDN: id, Name: id, MaxSampleRate: 48000,
			InitialVolume: &volumes[i], Capabilities: zonesSampleCapabilities(), FormatDeclared: true,
		})
	}
	f.service.SetManagedOutputs(outputs)
	snapshot := jukeboxSnapshot(c)
	if len(snapshot.Devices) != 2 {
		t.Fatalf("网页快照中设备数为 %d，期望 2", len(snapshot.Devices))
	}
	leader, member := snapshot.Devices[0], snapshot.Devices[1]
	if leader.ExternalID != outputs.Devices[0].UDN {
		leader, member = member, leader
	}
	c.call("", http.MethodPost, "/devices/"+leader.DeviceID+"/activate", map[string]any{}, 200)
	c.call("", http.MethodPost, "/zones/"+leader.ZoneID+"/group/members", map[string]string{"device_id": member.DeviceID}, 200)
	selectJukeboxZone(t, f, leader.ZoneID)
	events := f.events(t, "")
	events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })
	response := jukeboxRequest(f, jukeboxMember, "json", "setGain", url.Values{"gain": {"0.5"}})
	requireJukeboxStatus(t, requireSubsonicOK(t, response), -1, false, 0.5, 0)
	want := map[string]float64{leader.DeviceID: 0.5, member.DeviceID: 0.25}
	for range len(want) {
		frame := events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "device" })
		var payload struct{ Device zones.Device }
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			t.Fatal(err)
		}
		volume, ok := want[payload.Device.DeviceID]
		if !ok || payload.Device.Volume != volume {
			t.Fatalf("编组的设备音量事件为 %+v，剩余期望 %v", payload.Device, want)
		}
		requireJukeboxDeviceVolume(t, c, payload.Device.DeviceID, volume)
		delete(want, payload.Device.DeviceID)
	}
}

func TestPostgreSQLSubsonicJukeboxRecentPlayback(t *testing.T) {
	f := newJukeboxPlaybackFixture(t)
	c := zonesSampleCalls{t: t, f: f}
	requireNoZone := func(user string, options url.Values) {
		t.Helper()
		code := 0
		body := requireSubsonicJSON(t, jukeboxRequest(f, user, "json", "status", options), 200, &code)
		if body["error"].(map[string]any)["message"] != "没有可控制的播放区" {
			t.Fatalf("没有可用播放区的协议错误为 %v", body["error"])
		}
	}
	requireNoZone(jukeboxMember, nil)
	first, latest, queued, paused := c.device(""), c.device(""), c.device(""), c.device("")
	c.command("", queued.Zone.ZoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[2:3]})
	pausedOutput := c.claim("", paused.Device.DeviceID, false, nil)
	pausedStart := c.command("", paused.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": f.tracks[3:4], "start_index": 0})
	if pausedStart.Zone.Stream == nil {
		t.Fatal("用于暂停测试的区缺少输出流")
	}
	c.report("", pausedOutput.SessionID, "status", "paused", &pausedStart.Zone.Stream.Seq, 0, nil)
	requireNoZone(jukeboxMember, nil)

	for i, device := range []zones.RegisterDeviceResult{first, latest} {
		output := c.claim("", device.Device.DeviceID, false, nil)
		started := c.command("", device.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": f.tracks[i : i+1], "start_index": 0})
		if started.Zone.Stream == nil {
			t.Fatal("用于测试最近播放的区缺少输出流")
		}
		result := c.report("", output.SessionID, "status", "playing", &started.Zone.Stream.Seq, 0, nil)
		if result.Zone.Status != "playing" {
			t.Fatalf("输出回报后的区状态为 %s，期望 playing", result.Zone.Status)
		}
		f.clock.Advance(time.Second)
	}
	c.device("")
	c.command("", queued.Zone.ZoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[4:]})
	c.command("", latest.Zone.ZoneID, map[string]any{"command": "pause"})
	requireJukeboxQueue(t, c, latest.Zone.ZoneID, f.tracks[1:2])

	const otherName = "subsonic-jukebox-other"
	other := seedSubsonicMember(t, f.integrationTestApplication, otherName)
	body := map[string]string{"jukebox_zone_id": latest.Zone.ZoneID}
	requireAPIError(t, f.request(t, http.MethodPut, subsonicMePath, body, &other, nil), 404, "not_found")
	snapshot := zonesSampleDecode[zones.Snapshot](t, f.request(t, http.MethodGet, zonesSamplePrefix, nil, &other, nil), 200)
	if len(snapshot.Zones) != 0 {
		t.Fatalf("另一成员的快照包含播放区 %v", snapshot.Zones)
	}
	requireNoZone(otherName, url.Values{"zoneId": {latest.Zone.ZoneID}})
	requireSubsonicFailure(t, jukeboxRequest(f, otherName, "json", "add", url.Values{
		"zoneId": {latest.Zone.ZoneID}, "id": f.tracks[4:],
	}), http.StatusOK, 0)
	requireJukeboxQueue(t, c, latest.Zone.ZoneID, f.tracks[1:2])
}
