package app

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestPostgreSQLScanModeFullDTO(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	member := application.createUser(t, "user")
	root := createLibraryDirectory(t, application, "full-mode")
	writeSyntheticTrack(t, filepath.Join(root, "Album", "01.flac"), 1, "TITLE=合成曲目", "ARTIST=合成艺人", "ALBUM=合成专辑", "TRACKNUMBER=1")
	createRoot(t, application, admin, root, "create-full-mode")
	// 用真实写入屏障保住 running 状态，避免活动查询和重复启动与扫描结束竞速。
	gate := newScanFeedbackGate(t, application, "tracks", "INSERT", "NEW.source_root_id IS NOT NULL")
	started := application.request(t, http.MethodPost, "/api/v1/scans", map[string]string{"mode": "full"}, &admin, nil)
	if started.Code != http.StatusAccepted {
		t.Fatalf("启动完整重扫：HTTP %d：%s", started.Code, started.Body.String())
	}
	scanID := requireScanModeDTO(t, decodeJSONMap(t, started), "full")
	waitScanFeedbackBlockedBy(t, application.database, gate.pid)

	// 请求增量扫描不得覆盖活动完整重扫的模式，也不得创建第二个扫描。
	repeated := application.request(t, http.MethodPost, "/api/v1/scans", map[string]string{"mode": "incremental"}, &admin, nil)
	if repeated.Code != http.StatusOK {
		t.Fatalf("复用活动扫描：HTTP %d：%s", repeated.Code, repeated.Body.String())
	}
	if got := requireScanModeDTO(t, decodeJSONMap(t, repeated), "full"); got != scanID {
		t.Fatalf("重复启动返回扫描 %s，期望沿用 %s", got, scanID)
	}
	for _, user := range []*integrationTestUser{&admin, &member} {
		active := application.request(t, http.MethodGet, "/api/v1/scans/active", nil, user, nil)
		if active.Code != http.StatusOK {
			t.Fatalf("读取活动完整重扫：HTTP %d：%s", active.Code, active.Body.String())
		}
		dto, ok := decodeJSONMap(t, active)["scan"].(map[string]any)
		if !ok || requireScanModeDTO(t, dto, "full") != scanID {
			t.Fatalf("活动扫描缺失或身份不符：%s", active.Body.String())
		}
		detail := application.request(t, http.MethodGet, "/api/v1/scans/"+scanID, nil, user, nil)
		if detail.Code != http.StatusOK || requireScanModeDTO(t, decodeJSONMap(t, detail), "full") != scanID {
			t.Fatalf("读取活动扫描详情：HTTP %d：%s", detail.Code, detail.Body.String())
		}
	}
	gate.release(t)
	if status := waitForScanToFinish(t, application, scanID); status != "succeeded" {
		t.Fatalf("完整重扫终态 = %s，期望 succeeded", status)
	}

	for _, user := range []*integrationTestUser{&admin, &member} {
		detail := application.request(t, http.MethodGet, "/api/v1/scans/"+scanID, nil, user, nil)
		if detail.Code != http.StatusOK {
			t.Fatalf("读取完整重扫终态：HTTP %d：%s", detail.Code, detail.Body.String())
		}
		dto := decodeJSONMap(t, detail)
		if requireScanModeDTO(t, dto, "full") != scanID || dto["status"] != "succeeded" {
			t.Fatalf("完整重扫终态 DTO 不符：%v", dto)
		}
	}
	listed := listScans(t, application, "", &admin)
	if len(listed.Items) != 1 || listed.Pagination.Total != 1 || requireScanModeDTO(t, listed.Items[0], "full") != scanID {
		t.Fatalf("完整重扫记录不符：%+v", listed)
	}
	canceled := application.request(t, http.MethodPost, "/api/v1/scans/"+scanID+"/cancel", nil, &admin, nil)
	if canceled.Code != http.StatusOK {
		t.Fatalf("取消已完成的完整重扫：HTTP %d：%s", canceled.Code, canceled.Body.String())
	}
	if dto := decodeJSONMap(t, canceled); requireScanModeDTO(t, dto, "full") != scanID || dto["status"] != "succeeded" {
		t.Fatalf("终态取消响应丢失模式或改变状态：%v", dto)
	}
	var storedMode string
	if err := application.database.QueryRow(`SELECT scan_mode FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&storedMode); err != nil {
		t.Fatal(err)
	}
	if storedMode != "full" {
		t.Fatalf("持久化模式 = %q，期望 full", storedMode)
	}
}

func TestPostgreSQLScanModeEmptyBodyMatchesIncremental(t *testing.T) {
	logger, logs := jsonLogger()
	application := newIntegrationTestApplicationWithLogger(t, logger)
	admin := application.createUser(t, "admin")
	root := createLibraryDirectory(t, application, "incremental-mode")
	writeSyntheticTrack(t, filepath.Join(root, "Album", "01.flac"), 1, "TITLE=合成曲目", "ARTIST=合成艺人", "ALBUM=合成专辑", "TRACKNUMBER=1")
	rootID := createRoot(t, application, admin, root, "create-incremental-mode")

	// 先用完整扫描建立解析缓存，再比较各类请求的真实增量行为。
	initial := application.request(t, http.MethodPost, "/api/v1/scans", map[string]string{"mode": "full"}, &admin, nil)
	if initial.Code != http.StatusAccepted {
		t.Fatalf("首次扫描：HTTP %d：%s", initial.Code, initial.Body.String())
	}
	initialID := requireScanModeDTO(t, decodeJSONMap(t, initial), "full")
	if status := waitForScanToFinish(t, application, initialID); status != "succeeded" {
		t.Fatalf("首次扫描终态 = %s", status)
	}
	var trackID, releaseID string
	if err := application.database.QueryRow(`SELECT t.id::text,m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1::uuid`, rootID).Scan(&trackID, &releaseID); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		body any
	}{
		{name: "空请求体", body: nil},
		{name: "空对象", body: map[string]any{}},
		{name: "显式增量模式", body: map[string]string{"mode": "incremental"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			started := application.request(t, http.MethodPost, "/api/v1/scans", testCase.body, &admin, nil)
			if started.Code != http.StatusAccepted {
				t.Fatalf("启动增量扫描：HTTP %d：%s", started.Code, started.Body.String())
			}
			scanID := requireScanModeDTO(t, decodeJSONMap(t, started), "incremental")
			if scanID == initialID {
				t.Fatal("上次完整扫描已退出，却仍返回旧扫描")
			}
			if status := waitForScanToFinish(t, application, scanID); status != "succeeded" {
				t.Fatalf("增量扫描终态 = %s", status)
			}
			var summary map[string]any
			for _, event := range logEvents(t, logs, "library.scan.incremental_summary") {
				if event["scan_run_id"] == scanID && event["library_root_id"] == rootID {
					if summary != nil {
						t.Fatal("同一个根重复输出增量统计")
					}
					summary = event
				}
			}
			if summary == nil || summary["mode"] != "incremental" || summary["files_parsed"] != float64(0) || summary["parse_cache_hits"] != float64(1) {
				t.Fatalf("空请求体、空对象和显式 incremental 都应命中缓存而不解析：%v", summary)
			}
			var storedMode string
			if err := application.database.QueryRow(`SELECT scan_mode FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&storedMode); err != nil {
				t.Fatal(err)
			}
			if storedMode != "incremental" {
				t.Fatalf("持久化模式 = %q，期望 incremental", storedMode)
			}
			var gotTrack, gotRelease, status string
			if err := application.database.QueryRow(`SELECT t.id::text,m.release_id::text,t.source_status FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1::uuid`, rootID).Scan(&gotTrack, &gotRelease, &status); err != nil {
				t.Fatal(err)
			}
			if gotTrack != trackID || gotRelease != releaseID || status != "present" {
				t.Fatalf("增量请求改变身份或来源状态：%s/%s/%s，期望 %s/%s/present", gotTrack, gotRelease, status, trackID, releaseID)
			}
		})
	}
}

func TestPostgreSQLScanModeRejectsInvalidInput(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	for _, testCase := range []struct {
		name string
		body any
	}{
		{name: "未知模式", body: map[string]string{"mode": "fast"}},
		{name: "大小写不符", body: map[string]string{"mode": "FULL"}},
		{name: "空模式", body: map[string]string{"mode": ""}},
		{name: "模式为 null", body: map[string]any{"mode": nil}},
		{name: "模式类型错误", body: map[string]int{"mode": 1}},
		{name: "未知字段", body: map[string]any{"mode": "full", "force": true}},
		{name: "不是对象", body: []string{"full"}},
		{name: "重复模式字段", body: json.RawMessage(`{"mode":"full","mode":"incremental"}`)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := application.request(t, http.MethodPost, "/api/v1/scans", testCase.body, &admin, nil)
			requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
		})
	}
	var scans int
	if err := application.database.QueryRow(`SELECT COUNT(*) FROM scan_runs`).Scan(&scans); err != nil {
		t.Fatal(err)
	}
	if scans != 0 {
		t.Fatalf("非法模式请求创建了 %d 条扫描记录，期望没有副作用", scans)
	}
}

func requireScanModeDTO(t *testing.T, dto map[string]any, mode string) string {
	t.Helper()
	id, _ := dto["id"].(string)
	if id == "" || dto["scan_run_id"] != id || dto["mode"] != mode {
		t.Fatalf("扫描 DTO 缺少身份或 mode=%s：%v", mode, dto)
	}
	return id
}
