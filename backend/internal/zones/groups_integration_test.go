package zones_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

func managedGroupDevice(index int, kind, volumeControl string, volume float64) zones.ManagedDevice {
	return zones.ManagedDevice{
		Kind: kind, UDN: fmt.Sprintf("%s:02:00:00:00:00:%02x", kind, index),
		Name: fmt.Sprintf("合成设备 %d", index), InitialVolume: &volume, MaxSampleRate: 48000,
		Capabilities: zones.Capabilities{
			StreamFormats: []zones.StreamFormatCapability{{Codec: "flac", MIMEType: "audio/flac", Lossless: true}},
			MaxChannels:   2, DSD: "none", Volume: volumeControl,
		},
	}
}

func installGroupOutputs(t *testing.T, f *controlFixture, user controlUser, devices ...zones.ManagedDevice) (*zonestest.GroupOutputs, []zones.Device) {
	t.Helper()
	f.handler = httpserver.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), f.handler)
	outputs := &zonestest.GroupOutputs{Service: f.service, Devices: devices}
	f.service.SetManagedOutputs(outputs)
	snapshot := controlDecode[zones.Snapshot](t, f.request(t, user, "GET", "", nil, ""), 200)
	byUDN := make(map[string]zones.Device)
	for _, device := range snapshot.Devices {
		byUDN[device.ExternalID] = device
	}
	projected := make([]zones.Device, len(devices))
	for i, device := range devices {
		var ok bool
		if projected[i], ok = byUDN[device.UDN]; !ok {
			t.Fatalf("快照缺少托管设备 %s", device.UDN)
		}
	}
	return outputs, projected
}

func groupHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	envelope := controlDecode[httpserver.APIError](t, response, status)
	if envelope.Error.Code != code || envelope.Error.Message == "" || envelope.RequestID == "" || envelope.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("HTTP 错误信封 = %+v，响应头 = %v，期望错误码 %s", envelope, response.Header(), code)
	}
}

func TestPostgreSQLZonesGroupMembership(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	outputs, devices := installGroupOutputs(t, f, user,
		managedGroupDevice(1, "slimproto", "number", 0.8),
		managedGroupDevice(2, "slimproto", "number", 0.2),
		managedGroupDevice(3, "upnp", "number", 0.5),
	)
	leader, member, other := devices[0], devices[1], devices[2]
	groupPath := "/zones/" + leader.ZoneID + "/group/members"
	rejectUnsupported := func(device zones.Device) {
		t.Helper()
		reply := f.request(t, user, "POST", groupPath, map[string]any{"device_id": device.DeviceID}, controlOrigin)
		groupHTTPError(t, reply, 409, "group_unsupported")
	}
	rejectUnsupported(member)
	if _, err := f.service.ActivateDevice(t.Context(), user.id, leader.DeviceID, false); err != nil {
		t.Fatal(err)
	}
	rejectUnsupported(other)
	reply := f.request(t, user, "PUT", "/devices/"+other.DeviceID+"/sync-delay", map[string]int{"sync_delay_ms": 0}, controlOrigin)
	groupHTTPError(t, reply, 409, "group_unsupported")

	memberPath := "/zones/" + member.ZoneID + "/commands"
	play := map[string]any{"command": "play_tracks", "track_ids": f.tracks[:1], "start_index": 0}
	playing := controlDecode[zones.CommandResult](t, f.request(t, user, "POST", memberPath, play, controlOrigin), 200)
	if playing.Zone.Intent != "play" || playing.Zone.Stream == nil {
		t.Fatalf("成员区尚未起播：%+v", playing.Zone)
	}

	joined := controlDecode[zones.RegisterDeviceResult](t, f.request(t, user, "POST", groupPath, map[string]any{"device_id": member.DeviceID, "takeover": false}, controlOrigin), 200)
	if joined.Device.DeviceID != member.DeviceID || joined.Device.Group == nil || joined.Device.Group.Role != "member" || joined.Device.Group.ZoneID != leader.ZoneID ||
		joined.Zone.ZoneID != leader.ZoneID || joined.Zone.Group == nil || len(joined.Zone.Group.Members) != 1 ||
		joined.Zone.Group.Members[0] != (zones.GroupMemberState{DeviceID: member.DeviceID, State: "waiting"}) {
		t.Fatalf("加入后投影无效：%+v", joined)
	}
	if got := outputs.MemberLeader(member.ExternalID); got != leader.DeviceID {
		t.Fatalf("成员占用指向 %q，期望 %q", got, leader.DeviceID)
	}
	snapshot := controlDecode[zones.Snapshot](t, f.request(t, user, "GET", "", nil, ""), 200)
	paused := groupSnapshotZone(t, snapshot, member.ZoneID)
	if paused.Intent != "pause" || paused.Status != "paused" || paused.Stream != nil || paused.Current == nil || paused.Current.EntryID != playing.Zone.Current.EntryID {
		t.Fatalf("加入后成员区未保留队列并暂停：%+v", paused)
	}

	for _, tc := range []struct {
		path string
		body any
	}{
		{memberPath, play},
		{"/devices/" + member.DeviceID + "/activate", map[string]bool{"takeover": true}},
		{"/zones/" + member.ZoneID + "/group/members", map[string]string{"device_id": leader.DeviceID}},
	} {
		reply := f.request(t, user, "POST", tc.path, tc.body, controlOrigin)
		groupHTTPError(t, reply, 409, "device_grouped")
	}

	left := controlDecode[zones.RegisterDeviceResult](t, f.request(t, user, "DELETE", groupPath+"/"+member.DeviceID, nil, controlOrigin), 200)
	if left.Device.DeviceID != member.DeviceID || left.Device.Group != nil || left.Zone.ZoneID != leader.ZoneID || left.Zone.Group != nil || left.Zone.Revision <= joined.Zone.Revision {
		t.Fatalf("移出后投影无效：%+v", left)
	}
	if got := outputs.MemberLeader(member.ExternalID); got != "" {
		t.Fatalf("移出后仍有成员占用：%q", got)
	}
	snapshot = controlDecode[zones.Snapshot](t, f.request(t, user, "GET", "", nil, ""), 200)
	paused = groupSnapshotZone(t, snapshot, member.ZoneID)
	if paused.Status != "paused" || paused.Stream != nil {
		t.Fatalf("移出后成员区应继续暂停：%+v", paused)
	}
	controlDecode[zones.DeviceResult](t, f.request(t, user, "POST", "/devices/"+member.DeviceID+"/activate", map[string]bool{}, controlOrigin), 200)
}

func TestPostgreSQLZonesGroupCapacityAndLeaderGuard(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	managed := make([]zones.ManagedDevice, 10)
	for i := range managed {
		managed[i] = managedGroupDevice(i+1, "slimproto", "number", 0.5)
	}
	outputs, devices := installGroupOutputs(t, f, user, managed...)
	leader, secondLeader := devices[0], devices[8]
	for _, device := range []zones.Device{leader, secondLeader} {
		if _, err := f.service.ActivateDevice(t.Context(), user.id, device.DeviceID, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, device := range devices[1:8] {
		if _, err := f.service.AddGroupMember(t.Context(), user.id, leader.ZoneID, device.DeviceID, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		zone, device zones.Device
		code         string
	}{
		{leader, devices[9], "group_full"},
		{secondLeader, leader, "device_grouped"},
	} {
		reply := f.request(t, user, "POST", "/zones/"+tc.zone.ZoneID+"/group/members", map[string]string{"device_id": tc.device.DeviceID}, controlOrigin)
		groupHTTPError(t, reply, 409, tc.code)
		if got := outputs.MemberLeader(tc.device.ExternalID); got != "" {
			t.Fatalf("拒绝加入后设备 %s 被占用为 %s 的成员", tc.device.DeviceID, got)
		}
	}
	snapshot := controlDecode[zones.Snapshot](t, f.request(t, user, "GET", "", nil, ""), 200)
	group := groupSnapshotZone(t, snapshot, leader.ZoneID).Group
	if group == nil || len(group.Members) != 7 || groupSnapshotZone(t, snapshot, secondLeader.ZoneID).Group != nil {
		t.Fatalf("拒绝加入后编组投影不符：%+v", snapshot.Zones)
	}
}

func groupSnapshotZone(t *testing.T, snapshot zones.Snapshot, id string) zones.Zone {
	t.Helper()
	for _, zone := range snapshot.Zones {
		if zone.ZoneID == id {
			return zone
		}
	}
	t.Fatalf("快照缺少播放区 %s", id)
	return zones.Zone{}
}

func TestPostgreSQLZonesGroupVolumeAndRestart(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	outputs, devices := installGroupOutputs(t, f, user,
		managedGroupDevice(1, "slimproto", "number", 0.25),
		managedGroupDevice(2, "slimproto", "number", 0.5),
		managedGroupDevice(3, "slimproto", "fixed", 1),
	)
	leader, member, fixed := devices[0], devices[1], devices[2]
	ctx := t.Context()
	if _, err := f.service.ActivateDevice(ctx, user.id, leader.DeviceID, false); err != nil {
		t.Fatal(err)
	}
	for _, device := range devices[1:] {
		if _, err := f.service.AddGroupMember(ctx, user.id, leader.ZoneID, device.DeviceID, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.SetVolume(ctx, user.id, member.DeviceID, 0.5, true); err != nil {
		t.Fatal(err)
	}
	checkVolumes := func(list []zones.Device, wantLeader, wantMember float64) {
		t.Helper()
		got := make(map[string]zones.Device)
		for _, device := range list {
			got[device.DeviceID] = device
		}
		if len(list) != 3 || len(got) != 3 || got[leader.DeviceID].Volume != wantLeader || got[member.DeviceID].Volume != wantMember ||
			got[fixed.DeviceID].Volume != 1 || !got[member.DeviceID].Muted {
			t.Fatalf("组音量 = %+v，期望主设备 %v、成员 %v（静音）、固定设备 1", got, wantLeader, wantMember)
		}
	}
	for _, tc := range []struct{ volume, leader, member float64 }{
		{1, 0.5, 1},
		{0, 0, 0},
		{0.75, 0.75, 0.75},
	} {
		result, err := f.service.SetGroupVolume(ctx, user.id, leader.ZoneID, tc.volume)
		if err != nil {
			t.Fatal(err)
		}
		checkVolumes(result.Devices, tc.leader, tc.member)
	}
	for _, ms := range []int{-500, 0, 500} {
		got, err := f.service.SetSyncDelay(ctx, user.id, member.DeviceID, ms)
		if err != nil || got.Device.SyncDelayMS == nil || *got.Device.SyncDelayMS != ms {
			t.Fatalf("同步微调 %d = %+v, %v", ms, got.Device, err)
		}
	}
	for _, ms := range []int{-501, 501} {
		if _, err := f.service.SetSyncDelay(ctx, user.id, member.DeviceID, ms); !errors.Is(err, zones.ErrInvalidInput) {
			t.Fatalf("越界微调 %d = %v，期望 %v", ms, err, zones.ErrInvalidInput)
		}
	}

	f.service.Close()
	restarted := zones.NewService(f.db, zones.Options{Now: f.clock.current, AfterFunc: f.clock.after})
	t.Cleanup(restarted.Close)
	restarted.SetManagedOutputs(&zonestest.GroupOutputs{Service: restarted, Devices: outputs.Devices})
	snapshot, err := restarted.Snapshot(ctx, user.id)
	if err != nil {
		t.Fatal(err)
	}
	checkVolumes(snapshot.Devices, 0.75, 0.75)
	group := groupSnapshotZone(t, snapshot, leader.ZoneID).Group
	if group == nil || len(group.Members) != 2 ||
		group.Members[0] != (zones.GroupMemberState{DeviceID: member.DeviceID, State: "waiting"}) ||
		group.Members[1] != (zones.GroupMemberState{DeviceID: fixed.DeviceID, State: "waiting"}) {
		t.Fatalf("重启后编组未按顺序恢复：%+v", group)
	}
	for _, device := range snapshot.Devices {
		want := zones.DeviceGroup{ZoneID: leader.ZoneID, Role: "member"}
		if device.DeviceID == leader.DeviceID {
			want.Role = "leader"
		}
		if device.Group == nil || *device.Group != want {
			t.Fatalf("重启后设备 %s 编组 = %+v，期望 %+v", device.DeviceID, device.Group, want)
		}
		if device.DeviceID == member.DeviceID && (device.SyncDelayMS == nil || *device.SyncDelayMS != 500) {
			t.Fatalf("重启后成员同步微调未恢复：%+v", device)
		}
	}
	if _, err := restarted.ActivateDevice(ctx, user.id, member.DeviceID, false); !errors.Is(err, zones.ErrDeviceGrouped) {
		t.Fatalf("重启后激活成员 = %v，期望 %v", err, zones.ErrDeviceGrouped)
	}
}

func TestPostgreSQLZonesGroupHTTPValidation(t *testing.T) {
	f := newControlFixture(t)
	user := f.user(t, "user")
	_, devices := installGroupOutputs(t, f, user, managedGroupDevice(1, "slimproto", "number", 0.5))
	device := devices[0]
	group := "/zones/" + device.ZoneID + "/group"
	delay := "/devices/" + device.DeviceID + "/sync-delay"
	for _, tc := range []struct {
		name, method, path, body, code string
	}{
		{"加入路径格式", "POST", "/zones/bad/group/members", "{}", "invalid_id"},
		{"加入缺设备", "POST", group + "/members", "{}", "invalid_input"},
		{"加入设备为空", "POST", group + "/members", `{"device_id":null}`, "invalid_input"},
		{"加入设备格式", "POST", group + "/members", `{"device_id":"bad"}`, "invalid_input"},
		{"接管为空", "POST", group + "/members", fmt.Sprintf(`{"device_id":%q,"takeover":null}`, device.DeviceID), "invalid_input"},
		{"接管类型", "POST", group + "/members", fmt.Sprintf(`{"device_id":%q,"takeover":"yes"}`, device.DeviceID), "invalid_json"},
		{"移出路径格式", "DELETE", group + "/members/bad", "", "invalid_id"},
		{"移出附带字段", "DELETE", group + "/members/" + device.DeviceID, `{"takeover":false}`, "invalid_input"},
		{"音量缺字段", "PUT", group + "/volume", "{}", "invalid_input"},
		{"音量为空", "PUT", group + "/volume", `{"volume":null}`, "invalid_input"},
		{"音量越界", "PUT", group + "/volume", `{"volume":1.1}`, "invalid_input"},
		{"音量重复字段", "PUT", group + "/volume", `{"volume":0,"volume":1}`, "invalid_input"},
		{"微调缺字段", "PUT", delay, "{}", "invalid_input"},
		{"微调小数", "PUT", delay, `{"sync_delay_ms":1.5}`, "invalid_json"},
		{"微调越界", "PUT", delay, `{"sync_delay_ms":-501}`, "invalid_input"},
		{"微调未知字段", "PUT", delay, `{"sync_delay_ms":0,"extra":0}`, "invalid_input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body any
			if tc.body != "" {
				body = json.RawMessage(tc.body)
			}
			reply := f.request(t, user, tc.method, tc.path, body, controlOrigin)
			groupHTTPError(t, reply, 400, tc.code)
		})
	}
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"加入成员", "POST", group + "/members", fmt.Sprintf(`{"device_id":%q}`, device.DeviceID)},
		{"移出成员", "DELETE", group + "/members/" + device.DeviceID, ""},
		{"组音量", "PUT", group + "/volume", `{"volume":0}`},
		{"同步微调", "PUT", delay, `{"sync_delay_ms":0}`},
	} {
		t.Run(tc.name+"的来源与鉴权", func(t *testing.T) {
			var body any
			if tc.body != "" {
				body = json.RawMessage(tc.body)
			}
			reply := f.request(t, user, tc.method, tc.path, body, "")
			groupHTTPError(t, reply, 403, "origin_required")
			reply = f.request(t, controlUser{}, tc.method, tc.path, body, controlOrigin)
			groupHTTPError(t, reply, 401, "unauthorized")
		})
	}
}
