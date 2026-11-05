package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 样本全部经过真实 app handler；仅对本能力清单写入，绝不清空共享样本目录。
func TestPostgreSQLPersonalAPIResponseSamples(t *testing.T) {
	f := newPersonalFixture(t, 3)
	recorder := newAPISampleRecorder(t, f.app)
	recorder.forbid(f.user.token)
	recordPersonalFavoriteSamples(t, f, recorder)
	recordPersonalPlaylistSamples(t, f, recorder)
	recordPersonalRestoreSamples(t, f, recorder)
	recordPersonalLimitSamples(t, recorder)
	finishPersonalSamples(t, recorder)
}

func recordPersonalFavoriteSamples(t *testing.T, f *personalFixture, s *apiSampleRecorder) {
	t.Helper()
	record := func(name, method, path string, body any, status int) *httptest.ResponseRecorder {
		response := f.call(method, path, body)
		s.record(name, response, status)
		return response
	}
	record("favorites/tracks-empty", "GET", favoritesURL+"/tracks", nil, 200)
	record("favorites/albums-empty", "GET", favoritesURL+"/albums", nil, 200)
	record("favorites/lookup-empty", "POST", favoritesURL+"/lookup", map[string]any{}, 200)
	record("favorites/queue-empty", "GET", favoritesURL+"/tracks/queue", nil, 200)
	trackURL, albumURL := favoritesURL+"/tracks/"+f.tracks[0], favoritesURL+"/albums/"+f.groups[0]
	first := record("favorites/track-put", "PUT", trackURL, nil, 200)
	repeated := record("favorites/track-put-repeated", "PUT", trackURL, map[string]any{}, 200)
	if !bytes.Equal(first.Body.Bytes(), repeated.Body.Bytes()) {
		t.Fatal("重复收藏改变原时间或版本")
	}
	record("favorites/album-put", "PUT", albumURL, nil, 200)
	record("favorites/tracks", "GET", favoritesURL+"/tracks", nil, 200)
	record("favorites/albums", "GET", favoritesURL+"/albums", nil, 200)
	record("favorites/tracks-out-of-range", "GET", favoritesURL+"/tracks?page=2", nil, 200)
	record("favorites/lookup", "POST", favoritesURL+"/lookup", map[string]any{"track_ids": []string{f.tracks[0], f.tracks[1], f.tracks[0]}, "group_ids": f.groups[:2]}, 200)
	record("favorites/queue", "GET", favoritesURL+"/tracks/queue", nil, 200)
	mustExec(t, f.app, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, f.tracks[0])
	record("favorites/tracks-missing", "GET", favoritesURL+"/tracks", nil, 200)
	record("favorites/albums-unavailable", "GET", favoritesURL+"/albums", nil, 200)
	record("favorites/queue-missing", "GET", favoritesURL+"/tracks/queue", nil, 200)
	record("errors/personal-favorites-changed", "GET", favoritesURL+"/tracks?revision=0", nil, 409)
	record("favorites/track-delete", "DELETE", trackURL, nil, 200)
	record("favorites/track-delete-repeated", "DELETE", trackURL, nil, 200)
	record("favorites/album-delete", "DELETE", albumURL, nil, 200)
	mustExec(t, f.app, `UPDATE tracks SET source_status='present' WHERE id=$1::uuid`, f.tracks[0])
	record("errors/personal-invalid-input", "PUT", favoritesURL+"/tracks/invalid", nil, 400)
	s.record("errors/personal-invalid-json", personalRaw(f.app, "POST", favoritesURL+"/lookup", "{}{}", &f.user, nil), 400)
	record("errors/personal-invalid-pagination", "GET", favoritesURL+"/tracks?page=0", nil, 400)
	s.record("errors/personal-unauthorized", personalRaw(f.app, "GET", favoritesURL+"/tracks?page=0", "", nil, nil), 401)
	s.record("errors/personal-origin-required", personalRaw(f.app, "POST", favoritesURL+"/lookup", "{", nil, map[string]string{"Origin": ""}), 403)
	s.record("errors/personal-origin-forbidden", personalRaw(f.app, "POST", favoritesURL+"/lookup", "{", nil, map[string]string{"Origin": "http://evil.example"}), 403)
	record("errors/personal-not-found", "PUT", favoritesURL+"/tracks/"+uuid.New(), nil, 404)
	record("errors/personal-method-not-allowed", "OPTIONS", favoritesURL+"/tracks", nil, 405)
	mustExec(t, f.app, `ALTER TABLE favorite_tracks RENAME TO favorite_tracks_unavailable`)
	record("errors/personal-database-unavailable", "GET", favoritesURL+"/tracks", nil, 503)
	mustExec(t, f.app, `ALTER TABLE favorite_tracks_unavailable RENAME TO favorite_tracks`)
}

func recordPersonalPlaylistSamples(t *testing.T, f *personalFixture, s *apiSampleRecorder) {
	t.Helper()
	record := func(name, method, path string, body any, status int) *httptest.ResponseRecorder {
		response := f.call(method, path, body)
		s.record(name, response, status)
		return response
	}
	record("playlists/list-empty", "GET", playlistsURL, nil, 200)
	record("playlists/containing-track-empty", "GET", playlistsURL+"/containing-track/"+f.tracks[0], nil, 200)
	empty := personalState(t, record("playlists/create", "POST", playlistsURL, map[string]any{"name": "晚间聆听"}, 201))
	record("playlists/items-empty", "GET", playlistsURL+"/"+empty.ID+"/items", nil, 200)
	record("playlists/queue-empty", "GET", playlistsURL+"/"+empty.ID+"/queue", nil, 200)
	key := uuid.New()
	input := map[string]any{"name": "周末聆听", "track_ids": f.tracks[:2]}
	created := f.app.request(t, "POST", playlistsURL, input, &f.user, map[string]string{"Idempotency-Key": key, "X-Request-ID": sampleRequestID})
	s.record("playlists/create-with-tracks", created, 201)
	s.record("playlists/create-replayed", f.app.request(t, "POST", playlistsURL, input, &f.user, map[string]string{"Idempotency-Key": key, "X-Request-ID": sampleRequestID}), 201)
	s.record("errors/personal-idempotency-conflict", f.app.request(t, "POST", playlistsURL, map[string]string{"name": "另一份意图"}, &f.user, map[string]string{"Idempotency-Key": key, "X-Request-ID": sampleRequestID}), 409)
	p := personalState(t, created)
	base := playlistsURL + "/" + p.ID
	record("playlists/list", "GET", playlistsURL, nil, 200)
	record("playlists/detail", "GET", base, nil, 200)
	record("playlists/items", "GET", base+"/items?page_size=1", nil, 200)
	record("playlists/items-next-page", "GET", base+"/items?page=2&page_size=1&revision=1", nil, 200)
	record("playlists/items-out-of-range", "GET", base+"/items?page=9", nil, 200)
	record("playlists/queue", "GET", base+"/queue", nil, 200)
	record("playlists/containing-track", "GET", playlistsURL+"/containing-track/"+f.tracks[0], nil, 200)
	mustExec(t, f.app, `UPDATE tracks SET duration_seconds=NULL,source_status='missing' WHERE id=$1::uuid`, f.tracks[1])
	record("playlists/list-unknown-duration", "GET", playlistsURL, nil, 200)
	record("playlists/items-missing", "GET", base+"/items", nil, 200)
	record("playlists/queue-missing", "GET", base+"/queue", nil, 200)
	mustExec(t, f.app, `UPDATE tracks SET duration_seconds=120,source_status='present' WHERE id=$1::uuid`, f.tracks[1])
	renamed := record("playlists/rename", "PATCH", base, map[string]any{"expected_revision": 1, "name": "书房聆听"}, 200)
	p = personalState(t, renamed)
	record("playlists/rename-noop", "PATCH", base, map[string]any{"expected_revision": p.Revision, "name": " 书房聆听 "}, 200)
	record("errors/personal-revision-conflict", "PATCH", base, map[string]any{"expected_revision": 1, "name": "过期修改"}, 409)
	p = personalState(t, record("playlists/add-items", "POST", base+"/items", map[string]any{"expected_revision": p.Revision, "track_ids": f.tracks[2:]}, 200))
	record("playlists/add-items-all-existing", "POST", base+"/items", map[string]any{"expected_revision": p.Revision, "track_ids": f.tracks}, 200)
	record("playlists/add-items-input-duplicates", "POST", base+"/items", map[string]any{"expected_revision": p.Revision, "track_ids": []string{f.tracks[0], f.tracks[0], f.tracks[1]}}, 200)
	entries := f.items(p.ID)
	first, second := entries[0]["id"].(string), entries[1]["id"].(string)
	for _, move := range []struct {
		name, action, item string
		before             any
	}{
		{"move-before", "before", second, first}, {"move-up", "up", first, nil}, {"move-down", "down", first, nil},
		{"move-first", "first", first, nil}, {"move-last", "last", first, nil}, {"move-noop", "last", first, nil},
	} {
		state := f.state(p.ID)
		body := map[string]any{"expected_revision": state.Revision, "action": move.action}
		if move.action == "before" {
			body["before_item_id"] = move.before
		}
		record("playlists/"+move.name, "POST", base+"/items/"+move.item+"/move", body, 200)
	}
	state := f.state(p.ID)
	removed := record("playlists/remove-item", "DELETE", base+"/items/"+first, map[string]any{"expected_revision": state.Revision}, 200)
	record("playlists/restore-item", "POST", base+"/restore", map[string]string{"token": personalToken(t, removed)}, 200)
	state = f.state(p.ID)
	deleteKey := uuid.New()
	deleteBody := map[string]any{"expected_revision": state.Revision}
	deleted := f.app.request(t, "DELETE", base, deleteBody, &f.user, map[string]string{"Idempotency-Key": deleteKey, "X-Request-ID": sampleRequestID})
	s.record("playlists/delete", deleted, 200)
	s.record("playlists/delete-replayed", f.app.request(t, "DELETE", base, deleteBody, &f.user, map[string]string{"Idempotency-Key": deleteKey, "X-Request-ID": sampleRequestID}), 200)
}

func recordPersonalRestoreSamples(t *testing.T, f *personalFixture, s *apiSampleRecorder) {
	t.Helper()
	for _, scenario := range []string{"after-previous", "original-index", "at-end", "already-present"} {
		p := f.create("恢复位置", f.tracks)
		base := playlistsURL + "/" + p.ID
		items := f.items(p.ID)
		token := personalToken(t, f.remove(p.ID, items[1]["id"].(string)))
		switch scenario {
		case "after-previous":
			state := f.state(p.ID)
			personalOK(t, f.call("POST", base+"/items/"+items[0]["id"].(string)+"/move", map[string]any{"expected_revision": state.Revision, "action": "last"}), 200)
		case "original-index", "at-end":
			f.remove(p.ID, items[0]["id"].(string))
			if scenario == "at-end" {
				f.remove(p.ID, items[2]["id"].(string))
			}
		case "already-present":
			state := f.state(p.ID)
			personalOK(t, f.call("POST", base+"/items", map[string]any{"expected_revision": state.Revision, "track_ids": []string{f.tracks[1]}}), 200)
		}
		response := f.call("POST", base+"/restore", map[string]string{"token": token})
		s.record("playlists/restore-"+scenario, response, 200)
		want := map[string]string{"after-previous": "after_previous", "original-index": "original_index", "at-end": "end", "already-present": "unchanged"}[scenario]
		if got := personalBody(t, response)["placement"]; got != want {
			t.Fatalf("恢复分支 %s，得到 %v", scenario, got)
		}
	}
}

func recordPersonalLimitSamples(t *testing.T, s *apiSampleRecorder) {
	t.Helper()
	f := newPersonalFixture(t, 1)
	s.forbid(f.user.token)
	s.forbid(f.app.allowedRoot)
	// 大型边界只批量准备合成目录事实，业务改变与响应仍全部走真实 handler。
	mustExec(t, f.app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,codec,duration_seconds)
 SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,t.medium_id,i+1,'合成曲目 '||i,'合成艺人',1,t.source_root_id,'limit-'||i||'.flac','present',NOW(),'flac_vorbis_comment','flac',120
 FROM tracks t CROSS JOIN generate_series(1,10001) i WHERE t.id=$1::uuid`, f.tracks[0])
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = fmt.Sprintf("20000000-0000-4000-8000-%012d", i+1)
	}
	p := f.create("容量边界", ids)
	base := playlistsURL + "/" + p.ID
	items := f.items(p.ID)
	token := personalToken(t, f.remove(p.ID, items[0]["id"].(string)))
	personalOK(t, f.call("POST", base+"/items", map[string]any{"expected_revision": 2, "track_ids": f.tracks}), 200)
	restored := f.call("POST", base+"/restore", map[string]string{"token": token})
	s.record("playlists/restore-over-limit", restored, 200)
	if got := personalState(t, restored); got.TrackCount != 10001 || got.OverLimitCount != 1 {
		t.Fatalf("合法撤销未超限: %+v", got)
	}
	s.record("playlists/detail-over-limit", f.call("GET", base, nil), 200)
	s.record("errors/personal-playlist-track-limit", f.call("POST", base+"/items", map[string]any{"expected_revision": 4, "track_ids": []string{"20000000-0000-4000-8000-000000010001"}}), 409)
	// 不提交一万条摘要的巨型 golden，但验证完整 HTTP 响应的省略恒等式。
	response := personalOK(t, f.call("GET", base+"/queue", nil), 200)
	body := personalBody(t, response)
	if body["omitted_count"] != float64(1) || body["returned_count"] != float64(10000) || len(body["items"].([]any)) != 10000 || body["truncated"] != true {
		t.Fatalf("队列计数不一致")
	}
	t.Logf("超限歌单队列 HTTP JSON 字节数：%d", response.Body.Len())
	mustExec(t, f.app, `INSERT INTO personal_playlists(id,user_id,name) SELECT gen_random_uuid(),$1::uuid,'配额占位 '||i FROM generate_series(1,999) i`, f.user.id)
	s.record("errors/personal-playlist-limit", f.call("POST", playlistsURL, map[string]string{"name": "超过配额"}), 409)
}

func finishPersonalSamples(t *testing.T, recorder *apiSampleRecorder) {
	t.Helper()
	names := strings.Fields(personalSampleNames)
	if len(recorder.samples) != len(names) {
		t.Fatalf("样本清单 %d 项，实际 %d 项", len(names), len(recorder.samples))
	}
	for _, name := range names {
		content, exists := recorder.samples[name]
		if !exists {
			t.Fatalf("未生成 %s", name)
		}
		path := sampleFilePath(name)
		if os.Getenv(updateAPISamplesEnv) == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, content, 0644); err != nil {
				t.Fatal(err)
			}
		} else {
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(expected, content) {
				t.Errorf("样本 %s 漂移：%s", name, firstDifference(expected, content))
			}
		}
	}
	// 新增或遗漏本能力文件均应显式更新合同，其他能力样本不在本测试范围内。
	if err := filepath.WalkDir(apiSamplesDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(apiSamplesDirectory, path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.ToSlash(relative), ".json")
		if isPersonalAPISample(name) && !isArtistAPISample(name) {
			if _, ok := recorder.samples[name]; !ok {
				t.Errorf("未覆盖的个人样本：%s", name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("已核对 %d 个真实 handler 个人响应样本", len(names))
}

const personalSampleNames = `
favorites/tracks favorites/tracks-empty favorites/tracks-missing favorites/tracks-out-of-range
favorites/albums favorites/albums-empty favorites/albums-unavailable favorites/track-put favorites/track-put-repeated
favorites/track-delete favorites/track-delete-repeated favorites/album-put favorites/album-delete
favorites/lookup favorites/lookup-empty favorites/queue favorites/queue-empty favorites/queue-missing
playlists/list playlists/list-empty playlists/list-unknown-duration playlists/create playlists/create-with-tracks
playlists/create-replayed playlists/detail playlists/detail-over-limit playlists/rename playlists/rename-noop
playlists/delete playlists/delete-replayed playlists/items playlists/items-next-page playlists/items-missing
playlists/items-empty playlists/items-out-of-range playlists/add-items playlists/add-items-all-existing
playlists/add-items-input-duplicates playlists/remove-item playlists/restore-item playlists/restore-after-previous
playlists/restore-original-index playlists/restore-at-end playlists/restore-already-present playlists/restore-over-limit
playlists/move-before playlists/move-up playlists/move-down playlists/move-first playlists/move-last playlists/move-noop
playlists/queue playlists/queue-empty playlists/queue-missing playlists/containing-track playlists/containing-track-empty
errors/personal-invalid-input errors/personal-invalid-json errors/personal-invalid-pagination errors/personal-unauthorized
errors/personal-origin-required errors/personal-origin-forbidden errors/personal-not-found errors/personal-method-not-allowed
errors/personal-revision-conflict errors/personal-idempotency-conflict errors/personal-favorites-changed
errors/personal-playlist-limit errors/personal-playlist-track-limit errors/personal-database-unavailable
`
