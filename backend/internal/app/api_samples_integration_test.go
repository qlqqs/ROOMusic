package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// API 响应样本：用真实 handler 产生响应，规范化 UUID 与时间后，与提交在 testdata/api-samples 的 JSON 逐字比较。
// 前端 decoder 的契约测试读取同一批样本，所以后端响应一旦漂移，这里先失败。
// 确认是有意的合同变更后，用 ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh 重写样本，并同步前端 decoder。
const (
	apiSamplesDirectory = "testdata/api-samples"
	updateAPISamplesEnv = "ROOMUSIC_UPDATE_API_SAMPLES"
	sampleRequestID     = "sample-request"

	sampleAdminPassword     = "curator-sample-password"
	sampleListenerPassword  = "listener-sample-password"
	sampleArchivistPassword = "archivist-sample-password"
)

func TestPostgreSQLAPIResponseSamples(t *testing.T) {
	// 派生用不需要 ffmpeg 的假实现：样本在任何机器上都相同，格式表里带有派生项，并能产生 202 与派生相关的错误。
	fake := newFakeDerivation(t)
	application := newDerivationApplication(t, fake)
	samples := newAPISampleRecorder(t, application)
	call := func(method, path string, body any, user *integrationTestUser, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		headers := map[string]string{"X-Request-ID": sampleRequestID}
		for _, extra := range extraHeaders {
			for name, value := range extra {
				headers[name] = value
			}
		}
		return application.request(t, method, path, body, user, headers)
	}
	idempotency := func(key string) map[string]string { return map[string]string{"Idempotency-Key": key} }
	login := func(response *httptest.ResponseRecorder) *integrationTestUser {
		t.Helper()
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == identity.SessionCookieName && cookie.Value != "" {
				samples.forbid(cookie.Value)
				return &integrationTestUser{token: cookie.Value}
			}
		}
		t.Fatalf("login response did not set the session cookie: %s", response.Body.String())
		return nil
	}
	credentials := func(username, password string) map[string]string {
		return map[string]string{"username": username, "password": password}
	}

	// 专用的不存在用户名只触发限速样本，不改变其他样本的账号状态。
	for i := 0; i < 5; i++ {
		requireAPIError(t, call(http.MethodPost, "/api/v1/auth/login", credentials("throttle-sample-missing", "wrong-sample-password"), nil), http.StatusUnauthorized, "invalid_credentials")
	}
	samples.record("errors/too-many-attempts", call(http.MethodPost, "/api/v1/auth/login", credentials("throttle-sample-missing", "wrong-sample-password"), nil), http.StatusTooManyRequests)

	// 初始化与登录。
	samples.record("auth/setup-status-required", call(http.MethodGet, "/api/v1/setup/status", nil, nil), http.StatusOK)
	samples.record("errors/invalid-input", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", "short"), nil), http.StatusBadRequest)
	samples.record("errors/unauthorized", call(http.MethodGet, "/api/v1/users", nil, nil), http.StatusUnauthorized)
	samples.record("auth/setup-admin-created", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", sampleAdminPassword), nil), http.StatusCreated)
	samples.record("auth/setup-status-completed", call(http.MethodGet, "/api/v1/setup/status", nil, nil), http.StatusOK)
	samples.record("errors/setup-closed", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", sampleAdminPassword), nil), http.StatusConflict)
	samples.record("errors/invalid-credentials", call(http.MethodPost, "/api/v1/auth/login", credentials("curator", "wrong-sample-password"), nil), http.StatusUnauthorized)
	samples.record("errors/origin-forbidden", call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil, map[string]string{"Origin": "http://untrusted.example"}), http.StatusForbidden)
	loginResponse := call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil)
	samples.record("auth/login", loginResponse, http.StatusOK)
	admin := login(loginResponse)
	samples.record("auth/me-admin", call(http.MethodGet, "/api/v1/auth/me", nil, admin), http.StatusOK)

	// 空库状态：没有扫描、没有发行组、没有目录。
	samples.record("scans/active-none", call(http.MethodGet, "/api/v1/scans/active", nil, admin), http.StatusOK)
	samples.record("scans/list-empty", call(http.MethodGet, "/api/v1/scans", nil, admin), http.StatusOK)
	samples.record("errors/invalid-pagination", call(http.MethodGet, "/api/v1/scans?page=0", nil, admin), http.StatusBadRequest)
	samples.record("release-groups/list-empty", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)
	samples.record("library-roots/list-empty", call(http.MethodGet, "/api/v1/library-roots", nil, admin), http.StatusOK)
	samples.record("library-root-operations/list-empty", call(http.MethodGet, "/api/v1/library-root-operations", nil, admin), http.StatusOK)

	// 成员：新建、重名、停用、重新启用、撤销会话、最后一个管理员。
	samples.record("users/create", call(http.MethodPost, "/api/v1/users", credentials("listener", sampleListenerPassword), admin), http.StatusCreated)
	samples.record("errors/username-taken", call(http.MethodPost, "/api/v1/users", credentials("listener", sampleListenerPassword), admin), http.StatusConflict)
	if response := call(http.MethodPost, "/api/v1/users", credentials("archivist", sampleArchivistPassword), admin); response.Code != http.StatusCreated {
		t.Fatalf("create archivist: HTTP %d: %s", response.Code, response.Body.String())
	}
	userIDs := func() map[string]string {
		t.Helper()
		response := call(http.MethodGet, "/api/v1/users", nil, admin)
		var listing struct {
			Items []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
			t.Fatalf("decode users: %v: %s", err, response.Body.String())
		}
		ids := map[string]string{}
		for _, item := range listing.Items {
			ids[item.Username] = item.ID
		}
		return ids
	}
	ids := userIDs()
	listener := login(call(http.MethodPost, "/api/v1/auth/login", credentials("listener", sampleListenerPassword), nil))
	samples.record("auth/me-user", call(http.MethodGet, "/api/v1/auth/me", nil, listener), http.StatusOK)
	samples.record("errors/forbidden", call(http.MethodGet, "/api/v1/users", nil, listener), http.StatusForbidden)
	samples.record("errors/last-admin", call(http.MethodPatch, "/api/v1/users/"+ids["curator"], map[string]bool{"disabled": true}, admin), http.StatusConflict)
	samples.record("users/disable", call(http.MethodPatch, "/api/v1/users/"+ids["archivist"], map[string]bool{"disabled": true}, admin), http.StatusOK)
	samples.record("users/list", call(http.MethodGet, "/api/v1/users", nil, admin), http.StatusOK)
	samples.record("users/enable", call(http.MethodPatch, "/api/v1/users/"+ids["archivist"], map[string]bool{"disabled": false}, admin), http.StatusOK)
	samples.record("users/revoke-sessions", call(http.MethodPost, "/api/v1/users/"+ids["listener"]+"/sessions/revoke", map[string]any{}, admin), http.StatusOK)
	// 只改 archivist 的密码，并在录完后恢复；后续 listener 登录与管理员会话保持原样。
	const (
		sampleChangedPassword = "archivist-changed-secret"
		sampleWrongPassword   = "wrong-current-secret"
		sampleInvalidPassword = "bad-next-11"
	)
	samples.forbid(sampleChangedPassword)
	samples.forbid(sampleWrongPassword)
	samples.forbid(sampleInvalidPassword)
	archivist := login(call(http.MethodPost, "/api/v1/auth/login", credentials("archivist", sampleArchivistPassword), nil))
	samples.record("errors/current-password-incorrect", call(http.MethodPost, "/api/v1/auth/password", ownPasswordBody(sampleWrongPassword, sampleChangedPassword), archivist), http.StatusForbidden)
	samples.record("errors/invalid-input-password", call(http.MethodPost, "/api/v1/auth/password", ownPasswordBody(sampleArchivistPassword, sampleInvalidPassword), archivist), http.StatusBadRequest)
	samples.record("auth/password-changed", call(http.MethodPost, "/api/v1/auth/password", ownPasswordBody(sampleArchivistPassword, sampleChangedPassword), archivist), http.StatusOK)
	samples.record("errors/self-password-reset", call(http.MethodPost, "/api/v1/users/"+ids["curator"]+"/password", map[string]string{"password": sampleChangedPassword}, admin), http.StatusForbidden)
	samples.record("users/password-reset", call(http.MethodPost, "/api/v1/users/"+ids["archivist"]+"/password", map[string]string{"password": sampleArchivistPassword}, admin), http.StatusOK)

	if response := call(http.MethodGet, "/api/v1/auth/me", nil, listener); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked member session still works: HTTP %d: %s", response.Code, response.Body.String())
	}
	samples.record("errors/invalid-id-user", call(http.MethodPatch, "/api/v1/users/not-a-uuid", map[string]bool{"disabled": true}, admin), http.StatusBadRequest)
	samples.record("errors/not-found-user", call(http.MethodPost, "/api/v1/users/"+uuid.New()+"/sessions/revoke", map[string]any{}, admin), http.StatusNotFound)

	// 目录与扫描的合成素材：一个有效的 FLAC 与一个损坏文件，都在测试专用的临时根目录里。
	libraryA := createLibraryDirectory(t, application, "classical-a")
	libraryB := createLibraryDirectory(t, application, "classical-b")
	albumDirectory := filepath.Join(libraryA, "album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	validFLAC := parsertest.FLACWithAudio(t, [16]byte{1, 2, 3}, bytes.Repeat([]byte{7}, 64),
		"TITLE=Prelude No. 1", "ARTIST=Sample Pianist", "ALBUM=Sample Preludes", "ALBUMARTIST=Sample Pianist", "DATE=2001", "TRACKNUMBER=1")
	if err := os.WriteFile(filepath.Join(albumDirectory, "01.flac"), validFLAC, 0o644); err != nil {
		t.Fatalf("write valid FLAC fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libraryB, "broken.flac"), []byte("this is not a FLAC stream"), 0o644); err != nil {
		t.Fatalf("write corrupt FLAC fixture: %v", err)
	}

	// 目录：新增、非法路径、缺少幂等键、幂等键冲突。
	samples.record("errors/invalid-library-path", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": "/definitely/outside/the/allowed/roots"}, admin, idempotency("sample-invalid-path")), http.StatusBadRequest)
	samples.record("errors/invalid-input-missing-idempotency-key", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryA}, admin), http.StatusBadRequest)
	createdA := call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryA}, admin, idempotency("sample-create-a"))
	samples.record("library-roots/create", createdA, http.StatusCreated)
	samples.record("errors/idempotency-conflict", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-create-a")), http.StatusConflict)
	createdB := call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-create-b"))
	if createdB.Code != http.StatusCreated {
		t.Fatalf("create second root: HTTP %d: %s", createdB.Code, createdB.Body.String())
	}
	rootB := decodeJSONMap(t, createdB)["id"].(string)

	// 第一次扫描：测试事务先独占 library_roots，worker 读不到目录而停在第一步，
	// 因此“新建（202）”“进行中”“已有扫描（200）”的响应都发生在 running 状态，不依赖时序。
	holder, err := application.database.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err := holder.Exec(`LOCK TABLE library_roots IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock library_roots: %v", err)
	}
	started := call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin)
	samples.record("scans/start-created", started, http.StatusAccepted)
	firstScan := decodeJSONMap(t, started)["id"].(string)
	waitForLockWaiters(t, application.database, 1)
	samples.record("scans/active-running", call(http.MethodGet, "/api/v1/scans/active", nil, admin), http.StatusOK)
	samples.record("scans/get-running", call(http.MethodGet, "/api/v1/scans/"+firstScan, nil, admin), http.StatusOK)
	samples.record("scans/start-existing", call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin), http.StatusOK)
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release lock holder: %v", err)
	}
	if status := waitForScanToFinish(t, application, firstScan); status != "incomplete" {
		t.Fatalf("first scan with a corrupt file should be incomplete, got %s", status)
	}
	samples.record("scans/get-incomplete", call(http.MethodGet, "/api/v1/scans/"+firstScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics", call(http.MethodGet, "/api/v1/scans/"+firstScan+"/diagnostics", nil, admin), http.StatusOK)

	// 目录：停用、版本冲突、重复登记已停用目录。
	samples.record("library-roots/disable", call(http.MethodPatch, "/api/v1/library-roots/"+rootB, map[string]any{"status": "disabled", "expected_revision": 1}, admin, idempotency("sample-disable-b")), http.StatusOK)
	samples.record("errors/revision-conflict", call(http.MethodPatch, "/api/v1/library-roots/"+rootB, map[string]any{"status": "disabled", "expected_revision": 1}, admin, idempotency("sample-disable-stale")), http.StatusConflict)
	samples.record("library-roots/create-existing-disabled", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-register-disabled")), http.StatusCreated)
	samples.record("library-roots/list", call(http.MethodGet, "/api/v1/library-roots", nil, admin), http.StatusOK)

	// 第二次扫描只覆盖启用的目录，成功结束；此后才有发行组。
	second := call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin)
	if second.Code != http.StatusAccepted {
		t.Fatalf("start second scan: HTTP %d: %s", second.Code, second.Body.String())
	}
	secondScan := decodeJSONMap(t, second)["id"].(string)
	if status := waitForScanToFinish(t, application, secondScan); status != "succeeded" {
		t.Fatalf("second scan should succeed, got %s", status)
	}
	samples.record("scans/get-succeeded", call(http.MethodGet, "/api/v1/scans/"+secondScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics-empty", call(http.MethodGet, "/api/v1/scans/"+secondScan+"/diagnostics", nil, admin), http.StatusOK)
	samples.record("release-groups/list-one", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)
	var releaseID string
	if err := application.database.QueryRow(`SELECT id::text FROM releases`).Scan(&releaseID); err != nil {
		t.Fatalf("read scanned release: %v", err)
	}
	if _, err := application.database.Exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1::uuid,$2,'image/webp',640,480,'sample-cover.webp','embedded')`, releaseID, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("insert artwork fixture: %v", err)
	}
	samples.record("release-groups/list-one-with-artwork", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)

	// 目录恢复与操作记录；再补一条失败记录（当前后端不会写入失败操作，但表允许，界面必须能显示）。
	samples.record("library-roots/restore", call(http.MethodPost, "/api/v1/library-roots/"+rootB+"/restore", map[string]int{"expected_revision": 2}, admin, idempotency("sample-restore-b")), http.StatusOK)
	if _, err := application.database.Exec(`INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,error_code,request_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,'disable','failed','sample-failed-operation','sample-fingerprint','operation_failed','sample-request')`, uuid.New(), rootB, ids["curator"]); err != nil {
		t.Fatalf("insert failed operation fixture: %v", err)
	}
	samples.record("library-root-operations/list", call(http.MethodGet, "/api/v1/library-root-operations", nil, admin), http.StatusOK)

	// 直接写入的扫描：失败（带多类诊断）、取消请求中、已取消。真实扫描无法按需产生这些终态。
	failedScan := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at,finished_at,error_message) VALUES($1::uuid,'failed',NOW()-INTERVAL '5 minutes',NOW()-INTERVAL '4 minutes','scan_failed')`, failedScan); err != nil {
		t.Fatalf("insert failed scan fixture: %v", err)
	}
	for _, diagnostic := range []struct {
		id      int
		kind    string
		path    any
		message string
	}{
		{101, "parse_failure", "disc-1/01.flac", "音频文件解析失败"},
		{102, "permission_error", "disc-2", "无法读取文件"},
		{103, "database_error", nil, "扫描数据库操作失败"},
		{104, "unsupported_format", "extras/cover.dsf", "不支持的音频格式"},
	} {
		if _, err := application.database.Exec(`INSERT INTO scan_diagnostics(id,scan_run_id,relative_path,kind,message) VALUES($1,$2::uuid,$3,$4,$5)`, diagnostic.id, failedScan, diagnostic.path, diagnostic.kind, diagnostic.message); err != nil {
			t.Fatalf("insert diagnostic fixture: %v", err)
		}
	}
	samples.record("scans/get-failed", call(http.MethodGet, "/api/v1/scans/"+failedScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics-multiple-kinds", call(http.MethodGet, "/api/v1/scans/"+failedScan+"/diagnostics", nil, admin), http.StatusOK)

	cancelScan := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, cancelScan); err != nil {
		t.Fatalf("insert running scan fixture: %v", err)
	}
	samples.record("scans/cancel-requested", call(http.MethodPost, "/api/v1/scans/"+cancelScan+"/cancel", map[string]any{}, admin), http.StatusAccepted)
	samples.record("scans/get-cancel-requested", call(http.MethodGet, "/api/v1/scans/"+cancelScan, nil, admin), http.StatusOK)
	samples.record("scans/list", call(http.MethodGet, "/api/v1/scans", nil, admin), http.StatusOK)
	if _, err := application.database.Exec(`UPDATE scan_runs SET status='canceled',finished_at=NOW() WHERE id=$1::uuid`, cancelScan); err != nil {
		t.Fatalf("finish canceled scan fixture: %v", err)
	}
	samples.record("scans/get-canceled", call(http.MethodGet, "/api/v1/scans/"+cancelScan, nil, admin), http.StatusOK)
	samples.record("scans/cancel-already-finished", call(http.MethodPost, "/api/v1/scans/"+cancelScan+"/cancel", map[string]any{}, admin), http.StatusOK)

	// 专辑详情（M1b）：用合成 FLAC 扫描出一张双碟专辑，再记录组、发行版本与管理员证据的详情。放在扫描记录样本之后，
	// 这样新增的扫描与专辑不会改变前面已记录的列表样本。
	recordAlbumDetailSamples(t, application, samples, call, admin, libraryA, libraryB)
	// 派生播放（M2b）：登记的 ALAC 曲目直接写入数据库，放在所有列表样本之后。
	recordDerivationSamples(t, application, samples, call, admin, playbackLibrary{rootID: rootIDOf(t, application, libraryA), root: libraryA}, fake)

	// 通用错误：扫描不存在、标识无效、未知接口、方法不符、数据库不可用的降级响应。
	samples.record("errors/not-found-scan", call(http.MethodGet, "/api/v1/scans/"+uuid.New(), nil, admin), http.StatusNotFound)
	samples.record("errors/invalid-id", call(http.MethodGet, "/api/v1/scans/not-a-uuid", nil, admin), http.StatusBadRequest)
	samples.record("errors/not-found-api", call(http.MethodGet, "/api/v1/missing", nil, admin), http.StatusNotFound)
	samples.record("errors/method-not-allowed", call(http.MethodPut, "/api/v1/users", nil, admin), http.StatusMethodNotAllowed)
	degraded := httptest.NewRecorder()
	degradedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	degradedRequest.Header.Set("X-Request-ID", sampleRequestID)
	Degraded(testFrontend(), slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(degraded, degradedRequest)
	samples.record("errors/database-unavailable", degraded, http.StatusServiceUnavailable)

	// 退出：用第二个会话，避免影响上面仍在使用的管理员会话。
	secondAdmin := login(call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil))
	samples.record("auth/logout", call(http.MethodPost, "/api/v1/auth/logout", map[string]any{}, secondAdmin), http.StatusOK)

	// 播放历史（阶段 P）：放在最后，用管理员自己的历史。
	recordListeningSamples(t, application, samples, call, admin)
	recordHistorySamples(t, samples)
	recordSearchSamples(t, samples)
	// 字段修正（M3）：修正会改变这张专辑的有效值，所以放在所有其他样本之后。
	recordCorrectionSamples(t, application, samples, call, admin)
	// 人工归组（M3）：移动合成的 CUE 专辑，放在最后。
	recordGroupChangeSamples(t, application, samples, call, admin)
	// 操作记录与撤销（M3）：汇总上面产生的记录，撤销最后一次移出，放在最后。
	recordOperationSamples(t, application, samples, call, admin)
	// 默认版本（M4）：它改变这张专辑的代表版本，并新增专辑目标的记录，所以放在上面的样本之后，已有样本不受影响。
	recordGroupDefaultSamples(t, application, samples, call, admin)
	// 核对问题（M4 第二项）：新建一张有问题的合成专辑，放在最后，已有样本不受影响。
	recordIssueReviewSamples(t, application, samples, call, admin)
	recordClaimSamples(t, application, samples, call, admin)

	samples.finish()
}

// apiSampleCall 是样本测试里发请求的闭包：带固定的请求 ID，把响应交给记录器。
type apiSampleCall func(method, path string, body any, user *integrationTestUser, extraHeaders ...map[string]string) *httptest.ResponseRecorder

// recordAlbumDetailSamples 记录专辑浏览用到的详情接口：一张合成的双碟专辑（厂牌、编号、条码、作曲/指挥/演奏署名），
// 既有的单曲专辑，一张 CUE 专辑，管理员的完整证据，把两个版本放进同一个组后的多版本组详情，以及两个 404。
// 素材全部是临时目录里的合成 FLAC，不读取真实音乐。
func recordAlbumDetailSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser, libraryA, libraryB string) {
	t.Helper()
	releaseIDByTitle := func(title string) (releaseID, groupID string) {
		t.Helper()
		if err := application.database.QueryRow(`SELECT id::text,group_id::text FROM releases WHERE title=$1`, title).Scan(&releaseID, &groupID); err != nil {
			t.Fatalf("read release %q: %v", title, err)
		}
		return releaseID, groupID
	}
	// 既有的单曲专辑：大多数字段为空，前面直接写库给它绑定了一张封面。重新扫描时没有对应的来源文件，封面关联会被清掉，
	// 所以在下面的扫描之前记录。
	singleID, singleGroup := releaseIDByTitle("Sample Preludes")
	samples.record("releases/get-single", call(http.MethodGet, "/api/v1/releases/"+singleID, nil, admin), http.StatusOK)

	// 第一次扫描用的损坏文件会让扫描不完整；去掉它，这次扫描完整成功，不再产生与专辑无关的诊断。
	if err := os.Remove(filepath.Join(libraryB, "broken.flac")); err != nil {
		t.Fatalf("remove corrupt FLAC fixture: %v", err)
	}
	albumDirectory := filepath.Join(libraryA, "double-album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create double album directory: %v", err)
	}
	// 每个文件的音频 MD5 不同，内容指纹互不相同。
	shared := []string{
		"ALBUM=Sample Symphonies", "ALBUMARTIST=Sample Orchestra", "DATE=1998", "GENRE=Classical",
		"LABEL=Sample Label", "CATALOGNUMBER=SL-0001", "BARCODE=0123456789012", "EDITION=Remastered",
		"COMPOSER=Sample Composer", "CONDUCTOR=Sample Conductor", "PERFORMER=Sample Soloist",
	}
	for _, track := range []struct {
		file     string
		md5      byte
		comments []string
	}{
		// 前两首带内嵌歌词（同步与纯文本），第三首没有：歌词接口的三种响应来自真实扫描。
		{"d1-01.flac", 11, []string{"TITLE=Allegro", "ARTIST=Sample Orchestra", "DISCNUMBER=1/2", "TRACKNUMBER=1", "LYRICS=" + sampleSyncedLyrics}},
		{"d1-02.flac", 12, []string{"TITLE=Andante", "ARTIST=Sample Soloist", "DISCNUMBER=1/2", "TRACKNUMBER=2", "UNSYNCEDLYRICS=" + samplePlainLyrics}},
		{"d2-01.flac", 13, []string{"TITLE=Finale", "ARTIST=Sample Orchestra", "DISCNUMBER=2/2", "TRACKNUMBER=1"}},
	} {
		content := parsertest.FLACWithAudio(t, [16]byte{track.md5}, bytes.Repeat([]byte{track.md5}, 64), append(append([]string{}, shared...), track.comments...)...)
		if err := os.WriteFile(filepath.Join(albumDirectory, track.file), content, 0o644); err != nil {
			t.Fatalf("write %s: %v", track.file, err)
		}
	}
	// 一张 CUE 专辑：一个整轨 FLAC 加一份 CUE，虚拟曲目的 cue 字段有值（起止帧、秒与 ISRC）。
	cueDirectory := filepath.Join(libraryA, "cue-album")
	if err := os.MkdirAll(cueDirectory, 0o755); err != nil {
		t.Fatalf("create CUE album directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cueDirectory, "image.flac"), parsertest.FLACWithAudio(t, [16]byte{21}, bytes.Repeat([]byte{21}, 64)), 0o644); err != nil {
		t.Fatalf("write CUE image fixture: %v", err)
	}
	cueSheet := strings.Join([]string{
		`TITLE "Sample Cue Album"`,
		`PERFORMER "Sample Cue Artist"`,
		`REM DATE 2005`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "Cue One"`,
		`    PERFORMER "Sample Cue Artist"`,
		`    ISRC XXAAA0500001`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Cue Two"`,
		`    PERFORMER "Sample Cue Artist"`,
		`    ISRC XXAAA0500002`,
		`    INDEX 01 02:00:00`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(cueDirectory, "album.cue"), []byte(cueSheet), 0o644); err != nil {
		t.Fatalf("write CUE sheet fixture: %v", err)
	}
	started := call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start album scan: HTTP %d: %s", started.Code, started.Body.String())
	}
	if status := waitForScanToFinish(t, application, decodeJSONMap(t, started)["id"].(string)); status != "succeeded" {
		t.Fatalf("album scan should succeed, got %s", status)
	}

	doubleID, doubleGroup := releaseIDByTitle("Sample Symphonies")

	samples.record("release-groups/get", call(http.MethodGet, "/api/v1/release-groups/"+doubleGroup, nil, admin), http.StatusOK)
	samples.record("releases/get", call(http.MethodGet, "/api/v1/releases/"+doubleID, nil, admin), http.StatusOK)
	samples.record("releases/evidence", call(http.MethodGet, "/api/v1/releases/"+doubleID+"/evidence", nil, admin), http.StatusOK)
	cueID, _ := releaseIDByTitle("Sample Cue Album")
	samples.record("releases/get-cue", call(http.MethodGet, "/api/v1/releases/"+cueID, nil, admin), http.StatusOK)

	// M3 之前没有人工归组，多版本的组只能直接写库构造：把双碟专辑并入单曲专辑所在的组。
	if _, err := application.database.Exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, singleGroup, doubleID); err != nil {
		t.Fatalf("move release into the other group: %v", err)
	}
	if _, err := application.database.Exec(`DELETE FROM release_groups WHERE id=$1::uuid`, doubleGroup); err != nil {
		t.Fatalf("remove the emptied group: %v", err)
	}
	samples.record("release-groups/get-multiple", call(http.MethodGet, "/api/v1/release-groups/"+singleGroup, nil, admin), http.StatusOK)

	samples.record("errors/not-found-release-group", call(http.MethodGet, "/api/v1/release-groups/"+uuid.New(), nil, admin), http.StatusNotFound)
	samples.record("errors/not-found-release", call(http.MethodGet, "/api/v1/releases/"+uuid.New(), nil, admin), http.StatusNotFound)

	recordLyricsSamples(t, application, samples, call, admin)
	recordPlaybackSamples(t, application, samples, call, admin, libraryA, rootIDOf(t, application, libraryA))
}

// recordListeningSamples 记录播放历史接口：空的最近播放、不够门槛的 400、记录一次播放（201）、有数据的最近播放（发行带封面）与清除。
// 素材是 recordAlbumDetailSamples 扫描出的合成 FLAC（240 秒，门槛 120 秒，服务端接受 ≥ 119 秒）。
func recordListeningSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	var trackID, releaseID string
	if err := application.database.QueryRow(`SELECT tracks.id::text, media.release_id::text FROM tracks JOIN media ON media.id=tracks.medium_id
		WHERE tracks.title='Allegro' AND tracks.source_status='present'`).Scan(&trackID, &releaseID); err != nil {
		t.Fatalf("read the track to play: %v", err)
	}
	if _, err := application.database.Exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1::uuid,$2,'image/webp',640,480,'sample-listening-cover.webp','embedded')`, releaseID, strings.Repeat("d", 64)); err != nil {
		t.Fatalf("insert artwork fixture: %v", err)
	}
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	samples.record("listening/recent-releases-empty", call(http.MethodGet, "/api/v1/me/plays/recent-releases", nil, admin), http.StatusOK)
	samples.record("errors/play-too-short", call(http.MethodPost, "/api/v1/me/plays", map[string]any{"track_id": trackID, "played_seconds": 30}, admin, key("0001")), http.StatusBadRequest)
	samples.record("listening/play-recorded", call(http.MethodPost, "/api/v1/me/plays", map[string]any{"track_id": trackID, "played_seconds": 150.5}, admin, key("0002")), http.StatusCreated)
	samples.record("listening/recent-releases", call(http.MethodGet, "/api/v1/me/plays/recent-releases", nil, admin), http.StatusOK)
	samples.record("listening/plays-cleared", call(http.MethodDelete, "/api/v1/me/plays", nil, admin), http.StatusOK)
}

// recordCorrectionSamples 记录发行字段修正接口：修正状态、设值与设空的成功响应、修正后的状态与详情（带 corrected_fields），
// 以及无效修正、没有变化与过期版本三种错误。素材是 recordAlbumDetailSamples 扫描出的合成双碟专辑。
func recordCorrectionSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	var releaseID string
	if err := application.database.QueryRow(`SELECT id::text FROM releases WHERE title='Sample Symphonies'`).Scan(&releaseID); err != nil {
		t.Fatalf("read the release to correct: %v", err)
	}
	path := "/api/v1/releases/" + releaseID + "/corrections"
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	samples.record("corrections/get", call(http.MethodGet, path, nil, admin), http.StatusOK)
	samples.record("corrections/patched", call(http.MethodPatch, path, map[string]any{"expected_revision": 1, "changes": map[string]any{
		"title": map[string]any{"mode": "set", "value": "Sample Symphonies (corrected)"},
		"year":  map[string]any{"mode": "null"},
		"label": map[string]any{"mode": "set", "value": "Corrected Label"},
	}}, admin, key("0101")), http.StatusOK)
	samples.record("corrections/get-corrected", call(http.MethodGet, path, nil, admin), http.StatusOK)
	samples.record("releases/get-corrected", call(http.MethodGet, "/api/v1/releases/"+releaseID, nil, admin), http.StatusOK)
	samples.record("errors/invalid-correction", call(http.MethodPatch, path, map[string]any{"expected_revision": 2, "changes": map[string]any{
		"title": map[string]any{"mode": "null"},
	}}, admin, key("0102")), http.StatusBadRequest)
	samples.record("errors/no-changes", call(http.MethodPatch, path, map[string]any{"expected_revision": 2, "changes": map[string]any{
		"genre": map[string]any{"mode": "clear"},
	}}, admin, key("0103")), http.StatusBadRequest)
	samples.record("errors/revision-conflict-release", call(http.MethodPatch, path, map[string]any{"expected_revision": 1, "changes": map[string]any{
		"genre": map[string]any{"mode": "set", "value": "Romantic"},
	}}, admin, key("0104")), http.StatusConflict)
}

// recordGroupChangeSamples 记录人工归组接口：把合成的 CUE 专辑归入单曲专辑所在的组、再移出为独立专辑的成功响应，
// 以及 same_group、already_alone 与 group_not_found 三种错误。
func recordGroupChangeSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	var cueID, targetGroup string
	if err := application.database.QueryRow(`SELECT id::text FROM releases WHERE title='Sample Cue Album'`).Scan(&cueID); err != nil {
		t.Fatalf("read the release to move: %v", err)
	}
	if err := application.database.QueryRow(`SELECT group_id::text FROM releases WHERE title='Sample Preludes'`).Scan(&targetGroup); err != nil {
		t.Fatalf("read the target group: %v", err)
	}
	path := "/api/v1/releases/" + cueID + "/group"
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	samples.record("group-changes/joined", call(http.MethodPost, path, map[string]any{"expected_revision": 1, "action": "join", "group_id": targetGroup}, admin, key("0201")), http.StatusOK)
	samples.record("errors/same-group", call(http.MethodPost, path, map[string]any{"expected_revision": 2, "action": "join", "group_id": targetGroup}, admin, key("0202")), http.StatusConflict)
	samples.record("group-changes/detached", call(http.MethodPost, path, map[string]any{"expected_revision": 2, "action": "detach"}, admin, key("0203")), http.StatusOK)
	samples.record("errors/already-alone", call(http.MethodPost, path, map[string]any{"expected_revision": 3, "action": "detach"}, admin, key("0204")), http.StatusConflict)
	samples.record("errors/group-not-found", call(http.MethodPost, path, map[string]any{"expected_revision": 3, "action": "join", "group_id": uuid.New()}, admin, key("0205")), http.StatusNotFound)
}

// recordOperationSamples 记录操作记录与撤销接口：撤销最后一次移出的成功响应，已撤销、撤销不能再撤销、之后又有修改与记录不存在
// 四种错误，以及撤销之后的全部记录和按发行筛选的分页。记录来自前面的修正与归组样本。
func recordOperationSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	operationID := func(query string) string {
		t.Helper()
		var id string
		if err := application.database.QueryRow(query).Scan(&id); err != nil {
			t.Fatalf("read operation: %v", err)
		}
		return id
	}
	detachOperation := operationID(`SELECT id::text FROM operations WHERE operation_type='release_group_change' ORDER BY created_at DESC, id DESC LIMIT 1`)
	joinOperation := operationID(`SELECT id::text FROM operations WHERE operation_type='release_group_change' ORDER BY created_at ASC, id ASC LIMIT 1`)
	cueRelease := operationID(`SELECT id::text FROM releases WHERE title='Sample Cue Album'`)
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	reverted := call(http.MethodPost, revertPath(detachOperation), nil, admin, key("0301"))
	samples.record("operations/reverted", reverted, http.StatusOK)
	revertRecord := decodeJSONMap(t, reverted)["operation_id"].(string)
	samples.record("errors/already-reverted", call(http.MethodPost, revertPath(detachOperation), nil, admin, key("0302")), http.StatusConflict)
	samples.record("errors/not-revertible", call(http.MethodPost, revertPath(revertRecord), nil, admin, key("0303")), http.StatusConflict)
	samples.record("errors/revert-conflict", call(http.MethodPost, revertPath(joinOperation), nil, admin, key("0304")), http.StatusConflict)
	samples.record("errors/not-found-operation", call(http.MethodPost, revertPath(uuid.New()), nil, admin, key("0305")), http.StatusNotFound)
	samples.record("operations/list", call(http.MethodGet, "/api/v1/operations?page_size=10", nil, admin), http.StatusOK)
	samples.record("operations/list-by-release", call(http.MethodGet, "/api/v1/operations?release_id="+cueRelease+"&page_size=2", nil, admin), http.StatusOK)
}

// recordGroupDefaultSamples 记录默认版本接口：设定默认版本的成功响应、设定后的组详情（带 revision 与 default_release_id）
// 与版本对比，以及不在这张专辑里的版本与过期版本号两种错误；再记录按专辑筛选的操作记录（专辑目标的记录，album 有值、release 为 null）
// 和撤销这次设定的响应（目标是专辑）。素材是前面的归组样本：撤销移出之后，单曲专辑所在的组有三个版本
// （单曲专辑、双碟专辑与 CUE 专辑），默认版本设为不是第一位的双碟专辑；对比里有真实扫描出的格式、人工修正过的字段与没有时长的版本。
func recordGroupDefaultSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	var groupID, doubleID string
	if err := application.database.QueryRow(`SELECT group_id::text, id::text FROM releases WHERE title='Sample Symphonies'`).Scan(&groupID, &doubleID); err != nil {
		t.Fatalf("read the album to give a default version: %v", err)
	}
	path := defaultPath(groupID)
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	samples.record("group-defaults/set", call(http.MethodPost, path, defaultBody(1, doubleID), admin, key("0401")), http.StatusOK)
	samples.record("release-groups/get-default", call(http.MethodGet, "/api/v1/release-groups/"+groupID, nil, admin), http.StatusOK)
	samples.record("release-groups/comparison", call(http.MethodGet, comparisonPath(groupID), nil, admin), http.StatusOK)
	samples.record("errors/not-in-group", call(http.MethodPost, path, defaultBody(2, uuid.New()), admin, key("0402")), http.StatusConflict)
	samples.record("errors/revision-conflict-group", call(http.MethodPost, path, defaultBody(1, nil), admin, key("0403")), http.StatusConflict)
	samples.record("operations/list-by-group", call(http.MethodGet, "/api/v1/operations?group_id="+groupID+"&page_size=10", nil, admin), http.StatusOK)
	var setOperation string
	if err := application.database.QueryRow(`SELECT id::text FROM operations WHERE operation_type='release_group_default' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&setOperation); err != nil {
		t.Fatalf("read the default-version operation: %v", err)
	}
	samples.record("operations/reverted-default", call(http.MethodPost, revertPath(setOperation), nil, admin, key("0404")), http.StatusOK)
}

// recordIssueReviewSamples 记录问题的读取与核对接口：问题列表（待处理、已核对、按类别筛选）与版本的问题（字段、归组冲突与同一目录的
// 另一个版本、缺失曲目；最后一份带已核对的归组与“核对之后又有曲目缺失”），核对归组冲突、核对缺失曲目、取消核对与撤销取消核对的响应，
// 依据已变化、过期版本号与筛选条件无效三种错误，以及这张专辑的操作记录。素材是直接写库的两张同一目录的合成专辑：
// 一张有拿不准的介质类型、归组冲突与一首缺失曲目，另一张只有归组冲突（扫描从不改这些行，与扫描造出的相同；
// 真实重扫见 TestPostgreSQLIssueReviewsSurviveARealRescan）。
func recordIssueReviewSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	rootID := uuid.New()
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, filepath.Join(application.allowedRoot, "issues"))
	releaseID, _ := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Sample Split Album", artist: "Sample Quartet", year: 2012, tracks: []string{"present", "missing"}})
	siblingID, _ := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Sample Split Album II", artist: "Sample Quartet", year: 2012, tracks: []string{"present"}})
	mustExec(t, application, `UPDATE releases SET relative_directory='split', edition='Live' WHERE id=$1::uuid OR id=$2::uuid`, releaseID, siblingID)
	mustExec(t, application, `UPDATE releases SET edition=NULL WHERE id=$1::uuid`, siblingID)
	for _, id := range []string{releaseID, siblingID} {
		mustExec(t, application, `INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'same_dir_split','organizer_v2','["split/01.flac","split/02.flac"]','same_directory_conflict')`, id)
	}
	mustExec(t, application, `INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason)
		VALUES($1::uuid,'media_type',to_jsonb('CD'::text),'tag','medium','uncertain_apply','majority_v1',to_jsonb(ARRAY['CD','Vinyl']::text[]),'inconsistent_candidates')`, releaseID)
	samples.record("release-issues/list", call(http.MethodGet, "/api/v1/release-issues", nil, admin), http.StatusOK)
	samples.record("release-issues/get", call(http.MethodGet, "/api/v1/releases/"+releaseID+"/issues", nil, admin), http.StatusOK)
	samples.record("errors/invalid-filter", call(http.MethodGet, "/api/v1/release-issues?status=ignored", nil, admin), http.StatusBadRequest)
	store := governance.NewStore(application.database)
	token := func(issue string) string {
		t.Helper()
		token, err := store.LoadIssueBasisToken(context.Background(), releaseID, issue)
		if err != nil || token == "" {
			t.Fatalf("load the %s basis token: %q, %v", issue, token, err)
		}
		return token
	}
	path := issueReviewPath(releaseID)
	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	samples.record("issue-reviews/reviewed", call(http.MethodPost, path, reviewBody(1, "grouping", token("grouping")), admin, key("0501")), http.StatusOK)
	samples.record("errors/issue-changed", call(http.MethodPost, path, reviewBody(2, "missing_tracks", strings.Repeat("0", 64)), admin, key("0502")), http.StatusConflict)
	if response := call(http.MethodPost, path, reviewBody(2, "missing_tracks", token("missing_tracks")), admin, key("0503")); response.Code != http.StatusOK {
		t.Fatalf("review the missing tracks: HTTP %d: %s", response.Code, response.Body.String())
	}
	unreviewed := call(http.MethodPost, path, unreviewBody(3, "grouping"), admin, key("0504"))
	samples.record("issue-reviews/unreviewed", unreviewed, http.StatusOK)
	samples.record("errors/revision-conflict-issue", call(http.MethodPost, path, unreviewBody(3, "missing_tracks"), admin, key("0505")), http.StatusConflict)
	samples.record("operations/reverted-issue-review", call(http.MethodPost, revertPath(decodeJSONMap(t, unreviewed)["operation_id"].(string)), nil, admin, key("0506")), http.StatusOK)
	samples.record("operations/list-issue-reviews", call(http.MethodGet, "/api/v1/operations?release_id="+releaseID+"&page_size=10", nil, admin), http.StatusOK)
	// 核对之后又有一首曲目缺失：缺失曲目回到待处理，核对行仍在。
	mustExec(t, application, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
		SELECT $2::uuid, media.id, 3, 'Sample Split Album track 3', 'Sample Quartet', 1, $3::uuid, 'split/03.flac', 'missing', NOW(), 'physical' FROM media WHERE media.release_id=$1::uuid`, releaseID, uuid.New(), rootID)
	samples.record("release-issues/get-reviewed", call(http.MethodGet, "/api/v1/releases/"+releaseID+"/issues", nil, admin), http.StatusOK)
	samples.record("release-issues/list-reviewed", call(http.MethodGet, "/api/v1/release-issues?status=reviewed", nil, admin), http.StatusOK)
	samples.record("release-issues/list-missing-tracks", call(http.MethodGet, "/api/v1/release-issues?category=missing_tracks&page_size=1", nil, admin), http.StatusOK)
}

// recordClaimSamples 记录认领接口：预览（四类迁移、跳过新版本已有的修正与已核对的问题、缺失曲目的核对不迁移）、认领的成功响应、
// 认领之后旧版本不再待认领（not_claimable），以及预览已过期（claim_changed）与版本号过期两种错误。素材是直接写库的合成发行：
// 旧版本有两个字段修正、与另一个版本同在一张专辑里并且是它的默认版本、核对了归组冲突和缺失曲目，已经没有可见的曲目；
// 新版本是可见的独立专辑，已有流派修正。
func recordClaimSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	rootID := uuid.New()
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, filepath.Join(application.allowedRoot, "claims"))
	albumID := uuid.New()
	mustExec(t, application, `INSERT INTO release_groups(id) VALUES($1::uuid)`, albumID)
	sourceID, _ := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Sample Replaced Album", artist: "Sample Trio", year: 2001, groupID: albumID, tracks: []string{"missing", "missing"}})
	insertGroupedRelease(t, application, rootID, groupedRelease{title: "Sample Replaced Album (reissue)", artist: "Sample Trio", year: 2008, groupID: albumID, tracks: []string{"present"}})
	targetID, _ := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Sample Replaced Album (new rip)", artist: "Sample Trio", year: 2001, tracks: []string{"present", "present"}})
	mustExec(t, application, `UPDATE releases SET relative_directory='replaced' WHERE id IN ($1::uuid, $2::uuid)`, sourceID, targetID)
	mustExec(t, application, `INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason) VALUES($1::uuid,'same_dir_split','organizer_v2','["replaced/01.flac","replaced/02.flac"]','same_directory_conflict')`, sourceID)
	mustExec(t, application, `UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, sourceID, albumID)
	operationID := uuid.New()
	mustExec(t, application, `INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		SELECT $1::uuid, id, 'release_correction', 'release', $2::uuid, $3::uuid, 'f', 1, 2, '{}', '{}', '{}', 'sample' FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1`, operationID, sourceID, uuid.New())
	mustExec(t, application, `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'title','set','Sample Replaced Album (corrected)',$2::uuid),($1::uuid,'label','set','Sample Label',$2::uuid)`, sourceID, operationID)
	mustExec(t, application, `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id)
		SELECT release_id, 'grouping', jsonb_build_object('reason', reason, 'source_refs', source_refs), $2::uuid FROM release_grouping_evidence WHERE release_id=$1::uuid`, sourceID, operationID)
	mustExec(t, application, `INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'label','set','Sample New Label',$2::uuid)`, targetID, operationID)

	key := func(suffix string) map[string]string {
		return map[string]string{"Idempotency-Key": "00000000-0000-4000-8000-00000000" + suffix}
	}
	// 读取：待认领的列表与详情（含推荐）、专辑详情用的提示；都在认领之前（认领之后旧版本就不再待认领）。
	samples.record("release-claims/list", call(http.MethodGet, "/api/v1/release-claims", nil, admin), http.StatusOK)
	samples.record("release-claims/get", call(http.MethodGet, claimReadPath(sourceID), nil, admin), http.StatusOK)
	samples.record("release-claims/hints", call(http.MethodGet, claimHintsPath(targetID), nil, admin), http.StatusOK)
	samples.record("errors/not-claimable-release", call(http.MethodGet, claimReadPath(targetID), nil, admin), http.StatusNotFound)
	previewResponse := call(http.MethodGet, claimPreviewPath(targetID, sourceID), nil, admin)
	samples.record("release-claims/preview", previewResponse, http.StatusOK)
	preview := decodeJSONMap(t, previewResponse)
	revision, sourceRevision, token := int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), preview["token"].(string)
	samples.record("errors/claim-changed", call(http.MethodPost, claimPath(targetID), claimBody(revision, sourceRevision, sourceID, strings.Repeat("0", 64)), admin, key("0601")), http.StatusConflict)
	samples.record("errors/revision-conflict-claim", call(http.MethodPost, claimPath(targetID), claimBody(revision+5, sourceRevision, sourceID, token), admin, key("0602")), http.StatusConflict)
	samples.record("release-claims/claimed", call(http.MethodPost, claimPath(targetID), claimBody(revision, sourceRevision, sourceID, token), admin, key("0603")), http.StatusOK)
	samples.record("errors/not-claimable", call(http.MethodPost, claimPath(targetID), claimBody(revision+1, sourceRevision+1, sourceID, token), admin, key("0604")), http.StatusConflict)
	samples.record("releases/get-claimed", call(http.MethodGet, "/api/v1/releases/"+targetID, nil, admin), http.StatusOK)
	// 操作记录里的认领：按旧版本（来源）筛选也能找到；撤销之后撤销记录同样带着来源。
	samples.record("operations/list-claims", call(http.MethodGet, "/api/v1/operations?release_id="+sourceID+"&page_size=10", nil, admin), http.StatusOK)
	var claimOperation string
	if err := application.database.QueryRow(`SELECT id::text FROM operations WHERE operation_type='release_claim' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&claimOperation); err != nil {
		t.Fatalf("read the claim operation: %v", err)
	}
	samples.record("operations/reverted-claim", call(http.MethodPost, revertPath(claimOperation), nil, admin, key("0605")), http.StatusOK)
	samples.record("operations/list-claims-reverted", call(http.MethodGet, "/api/v1/operations?release_id="+sourceID+"&page_size=10", nil, admin), http.StatusOK)
}

// 样本里的内嵌歌词：同步歌词带元标签、间奏（空行）、同一时间戳的双语两行与一行多个时间戳；纯文本歌词带分段。
const (
	sampleSyncedLyrics = "[ti:Allegro]\n[ar:Sample Orchestra]\n[00:00.00]\n[00:12.34]First line of the sample\n[00:15.00]Second line of the sample\n[00:15.00]样本的第二行\n[00:20.50][00:40.50]Chorus line"
	samplePlainLyrics  = "First verse, line one\nFirst verse, line two\n\nSecond verse, line one"
)

// recordLyricsSamples 记录歌词接口：同步歌词、纯文本歌词与没有歌词。素材是 recordAlbumDetailSamples 扫描出的合成 FLAC。
func recordLyricsSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	for sample, title := range map[string]string{"tracks/lyrics-synced": "Allegro", "tracks/lyrics-plain": "Andante", "tracks/lyrics-none": "Finale"} {
		var trackID string
		if err := application.database.QueryRow(`SELECT id::text FROM tracks WHERE title=$1 AND source_status='present'`, title).Scan(&trackID); err != nil {
			t.Fatalf("read track %q: %v", title, err)
		}
		samples.record(sample, call(http.MethodGet, "/api/v1/tracks/"+trackID+"/lyrics", nil, admin), http.StatusOK)
	}
}

// rootIDOf 返回按路径登记的根目录 ID。
func rootIDOf(t *testing.T, application *integrationTestApplication, path string) string {
	t.Helper()
	var rootID string
	if err := application.database.QueryRow(`SELECT id::text FROM library_roots WHERE path=$1`, path).Scan(&rootID); err != nil {
		t.Fatalf("read root id: %v", err)
	}
	return rootID
}

// recordPlaybackSamples 记录播放接口：直出格式表、播放预检，以及各个 409 与 416。素材是上面扫描出的合成 FLAC，
// 预检的大小因此是确定的；每个故障都在记录之后立即复原（改回文件名与修改时间、重新启用目录），不影响其他样本。
func recordPlaybackSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser, library, rootID string) {
	t.Helper()
	trackID := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := application.database.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("read track: %v", err)
		}
		return id
	}
	playable := trackID(`SELECT id::text FROM tracks WHERE title='Allegro' AND source_status='present'`)
	cue := trackID(`SELECT id::text FROM tracks WHERE title='Cue One' AND source_kind='cue_virtual'`)

	samples.record("playback/formats", call(http.MethodGet, "/api/v1/playback/formats", nil, admin), http.StatusOK)
	samples.record("tracks/playback", call(http.MethodGet, "/api/v1/tracks/"+playable+"/playback", nil, admin), http.StatusOK)
	samples.record("errors/not-found-track", call(http.MethodGet, "/api/v1/tracks/"+uuid.New()+"/playback", nil, admin), http.StatusNotFound)
	samples.record("errors/not-directly-playable", call(http.MethodGet, "/api/v1/tracks/"+cue+"/playback", nil, admin), http.StatusConflict)
	samples.record("errors/range-not-satisfiable", call(http.MethodGet, "/api/v1/tracks/"+playable+"/audio", nil, admin, map[string]string{"Range": "bytes=999999-"}), http.StatusRequestedRangeNotSatisfiable)

	file := filepath.Join(library, "double-album", "d1-01.flac")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat playable fixture: %v", err)
	}
	if err := os.Rename(file, file+".moved"); err != nil {
		t.Fatalf("hide playable fixture: %v", err)
	}
	samples.record("errors/source-unavailable", call(http.MethodGet, "/api/v1/tracks/"+playable+"/playback", nil, admin), http.StatusConflict)
	if err := os.Rename(file+".moved", file); err != nil {
		t.Fatalf("restore playable fixture: %v", err)
	}
	if err := os.Chtimes(file, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatalf("touch playable fixture: %v", err)
	}
	samples.record("errors/source-changed", call(http.MethodGet, "/api/v1/tracks/"+playable+"/playback", nil, admin), http.StatusConflict)
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore modification time: %v", err)
	}

	if _, err := application.database.Exec(`UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, rootID); err != nil {
		t.Fatalf("disable root: %v", err)
	}
	samples.record("errors/root-disabled", call(http.MethodGet, "/api/v1/tracks/"+playable+"/playback", nil, admin), http.StatusConflict)
	if _, err := application.database.Exec(`UPDATE library_roots SET status='active' WHERE id=$1::uuid`, rootID); err != nil {
		t.Fatalf("restore root: %v", err)
	}
	if response := call(http.MethodGet, "/api/v1/tracks/"+playable+"/playback", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("the playable fixture did not recover: HTTP %d: %s", response.Code, response.Body.String())
	}
}

// recordDerivationSamples 记录派生相关的响应：同一首 ALAC 曲目先因等待时间用完得到 202，音频请求得到“尚未完成”，放行后预检得到
// derived 表示；另有转换失败与服务器忙。它放在最后：登记的曲目是直接写入数据库的，不属于前面扫描出的发行。
func recordDerivationSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser, library playbackLibrary, fake *fakeDerivation) {
	t.Helper()
	add := func(relative string) string {
		return library.addTrack(t, application, alacSpec(relative, mp4Bytes(4096)))
	}
	release := make(chan struct{})
	entered := fake.blockUntil(release)
	preparing := add("derived/01 Preparing.m4a")
	samples.record("tracks/playback-preparing", call(http.MethodGet, preflightPath(preparing), nil, admin), http.StatusAccepted)
	select {
	case <-entered:
	default:
		t.Fatal("the waiting preflight never reached the derivation")
	}
	samples.record("errors/derivation-not-ready", call(http.MethodGet, audioPath(preparing), nil, admin), http.StatusConflict)
	close(release)
	samples.record("tracks/playback-derived", call(http.MethodGet, preflightPath(preparing), nil, admin), http.StatusOK)

	failed := add("derived/02 Failed.m4a")
	fake.await = func(context.Context, derivation.Request) error {
		return &derivation.FailedError{Category: derivation.FailureFFmpegExit, Err: errors.New("ffmpeg exited with status 1")}
	}
	samples.record("errors/derivation-failed", call(http.MethodGet, preflightPath(failed), nil, admin), http.StatusConflict)

	busy := add("derived/03 Busy.m4a")
	fake.await = func(context.Context, derivation.Request) error { return derivation.ErrBusy }
	samples.record("errors/derivation-busy", call(http.MethodGet, preflightPath(busy), nil, admin), http.StatusServiceUnavailable)
}

// waitForScanToFinish 等扫描离开 running，再等 worker 退出：worker 在写入终态之后才释放扫描互斥并清除运行标记，
// 不等它退出，紧接着的下一次启动可能看到残留的“已有扫描”。
func waitForScanToFinish(t *testing.T, application *integrationTestApplication, scanID string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var status string
	for {
		if err := application.database.QueryRow(`SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
			t.Fatalf("read scan status: %v", err)
		}
		if status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan %s did not finish", scanID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := application.app.WaitForScans(waitContext); err != nil {
		t.Fatalf("wait for scan worker: %v", err)
	}
	return status
}

type apiSampleRecorder struct {
	// scope 非空时仅管理对应子目录；扫描反馈的独立样本不互相覆盖。
	scope     string
	t         *testing.T
	samples   map[string][]byte
	forbidden []string
}

func newAPISampleRecorder(t *testing.T, application *integrationTestApplication) *apiSampleRecorder {
	t.Helper()
	return &apiSampleRecorder{
		t:       t,
		samples: map[string][]byte{},
		// 样本不得包含服务器路径、凭据、会话令牌与 Cookie 名。
		forbidden: []string{
			application.allowedRoot, os.TempDir(), identity.SessionCookieName, "password",
			sampleAdminPassword, sampleListenerPassword, sampleArchivistPassword,
		},
	}
}

// forbid 把运行期产生的敏感值（会话令牌）加入禁止出现在样本里的清单。
func (recorder *apiSampleRecorder) forbid(value string) {
	recorder.forbidden = append(recorder.forbidden, value)
}

func (recorder *apiSampleRecorder) record(name string, response *httptest.ResponseRecorder, wantStatus int) {
	t := recorder.t
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("sample %s: expected HTTP %d, got %d: %s", name, wantStatus, response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("sample %s: expected Content-Type application/json, got %q", name, contentType)
	}
	if _, exists := recorder.samples[name]; exists {
		t.Fatalf("sample %s was recorded twice", name)
	}
	body := response.Body.String()
	for _, secret := range recorder.forbidden {
		if apiSampleContainsSecret(body, secret) {
			t.Fatalf("sample %s contains a forbidden value %q: %s", name, secret, body)
		}
	}
	recorder.samples[name] = encodeAPISample(t, name, response.Code, response.Body.Bytes())
}

// apiSampleContainsSecret 仅豁免新增接口公开的四个合同标识；密码字段、哈希与凭据仍禁止录入。
func apiSampleContainsSecret(body, secret string) bool {
	if secret == "password" {
		for _, identifier := range []string{`"password_changed"`, `"password_reset"`, `"current_password_incorrect"`, `"self_password_reset"`} {
			body = strings.ReplaceAll(body, identifier, "")
		}
	}
	return strings.Contains(body, secret)
}

func TestAPISamplesPasswordRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, body, secret string
		want               bool
	}{
		{"改密响应", `{"password_changed":true}`, "password", false},
		{"重置响应", `{"password_reset":true}`, "password", false},
		{"当前密码错误码", `{"error":{"code":"current_password_incorrect"}}`, "password", false},
		{"重置自己错误码", `{"error":{"code":"self_password_reset"}}`, "password", false},
		{"密码字段", `{"password":"secret"}`, "password", true},
		{"新密码字段", `{"new_password":"secret"}`, "password", true},
		{"当前密码字段", `{"current_password":"secret"}`, "password", true},
		{"密码哈希字段", `{"password_hash":"hash"}`, "password", true},
		{"包含公开标识也不放过密码", `{"password_changed":true,"password":"secret"}`, "password", true},
		{"凭据值", `{"message":"archivist-changed-secret"}`, "archivist-changed-secret", true},
		{"会话令牌", `{"token":"a1b2c3"}`, "a1b2c3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiSampleContainsSecret(tc.body, tc.secret); got != tc.want {
				t.Fatalf("脱敏检测 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// finish 只维护当前 recorder 的样本；默认集不覆盖 personal 与 scan-feedback 的独立样本。
func (recorder *apiSampleRecorder) finish() {
	t := recorder.t
	t.Helper()
	if os.Getenv(updateAPISamplesEnv) == "1" {
		if err := filepath.WalkDir(apiSamplesDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(apiSamplesDirectory, path)
			if err != nil {
				return err
			}
			if !recorder.ownsSample(filepath.ToSlash(relative)) {
				return nil
			}
			return os.Remove(path)
		}); err != nil {
			t.Fatalf("clear sample directory: %v", err)
		}
		for name, content := range recorder.samples {
			path := sampleFilePath(name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create sample directory: %v", err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatalf("write sample %s: %v", name, err)
			}
		}
		t.Logf("已重写 %d 个 API 样本；请检查 git diff，并同步前端 decoder 与契约测试", len(recorder.samples))
		return
	}

	committed := map[string][]byte{}
	walkErr := filepath.WalkDir(apiSamplesDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, relErr := filepath.Rel(apiSamplesDirectory, path)
		if relErr != nil {
			return relErr
		}
		if !recorder.ownsSample(filepath.ToSlash(relative)) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		committed[strings.TrimSuffix(filepath.ToSlash(relative), ".json")] = content
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		t.Fatalf("read committed samples: %v", walkErr)
	}

	var problems []string
	for _, name := range sortedKeys(recorder.samples) {
		expected, exists := committed[name]
		switch {
		case !exists:
			problems = append(problems, fmt.Sprintf("缺少样本文件 %s", sampleFilePath(name)))
		case !bytes.Equal(expected, recorder.samples[name]):
			problems = append(problems, fmt.Sprintf("样本 %s 与当前响应不一致：%s", sampleFilePath(name), firstDifference(expected, recorder.samples[name])))
		}
	}
	for _, name := range sortedKeys(committed) {
		if _, produced := recorder.samples[name]; !produced {
			problems = append(problems, fmt.Sprintf("多余的样本文件 %s（本次测试没有产出）", sampleFilePath(name)))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("API 响应样本与提交的文件不一致：\n  %s\n如果这是有意的合同变更，请运行 %s=1 ./scripts/test-integration.sh 重写样本，并同步前端 decoder 与契约测试。",
			strings.Join(problems, "\n  "), updateAPISamplesEnv)
	}
}

// ownsSample 同时保留 personal 的目录/错误前缀隔离与扫描反馈的子目录隔离。
func (recorder *apiSampleRecorder) ownsSample(name string) bool {
	if isSystemAPISample(name) {
		return recorder.scope == "system"
	}
	if strings.HasPrefix(name, "artists/adjudication/") {
		return recorder.scope == "artists/adjudication"
	}
	if recorder.scope == "artist-increment" {
		return isArtistAPISample(name)
	}
	if recorder.scope != "" {
		return strings.HasPrefix(name, recorder.scope+"/")
	}
	return !strings.HasPrefix(name, "scan-feedback/") && !isPersonalAPISample(name) && !isArtistAPISample(name)
}

// 两套独立样本加入后，比较和重写都必须使用相同的所有权边界。
func TestAPISampleRecorderOwnership(t *testing.T) {
	for _, sample := range []struct {
		name     string
		ordinary bool
		feedback bool
	}{
		{"system/backups.json", false, false},
		{"errors/backup-busy.json", false, false},
		{"release-groups/list.json", true, false},
		{"scans/active.json", true, false},
		{"errors/not-found-api.json", true, false},
		{"favorites/list.json", false, false},
		{"playlists/list.json", false, false},
		{"errors/personal-not-found.json", false, false},
		{"scan-feedback/report-ready.json", false, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			system := &apiSampleRecorder{scope: "system"}
			if got := system.ownsSample(sample.name); got != isSystemAPISample(sample.name) {
				t.Errorf("系统样本所有权 = %v", got)
			}
			ordinary := &apiSampleRecorder{}
			feedback := &apiSampleRecorder{scope: "scan-feedback"}
			if got := ordinary.ownsSample(sample.name); got != sample.ordinary {
				t.Errorf("默认样本所有权 = %v，期望 %v", got, sample.ordinary)
			}
			if got := feedback.ownsSample(sample.name); got != sample.feedback {
				t.Errorf("扫描反馈样本所有权 = %v，期望 %v", got, sample.feedback)
			}
		})
	}
}

func sampleFilePath(name string) string {
	return filepath.Join(apiSamplesDirectory, filepath.FromSlash(name)+".json")
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstDifference(committed, actual []byte) string {
	committedLines := strings.Split(string(committed), "\n")
	actualLines := strings.Split(string(actual), "\n")
	for index := 0; index < len(committedLines) || index < len(actualLines); index++ {
		var left, right string
		if index < len(committedLines) {
			left = committedLines[index]
		}
		if index < len(actualLines) {
			right = actualLines[index]
		}
		if left != right {
			return fmt.Sprintf("第 %d 行，提交的是 %q，当前是 %q", index+1, strings.TrimSpace(left), strings.TrimSpace(right))
		}
	}
	return "内容相同"
}

var (
	// sampleUUIDPattern 匹配字符串中出现的每一个 UUID，包括嵌在路径里的（例如播放预检的音频地址）。
	sampleUUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	sampleTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	// sampleDigestPattern 匹配整个字符串是 SHA-256 十六进制摘要的值（问题的依据令牌）：依据含 UUID 与时间，摘要每次运行都不同。
	sampleDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// encodeAPISample 把响应写成 {"status":…, "body":…}：用 UseNumber 保留数字原样，规范化 UUID、时间与依据令牌，
// 按键排序、两空格缩进、末尾换行，使重复运行得到完全相同的字节。
func encodeAPISample(t *testing.T, name string, status int, body []byte) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		t.Fatalf("sample %s: response is not JSON: %v: %s", name, err, body)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("sample %s: response has trailing data: %s", name, body)
	}
	normalizer := newSampleNormalizer()
	normalizer.collect(parsed)
	normalizer.finalize()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"status": status, "body": normalizer.replace(parsed)}); err != nil {
		t.Fatalf("sample %s: encode: %v", name, err)
	}
	return output.Bytes()
}

// sampleNormalizer 把随机 UUID 替换为按首次出现顺序编号的固定值，把时间替换为按先后顺序逐秒递增的固定值：
// 相同的值得到相同的替换，先后关系保留，具体的随机值与墙钟时间不进入样本。
type sampleNormalizer struct {
	uuidOrder []string
	uuids     map[string]string
	instants  map[int64]string
	digests   map[string]string
}

func newSampleNormalizer() *sampleNormalizer {
	return &sampleNormalizer{uuids: map[string]string{}, instants: map[int64]string{}, digests: map[string]string{}}
}

func (normalizer *sampleNormalizer) collect(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedKeys(typed) {
			normalizer.collect(typed[key])
		}
	case []any:
		for _, item := range typed {
			normalizer.collect(item)
		}
	case string:
		for _, found := range sampleUUIDPattern.FindAllString(typed, -1) {
			lower := strings.ToLower(found)
			if _, seen := normalizer.uuids[lower]; !seen {
				normalizer.uuids[lower] = ""
				normalizer.uuidOrder = append(normalizer.uuidOrder, lower)
			}
		}
		if sampleTimePattern.MatchString(typed) {
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				normalizer.instants[parsed.UnixNano()] = ""
			}
		}
		if sampleDigestPattern.MatchString(typed) {
			if _, seen := normalizer.digests[typed]; !seen {
				normalizer.digests[typed] = fmt.Sprintf("%064d", len(normalizer.digests)+1)
			}
		}
	}
}

func (normalizer *sampleNormalizer) finalize() {
	for index, original := range normalizer.uuidOrder {
		normalizer.uuids[original] = fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	}
	instants := make([]int64, 0, len(normalizer.instants))
	for instant := range normalizer.instants {
		instants = append(instants, instant)
	}
	sort.Slice(instants, func(left, right int) bool { return instants[left] < instants[right] })
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for rank, instant := range instants {
		normalizer.instants[instant] = base.Add(time.Duration(rank) * time.Second).Format("2006-01-02T15:04:05Z")
	}
}

func (normalizer *sampleNormalizer) replace(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalizer.replace(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizer.replace(item)
		}
		return result
	case string:
		if sampleTimePattern.MatchString(typed) {
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				return normalizer.instants[parsed.UnixNano()]
			}
		}
		if digest, isDigest := normalizer.digests[typed]; isDigest {
			return digest
		}
		return sampleUUIDPattern.ReplaceAllStringFunc(typed, func(found string) string {
			return normalizer.uuids[strings.ToLower(found)]
		})
	}
	return value
}

// isPersonalAPISample 将独立样本套件隔离，更新旧样本不能删除新能力的证据。
func isPersonalAPISample(name string) bool {
	return strings.HasPrefix(name, "favorites/") || strings.HasPrefix(name, "playlists/") || strings.HasPrefix(name, "errors/personal-")
}
