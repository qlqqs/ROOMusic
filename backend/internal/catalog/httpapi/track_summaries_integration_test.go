package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLTrackSummariesHTTPContract(t *testing.T) {
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "roomusic_track_summaries_http_"))
	if err != nil {
		t.Fatal(err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(context.Background(), db); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, token := uuid.New(), identity.GenerateSessionToken()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'member','test','user')`, user)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,now()+interval '1 hour')`, identity.HashSessionToken(token), user)
	h := New(db, identity.NewGuard(db, false, "http://roomusic.test"), t.TempDir(), nil)
	mux := http.NewServeMux()
	// 只装配本切片；生产路由由主会话集中注册。
	mux.HandleFunc("POST /api/v1/tracks/summaries", h.trackSummaries)
	request := func(body, origin string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://roomusic.test/api/v1/tracks/summaries", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		w.Header().Set("X-Request-ID", "summary-request")
		mux.ServeHTTP(w, r)
		return w
	}
	post := func(body string) *httptest.ResponseRecorder { return request(body, "http://roomusic.test", true) }
	decode := func(w *httptest.ResponseRecorder) []catalog.TrackSummary {
		t.Helper()
		var payload struct {
			Items []catalog.TrackSummary `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.Items == nil {
			t.Fatalf("摘要响应无效: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("响应头无效: %v", w.Header())
		}
		for _, secret := range []string{"/synthetic", "relative_path", "source_root_id", "fingerprint", "password_hash", token} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("摘要泄露私有字段 %q", secret)
			}
		}
		return payload.Items
	}
	assertSummaryHTTPError(t, request(`{}`, "http://roomusic.test", false), 401, "unauthorized")
	assertSummaryHTTPError(t, request(`{}`, "http://foreign.test", true), 403, "origin_forbidden")
	assertSummaryHTTPError(t, request(`{}`, "", true), 403, "origin_required")
	for _, body := range []string{
		`{"track_ids":[]}`, summaryIDsBody(summaryTestIDs(1001)), `{"track_ids":null}`,
		`{"track_ids":["invalid"]}`, `{"track_ids":[null]}`, `{"track_ids":{}}`, `null`,
		`{"track_ids":[],"other":true}`, `{"track_ids":[],"track_ids":["` + summaryTrackA + `"]}`,
	} {
		assertSummaryHTTPError(t, post(body), 400, "invalid_input")
	}
	assertSummaryHTTPError(t, post(`{"track_ids":`), 400, "invalid_json")
	if items := decode(post(summaryIDsBody(summaryTestIDs(1000)))); len(items) != 0 {
		t.Fatal("1000 个未知 ID 应返回空数组")
	}

	root, artist, key, name := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/synthetic/private-root','active',1)`, root)
	exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'原署名')`, artist)
	exec(`INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version)
 VALUES($1::uuid,'原署名','主显示名','name','N',1)`, artist)
	hash := sha256.Sum256([]byte("原署名"))
	exec(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
 VALUES($1::uuid,1,'原署名',$2,$3::uuid)`, key, hash[:], artist)
	exec(`INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES($1::uuid,$2::uuid,'原署名',$3,'scan')`, name, key, hash[:])
	for i, track := range []string{summaryTrackA, summaryTrackB} {
		group, release, medium := uuid.New(), uuid.New(), uuid.New()
		status := "present"
		if i == 1 {
			status = "missing"
		}
		exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
		exec(`INSERT INTO releases(id,group_id,title,artist,album_artist) VALUES($1::uuid,$2::uuid,'合成专辑','原署名','原署名')`, release, group)
		exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'碟')`, medium, release)
		exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds,codec,bit_depth,sample_rate,channels)
 VALUES($1::uuid,$2::uuid,1,'合成曲目','原署名',$3::uuid,$4,$5,now(),'physical',123.5,'flac',24,96000,2)`, track, medium, root, fmt.Sprintf("private-%d.flac", i), status)
		for _, role := range []string{"release_artist", "album_artist", "track_artist"} {
			credit, releaseID, trackID := uuid.New(), release, ""
			if role == "track_artist" {
				releaseID, trackID = "", track
			}
			exec(`INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer)
 VALUES($1::uuid,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,1,'scan')`, credit, releaseID, trackID, role, name)
			exec(`INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
 VALUES($1::uuid,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,$6::uuid,1)`, credit, releaseID, trackID, role, name, artist)
		}
	}
	body := summaryIDsBody([]string{strings.ToUpper(summaryTrackB), uuid.New(), summaryTrackA, summaryTrackB, strings.ToUpper(summaryTrackA)})
	assertItems := func(items []catalog.TrackSummary, ready bool) {
		t.Helper()
		if len(items) != 2 || items[0].TrackID != summaryTrackB || items[1].TrackID != summaryTrackA || items[0].Present || !items[1].Present {
			t.Fatalf("去重、顺序、未知或缺失语义不符: %+v", items)
		}
		if items[0].Release.TrackCount != 0 || items[0].Release.MediumCount != 0 {
			t.Fatalf("完全缺失的发行摘要必须保留: %+v", items[0])
		}
		for _, item := range items {
			if item.Artist != "原署名" || item.DurationSeconds == nil || *item.DurationSeconds != 123.5 || item.Source.SourceKind != "physical" || item.Source.Codec == nil || *item.Source.Codec != "flac" || item.Source.SampleRate == nil || *item.Source.SampleRate != 96000 || item.Source.Bitrate != nil || item.GroupID == "" || item.Release.ID == "" {
				t.Fatalf("安全摘要事实不符: %+v", item)
			}
			for _, refs := range [][]catalog.ArtistRef{item.ArtistRefs, item.Release.ArtistRefs, item.Release.AlbumArtistRefs} {
				if refs == nil || (!ready && len(refs) != 0) || (ready && (len(refs) != 1 || refs[0].ArtistID != artist || refs[0].Name != "原署名" || refs[0].DisplayName != "主显示名")) {
					t.Fatalf("艺人引用不符: ready=%v refs=%+v", ready, refs)
				}
			}
		}
	}
	// 即使已有部分关联，整理期也只返回原文与必传空数组，不能拒绝整个摘要请求。
	assertItems(decode(post(body)), false)
	exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,
 published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	assertItems(decode(post(body)), true)
	// 整批读取失败必须安全失败，不能把已存在曲目伪装成未知并返回空数组。
	exec(`DROP VIEW release_effective`)
	failed := post(body)
	if strings.Contains(failed.Body.String(), "release_effective") || strings.Contains(failed.Body.String(), "SELECT") {
		t.Fatalf("数据库故障泄露内部信息: %s", failed.Body.String())
	}
	assertSummaryHTTPError(t, failed, 503, "database_unavailable")
}
