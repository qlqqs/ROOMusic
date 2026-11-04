package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPersonalHTTPStrictValidation(t *testing.T) {
	f := newPersonalFixture(t, 1)
	playlist := f.create("校验", f.tracks)
	item := f.items(playlist.ID)[0]["id"].(string)
	base := playlistsURL + "/" + playlist.ID
	routes := []struct{ method, path, valid string }{
		{"POST", playlistsURL, `{"name":"新歌单"}`}, {"PATCH", base, `{"expected_revision":1,"name":"新名字"}`},
		{"DELETE", base, `{"expected_revision":1}`}, {"POST", base + "/items", `{"expected_revision":1,"track_ids":["` + f.tracks[0] + `"]}`},
		{"DELETE", base + "/items/" + item, `{"expected_revision":1}`},
		{"POST", base + "/restore", `{"token":"` + uuid.New() + `"}`},
		{"POST", base + "/items/" + item + "/move", `{"expected_revision":1,"action":"up"}`},
		{"PUT", favoritesURL + "/tracks/" + f.tracks[0], `{}`}, {"DELETE", favoritesURL + "/tracks/" + f.tracks[0], `{}`},
		{"PUT", favoritesURL + "/albums/" + f.groups[0], `{}`}, {"DELETE", favoritesURL + "/albums/" + f.groups[0], `{}`},
		{"POST", favoritesURL + "/lookup", `{}`},
	}
	for _, route := range routes {
		t.Run(route.method+route.path, func(t *testing.T) {
			for _, user := range []*integrationTestUser{nil, &f.user} {
				requireAPIError(t, personalRaw(f.app, route.method, route.path, "{", user, map[string]string{"Origin": ""}), 403, "origin_required")
				requireAPIError(t, personalRaw(f.app, route.method, route.path, "{", user, map[string]string{"Origin": "http://evil.example"}), 403, "origin_forbidden")
			}
			requireAPIError(t, personalRaw(f.app, route.method, route.path, "{", nil, nil), 401, "unauthorized")
			for _, body := range []string{"{", route.valid + "{}", route.valid + "x", strings.Repeat(" ", 1<<20) + route.valid} {
				requireAPIError(t, personalRaw(f.app, route.method, route.path, body, &f.user, nil), 400, "invalid_json")
			}
			for _, body := range []string{"null", "[]", "1", `{"user_id":"` + f.user.id + `"}`, `{"user_id":1,"user_id":2}`} {
				requireAPIError(t, personalRaw(f.app, route.method, route.path, body, &f.user, nil), 400, "invalid_input")
			}
			if strings.HasPrefix(route.path, playlistsURL) {
				for _, key := range []string{"", "not-a-uuid"} {
					requireAPIError(t, personalRaw(f.app, route.method, route.path, route.valid, &f.user, map[string]string{"Idempotency-Key": key}), 400, "invalid_input")
				}
			}
		})
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", playlistsURL, `{}`}, {"POST", playlistsURL, `{"name":null}`}, {"POST", playlistsURL, `{"name":123}`},
		{"POST", playlistsURL, `{"name":"a","name":"b"}`}, {"POST", playlistsURL, `{"name":"a","track_ids":null}`},
		{"POST", playlistsURL, `{"name":"a","track_ids":[null]}`}, {"POST", playlistsURL, `{"name":"a","track_ids":[1]}`},
		{"PATCH", base, `{"expected_revision":1.0,"name":"a"}`}, {"PATCH", base, `{"expected_revision":"1","name":"a"}`},
		{"DELETE", base, `{}`}, {"DELETE", base, `{"expected_revision":null}`}, {"DELETE", base, `{"expected_revision":9007199254740992}`},
		{"POST", base + "/items", `{"expected_revision":1,"track_ids":[]}`},
		{"POST", base + "/restore", `{"token":"` + uuid.New() + `","expected_revision":1}`},
		{"POST", base + "/items/" + item + "/move", `{"expected_revision":1,"action":"up","before_item_id":null}`},
		{"POST", base + "/items/" + item + "/move", `{"expected_revision":1,"action":"before"}`},
		{"POST", base + "/items/" + item + "/move", `{"expected_revision":1,"action":"up","position":1}`},
		{"POST", favoritesURL + "/lookup", `{"track_ids":null}`}, {"POST", favoritesURL + "/lookup", `{"group_ids":[false]}`},
		{"POST", favoritesURL + "/lookup", `{"artist_ids":[]}`}, {"PUT", favoritesURL + "/tracks/not-an-id", `{}`},
	} {
		requireAPIError(t, personalRaw(f.app, tc.method, tc.path, tc.body, &f.user, nil), 400, "invalid_input")
	}
	for _, path := range []string{favoritesURL + "/tracks", favoritesURL + "/albums", playlistsURL, base + "/items"} {
		requireAPIError(t, f.call("GET", path+"?page=0", nil), 400, "invalid_pagination")
		requireAPIError(t, f.call("GET", path+"?page=9223372036854775807&page_size=20", nil), 400, "invalid_pagination")
		requireAPIError(t, f.call("GET", path+"?user_id="+f.user.id, nil), 400, "invalid_input")
	}
	requireAPIError(t, f.call("GET", playlistsURL+"?page_size=21", nil), 400, "invalid_pagination")
	for _, path := range []string{favoritesURL + "/tracks/queue", base, base + "/queue", playlistsURL + "/containing-track/" + f.tracks[0]} {
		requireAPIError(t, f.call("GET", path+"?cursor=x", nil), 400, "invalid_input")
	}
	for _, path := range []string{favoritesURL + "/tracks", favoritesURL + "/albums", base + "/items"} {
		requireAPIError(t, f.call("GET", path+"?revision=9007199254740992", nil), 400, "invalid_input")
	}
	// 所有拒绝均发生在业务写入前。
	if state := f.state(playlist.ID); state.Revision != 1 || state.Name != "校验" || state.TrackCount != 1 {
		t.Fatalf("拒绝的请求修改数据: %+v", state)
	}
	var receipts int
	if err := f.app.database.QueryRow(`SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1`, f.user.id).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("非法请求写入收据: %d", receipts)
	}
}

func TestPersonalHTTPIsolationReplayAndSessions(t *testing.T) {
	f := newPersonalFixture(t, 2)
	users := []integrationTestUser{f.user, f.app.createUser(t, "user"), f.app.createUser(t, "admin")}
	playlists := make([]string, len(users))
	items := make([]string, len(users))
	tokens := make([]string, len(users))
	anchors := make([]string, len(users))
	key := uuid.New()
	for i, user := range users {
		f.user = user
		for _, kindID := range []string{"tracks/" + f.tracks[i%2], "albums/" + f.groups[i%2]} {
			personalOK(t, f.call("PUT", favoritesURL+"/"+kindID, nil), 200)
		}
		p := f.create(fmt.Sprintf("本人歌单%d", i), f.tracks)
		playlists[i] = p.ID
		items[i] = f.items(p.ID)[0]["id"].(string)
		tokens[i] = personalToken(t, f.remove(p.ID, items[i]))
		anchors[i] = f.items(p.ID)[0]["id"].(string)
	}
	for i, user := range users {
		f.user = user
		for _, path := range []string{favoritesURL + "/tracks", favoritesURL + "/albums", playlistsURL} {
			body := personalBody(t, personalOK(t, f.call("GET", path, nil), 200))
			if body["pagination"].(map[string]any)["total"] != float64(1) {
				t.Fatalf("个人列表串用户: %s", path)
			}
		}
		lookup := personalBody(t, personalOK(t, f.call("POST", favoritesURL+"/lookup", map[string]any{"track_ids": f.tracks, "group_ids": f.groups}), 200))
		for j, item := range lookup["tracks"].([]any) {
			if item.(map[string]any)["favorited"] != (j == i%2) {
				t.Fatal("lookup 串用户")
			}
		}
		queue := personalBody(t, personalOK(t, f.call("GET", favoritesURL+"/tracks/queue", nil), 200))
		if queue["total"] != float64(1) {
			t.Fatal("收藏队列串用户")
		}
		membership := personalBody(t, personalOK(t, f.call("GET", playlistsURL+"/containing-track/"+f.tracks[1], nil), 200))["items"].([]any)
		if len(membership) != 1 || membership[0].(map[string]any)["playlist_id"] != playlists[i] {
			t.Fatal("歌单反查串用户")
		}
		for j, id := range playlists {
			if j == i {
				continue
			}
			for _, suffix := range []string{"", "/items", "/queue"} {
				requireAPIError(t, f.call("GET", playlistsURL+"/"+id+suffix, nil), 404, "not_found")
			}
			requireAPIError(t, f.call("PATCH", playlistsURL+"/"+id, map[string]any{"name": "不应修改", "expected_revision": 2}), 404, "not_found")
			requireAPIError(t, f.call("DELETE", playlistsURL+"/"+id, map[string]any{"expected_revision": 2}), 404, "not_found")
			requireAPIError(t, f.call("POST", playlistsURL+"/"+id+"/items", map[string]any{"expected_revision": 2, "track_ids": f.tracks}), 404, "not_found")
			requireAPIError(t, f.call("POST", playlistsURL+"/"+playlists[i]+"/items/"+anchors[j]+"/move", map[string]any{"expected_revision": 2, "action": "first"}), 404, "not_found")
			requireAPIError(t, f.call("POST", playlistsURL+"/"+id+"/restore", map[string]any{"token": tokens[j]}), 404, "not_found")
			requireAPIError(t, f.call("POST", playlistsURL+"/"+playlists[i]+"/restore", map[string]any{"token": tokens[j]}), 404, "not_found")
			current := f.items(playlists[i])[0]["id"].(string)
			requireAPIError(t, f.call("DELETE", playlistsURL+"/"+playlists[i]+"/items/"+anchors[j], map[string]any{"expected_revision": 2}), 404, "not_found")
			requireAPIError(t, f.call("POST", playlistsURL+"/"+playlists[i]+"/items/"+current+"/move", map[string]any{"expected_revision": 2, "action": "before", "before_item_id": anchors[j]}), 404, "not_found")
		}
	}
	f.user = users[0]
	created := personalOK(t, f.app.request(t, "POST", playlistsURL, map[string]string{"name": "重试"}, &f.user, map[string]string{"Idempotency-Key": key}), 201)
	id := personalState(t, created).ID
	personalOK(t, f.call("DELETE", playlistsURL+"/"+id, map[string]int{"expected_revision": 1}), 200)
	replay := personalOK(t, f.app.request(t, "POST", playlistsURL, map[string]string{"name": "重试"}, &f.user, map[string]string{"Idempotency-Key": key}), 201)
	var originalBody, replayedBody any
	if err := json.Unmarshal(created.Body.Bytes(), &originalBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayedBody); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(originalBody)
	b, _ := json.Marshal(replayedBody)
	if string(a) != string(b) {
		t.Fatal("重放不是原响应")
	}
	requireAPIError(t, f.call("GET", playlistsURL+"/"+id, nil), 404, "not_found")
	requireAPIError(t, f.app.request(t, "POST", playlistsURL, map[string]string{"name": "不同意图"}, &f.user, map[string]string{"Idempotency-Key": key}), 409, "idempotency_conflict")
	for _, statement := range []string{
		`UPDATE users SET disabled_at=NOW() WHERE id=$1`,
		`UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1`,
		`UPDATE sessions SET expires_at=NOW()-interval '1 hour' WHERE user_id=$1`,
	} {
		mustExec(t, f.app, statement, f.user.id)
		requireAPIError(t, f.app.request(t, "POST", playlistsURL, map[string]string{"name": "重试"}, &f.user, map[string]string{"Idempotency-Key": key}), 401, "unauthorized")
		requireAPIError(t, f.call("GET", favoritesURL+"/tracks", nil), 401, "unauthorized")
		mustExec(t, f.app, `UPDATE users SET disabled_at=NULL WHERE id=$1`, f.user.id)
		mustExec(t, f.app, `UPDATE sessions SET revoked_at=NULL,expires_at=NOW()+interval '1 hour' WHERE user_id=$1`, f.user.id)
	}
}

func TestPersonalHTTPMethodsAndDatabaseFailures(t *testing.T) {
	f := newPersonalFixture(t, 1)
	p := f.create("故障", f.tracks)
	base := playlistsURL + "/" + p.ID
	for _, route := range []struct{ path, allow string }{
		{favoritesURL + "/tracks", "GET, HEAD"}, {favoritesURL + "/tracks/queue", "GET, HEAD"},
		{favoritesURL + "/tracks/" + f.tracks[0], "PUT, DELETE"}, {favoritesURL + "/albums/" + f.groups[0], "PUT, DELETE"},
		{favoritesURL + "/lookup", "POST"}, {playlistsURL, "GET, HEAD, POST"}, {base, "GET, HEAD, PATCH, DELETE"},
		{base + "/items", "GET, HEAD, POST"}, {base + "/queue", "GET, HEAD"}, {base + "/restore", "POST"},
		{base + "/items/" + uuid.New(), "DELETE"}, {base + "/items/" + uuid.New() + "/move", "POST"},
		{playlistsURL + "/containing-track/" + f.tracks[0], "GET, HEAD"},
	} {
		response := f.call("OPTIONS", route.path, nil)
		requireAPIError(t, response, 405, "method_not_allowed")
		if response.Header().Get("Allow") != route.allow {
			t.Fatalf("%s Allow=%q, want %q", route.path, response.Header().Get("Allow"), route.allow)
		}
		if response.Header().Get("Retry-After") != "" {
			t.Fatal("个人错误不应伪造艺人准备态重试头")
		}
	}
	for _, route := range []struct{ method, path, allow string }{
		{"PUT", favoritesURL + "/tracks/queue", "GET, HEAD"},
		{"DELETE", favoritesURL + "/tracks/queue", "GET, HEAD"},
		{"GET", base + "/restore", "POST"},
		{"POST", playlistsURL + "/containing-track/" + f.tracks[0], "GET, HEAD"},
	} {
		response := f.call(route.method, route.path, nil)
		requireAPIError(t, response, 405, "method_not_allowed")
		if response.Header().Get("Allow") != route.allow {
			t.Fatalf("静态路由被通配符吞掉：%s", route.path)
		}
	}
	mustExec(t, f.app, `ALTER TABLE personal_playlists RENAME TO personal_playlists_unavailable`)
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"GET", playlistsURL, nil}, {"GET", base, nil}, {"GET", base + "/items", nil}, {"GET", base + "/queue", nil},
		{"GET", playlistsURL + "/containing-track/" + f.tracks[0], nil}, {"POST", playlistsURL, map[string]string{"name": "不能创建"}},
	} {
		response := f.call(route.method, route.path, route.body)
		requireAPIError(t, response, 503, "database_unavailable")
		requireNoPathLeak(t, f.app, response)
	}
	mustExec(t, f.app, `ALTER TABLE personal_playlists_unavailable RENAME TO personal_playlists`)
	mustExec(t, f.app, `ALTER TABLE favorite_tracks RENAME TO favorite_tracks_unavailable`)
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"GET", favoritesURL + "/tracks", nil}, {"GET", favoritesURL + "/tracks/queue", nil},
		{"POST", favoritesURL + "/lookup", map[string]any{"track_ids": f.tracks}},
		{"PUT", favoritesURL + "/tracks/" + f.tracks[0], nil}, {"DELETE", favoritesURL + "/tracks/" + f.tracks[0], nil},
	} {
		requireAPIError(t, f.call(route.method, route.path, route.body), 503, "database_unavailable")
	}
	mustExec(t, f.app, `ALTER TABLE favorite_tracks_unavailable RENAME TO favorite_tracks`)
	mustExec(t, f.app, `ALTER TABLE sessions RENAME TO sessions_unavailable`)
	requireAPIError(t, f.call(http.MethodGet, favoritesURL+"/tracks", nil), 401, "unauthorized")
}

func TestPersonalHTTPNormalizedInputsAndPageBoundaries(t *testing.T) {
	f := newPersonalFixture(t, 3)
	name := strings.Repeat("😀", 100)
	input := map[string]any{"name": " " + name + " ", "track_ids": []string{strings.ToUpper(f.tracks[0]), f.tracks[1], f.tracks[0], f.tracks[2]}}
	key := strings.ToUpper(uuid.New())
	created := personalOK(t, f.app.request(t, "POST", playlistsURL, input, &f.user, map[string]string{"Idempotency-Key": key}), 201)
	body := personalBody(t, created)
	p := personalState(t, created)
	if p.Name != name || p.TrackCount != 3 || body["added_count"] != float64(3) || body["duplicate_count"] != float64(1) {
		t.Fatal("规范化或重复计数错误")
	}
	input["name"] = name
	input["track_ids"] = []string{f.tracks[0], f.tracks[1], f.tracks[0], f.tracks[2]}
	replay := personalOK(t, f.app.request(t, "POST", playlistsURL, input, &f.user, map[string]string{"Idempotency-Key": strings.ToLower(key)}), 201)
	if personalState(t, replay).ID != p.ID {
		t.Fatal("规范化同意图没有重放")
	}
	base := playlistsURL + "/" + strings.ToUpper(p.ID)
	first := personalBody(t, personalOK(t, f.call("GET", base+"/items?page_size=2&revision=1", nil), 200))
	second := personalBody(t, personalOK(t, f.call("GET", base+"/items?page=2&page_size=2&revision=1", nil), 200))
	firstItems, secondItems := first["items"].([]any), second["items"].([]any)
	if len(firstItems) != 2 || len(secondItems) != 1 || first["page_after_item_id"] != secondItems[0].(map[string]any)["id"] || second["page_after_item_id"] != nil {
		t.Fatal("分页边界错误")
	}
	entries := append(firstItems, secondItems...)
	for i, item := range entries {
		row := item.(map[string]any)
		if row["position"] != float64(i+1) || row["track"].(map[string]any)["track_id"] != f.tracks[i] {
			t.Fatal("分页顺序或位置不连续")
		}
	}
	// before:null 是唯一可接受 null 的动作字段，等价于移至整个歌单最后。
	itemID := firstItems[0].(map[string]any)["id"].(string)
	moved := personalBody(t, personalOK(t, f.call("POST", base+"/items/"+strings.ToUpper(itemID)+"/move", map[string]any{"expected_revision": 1, "action": "before", "before_item_id": nil}), 200))
	if moved["moved"].(map[string]any)["position"] != float64(3) {
		t.Fatal("null 锚点未移到末尾")
	}
	requireAPIError(t, f.call("GET", base+"/items?revision=1", nil), 409, "revision_conflict")
	for _, badName := range []string{strings.Repeat("😀", 101), "   ", "含\x00控制字符"} {
		requireAPIError(t, f.call("POST", playlistsURL, map[string]string{"name": badName}), 400, "invalid_input")
	}
	// 同一幂等键对不同本人仍是独立意图，管理员也没有特例。
	another := f.app.createUser(t, "admin")
	otherCreated := personalOK(t, f.app.request(t, "POST", playlistsURL, input, &another, map[string]string{"Idempotency-Key": key}), 201)
	if personalState(t, otherCreated).ID == p.ID {
		t.Fatal("幂等收据跨用户复用")
	}
	for _, tc := range []struct {
		field string
		limit int
		id    string
	}{{"track_ids", 10000, f.tracks[0]}, {"group_ids", 1000, f.groups[0]}} {
		ids := make([]string, tc.limit)
		for i := range ids {
			ids[i] = strings.ToUpper(tc.id)
		}
		lookup := personalBody(t, personalOK(t, f.call("POST", favoritesURL+"/lookup", map[string]any{tc.field: ids}), 200))
		resultField := "tracks"
		if tc.field == "group_ids" {
			resultField = "albums"
		}
		if len(lookup[resultField].([]any)) != 1 {
			t.Fatal("lookup 未按首次出现去重")
		}
		requireAPIError(t, f.call("POST", favoritesURL+"/lookup", map[string]any{tc.field: append(ids, tc.id)}), 400, "invalid_input")
	}
	for _, kind := range []string{"tracks", "albums"} {
		id := uuid.New()
		requireAPIError(t, f.call("PUT", favoritesURL+"/"+kind+"/"+id, nil), 404, "not_found")
		for i := 0; i < 2; i++ {
			deleted := personalBody(t, personalOK(t, f.call("DELETE", favoritesURL+"/"+kind+"/"+id, nil), 200))
			if deleted["favorited"] != false || deleted["created_at"] != nil || deleted["revision"] != float64(0) {
				t.Fatal("未知目标 DELETE 不应产生关系或版本")
			}
		}
	}
	// 未带鉴权的超大输入也先返回鉴权错误，不提前读取请求体。
	requireAPIError(t, personalRaw(f.app, "POST", playlistsURL, strings.Repeat("x", (1<<20)+1), nil, nil), 401, "unauthorized")
}
