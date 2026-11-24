package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
)

const (
	b3AcoustIDPath = curationBase + "/sources/acoustid/keys/"
	b3AppKey       = "fixture-application-9123"
	b3UserKey      = "fixture-user-8675"
	b3EmptyLookup  = `{"status":"ok","results":[]}`
)

func b3HTTPClient(server *httptest.Server) sources.ClientFactory {
	return func(egress.Options) (*http.Client, error) { return server.Client(), nil }
}

func b3AcoustOptions(server *httptest.Server) acoustid.Options {
	return acoustid.Options{BaseURL: server.URL, NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil)}
}

func b3LookupForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	body, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Error(err)
		return nil
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Error(err)
		return nil
	}
	form, err := url.ParseQuery(string(data))
	if err != nil {
		t.Error(err)
	}
	return form
}

func b3SetHTTPKey(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, name, key string) curation.SourceSettings {
	t.Helper()
	response := app.request(t, http.MethodPut, b3AcoustIDPath+name, map[string]string{"key": key}, admin, nil)
	var item curation.SourceSettings
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &item) != nil {
		t.Fatalf("填写 %s 返回 HTTP %d %s", name, response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), strings.TrimSpace(key)) {
		t.Fatal("响应中包含完整的合成密钥")
	}
	return item
}

func b3SourceList(t *testing.T, app *integrationTestApplication, admin *integrationTestUser) map[string]curation.SourceSettings {
	t.Helper()
	var raw struct{ Items []json.RawMessage }
	getJSON(t, app, admin, curationBase+"/sources", &raw)
	items := make(map[string]curation.SourceSettings)
	for _, payload := range raw.Items {
		var item curation.SourceSettings
		if err := json.Unmarshal(payload, &item); err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(payload, &object); err != nil {
			t.Fatal(err)
		}
		if _, present := object["keys"]; present != (item.ID == "acoustid" || item.ID == "fanarttv" || item.ID == "theaudiodb") {
			t.Errorf("源 %s 的 keys 字段出现=%t", item.ID, present)
		}
		if bytes.Contains(payload, []byte(b3AppKey)) || bytes.Contains(payload, []byte(b3UserKey)) {
			t.Error("源列表包含完整的合成密钥")
		}
		items[item.ID] = item
	}
	if len(items) != 10 {
		t.Fatalf("源列表长度=%d，期望 10", len(items))
	}
	return items
}

func b3Reason(t *testing.T, item curation.SourceSettings, want string) {
	t.Helper()
	var reason string
	if item.InactiveReason != nil {
		reason = *item.InactiveReason
	}
	if reason != want || item.Active != (want == "") {
		t.Errorf("源 %s active=%t 原因=%q，期望原因=%q", item.ID, item.Active, reason, want)
	}
}

func b3Changes(t *testing.T, app *integrationTestApplication) time.Time {
	t.Helper()
	changes, err := curation.LoadProgramChanges(t.Context(), app.database, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return changes.Sources.Time
}

type b3KeyRow struct {
	ciphertext []byte
	status     string
	updated    time.Time
}

func b3StoredKey(t *testing.T, app *integrationTestApplication, name string) b3KeyRow {
	t.Helper()
	var row b3KeyRow
	if err := app.database.QueryRow(`SELECT ciphertext,status,updated_at FROM curation_source_keys WHERE source='acoustid' AND name=$1`, name).
		Scan(&row.ciphertext, &row.status, &row.updated); err != nil {
		t.Fatal(err)
	}
	return row
}

type b3FingerprintFinderFunc func(context.Context, sources.Fingerprint) ([]sources.FingerprintMatch, error)

func (f b3FingerprintFinderFunc) LookupFingerprint(ctx context.Context, fp sources.Fingerprint) ([]sources.FingerprintMatch, error) {
	return f(ctx, fp)
}

func TestPostgreSQLB3AcoustIDBundledKeyGate(t *testing.T) {
	const sourceID = "acoustid"
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	b3Enable(t, app, admin)
	ctx := t.Context()
	store := newInstanceSecrets(app.dataDirectory)

	declaration := sources.Declaration{
		ID: sourceID, Name: "AcoustID", DefaultEnabled: true,
		Capabilities: []sources.Capability{sources.FingerprintLookup}, Keys: []string{"user"},
	}
	legacy := declaration
	legacy.Keys = []string{"application", "user"}
	legacyGate := curation.NewSources(app.database, curation.SourceRegistration{Declaration: legacy}).WithKeyStore(store)
	if _, err := legacyGate.SetKey(ctx, sourceID, "application", admin.id, b3AppKey); err != nil {
		t.Fatal(err)
	}
	applicationBefore := b3StoredKey(t, app, "application")

	var unavailable string
	var gate *curation.Sources
	gate = curation.NewSources(app.database, curation.SourceRegistration{
		Declaration: declaration,
		Fingerprints: b3FingerprintFinderFunc(func(ctx context.Context, _ sources.Fingerprint) ([]sources.FingerprintMatch, error) {
			// 用 finder 收到的上下文读取密钥，让闸门记录本次用到的密文。
			keys, err := gate.Keys(ctx, sourceID)
			if err != nil {
				return nil, err
			}
			if !reflect.DeepEqual(keys, map[string]string{"user": b3UserKey}) {
				t.Error("指纹查询应只读到合成用户密钥")
			}
			return nil, &sources.InvalidKeyError{Name: "application"}
		}),
		Unavailable: func(context.Context, string) string { return unavailable },
	}).WithKeyStore(store)
	settings := func() curation.SourceSettings {
		t.Helper()
		items, err := gate.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != sourceID {
			t.Fatalf("源列表=%+v，期望只有 AcoustID", items)
		}
		return items[0]
	}
	item := settings()
	b3Reason(t, item, "key_missing")
	if want := []curation.SourceKeySettings{{Name: "user", Status: "unchecked"}}; !reflect.DeepEqual(item.Keys, want) {
		t.Fatalf("未填写时密钥列表=%+v，期望 %+v", item.Keys, want)
	}

	item, err := gate.SetKey(ctx, sourceID, "user", admin.id, b3UserKey)
	if err != nil {
		t.Fatal(err)
	}
	b3Reason(t, item, "")
	wantKeys := []curation.SourceKeySettings{{Name: "user", Configured: true, LastFour: "8675", Status: "unchecked"}}
	if !reflect.DeepEqual(item.Keys, wantKeys) {
		t.Fatalf("填写后密钥列表=%+v，期望 %+v", item.Keys, wantKeys)
	}
	if err := gate.Check(ctx, sourceID); err != nil {
		t.Fatalf("填写用户密钥后闸门返回 %v", err)
	}
	if _, err := gate.ClearKey(ctx, sourceID, "application", admin.id); err == nil {
		t.Fatal("清除旧应用密钥成功，期望 404 not_found")
	} else if status, code, _ := operations.Problem(err); status != http.StatusNotFound || code != "not_found" {
		t.Fatalf("清除旧应用密钥返回 %d %s，期望 404 not_found", status, code)
	}

	finder, ok := gate.FingerprintFinder(sourceID)
	if !ok {
		t.Fatal("指纹源未注册")
	}
	_, err = finder.LookupFingerprint(ctx, sources.Fingerprint{Value: "bundled-gate", DurationSeconds: 20})
	var invalid *sources.InvalidKeyError
	if !errors.As(err, &invalid) || invalid.Name != "application" {
		t.Fatalf("指纹查询错误=%v，期望应用密钥无效", err)
	}
	item = settings()
	b3Reason(t, item, "")
	if !reflect.DeepEqual(item.Keys, wantKeys) {
		t.Errorf("拒绝应用密钥后密钥列表=%+v，期望 %+v", item.Keys, wantKeys)
	}
	if got := b3StoredKey(t, app, "application"); !reflect.DeepEqual(got, applicationBefore) {
		t.Error("旧应用密钥行发生变化")
	}

	unavailable = "bundled_key_rejected"
	b3Reason(t, settings(), unavailable)
	if err := gate.Check(ctx, sourceID); err == nil {
		t.Fatal("内置密钥被拒后闸门仍放行，期望 409 source_disabled")
	} else if status, code, _ := operations.Problem(err); status != http.StatusConflict || code != "source_disabled" {
		t.Errorf("内置密钥被拒后闸门返回 %d %s，期望 409 source_disabled", status, code)
	}
}

func TestPostgreSQLB3AcoustIDKeyAPI(t *testing.T) {
	tool := b3RealFingerprinter(t)
	app := newIntegrationTestApplication(t, WithFingerprinter(tool))
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	item := b3SourceList(t, app, &admin)["acoustid"]
	b3Reason(t, item, "curation_disabled")
	if !item.Enabled || !reflect.DeepEqual(item.Keys, []curation.SourceKeySettings{{Name: "application", Status: "unchecked"}, {Name: "user", Status: "unchecked"}}) {
		t.Fatalf("初始密钥列表=%+v", item)
	}
	if _, err := writer.SetSettings(t.Context(), admin.id, true); err != nil {
		t.Fatal(err)
	}
	b3Reason(t, b3SourceList(t, app, &admin)["acoustid"], "key_missing")
	for _, tc := range []struct {
		method, path, key string
		actor             *integrationTestUser
		status            int
		code              string
	}{
		{"PUT", b3AcoustIDPath + "application", b3AppKey, &member, 403, "forbidden"},
		{"DELETE", b3AcoustIDPath + "application", "", &member, 403, "forbidden"},
		{"PUT", b3AcoustIDPath + "application", " \t ", &admin, 400, "invalid_input"},
		{"PUT", b3AcoustIDPath + "application", "ascii\x00only", &admin, 400, "invalid_input"},
		{"PUT", b3AcoustIDPath + "application", "合成", &admin, 400, "invalid_input"},
		{"PUT", b3AcoustIDPath + "application", strings.Repeat("k", 129), &admin, 400, "invalid_input"},
		{"PUT", curationBase + "/sources/musicbrainz/keys/application", b3AppKey, &admin, 400, "key_not_supported"},
		{"DELETE", curationBase + "/sources/musicbrainz/keys/application", "", &admin, 400, "key_not_supported"},
		{"PUT", curationBase + "/sources/unknown/keys/application", b3AppKey, &admin, 404, "not_found"},
		{"DELETE", curationBase + "/sources/unknown/keys/application", "", &admin, 404, "not_found"},
		{"PUT", b3AcoustIDPath + "wrong", b3AppKey, &admin, 404, "not_found"},
		{"DELETE", b3AcoustIDPath + "wrong", "", &admin, 404, "not_found"},
	} {
		t.Run(fmt.Sprintf("%s_%s_%d_%d", tc.method, tc.path, tc.status, len(tc.key)), func(t *testing.T) {
			var body any
			if tc.method == http.MethodPut {
				body = map[string]string{"key": tc.key}
			}
			response := app.request(t, tc.method, tc.path, body, tc.actor, nil)
			requireAPIError(t, response, tc.status, tc.code)
		})
	}
	baseline := b3Changes(t, app)
	item = b3SetHTTPKey(t, app, &admin, "application", " "+b3AppKey+" \t")
	b3Reason(t, item, "key_missing")
	if !reflect.DeepEqual(item.Keys, []curation.SourceKeySettings{{Name: "application", Configured: true, LastFour: "9123", Status: "unchecked"}, {Name: "user", Status: "unchecked"}}) {
		t.Fatalf("填入应用密钥后的响应=%+v", item)
	}
	row := b3StoredKey(t, app, "application")
	if bytes.Contains(row.ciphertext, []byte(b3AppKey)) || !b3Changes(t, app).After(baseline) {
		t.Fatal("保存后应仅持久化密文并推进变化标记")
	}
	plain, err := newInstanceSecrets(app.dataDirectory).Open("instance", "curation.source_key.acoustid.application", row.ciphertext)
	if err != nil || !bytes.Equal(plain, []byte(b3AppKey)) {
		t.Fatal("保存的密文无法按声明的用途解密")
	}
	mustExec(t, app, `UPDATE curation_source_keys SET status='invalid' WHERE name='application'`)
	b3Reason(t, b3SourceList(t, app, &admin)["acoustid"], "key_missing")
	item = b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
	b3Reason(t, item, "key_invalid")
	if item.Keys[0].Status != "invalid" || item.Keys[1] != (curation.SourceKeySettings{Name: "user", Configured: true, LastFour: "8675", Status: "unchecked"}) {
		t.Fatalf("两把密钥的状态=%+v", item.Keys)
	}
	item = b3SetHTTPKey(t, app, &admin, "application", b3AppKey+"-new")
	b3Reason(t, item, "")
	mustExec(t, app, `UPDATE curation_source_keys SET updated_at=now()-INTERVAL '2 days' WHERE source='acoustid' AND name='application'`)
	before := b3Changes(t, app)
	if n := countRows(t, app, `SELECT count(*) FROM curation_source_settings WHERE source='acoustid'`); n != 0 {
		t.Fatalf("清除前开关覆盖行数=%d，期望 0", n)
	}
	response := app.request(t, http.MethodDelete, b3AcoustIDPath+"application", nil, &admin, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &item) != nil {
		t.Fatalf("清除返回 HTTP %d %s", response.Code, response.Body)
	}
	b3Reason(t, item, "key_missing")
	if !item.Enabled || item.Keys[0] != (curation.SourceKeySettings{Name: "application", Status: "unchecked"}) ||
		countRows(t, app, `SELECT count(*) FROM curation_source_keys WHERE source='acoustid' AND name='application'`) != 0 || !b3Changes(t, app).After(before) {
		t.Fatalf("清除的响应或数据库状态错误：%+v", item)
	}
	// 清除最后一条密钥时，变化标记仍有独立保存位置。
	before = b3Changes(t, app)
	response = app.request(t, http.MethodDelete, b3AcoustIDPath+"user", nil, &admin, nil)
	if response.Code != 200 || countRows(t, app, `SELECT count(*) FROM curation_source_keys`) != 0 || !b3Changes(t, app).After(before) {
		t.Fatalf("清除最后一把密钥返回 HTTP %d %s，或变化标记未推进", response.Code, response.Body)
	}
	secondAdmin := app.createUser(t, "admin")
	mustExec(t, app, `UPDATE users SET disabled_at=now() WHERE id=$1`, admin.id)
	b3Reason(t, b3SourceList(t, app, &secondAdmin)["acoustid"], "enabler_unavailable")
}

func TestPostgreSQLB3AcoustIDKeyWriteback(t *testing.T) {
	b3RequireTool(t, "ROOMUSIC_TEST_FPCALC")
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	if _, err := curation.NewWriter(app.database, app.dataDirectory, nil).SetSettings(t.Context(), admin.id, true); err != nil {
		t.Fatal(err)
	}
	var outbound atomic.Int32
	inFlight, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outbound.Add(1)
		form := b3LookupForm(t, r)
		switch form.Get("fingerprint") {
		case "invalid-user", "old-user":
			if form.Get("user") != b3UserKey {
				t.Errorf("请求未使用预期的合成用户密钥")
			}
			if form.Get("fingerprint") == "old-user" {
				close(inFlight)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"status":"error","error":{"code":6}}`)
		default:
			io.WriteString(w, b3EmptyLookup)
		}
	}))
	t.Cleanup(server.Close)
	opts := b3AcoustOptions(server)
	opts.DataDirectory = app.dataDirectory
	var gate *curation.Sources
	opts.Keys = func(ctx context.Context) (map[string]string, error) { return gate.Keys(ctx, "acoustid") }
	gate = curation.NewSources(app.database, curation.SourceRegistration{Declaration: acoustid.Declaration(), Fingerprints: acoustid.New(opts)}).
		WithKeyStore(newInstanceSecrets(app.dataDirectory))
	set := func(name, key string) {
		t.Helper()
		if _, err := gate.SetKey(t.Context(), "acoustid", name, admin.id, key); err != nil {
			t.Fatal(err)
		}
	}
	set("application", b3AppKey)
	set("user", b3UserKey)
	finder, ok := gate.FingerprintFinder("acoustid")
	if !ok {
		t.Fatal("指纹源未注册")
	}
	lookup := func(value string) error {
		_, err := finder.LookupFingerprint(t.Context(), sources.Fingerprint{Value: value, DurationSeconds: 20})
		return err
	}
	assertRow := func(name, status string, old b3KeyRow) {
		t.Helper()
		got := b3StoredKey(t, app, name)
		if got.status != status || !got.updated.Equal(old.updated) || !bytes.Equal(got.ciphertext, old.ciphertext) {
			t.Errorf("%s 状态=%s，期望 %s，时间相同=%t，密文相同=%t", name, got.status, status, got.updated.Equal(old.updated), bytes.Equal(got.ciphertext, old.ciphertext))
		}
	}
	appBefore, userBefore := b3StoredKey(t, app, "application"), b3StoredKey(t, app, "user")
	marker := b3Changes(t, app)
	if err := lookup("cacheable"); err != nil {
		t.Fatal(err)
	}
	assertRow("application", "valid", appBefore)
	assertRow("user", "valid", userBefore)
	if err := lookup("invalid-user"); !errors.Is(err, sources.ErrInvalidKey) {
		t.Fatalf("码 6 返回 %v", err)
	}
	assertRow("application", "valid", appBefore)
	assertRow("user", "invalid", userBefore)
	if !b3Changes(t, app).Equal(marker) {
		t.Error("valid 或 invalid 回写推进了变化标记")
	}

	set("application", b3AppKey+"-replacement")
	set("user", b3UserKey)
	appBefore, userBefore = b3StoredKey(t, app, "application"), b3StoredKey(t, app, "user")
	if err := lookup("cacheable"); err != nil || outbound.Load() != 2 {
		t.Fatalf("命中缓存时错误=%v，出站=%d，期望累计 2 次", err, outbound.Load())
	}
	assertRow("application", "unchecked", appBefore)
	assertRow("user", "unchecked", userBefore)

	done := make(chan error, 1)
	go func() { done <- lookup("old-user") }()
	select {
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("旧密钥请求未到达受控服务")
	}
	set("user", b3UserKey+"-replacement")
	userBefore, marker = b3StoredKey(t, app, "user"), b3Changes(t, app)
	close(release)
	released = true
	select {
	case err := <-done:
		if !errors.Is(err, sources.ErrInvalidKey) {
			t.Fatalf("旧请求错误=%v，期望 ErrInvalidKey", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("旧请求未结束")
	}
	assertRow("application", "unchecked", appBefore)
	assertRow("user", "unchecked", userBefore)
	if !b3Changes(t, app).Equal(marker) {
		t.Error("旧请求的回写推进了变化标记")
	}
}
