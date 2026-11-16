package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
)

const tokenPath = "me/steward/tokens"

func newTokenAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	f := newAPIFixture(t)
	f.api.WithTokens(token.NewStore(f.db, identity.NewGuard(f.db, false, "")))
	mux := http.NewServeMux()
	f.api.Register(mux)
	f.handler = httpserver.Middleware(f.api.logger, mux)
	return f
}
func issueAPIToken(t *testing.T, f *apiFixture, role string) map[string]any {
	t.Helper()
	w := f.request(role, "POST", tokenPath, map[string]any{"name": "合成令牌", "scope": "read"})
	apiStatus(t, w, 201, "")
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
func tokenKeys(t *testing.T, body map[string]any, want ...string) {
	t.Helper()
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("字段=%v，期望 %v", keys, want)
	}
}

func TestPostgreSQLTokenAPIShapesOwnershipAndPrivacy(t *testing.T) {
	f := newTokenAPIFixture(t)
	body := issueAPIToken(t, f, "member")
	tokenKeys(t, body, "id", "name", "scope", "tail", "created_at", "expires_at", "last_used_at", "token")
	plaintext, id := body["token"].(string), body["id"].(string)
	if len(plaintext) != 47 || body["tail"] != plaintext[len(plaintext)-4:] || body["expires_at"] != nil || body["last_used_at"] != nil {
		t.Fatal("签发响应值不正确")
	}
	if _, err := time.Parse(time.RFC3339Nano, body["created_at"].(string)); err != nil {
		t.Fatal(err)
	}
	listed := f.request("member", "GET", tokenPath, nil)
	apiStatus(t, listed, 200, "")
	var list struct {
		Available bool             `json:"available"`
		Tokens    []map[string]any `json:"tokens"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Available || len(list.Tokens) != 1 || list.Tokens[0]["id"] != id {
		t.Fatal("列表未返回本人令牌")
	}
	tokenKeys(t, list.Tokens[0], "id", "name", "scope", "tail", "created_at", "expires_at", "last_used_at")
	for _, role := range []string{"other", "admin"} {
		apiStatus(t, f.request(role, "DELETE", tokenPath+"/"+id, nil), 404, "not_found")
		w := f.request(role, "GET", tokenPath, nil)
		apiStatus(t, w, 200, "")
		if strings.Contains(w.Body.String(), id) {
			t.Fatal("他人令牌出现在列表")
		}
	}
	apiStatus(t, f.request("member", "DELETE", tokenPath+"/"+id, nil), 204, "")
	apiStatus(t, f.request("member", "DELETE", tokenPath+"/"+id, nil), 404, "not_found")
	after := f.request("member", "GET", tokenPath, nil)
	apiStatus(t, after, 200, "")
	if strings.Contains(after.Body.String(), id) {
		t.Fatal("吊销后仍出现在列表")
	}
	var hash []byte
	var revoked bool
	if err := f.db.QueryRow(`SELECT token_hash,revoked_at IS NOT NULL FROM steward_access_tokens WHERE id=$1`, id).Scan(&hash, &revoked); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, token.Hash(plaintext)) || !revoked {
		t.Fatal("哈希或吊销状态不正确")
	}
	// 扫描当前隔离 schema 的全部普通表、全部列，包括 bytea 的十六进制表示。
	rows, err := f.db.Query(`SELECT format('%I.%I',schemaname,tablename) FROM pg_tables WHERE schemaname=current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		var found bool
		if err := f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` t WHERE strpos(to_jsonb(t)::text,$1)>0 OR strpos(to_jsonb(t)::text,$2)>0)`, plaintext, hex.EncodeToString([]byte(plaintext))).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found {
			t.Errorf("表 %s 含明文", table)
		}
	}
	for _, value := range []string{listed.Body.String(), after.Body.String(), f.logs.String()} {
		if strings.Contains(value, plaintext) {
			t.Error("列表或日志泄露明文")
		}
	}
}

func TestPostgreSQLTokenAPIMemberSwitchAndOrigin(t *testing.T) {
	f := newTokenAPIFixture(t)
	body := issueAPIToken(t, f, "member")
	id := body["id"].(string)
	if err := f.store.SetMembersEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	apiStatus(t, f.request("member", "POST", tokenPath, map[string]string{"name": "合成", "scope": "read"}), 403, "steward_disabled")
	listed := f.request("member", "GET", tokenPath, nil)
	apiStatus(t, listed, 200, "")
	if !strings.Contains(listed.Body.String(), `"available":false`) || !strings.Contains(listed.Body.String(), id) {
		t.Fatal("成员开关不应隐藏已有令牌")
	}
	for _, method := range []string{"POST", "DELETE"} {
		for _, origin := range []string{"", "http://untrusted.invalid"} {
			path := tokenPath
			if method == "DELETE" {
				path += "/" + id
			}
			r := httptest.NewRequest(method, "http://roomusic.test/api/v1/"+path, strings.NewReader(`{"name":"合成","scope":"read"}`))
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.tokens["member"]})
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			code := "origin_required"
			if origin != "" {
				code = "origin_forbidden"
			}
			apiStatus(t, w, 403, code)
		}
	}
	apiStatus(t, f.request("member", "DELETE", tokenPath+"/"+id, nil), 204, "")
	issueAPIToken(t, f, "admin")
	if err := f.store.SetMembersEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	issueAPIToken(t, f, "member")
}

func TestPostgreSQLTokenAPIInputBoundaries(t *testing.T) {
	f := newTokenAPIFixture(t)
	for _, body := range []any{
		map[string]any{"name": " ", "scope": "read"}, map[string]any{"name": strings.Repeat("名", 65), "scope": "read"},
		map[string]any{"name": "合成", "scope": "unknown"}, map[string]any{"name": "合成", "scope": "read", "expires_at": "invalid"},
		map[string]any{"name": "合成", "scope": "read", "expires_at": time.Now().Add(-time.Hour)},
	} {
		apiStatus(t, f.request("admin", "POST", tokenPath, body), 400, "invalid_input")
	}
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w := f.request("admin", "POST", tokenPath, map[string]any{"name": "  " + strings.Repeat("名", 64) + "  ", "scope": "read_write", "expires_at": expires})
	apiStatus(t, w, 201, "")
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != strings.Repeat("名", 64) || body["scope"] != "read_write" || body["expires_at"] != expires.Format(time.RFC3339) {
		t.Fatal("边界输入未正确保存")
	}
}

func TestPostgreSQLTokenAPIConcurrentLimit(t *testing.T) {
	f := newTokenAPIFixture(t)
	var id string
	for range 19 {
		id = issueAPIToken(t, f, "member")["id"].(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1,hashtext($2))`, int32(0x53544f4b), f.users["member"]); err != nil {
		t.Fatal(err)
	}
	responses := make(chan *httptest.ResponseRecorder, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			responses <- f.request("member", "POST", tokenPath, map[string]string{"name": fmt.Sprintf("并发 %d", i), "scope": "read"})
		}(i)
	}
	// 持有配额锁，确认四个签发事务全部进入等待，再同时放行。
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND classid=$1 AND objid=(hashtext($2)::bigint & 4294967295)::oid AND NOT granted`, int64(0x53544f4b), f.users["member"]).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("签发事务未全部进入等待：%d", waiting)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := lock.Commit(); err != nil {
		t.Fatal(err)
	}
	succeeded := 0
	for range 4 {
		w := <-responses
		if w.Code == 201 {
			succeeded++
		} else {
			apiStatus(t, w, 409, "token_limit_reached")
		}
	}
	if succeeded != 1 {
		t.Fatalf("并发成功数=%d，期望 1", succeeded)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM steward_access_tokens WHERE user_id=$1 AND revoked_at IS NULL`, f.users["member"]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 20 {
		t.Fatalf("未吊销令牌数=%d", count)
	}
	if _, err := f.db.Exec(`UPDATE steward_access_tokens SET expires_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	apiStatus(t, f.request("member", "POST", tokenPath, map[string]string{"name": "超限", "scope": "read"}), 409, "token_limit_reached")
	apiStatus(t, f.request("member", "DELETE", tokenPath+"/"+id, nil), 204, "")
	issueAPIToken(t, f, "member")
	issueAPIToken(t, f, "other")
}
