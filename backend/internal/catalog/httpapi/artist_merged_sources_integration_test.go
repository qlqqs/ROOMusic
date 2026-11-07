package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistMergedSourcesHTTP(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_merged_sources_http_"))
	if err != nil {
		t.Fatal(err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(context.Background(), db); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	defer db.Close()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	admin, member, artist, source := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'admin','x','admin'),($2::uuid,'member','x','user')`, admin, member)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,now()+interval '1 hour'),($3,$4::uuid,now()+interval '1 hour')`, identity.HashSessionToken("merged-admin"), admin, identity.HashSessionToken("merged-member"), member)
	exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'甲'),($2::uuid,'乙')`, artist, source)
	h := New(db, identity.NewGuard(db, false, ""), t.TempDir(), nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artists/{id}/merged-sources", h.artistMergedSources)
	call := func(id, token string, status int, code string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/artists/"+id+"/merged-sources", nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status || (code != "" && !strings.Contains(w.Body.String(), `"`+code+`"`)) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body.String())
		}
		return w
	}
	for _, id := range []string{artist, "bad", uuid.New()} {
		call(id, "", 401, "unauthorized")
		call(id, "merged-member", 403, "forbidden")
	}
	call("bad", "merged-admin", 400, "invalid_id")
	w := call(artist, "merged-admin", 503, "artist_catalog_preparing")
	if w.Header().Get("Retry-After") != "3" {
		t.Fatal("缺少 Retry-After")
	}
	call(uuid.New(), "merged-admin", 503, "artist_catalog_preparing")
	exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	call(uuid.New(), "merged-admin", 404, "not_found")
	w = call(artist, "merged-admin", 200, "")
	if strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
		t.Fatal(w.Body.String())
	}
	governor := governance.NewArtistGovernance(artistHTTPResolution{})
	command := governance.ArtistMergeRequest{ArtistID: artist, ExpectedRevision: 1, SourceArtistID: source, SourceExpectedRevision: 1}
	merged, err := operations.Run(ctx, db, operations.Spec{ActorID: admin, Key: uuid.New(), Type: operations.TypeArtistMerge, Fingerprint: command.Fingerprint()}, governor.MergeExecutor(command))
	if err != nil {
		t.Fatal(err)
	}
	w = call(strings.ToUpper(artist), "merged-admin", 200, "")
	var result governance.ArtistMergedSources
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("%s %v", w.Body.String(), err)
	}
	item := result.Items[0]
	if item.ArtistID != source || item.Name != "乙" || item.Revision != 2 || item.OperationID != merged.OperationID || item.MergedAt.IsZero() || item.NameKeyIDs == nil || len(item.NameKeyIDs) != 0 {
		t.Fatal(item)
	}
	call(source, "merged-admin", 200, "")
	// 真实依赖故障使用安全通用错误，不能伪装空列表或整理状态。
	exec(`DROP TABLE artist_profiles CASCADE`)
	w = call(artist, "merged-admin", 503, "database_unavailable")
	if strings.Contains(w.Body.String(), "artist_profiles") || strings.Contains(w.Body.String(), "SELECT") {
		t.Fatal("泄漏 SQL", w.Body.String())
	}
}
