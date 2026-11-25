package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLSubsonicJukeboxWebSettings(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	const memberName = "jukebox-settings-member"
	const otherName = "jukebox-settings-other"
	member := seedSubsonicMember(t, a, memberName)
	other := seedSubsonicMember(t, a, otherName)
	owned := registerJukeboxDevice(t, a, &member)
	otherZone := registerJukeboxDevice(t, a, &other).Zone.ZoneID
	selection := map[string]any{"jukebox_zone_id": owned.Zone.ZoneID}
	put := func(body map[string]any) {
		t.Helper()
		requireSubsonicWebJSON(t, a.request(t, http.MethodPut, subsonicMePath, body, &member, nil),
			map[string]any{"enabled": true, "ready": false, "username": memberName, "jukebox_zone_id": body["jukebox_zone_id"]})
	}
	requireAPIError(t, a.request(t, http.MethodGet, subsonicMePath, nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, a.request(t, http.MethodPut, subsonicMePath, selection, nil, nil), 401, "unauthorized")
	for _, origin := range []struct{ value, code string }{
		{"", "origin_required"},
		{"https://other.example", "origin_forbidden"},
	} {
		requireAPIError(t, a.request(t, http.MethodPut, subsonicMePath, selection, &member, map[string]string{"Origin": origin.value}), 403, origin.code)
	}
	requireSubsonicMe(t, a, &member, memberName, true, false)
	put(selection)
	requireSubsonicWebJSON(t, a.request(t, http.MethodGet, subsonicMePath, nil, &member, nil),
		map[string]any{"enabled": true, "ready": false, "username": memberName, "jukebox_zone_id": owned.Zone.ZoneID})
	requireSubsonicMe(t, a, &other, otherName, true, false)

	for _, tc := range []struct{ name, body string }{
		{"unknown-field", `{"jukebox_zone_id":null,"unrecognized":true}`},
		{"wrong-type", `{"jukebox_zone_id":12}`},
		{"missing-field", `{}`},
		{"duplicate-key", `{"jukebox_zone_id":null,"jukebox_zone_id":null}`},
		{"invalid-uuid", `{"jukebox_zone_id":"invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := a.request(t, http.MethodPut, subsonicMePath, json.RawMessage(tc.body), &member, nil)
			requireAPIError(t, response, 400, "invalid_input")
		})
	}
	for _, id := range []string{otherZone, uuid.New()} {
		response := a.request(t, http.MethodPut, subsonicMePath, map[string]string{"jukebox_zone_id": id}, &member, nil)
		requireAPIError(t, response, 404, "not_found")
	}
	requireSubsonicWebJSON(t, a.request(t, http.MethodGet, subsonicMePath, nil, &member, nil),
		map[string]any{"enabled": true, "ready": false, "username": memberName, "jukebox_zone_id": owned.Zone.ZoneID})

	clear := map[string]any{"jukebox_zone_id": nil}
	put(clear)
	requireSubsonicMe(t, a, &member, memberName, true, false)
	put(selection)
	deletion := a.request(t, http.MethodDelete, zonesSamplePrefix+"/devices/"+owned.Device.DeviceID, nil, &member, nil)
	if deletion.Code != http.StatusNoContent {
		t.Fatalf("删除已选区的设备返回 %d：%s", deletion.Code, deletion.Body)
	}
	requireSubsonicMe(t, a, &member, memberName, true, false)

	setSubsonicEnabled(t, a, &admin, false)
	requireSubsonicMe(t, a, &member, memberName, false, false)
	requireAPIError(t, a.request(t, http.MethodPut, subsonicMePath, clear, &member, nil), 409, "subsonic_disabled")
}
