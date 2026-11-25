package app

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

const (
	slimProtoGroupSecondKey = "slim:02:00:00:00:00:02"
	slimProtoGroupMemberKey = "slim:02:00:00:00:00:03"
)

func addSlimProtoGroupMember(t *testing.T, f *upnpFixture, user *integrationTestUser, leader, member zones.Device, takeover bool) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, "POST", zonesSamplePrefix+"/zones/"+leader.ZoneID+"/group/members",
		map[string]any{"device_id": member.DeviceID, "takeover": takeover}, user, nil)
}

func TestPostgreSQLSlimProtoGroupMemberTakeoverAndLeaderRevocation(t *testing.T) {
	f := newUPnPFixture(t, false)
	a, b := f.createUser(t, "user"), f.createUser(t, "user")
	if settings := slimProtoSettings(t, f, true); settings.SlimProtoUnavailable != nil {
		t.Fatalf("SlimProto 设置 = %+v", settings)
	}
	startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 1)
	startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 2)
	startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 3)

	updateGrants := func(key string, users ...string) {
		t.Helper()
		if len(users) == 0 {
			users = []string{}
		}
		path := upnpAdmin + "/devices/" + url.PathEscape(key) + "/grants"
		zonesSampleDecode[map[string]any](t,
			f.request(t, "PUT", path, map[string][]string{"user_ids": users}, &f.admin, nil), 200)
	}
	updateGrants(slimProtoTestKey, a.id)
	updateGrants(slimProtoGroupSecondKey, b.id)
	updateGrants(slimProtoGroupMemberKey, a.id, b.id)
	zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings", map[string]bool{"shared": false}, &f.admin, nil), 200)

	aLeader := slimProtoDeviceByKey(t, f, &a, slimProtoTestKey)
	bLeader := slimProtoDeviceByKey(t, f, &b, slimProtoGroupSecondKey)
	aMember := slimProtoDeviceByKey(t, f, &a, slimProtoGroupMemberKey)
	bMember := slimProtoDeviceByKey(t, f, &b, slimProtoGroupMemberKey)
	zonesSampleDecode[zones.DeviceResult](t, f.activate(t, &a, aLeader, false), 200)
	zonesSampleDecode[zones.DeviceResult](t, f.activate(t, &b, bLeader, false), 200)

	zonesSampleDecode[zones.RegisterDeviceResult](t, addSlimProtoGroupMember(t, f, &a, aLeader, aMember, false), 200)
	if got := slimProtoDeviceByKey(t, f, &b, slimProtoGroupMemberKey).OccupiedBy; got == nil || got.UserID != a.id {
		t.Fatalf("成员设备当前占用 = %+v，期望由第一个账号占用", got)
	}
	requireAPIError(t, addSlimProtoGroupMember(t, f, &b, bLeader, bMember, false), 409, "device_busy")
	zonesSampleDecode[zones.RegisterDeviceResult](t, addSlimProtoGroupMember(t, f, &b, bLeader, bMember, true), 200)
	if got := slimProtoDeviceByKey(t, f, &a, slimProtoGroupMemberKey).OccupiedBy; got == nil || got.UserID != b.id {
		t.Fatalf("接管后成员设备占用 = %+v，期望由第二个账号占用", got)
	}

	// 单独收回主设备的授权，成员设备仍在线且授权仍有效。
	updateGrants(slimProtoGroupSecondKey)
	if got := slimProtoDeviceByKey(t, f, &b, slimProtoGroupMemberKey).OccupiedBy; got != nil {
		t.Fatalf("撤销主设备授权后成员设备仍被占用：%+v", got)
	}
	if got := slimProtoDeviceByKey(t, f, &a, slimProtoTestKey).OccupiedBy; got == nil || got.UserID != a.id {
		t.Fatalf("另一个主设备的占用 = %+v，期望仍由第一个账号占用", got)
	}
}

func TestPostgreSQLSlimProtoGroupPausesWhenLeaderDisconnects(t *testing.T) {
	f := newUPnPFixture(t, true)
	tracks := f.tracks(t, 8)
	if settings := slimProtoSettings(t, f, true); settings.SlimProtoUnavailable != nil {
		t.Fatalf("SlimProto 设置 = %+v", settings)
	}
	user := f.createUser(t, "user")
	leader := startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 1)
	member := startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 2)
	leaderDevice := slimProtoDeviceByKey(t, f, &user, slimProtoTestKey)
	memberDevice := slimProtoDeviceByKey(t, f, &user, slimProtoGroupSecondKey)
	zonesSampleDecode[zones.DeviceResult](t, f.activate(t, &user, leaderDevice, false), 200)
	zonesSampleDecode[zones.RegisterDeviceResult](t, addSlimProtoGroupMember(t, f, &user, leaderDevice, memberDevice, false), 200)
	f.play(t, &user, leaderDevice, tracks)

	for _, player := range []*slimProtoTestPlayer{leader, member} {
		player.expectControl(t, 'q')
		stream := player.expectControl(t, 's')
		player.Status(t, "STMc", 0)
		readSlimProtoTestAudio(t, stream)
		player.Status(t, "STMl", 0)
	}
	for _, player := range []*slimProtoTestPlayer{leader, member} {
		player.expectControl(t, 'u')
		player.Status(t, "STMs", 0)
	}
	f.wait(t, 3*time.Second, "组输出开始播放", func() bool {
		return f.zone(t, &user, leaderDevice).Status == "playing"
	})
	if err := leader.Close(); err != nil {
		t.Fatal(err)
	}
	member.expectControl(t, 'q')
	f.wait(t, 3*time.Second, "主设备离线且成员占用释放", func() bool {
		snapshot := f.snapshot(t, &user)
		_, leaderVisible := deviceFor(snapshot, slimProtoTestKey)
		row, ok := deviceFor(snapshot, slimProtoGroupSecondKey)
		return !leaderVisible && ok && row.Online && row.OccupiedBy == nil
	})
	startSlimProtoTestPlayerWithMAC(t, f, 96000, "flc", 1)
	back := slimProtoDeviceByKey(t, f, &user, slimProtoTestKey)
	z := f.zone(t, &user, back)
	if back.DeviceID != leaderDevice.DeviceID || z.Intent != "pause" || z.Status != "paused" || z.Stream != nil {
		t.Fatalf("主设备重连后的原播放区 = %+v，设备 ID = %s", z, back.DeviceID)
	}
}
