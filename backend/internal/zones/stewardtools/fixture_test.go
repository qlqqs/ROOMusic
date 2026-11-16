package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

type c5Fixture struct {
	db             *sql.DB
	registry       *tool.Registry
	a, b           string
	release, group string
	tracks         []string
	service        *zones.Service
}

func newC5Fixture(t *testing.T) *c5Fixture {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_c5_zones_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &c5Fixture{db: db, registry: tool.NewRegistry(), a: uuid.New(), b: uuid.New(), release: uuid.New(), group: uuid.New()}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'c5-a','synthetic','user'),($2,'c5-b','synthetic','user')`, f.a, f.b)
	root, medium := uuid.New(), uuid.New()
	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1,'/synthetic/c5','active',1)`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1)`, f.group)
	exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'合成专辑','合成艺人',$3,'album','c5','ordinary_directory')`, f.release, f.group, root)
	exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, f.release)
	for i := 0; i < 2; i++ {
		id := uuid.New()
		f.tracks = append(f.tracks, id)
		exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at,duration_seconds) VALUES($1,$2,$3,'合成曲目','合成艺人',$4,$5,NOW(),180)`, id, medium, i+1, root, fmt.Sprintf("album/%d.flac", i))
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation`)
	f.service = zones.NewService(db, zones.Options{Renderer: zonestest.NewRenderer(zonestest.Options{})})
	t.Cleanup(f.service.Close)
	for _, d := range StewardTools(f.service, catalog.NewStore(db)) {
		if d.MinRole != tool.RoleMember || d.Revertible {
			t.Fatalf("工具合同错误：%+v", d)
		}
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *c5Fixture) invoke(t *testing.T, user, name string, input any, want string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.registry.Invoke(context.Background(), tool.Caller{User: identity.User{ID: user, Role: "user"}}, name, raw)
	if want != "" {
		var failure *tool.Error
		if !errors.As(err, &failure) || string(failure.Code) != want {
			t.Fatalf("%s: err=%v want=%s", name, err, want)
		}
		return nil
	}
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var value any
	if err := json.Unmarshal(result.Output, &value); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch k {
				case "stream", "next_stream", "output_session_id", "url":
					t.Errorf("%s 输出包含 %s", name, k)
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case string:
			if strings.Contains(x, "/api/v1/me/playback/streams/") {
				t.Errorf("%s 泄露流地址", name)
			}
		}
	}
	walk(value)
	return result.Output
}
