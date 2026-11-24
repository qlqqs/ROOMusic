package app

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
)

func assertUPnPSample(t *testing.T, name string, response *httptest.ResponseRecorder, replacements ...string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/api-samples/upnp/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Status int
		Body   any
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer(replacements...).Replace(string(raw))), &want); err != nil {
		t.Fatal(err)
	}
	var got any
	if response.Body.Len() != 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
	}
	normalizeUPnPSampleTimes(t, got)
	if response.Code != want.Status || !reflect.DeepEqual(got, want.Body) {
		t.Fatalf("接口样本 %s 漂移\n实际 %d %s\n期望 %d %v", name, response.Code, response.Body, want.Status, want.Body)
	}
}

// 样本的时间字段仍需可解析，比较时统一成样本时钟。
func normalizeUPnPSampleTimes(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, field := range value {
			switch key {
			case "server_time", "last_seen_at", "paused_at", "at":
				if field == nil {
					continue
				}
				raw, ok := field.(string)
				_, err := time.Parse(time.RFC3339, raw)
				if !ok || err != nil {
					t.Fatalf("接口样本时间 %s = %v", key, field)
				}
				value[key] = "2026-01-01T00:00:00Z"
			default:
				normalizeUPnPSampleTimes(t, field)
			}
		}
	case []any:
		for _, item := range value {
			normalizeUPnPSampleTimes(t, item)
		}
	}
}

func TestPostgreSQLUPnPAPISamples(t *testing.T) {
	r := newUPnPTestRenderer(t, "normal")
	f := newUPnPFixture(t, false)
	member := f.createUser(t, "user")
	headers := map[string]string{"X-Request-ID": "sample-request"}
	replacements := []string{
		"uuid:00000000-0000-4000-8000-000000000001", r.udn,
		"http://192.168.44.2:49152/description.xml", r.address,
	}
	body := map[string]string{"address": r.address}
	assertUPnPSample(t, "disabled", f.request(t, "POST", upnpAdmin+"/devices", body, &f.admin, headers))
	assertUPnPSample(t, "settings-updated", f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"enabled": true, "shared": false}, &f.admin, headers))
	assertUPnPSample(t, "invalid-address", f.request(t, "POST", upnpAdmin+"/devices",
		map[string]string{"address": "http://127.0.0.1/description.xml"}, &f.admin, headers))
	assertUPnPSample(t, "manual-added", f.add(t, r), replacements...)
	devicePath := upnpAdmin + "/devices/" + url.PathEscape(r.udn)
	assertUPnPSample(t, "grants-updated", f.request(t, "PUT", devicePath+"/grants",
		map[string][]string{"user_ids": {member.id}}, &f.admin, headers),
		"00000000-0000-4000-8000-000000000001", member.id)
	assertUPnPSample(t, "list", f.request(t, "GET", upnpAdmin+"/devices", nil, &f.admin, headers),
		append(replacements, "00000000-0000-4000-8000-000000000002", member.id)...)
	d := f.device(t, &member, r)
	assertUPnPSample(t, "playback-snapshot", f.request(t, "GET", zonesSamplePrefix, nil, &member, headers),
		"00000000-0000-4000-8000-000000000001", d.DeviceID,
		"00000000-0000-4000-8000-000000000002", d.ZoneID,
		"uuid:00000000-0000-4000-8000-000000000003", r.udn)
	assertUPnPSample(t, "manual-removed", f.request(t, "DELETE", devicePath, nil, &f.admin, headers))
	assertUPnPSample(t, "list-empty", f.request(t, "GET", upnpAdmin+"/devices", nil, &f.admin, headers))
}

func TestPostgreSQLUPnPSharingTakeoverAndRevocation(t *testing.T) {
	r := newUPnPTestRenderer(t, "normal")
	f := newUPnPFixture(t, true)
	a, b := f.createUser(t, "user"), f.createUser(t, "user")
	mustExec(t, f.integrationTestApplication, "UPDATE users SET username='listener' WHERE id=$1", a.id)
	track := f.tracks(t, 40, 35)
	assertUPnPSample(t, "settings", f.request(t, "GET", upnpAdmin+"/settings", nil, &f.admin, nil))
	assertUPnPSample(t, "list-empty", f.request(t, "GET", upnpAdmin+"/devices", nil, &f.admin, nil))
	requireAPIError(t, f.request(t, "POST", upnpAdmin+"/devices", map[string]string{"address": r.address}, &f.admin, nil), 409, "upnp_disabled")
	f.enable(t)
	assertUPnPSample(t, "manual-added", f.add(t, r),
		"uuid:00000000-0000-4000-8000-000000000001", r.udn, "http://192.168.44.2:49152/description.xml", r.address)
	da, db := f.device(t, &a, r), f.device(t, &b, r)
	if da.ZoneID == db.ZoneID || da.DeviceID == db.DeviceID {
		t.Fatal("两个成员获得了同一播放区或设备记录")
	}
	f.play(t, &a, da, track[:1])
	f.wait(t, 8*time.Second, "成员 A 开始播放", func() bool {
		return r.State().BytesRead > 0 && f.zone(t, &a, da).Status == "playing"
	})
	oldURI := r.State().URI
	if occupant := f.device(t, &b, r).OccupiedBy; occupant == nil || occupant.UserID != a.id || occupant.DisplayName != "listener" {
		t.Fatalf("成员 B 所见占用者 = %+v", occupant)
	}
	busy := f.request(t, "POST", zonesSamplePrefix+"/devices/"+db.DeviceID+"/activate", map[string]bool{"takeover": false}, &b, map[string]string{"X-Request-ID": "sample-request"})
	assertUPnPSample(t, "device-busy", busy)
	occupied := f.request(t, "PUT", zonesSamplePrefix+"/devices/"+db.DeviceID+"/sample-rate",
		map[string]int{"max_sample_rate": 48000}, &b, nil)
	assertUPnPSample(t, "playback-occupied", occupied,
		"00000000-0000-4000-8000-000000000001", db.DeviceID,
		"00000000-0000-4000-8000-000000000002", db.ZoneID,
		"uuid:00000000-0000-4000-8000-000000000003", r.udn,
		"00000000-0000-4000-8000-000000000004", a.id)

	zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings", map[string]bool{"shared": false}, &f.admin, nil), 200)
	if _, visible := deviceFor(f.snapshot(t, &b), r.udn); visible {
		t.Fatal("关闭共享后未分配的成员仍看见设备")
	}
	requireAPIError(t, f.activate(t, &b, db, true), 404, "not_found")
	if !f.device(t, &f.admin, r).Online || r.State().TransportState != "STOPPED" {
		t.Fatal("管理员应仍能看到设备，失去分配的旧输出应停止")
	}
	grants := upnpAdmin + "/devices/" + url.PathEscape(r.udn) + "/grants"
	result := zonesSampleDecode[struct {
		UserIDs []string `json:"user_ids"`
	}](t,
		f.request(t, "PUT", grants, map[string][]string{"user_ids": {a.id, b.id, b.id}}, &f.admin, nil), 200)
	ids := []string{a.id, b.id}
	slices.Sort(ids)
	if !slices.Equal(result.UserIDs, ids) || f.device(t, &b, r).DeviceID != db.DeviceID {
		t.Fatalf("分配结果 = %+v，期望 %v", result, ids)
	}

	f.play(t, &a, da, track[:1])
	f.wait(t, 8*time.Second, "分配后成员 A 再次播放", func() bool {
		return r.State().URI != oldURI && r.State().BytesRead > 0 && f.zone(t, &a, da).Status == "playing"
	})
	oldURI = r.State().URI
	f.command(t, &b, db, map[string]any{"command": "play_tracks", "track_ids": track[1:], "start_index": 0, "takeover": true})
	f.wait(t, 8*time.Second, "成员 B 接管并播放自己的曲目", func() bool {
		z := f.zone(t, &b, db)
		return r.State().URI != oldURI && r.State().BytesRead > 0 && z.Status == "playing" && z.Current != nil && z.Current.Track.TrackID == track[1]
	})
	if z := f.zone(t, &a, da); z.Intent != "pause" || z.Status != "paused" || z.Stream != nil || z.QueueLength != 1 {
		t.Fatalf("接管后 A 的播放区 = %+v", z)
	}

	zonesSampleDecode[map[string]any](t, f.request(t, "POST", "/api/v1/users/"+b.id+"/sessions/revoke", map[string]any{}, &f.admin, nil), 200)
	requireAPIError(t, f.request(t, "GET", "/api/v1/auth/me", nil, &b, nil), 401, "unauthorized")
	if occupant := f.device(t, &a, r).OccupiedBy; occupant != nil || r.State().TransportState != "STOPPED" {
		t.Fatalf("吊销会话后设备状态 = %s，占用 = %+v", r.State().TransportState, occupant)
	}
	f.play(t, &a, da, track[:1])
	f.wait(t, 8*time.Second, "成员 A 重新取得占用", func() bool {
		return r.State().BytesRead > 0 && f.zone(t, &a, da).Status == "playing"
	})
	zonesSampleDecode[map[string]any](t, f.request(t, "PATCH", "/api/v1/users/"+a.id, map[string]bool{"disabled": true}, &f.admin, nil), 200)
	requireAPIError(t, f.request(t, "GET", "/api/v1/auth/me", nil, &a, nil), 401, "unauthorized")
	if occupant := f.device(t, &f.admin, r).OccupiedBy; occupant != nil || r.State().TransportState != "STOPPED" {
		t.Fatalf("停用成员后设备状态 = %s，占用 = %+v", r.State().TransportState, occupant)
	}
}
