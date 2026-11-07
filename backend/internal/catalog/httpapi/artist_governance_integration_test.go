package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type artistHTTPResolution struct{}

func (artistHTTPResolution) RefreshArtistResolution(context.Context, *sql.Tx, map[string]string) error {
	return nil
}
func TestPostgreSQLArtistGovernanceHTTPGuards(t *testing.T) {
	url := postgrestest.NewSchema(t, "roomusic_artist_http_")
	db, err := postgres.Open(context.Background(), url)
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
	admin, member, artist, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'admin','x','admin'),($2::uuid,'member','x','user')`, admin, member)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+interval '1 hour'),($3,$4::uuid,NOW()+interval '1 hour')`, identity.HashSessionToken("admin-token"), admin, identity.HashSessionToken("member-token"), member)
	exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'甲'),($2::uuid,'乙')`, artist, other)
	mux := http.NewServeMux()
	NewArtistGovernance(db, identity.NewGuard(db, false, ""), nil, artistHTTPResolution{}).Register(mux)
	type route struct{ method, path, body string }
	cases := []route{{"POST", "merge", fmt.Sprintf(`{"expected_revision":1,"source_artist_id":%q,"source_expected_revision":1}`, other)},
		{"POST", "split-preview", fmt.Sprintf(`{"mode":"aliases","name_key_ids":[%q]}`, uuid.New())},
		{"POST", "split", fmt.Sprintf(`{"expected_revision":1,"mode":"aliases","name_key_ids":[%q],"basis":%q,"target":{"kind":"new"}}`, uuid.New(), strings.Repeat("a", 64))},
		{"PATCH", "name", `{"expected_revision":1,"mode":"set","name":"新名"}`},
		{"POST", "candidate-ignore", fmt.Sprintf(`{"expected_revision":1,"other_artist_id":%q,"other_expected_revision":1,"key_pairs":[{"left_key_id":%q,"right_key_id":%q}],"ignored":true}`, other, uuid.New(), uuid.New())}}
	call := func(method, path, body, token, origin, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, c := range cases {
		path := "/api/v1/artists/" + artist + "/" + c.path
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {"member-token", 403}} {
			w := call(c.method, path, c.body, actor.token, "http://example.com", uuid.New())
			if w.Code != actor.status {
				t.Fatalf("%s actor %s: %d %s", c.path, actor.token, w.Code, w.Body.String())
			}
		}
		w := call(c.method, path, c.body, "admin-token", "", uuid.New())
		if w.Code != 403 {
			t.Fatal("missing origin not rejected")
		}
		w = call(c.method, path, c.body, "admin-token", "http://example.com", uuid.New())
		want := 409
		code := "artist_catalog_not_ready"
		if c.path == "split-preview" {
			want = 503
			code = "artist_catalog_preparing"
		}
		if w.Code != want || !strings.Contains(w.Body.String(), code) {
			t.Fatalf("%s readiness: %d %s", c.path, w.Code, w.Body.String())
		}
	}
	for _, token := range []string{"member-token", "admin-token"} {
		w := call("GET", "/api/v1/artist-candidates", "", token, "", "")
		want := 403
		if token == "admin-token" {
			want = 503
		}
		if w.Code != want {
			t.Fatalf("candidate guard: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected operations persisted %d %v", count, err)
	}
	// 同一键的整理期拒绝不消费幂等记录，ready 后可以执行，再次提交精确重放。
	key := uuid.New()
	path := "/api/v1/artists/" + artist + "/name"
	body := `{"expected_revision":1,"mode":"set","name":"新名"}`
	w := call("PATCH", path, body, "admin-token", "http://example.com", key)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	w = call("PATCH", path, body, "admin-token", "http://example.com", key)
	if w.Code != 200 {
		t.Fatalf("rename ready: %d %s", w.Code, w.Body.String())
	}
	first := w.Body.String()
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["display_name"] != "新名" {
		t.Fatal("zero-credit rename not reflected", response, err)
	}
	w = call("PATCH", path, body, "admin-token", "http://example.com", key)
	if w.Code != 200 || !artistHTTPJSONEqual(w.Body.String(), first) {
		t.Fatal("HTTP replay mismatch")
	}
	for _, invalid := range []string{`{"expected_revision":2,"mode":"auto","name":null}`, `{"expected_revision":2,"mode":"auto","unknown":true}`, `{"expected_revision":2,"expected_revision":1,"mode":"auto"}`} {
		w = call("PATCH", path, invalid, "admin-token", "http://example.com", uuid.New())
		if w.Code != 400 {
			t.Fatalf("strict HTTP accepted %s", invalid)
		}
	}
}

func artistHTTPJSONEqual(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}
