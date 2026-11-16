package stewardtools

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func catalogToolFixture(t *testing.T) (*sql.DB, string, string, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_catalog_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation`)
	root, group, release, medium, track, artist, keyID, nameID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1,'/synthetic-only/mounted-library','active',1)`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'合成专辑','合成艺人',$3,'album','synthetic-anchor','ordinary_directory')`, release, group, root)
	exec(`INSERT INTO media(id,release_id,position,title) VALUES($1,$2,1,'合成介质')`, medium, release)
	for i := 0; i < 55; i++ {
		id := uuid.New()
		if i == 0 {
			id = track
		}
		exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,lyrics,lyrics_source) VALUES($1,$2,$3,'合成曲目','合成艺人',1,$4,$5,'present',NOW(),'physical','synthetic-private-lyrics','vorbis_lyrics')`, id, medium, i+1, root, fmt.Sprintf("album/%02d.flac", i))
	}
	exec(`INSERT INTO artists(id,initial_name) VALUES($1,'合成艺人')`, artist)
	exec(`INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version) VALUES($1,'合成艺人','合成艺人','synthetic','S',1)`, artist)
	hash := sha256.Sum256([]byte("synthetic"))
	exec(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES($1,1,'synthetic',$2,$3)`, keyID, hash[:], artist)
	exec(`INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES($1,$2,'合成艺人',$3,'scan')`, nameID, keyID, hash[:])
	for _, target := range []struct{ release, track, role string }{{release, "", "release_artist"}, {"", track, "track_artist"}} {
		credit := uuid.New()
		exec(`INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,1,'scan')`, credit, target.release, target.track, target.role, nameID)
		exec(`INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,1)`, credit, target.release, target.track, target.role, nameID, artist)
	}
	return db, group, release, track, artist
}
func assertSafeCatalogOutput(t *testing.T, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if strings.HasPrefix(x, "/") || strings.Contains(x, "synthetic-private-lyrics") {
				t.Errorf("unsafe output string=%q", x)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case map[string]any:
			for key, child := range x {
				if key == "lyrics" {
					t.Error("lyrics field leaked")
				}
				walk(child)
			}
		}
	}
	walk(value)
}
func TestPostgreSQLStewardCatalogToolsRolesPrivacyAndPagination(t *testing.T) {
	db, group, release, track, artist := catalogToolFixture(t)
	registry := tool.NewRegistry()
	definitions := StewardTools(catalog.NewStore(db), governance.NewStore(db))
	for _, d := range definitions {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	// 最低身份与 catalog/httpapi 的对应读接口保持一致；曲目摘要沿用已登录的版本详情接口。
	cases := []struct {
		name  string
		role  tool.Role
		input string
	}{{"search_library", tool.RoleMember, `{"q":"合成","type":"tracks"}`}, {"list_albums", tool.RoleMember, `{}`}, {"get_album", tool.RoleMember, `{"id":"` + group + `"}`}, {"get_release", tool.RoleMember, `{"id":"` + release + `","page_size":50}`}, {"get_track", tool.RoleMember, `{"id":"` + track + `"}`}, {"list_artists", tool.RoleMember, `{}`}, {"get_artist", tool.RoleMember, `{"id":"` + artist + `"}`}, {"list_artist_albums", tool.RoleMember, `{"id":"` + artist + `"}`}, {"list_artist_tracks", tool.RoleMember, `{"id":"` + artist + `","role":"track_artist"}`}, {"get_release_evidence", tool.RoleAdmin, `{"id":"` + release + `"}`}, {"list_release_issues", tool.RoleAdmin, `{}`}, {"get_release_issues", tool.RoleAdmin, `{"id":"` + release + `"}`}}
	if len(definitions) != len(cases) {
		t.Fatalf("uncovered definitions=%d cases=%d", len(definitions), len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d tool.Definition
			for _, candidate := range definitions {
				if candidate.Name == tc.name {
					d = candidate
				}
			}
			if d.MinRole != tc.role || d.Scope != tool.ScopeRead || d.Revertible {
				t.Fatalf("wrong contract=%+v", d)
			}
			caller := tool.Caller{User: identity.User{Role: "user"}}
			if tc.role == tool.RoleAdmin {
				_, err := registry.Invoke(context.Background(), caller, tc.name, json.RawMessage(tc.input))
				var denied *tool.Error
				if !errors.As(err, &denied) || denied.Code != tool.CodePermissionDenied {
					t.Fatalf("member error=%v", err)
				}
				caller.User.Role = "admin"
			}
			result, err := registry.Invoke(context.Background(), caller, tc.name, json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			assertSafeCatalogOutput(t, result.Output)
			if tc.name == "get_release" {
				var value struct {
					Media []struct {
						Tracks []any `json:"tracks"`
					} `json:"media"`
					Pagination struct {
						Total int `json:"total"`
					} `json:"pagination"`
				}
				if err := json.Unmarshal(result.Output, &value); err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, m := range value.Media {
					count += len(m.Tracks)
				}
				if count != 50 || value.Pagination.Total != 55 {
					t.Fatalf("page tracks=%d total=%d", count, value.Pagination.Total)
				}
			}
			if strings.Contains(string(d.Input), `"page_size"`) {
				var input map[string]any
				_ = json.Unmarshal([]byte(tc.input), &input)
				input["page_size"] = 51
				raw, _ := json.Marshal(input)
				_, err := registry.Invoke(context.Background(), caller, tc.name, raw)
				var invalid *tool.Error
				if !errors.As(err, &invalid) || invalid.Code != tool.CodeInvalidInput {
					t.Fatalf("page size 51 accepted: %v", err)
				}
			}
		})
	}
	result, err := registry.Invoke(context.Background(), tool.Caller{User: identity.User{Role: "user"}}, "get_release", json.RawMessage(`{"id":"`+release+`","page":2,"page_size":50}`))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Media []struct {
			Tracks []any `json:"tracks"`
		} `json:"media"`
	}
	_ = json.Unmarshal(result.Output, &page)
	count := 0
	for _, m := range page.Media {
		count += len(m.Tracks)
	}
	if count != 5 {
		t.Fatalf("second page=%d", count)
	}
}

func TestPostgreSQLStewardCatalogMissingTargetsReturnNotFound(t *testing.T) {
	db, _, _, _, _ := catalogToolFixture(t)
	registry := tool.NewRegistry()
	for _, definition := range StewardTools(catalog.NewStore(db), governance.NewStore(db)) {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"get_album", "get_release", "get_track", "get_artist", "get_release_evidence", "get_release_issues"} {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Invoke(context.Background(), tool.Caller{User: identity.User{Role: "admin"}}, name, json.RawMessage(`{"id":"`+uuid.New()+`"}`))
			var failure *tool.Error
			if !errors.As(err, &failure) || failure.Code != "not_found" {
				t.Fatalf("目标不存在应返回 not_found，实际 %v", err)
			}
		})
	}
}
