package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/plugins"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

const pluginAPIBase = "/api/v1/admin/plugins"
const demoPluginSource = "plugin:lyrics-demo"
const demoSourcePath = curationBase + "/sources/" + demoPluginSource

func demoPluginFiles(t *testing.T) (plugins.Manifest, []byte) {
	t.Helper()
	root := "../../../temp"
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "plugin-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	output, err := filepath.Abs(filepath.Join(dir, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-buildmode=c-shared", "-o", output, ".")
	cmd.Dir = "../../plugins/examples/lyrics-demo"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建示例插件失败 %v\n%s", err, out)
	}
	module, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(cmd.Dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, module
}

func demoPluginArchive(t *testing.T, manifest plugins.Manifest, wasm []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, entry := range []struct {
		name string
		body []byte
	}{{"manifest.json", raw}, {"plugin.wasm", wasm}} {
		w, err := z.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func previewPluginRequest(t *testing.T, app *integrationTestApplication, user integrationTestUser, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, pluginAPIBase+"/preview", bytes.NewReader(archive))
	request.Host = "roomusic.test"
	request.Header.Set("Origin", app.origin)
	request.Header.Set("Content-Type", "application/zip")
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}

func previewDemoPlugin(t *testing.T, app *integrationTestApplication, admin integrationTestUser, archive []byte) (*httptest.ResponseRecorder, plugins.Preview) {
	t.Helper()
	response := previewPluginRequest(t, app, admin, archive)
	var preview plugins.Preview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != 200 ||
		preview.Token == "" || preview.SHA256 != fmt.Sprintf("%x", sha256.Sum256(archive)) ||
		!reflect.DeepEqual(preview.Capabilities, []sources.Capability{sources.LyricsLookup, sources.ArtistBioLookup}) {
		t.Fatalf("预检未返回确认信息，状态 %d，能力 %v，错误 %v", response.Code, preview.Capabilities, err)
	}
	return response, preview
}

func requirePluginSample(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	var expected struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	raw, err := os.ReadFile(sampleFilePath("plugins/" + name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	actual := bytes.TrimSpace(response.Body.Bytes())
	if len(actual) == 0 {
		actual = []byte("null")
	} else if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("响应内容类型 %q", response.Header().Get("Content-Type"))
	}
	normalize := func(raw []byte) any {
		t.Helper()
		var body any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		item, ok := body.(map[string]any)
		if !ok {
			return body
		}
		if items, ok := item["items"].([]any); ok && len(items) == 1 {
			item, _ = items[0].(map[string]any)
		}
		if date, ok := item["installed_at"].(string); ok {
			if _, err := time.Parse(time.RFC3339Nano, date); err != nil {
				t.Fatal("installed_at 应为 RFC3339 日期")
			}
			item["installed_at"] = "TIMESTAMP"
		}
		if _, ok := item["sha256"].(string); ok {
			item["sha256"] = "SHA256"
		}
		if _, ok := item["preview_token"].(string); ok {
			item["preview_token"] = "TOKEN"
		}
		return body
	}
	if response.Code != expected.Status || !reflect.DeepEqual(normalize(actual), normalize(expected.Body)) {
		t.Errorf("样本 %s 与 HTTP %d 响应不符 %s", name, response.Code, response.Body)
	}
}

func TestPostgreSQLPluginCuration(t *testing.T) {
	if os.Getenv("ROOMUSIC_TEST_DATABASE_URL") == "" {
		t.Skip("需要隔离 PostgreSQL")
	}
	mock, err := upstreammock.New(upstreammock.Options{Scenarios: map[string]string{"lrclib": "not_found", "wikipedia": "not_found"}})
	if err != nil {
		t.Fatal(err)
	}
	var pluginRequests, builtinLyricsRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/plugins/lyrics-demo/main/") {
			pluginRequests.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/lrclib/") {
			builtinLyricsRequests.Add(1)
		}
		mock.ServeHTTP(w, r)
	}))
	defer server.Close()

	t.Setenv("ROOMUSIC_UPSTREAM_MOCK_URL", server.URL)
	t.Setenv("ROOMUSIC_DATABASE_URL", os.Getenv("ROOMUSIC_TEST_DATABASE_URL"))
	t.Setenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	clock := newProgramClock()
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)), settings, WithCurationProgram(true, clock.options()))
	clock.wait(t)
	admin := app.createUser(t, "admin")
	member := app.createUser(t, "user")
	root := uuid.New()
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, root, app.allowedRoot)
	release, _ := insertGroupedRelease(t, app, root, groupedRelease{title: "潮汐专辑", artist: "纸屿", tracks: []string{"present"}})
	track, _ := externalTrack(t, app, release)
	mustExec(t, app, `UPDATE tracks SET title='潮汐信箱',artist='纸屿',duration_seconds=180 WHERE id=$1`, track)
	publishSearch(t, app)
	artist := programString(t, app, `SELECT id FROM artists WHERE initial_name='纸屿'`)
	linkArtistMedia(t, app, artist, "10000000-0000-4000-8000-000000000003")
	fixture := &curationFixture{operationsEnvironment: &operationsEnvironment{groupEnvironment: &groupEnvironment{application: app, admin: admin, a: release}}}

	manifest, wasm := demoPluginFiles(t)
	archive := demoPluginArchive(t, manifest, wasm)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return app.request(t, method, path, body, &admin, nil)
	}
	enabled := func(path string, value bool) *httptest.ResponseRecorder {
		t.Helper()
		r := call("PUT", path, map[string]bool{"enabled": value})
		if r.Code != 200 {
			t.Fatalf("切换 %s 返回 HTTP %d %s", path, r.Code, r.Body)
		}
		return r
	}

	requireAPIError(t, app.request(t, "GET", pluginAPIBase, nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, previewPluginRequest(t, app, member, archive), 403, "forbidden")
	requireAPIError(t, previewPluginRequest(t, app, admin, []byte("bad package")), 400, "invalid_plugin")
	previewResponse, preview := previewDemoPlugin(t, app, admin, archive)
	requirePluginSample(t, "preview", previewResponse)
	confirm := map[string]string{"preview_token": preview.Token}
	requirePluginSample(t, "install", call("POST", pluginAPIBase, confirm))
	requireAPIError(t, call("POST", pluginAPIBase, confirm), 400, "invalid_plugin")
	list := func() plugins.Item {
		t.Helper()
		r := call("GET", pluginAPIBase, nil)
		var body struct{ Items []plugins.Item }
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || r.Code != 200 || len(body.Items) != 1 {
			t.Fatalf("插件列表返回 HTTP %d %s，错误 %v", r.Code, r.Body, err)
		}
		return body.Items[0]
	}
	if item := list(); item.Enabled || item.Manifest.ID != manifest.ID {
		t.Fatalf("刚安装的插件应默认关闭，实际 %+v", item)
	}
	enabled(demoSourcePath, true)
	modulePath := filepath.Join(app.dataDirectory, "plugins", manifest.ID, "plugin.wasm")
	if err := os.WriteFile(modulePath, []byte("different wasm"), 0600); err != nil {
		t.Fatal(err)
	}
	assertNoPluginRequests := func(t *testing.T) {
		t.Helper()
		if got := pluginRequests.Load(); got != 0 {
			t.Errorf("开关关闭期间仍发出 %d 个插件上游请求", got)
		}
		if item := list(); item.DisabledReason != "" {
			t.Errorf("开关关闭期间仍检查了插件模块，停用原因 %q", item.DisabledReason)
		}
		if n := countRows(t, app, `SELECT count(*) FROM operations WHERE origin->>'provider'=$1`, demoPluginSource); n != 0 {
			t.Errorf("开关关闭期间出现 %d 条插件写入", n)
		}
	}
	t.Run("global_disabled", func(t *testing.T) {
		clock.advance(t, time.Minute)
		assertNoPluginRequests(t)
	})
	t.Run("source_disabled", func(t *testing.T) {
		enabled(demoSourcePath, false)
		enabled(curationBase+"/settings", true)
		clock.advance(t, time.Minute)
		assertNoPluginRequests(t)
	})
	if err := os.WriteFile(modulePath, wasm, 0600); err != nil {
		t.Fatal(err)
	}
	requirePluginSample(t, "enable-source", enabled(demoSourcePath, true))
	requirePluginSample(t, "list", call("GET", pluginAPIBase, nil))

	t.Run("writes_and_reverts_by_source", func(t *testing.T) {
		clock.advance(t, time.Minute)
		if pluginRequests.Load() < 2 || builtinLyricsRequests.Load() == 0 {
			t.Errorf("内置歌词请求 %d 次，插件请求 %d 次", builtinLyricsRequests.Load(), pluginRequests.Load())
		}
		lyrics := call("GET", "/api/v1/tracks/"+track+"/lyrics", nil)
		if lyrics.Code != 200 || !strings.Contains(lyrics.Body.String(), "潮汐寄来一封信") {
			t.Errorf("歌词 HTTP %d %s", lyrics.Code, lyrics.Body)
		}
		bio := readArtistMedia(t, app, &admin, artist).Bio
		if bio == nil || bio.Text != "纸屿是合成目录中的独奏者，作品记录海岸的声音。" || bio.Source != demoPluginSource {
			t.Errorf("插件简介或来源不正确 %+v", bio)
		}
		if got := programString(t, app, `SELECT source FROM track_external_lyrics WHERE track_id=$1`, track); got != demoPluginSource {
			t.Errorf("歌词来源 %q", got)
		}
		if n := countRows(t, app, `SELECT count(*) FROM operations WHERE origin->>'provider'=$1`, demoPluginSource); n != 2 {
			t.Errorf("期望记录两条插件操作，实际 %d", n)
		}
		if t.Failed() {
			return
		}
		reverted := fixture.scope(t, curation.Scope{Kind: "provider", Provider: demoPluginSource}, "", uuid.New(), "plugin-revert")
		if reverted.Reverted != 2 || len(reverted.Skipped) != 0 {
			t.Fatalf("按插件来源撤销 %+v", reverted)
		}
		generation := func() string {
			return programString(t, app, `SELECT generation::text FROM curation_work_states WHERE subject='artist_media' AND target_id=$1`, artist)
		}
		before := generation()
		for _, after := range []bool{false, true} {
			if after {
				clock.advance(t, time.Minute)
			}
			if n := countRows(t, app, `SELECT (SELECT count(*) FROM track_external_lyrics WHERE track_id=$1) + (SELECT count(*) FROM artist_external_bios WHERE artist_id=$2)`, track, artist); n != 0 {
				t.Errorf("撤销后仍有 %d 条资料，再次运行 %t", n, after)
			}
		}
		if generation() == before {
			t.Error("未重新评估被抑制的艺人简介")
		}
		if n := countRows(t, app, `SELECT count(*) FROM curation_suppressions WHERE target_id IN ($1,$2)`, track, artist); n < 2 {
			t.Errorf("歌词与简介缺少抑制标记，实际 %d 条", n)
		}
	})

	t.Run("hash_changed", func(t *testing.T) {
		// 更换模块后提交新的整理任务，下一次调用应发现哈希变化。
		if err := os.WriteFile(modulePath, []byte("different wasm"), 0600); err != nil {
			t.Fatal(err)
		}
		fresh, _ := insertGroupedRelease(t, app, root, groupedRelease{title: "完整性测试", artist: "纸屿", tracks: []string{"present"}})
		freshTrack, _ := externalTrack(t, app, fresh)
		mustExec(t, app, `UPDATE tracks SET title='潮汐信箱',artist='纸屿',duration_seconds=180 WHERE id=$1`, freshTrack)
		publishSearch(t, app)
		clock.advance(t, 6*time.Hour)
		item := list()
		if item.Enabled || item.DisabledReason != "hash_changed" {
			t.Fatalf("篡改后应自动停用并提示重新确认，实际 %+v", item)
		}
		requirePluginSample(t, "hash-changed", call("GET", pluginAPIBase, nil))
		requireAPIError(t, call("PUT", demoSourcePath, map[string]bool{"enabled": true}), 409, "hash_changed")
	})

	t.Run("upgrade_resets_key_and_switch", func(t *testing.T) {
		manifest.Version = "2.0.0"
		manifest.Keys = []plugins.Key{{Name: "api_key", Optional: true, Upstream: "main", Inject: plugins.Injection{In: "header", Name: "X-Api-Key"}}}
		install := func() {
			t.Helper()
			_, next := previewDemoPlugin(t, app, admin, demoPluginArchive(t, manifest, wasm))
			r := call("POST", pluginAPIBase, map[string]string{"preview_token": next.Token})
			if r.Code != 201 {
				t.Fatalf("确认更新返回 HTTP %d %s", r.Code, r.Body)
			}
		}
		install()
		const key = "synthetic-demo-key"
		r := call("PUT", demoSourcePath+"/keys/api_key", map[string]string{"key": key})
		if r.Code != 200 || strings.Contains(r.Body.String(), key) ||
			countRows(t, app, `SELECT count(*) FROM curation_source_keys WHERE source=$1`, demoPluginSource) != 1 {
			t.Fatalf("密钥未安全保存，HTTP %d", r.Code)
		}
		enabled(demoSourcePath, true)
		manifest.Version = "3.0.0"
		install()
		item := list()
		if item.Enabled || item.DisabledReason != "" || item.Manifest.Version != "3.0.0" ||
			countRows(t, app, `SELECT count(*) FROM curation_source_keys WHERE source=$1`, demoPluginSource) != 0 {
			t.Errorf("更新确认后版本、开关或密钥状态错误 %+v", item)
		}
		requirePluginSample(t, "delete", call("DELETE", pluginAPIBase+"/"+manifest.ID, nil))
		if n := countRows(t, app, `SELECT count(*) FROM source_plugins WHERE id=$1`, manifest.ID); n != 0 {
			t.Errorf("删除后仍有 %d 条安装记录", n)
		}
	})
}
