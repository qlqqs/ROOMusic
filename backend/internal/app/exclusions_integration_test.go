package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLExclusionAPIIdempotentReplay(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	rootPath := createLibraryDirectory(t, application, "exclusions")
	for _, relative := range []string{"Z", "Artist/Live & Rare"} {
		if err := os.MkdirAll(filepath.Join(rootPath, filepath.FromSlash(relative)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rootID := createRoot(t, application, admin, rootPath, "create")
	endpoint := "/api/v1/library-roots/" + rootID + "/exclusions"
	add := func(relative, key string) *httptest.ResponseRecorder {
		return application.request(t, http.MethodPost, endpoint, map[string]string{"path": filepath.Join(rootPath, filepath.FromSlash(relative))}, &admin, map[string]string{"Idempotency-Key": key})
	}
	remove := func(relative, key string) *httptest.ResponseRecorder {
		return application.request(t, http.MethodDelete, endpoint+"?path="+url.QueryEscape(relative), nil, &admin, map[string]string{"Idempotency-Key": key})
	}

	requireListedExclusions(t, application, admin, rootID, []string{})
	added := add("Z", "same-change")
	requireExclusionRoot(t, added, rootID, []string{"Z"})
	requireExclusionRoot(t, add("Artist/Live & Rare", "add-artist"), rootID, []string{"Artist/Live & Rare", "Z"})
	// 重放返回当时的收据，而不是后来改变的集合；UUID 大小写不改变目标身份。
	replayed := application.request(t, http.MethodPost, "/api/v1/library-roots/"+strings.ToUpper(rootID)+"/exclusions", map[string]string{"path": filepath.Join(rootPath, "Z")}, &admin, map[string]string{"Idempotency-Key": "same-change"})
	requireExclusionRoot(t, replayed, rootID, []string{"Z"})
	if !jsonResponsesEqual(t, added, replayed) {
		t.Fatal("添加重放没有返回原收据")
	}
	requireExclusionRoot(t, add("Z", "duplicate"), rootID, []string{"Artist/Live & Rare", "Z"})

	// 幂等键按操作类型隔离，同一个键可以用于添加和移除。
	removed := remove("Z", "same-change")
	requireExclusionRoot(t, removed, rootID, []string{"Artist/Live & Rare"})
	requireExclusionRoot(t, remove("Z", "remove-missing"), rootID, []string{"Artist/Live & Rare"})
	requireExclusionRoot(t, add("Z", "readd"), rootID, []string{"Artist/Live & Rare", "Z"})
	removeReplay := remove("Z", "same-change")
	requireExclusionRoot(t, removeReplay, rootID, []string{"Artist/Live & Rare"})
	if !jsonResponsesEqual(t, removed, removeReplay) {
		t.Fatal("移除重放没有返回原收据")
	}
	requireListedExclusions(t, application, admin, rootID, []string{"Artist/Live & Rare", "Z"})
	requireExclusionRoot(t, remove("Artist/Live & Rare", "remove-artist"), rootID, []string{"Z"})
	requireExclusionRoot(t, remove("Z", "remove-final"), rootID, []string{})
	requireListedExclusions(t, application, admin, rootID, []string{})
	assertRootState(t, application.database, rootID, "active", 1)
	assertOperationCount(t, application.database, rootID, 9)
	var receipts int
	if err := application.database.QueryRow(`SELECT COUNT(*) FROM library_root_operations
		WHERE root_id=$1::uuid AND operation_type IN ('exclude','unexclude')
		AND expected_revision IS NULL AND result_revision=1 AND after_state->>'relative_path' IN ('Z','Artist/Live & Rare')`, rootID).Scan(&receipts); err != nil || receipts != 8 {
		t.Fatalf("排除项操作记录不完整：count=%d err=%v", receipts, err)
	}
}

func TestPostgreSQLExclusionAPIIdempotencyBindsPathAndRoot(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	firstPath := createLibraryDirectory(t, application, "first-exclusions")
	secondPath := createLibraryDirectory(t, application, "second-exclusions")
	for _, root := range []string{firstPath, secondPath} {
		for _, name := range []string{"A", "B"} {
			if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	firstID := createRoot(t, application, admin, firstPath, "create-first")
	secondID := createRoot(t, application, admin, secondPath, "create-second")
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			call := func(id, root, name string) *httptest.ResponseRecorder {
				endpoint := "/api/v1/library-roots/" + id + "/exclusions"
				var body any
				if method == http.MethodPost {
					body = map[string]string{"path": filepath.Join(root, name)}
				} else {
					endpoint += "?path=" + url.QueryEscape(name)
				}
				return application.request(t, method, endpoint, body, &admin, map[string]string{"Idempotency-Key": "bound-target"})
			}
			response := call(firstID, firstPath, "A")
			if response.Code != http.StatusOK {
				t.Fatalf("首次操作失败：%d %s", response.Code, response.Body.String())
			}
			requireAPIError(t, call(firstID, firstPath, "B"), http.StatusConflict, "idempotency_conflict")
			requireAPIError(t, call(secondID, secondPath, "A"), http.StatusConflict, "idempotency_conflict")
			assertOperationCount(t, application.database, secondID, 1)
			requireListedExclusions(t, application, admin, secondID, []string{})
		})
	}
	assertRootState(t, application.database, firstID, "active", 1)
	assertOperationCount(t, application.database, firstID, 3)
	requireListedExclusions(t, application, admin, firstID, []string{})
}

func TestPostgreSQLExclusionAPIRejectsInvalidRequests(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")
	rootPath := createLibraryDirectory(t, application, "exclusions-validation")
	child := filepath.Join(rootPath, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := createLibraryDirectory(t, application, "sibling")
	rootID := createRoot(t, application, admin, rootPath, "create")
	endpoint := "/api/v1/library-roots/" + rootID + "/exclusions"
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := endpoint
		var body any = map[string]string{"path": child}
		if method == http.MethodDelete {
			path += "?path=child"
			body = nil
		}
		headers := map[string]string{"Idempotency-Key": "guard-" + method}
		requireAPIError(t, application.request(t, method, path, body, nil, headers), http.StatusUnauthorized, "unauthorized")
		requireAPIError(t, application.request(t, method, path, body, &ordinary, headers), http.StatusForbidden, "forbidden")
		requireAPIError(t, application.request(t, method, path, body, &admin, map[string]string{"Idempotency-Key": headers["Idempotency-Key"], "Origin": "http://untrusted.example"}), http.StatusForbidden, "origin_forbidden")
		requireAPIError(t, application.request(t, method, path, body, &admin, nil), http.StatusBadRequest, "invalid_input")
		for _, invalid := range []struct {
			id, code string
			status   int
		}{
			{"not-a-uuid", "invalid_id", http.StatusBadRequest},
			{uuid.New(), "not_found", http.StatusNotFound},
		} {
			invalidPath := strings.Replace(path, rootID, invalid.id, 1)
			requireAPIError(t, application.request(t, method, invalidPath, body, &admin, headers), invalid.status, invalid.code)
		}
	}
	for name, path := range map[string]string{"等于根": rootPath, "允许根内兄弟目录": sibling, "允许根之外": t.TempDir()} {
		t.Run(name, func(t *testing.T) {
			response := application.request(t, http.MethodPost, endpoint, map[string]string{"path": path}, &admin, map[string]string{"Idempotency-Key": name})
			requireAPIError(t, response, http.StatusBadRequest, "invalid_library_path")
			if strings.Contains(response.Body.String(), path) {
				t.Fatal("路径错误泄露了绝对路径")
			}
		})
	}
	for _, path := range []string{"", ".", "../child", "child/../other", "/child", "child//other", "child/"} {
		response := application.request(t, http.MethodDelete, endpoint+"?path="+url.QueryEscape(path), nil, &admin, map[string]string{"Idempotency-Key": "invalid-remove"})
		requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
	}
	requireListedExclusions(t, application, admin, rootID, []string{})
	assertOperationCount(t, application.database, rootID, 1)
}

func TestPostgreSQLExclusionAPILimit(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	rootPath := createLibraryDirectory(t, application, "exclusions-limit")
	for _, name := range []string{"entry-001", "overflow"} {
		if err := os.Mkdir(filepath.Join(rootPath, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rootID := createRoot(t, application, admin, rootPath, "create")
	// 批量造边界数据，避免为了上限测试发送一百次 HTTP 或创建一百个文件夹。
	if _, err := application.database.Exec(`INSERT INTO library_root_exclusions(root_id,relative_path)
		SELECT $1::uuid,'entry-'||lpad(n::text,3,'0') FROM generate_series(1,100) n`, rootID); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 100)
	for i := range paths {
		paths[i] = fmt.Sprintf("entry-%03d", i+1)
	}
	endpoint := "/api/v1/library-roots/" + rootID + "/exclusions"
	add := func(name, key string) *httptest.ResponseRecorder {
		return application.request(t, http.MethodPost, endpoint, map[string]string{"path": filepath.Join(rootPath, name)}, &admin, map[string]string{"Idempotency-Key": key})
	}
	requireAPIError(t, add("overflow", "at-limit"), http.StatusConflict, "exclusion_limit")
	requireExclusionRoot(t, add("entry-001", "duplicate-at-limit"), rootID, paths)
	requireListedExclusions(t, application, admin, rootID, paths)
	assertOperationCount(t, application.database, rootID, 2)
	removed := application.request(t, http.MethodDelete, endpoint+"?path=entry-001", nil, &admin, map[string]string{"Idempotency-Key": "free-slot"})
	requireExclusionRoot(t, removed, rootID, paths[1:])
	// 被拒绝的请求没有留下收据；腾出名额后相同请求和键可以成功。
	paths = append(paths[1:], "overflow")
	requireExclusionRoot(t, add("overflow", "at-limit"), rootID, paths)
	assertRootState(t, application.database, rootID, "active", 1)
	assertOperationCount(t, application.database, rootID, 4)
}

func TestPostgreSQLExclusionFolderBrowsingWithinRoot(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")
	rootPath := createLibraryDirectory(t, application, "browse-exclusions")
	child := filepath.Join(rootPath, "Child")
	grandchild := filepath.Join(child, "Nested")
	if err := os.MkdirAll(grandchild, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := createLibraryDirectory(t, application, "browse-sibling")
	rootID := createRoot(t, application, admin, rootPath, "create-root")
	createAndDisableRoot(t, application, admin, child, "child")
	endpoint := "/api/v1/library-folders?root_id=" + rootID
	browse := func(query string) library.FolderListing {
		t.Helper()
		response := application.request(t, http.MethodGet, query, nil, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("目录浏览失败：%d %s", response.Code, response.Body.String())
		}
		var listing library.FolderListing
		if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		return listing
	}
	for _, query := range []string{endpoint, endpoint + "&path=" + url.QueryEscape(rootPath)} {
		listing := browse(query)
		if listing.Path != rootPath || listing.Parent != nil || listing.CanGoUp || listing.Selectable || !listing.Registered {
			t.Fatalf("登记根的导航边界错误：%+v", listing)
		}
		if len(listing.Items) != 1 || listing.Items[0].Path != child || !listing.Items[0].Selectable || !listing.Items[0].Registered {
			t.Fatalf("子目录选择或停用登记标记错误：%+v", listing.Items)
		}
	}
	listing := browse(endpoint + "&path=" + url.QueryEscape(child))
	if listing.Path != child || listing.Parent == nil || *listing.Parent != rootPath || !listing.CanGoUp || !listing.Selectable || !listing.Registered {
		t.Fatalf("子目录导航错误：%+v", listing)
	}
	if len(listing.Items) != 1 || listing.Items[0].Path != grandchild || !listing.Items[0].Selectable || listing.Items[0].Registered {
		t.Fatalf("孙目录选择或登记标记错误：%+v", listing.Items)
	}
	for _, path := range []string{application.allowedRoot, sibling} {
		requireAPIError(t, application.request(t, http.MethodGet, endpoint+"&path="+url.QueryEscape(path), nil, &admin, nil), http.StatusBadRequest, "invalid_library_path")
	}
	for _, query := range []string{"root_id=not-a-uuid", "root_id=" + uuid.New()} {
		path := "/api/v1/library-folders?" + query
		// 鉴权先于 ID 和存在性检查。
		requireAPIError(t, application.request(t, http.MethodGet, path, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
		requireAPIError(t, application.request(t, http.MethodGet, path, nil, &ordinary, nil), http.StatusForbidden, "forbidden")
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/library-folders?root_id=not-a-uuid", nil, &admin, nil), http.StatusBadRequest, "invalid_id")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/library-folders?root_id="+uuid.New(), nil, &admin, nil), http.StatusNotFound, "not_found")
}

func requireExclusionRoot(t *testing.T, response *httptest.ResponseRecorder, rootID string, paths []string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("排除项请求失败：%d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("排除项响应缺少 JSON、缓存或关联头：%v", response.Header())
	}
	requireExclusionRootJSON(t, response.Body.Bytes(), rootID, paths)
}

func requireExclusionRootJSON(t *testing.T, body []byte, rootID string, paths []string) {
	t.Helper()
	var root struct {
		ID            string   `json:"id"`
		Revision      int64    `json:"revision"`
		ExcludedPaths []string `json:"excluded_paths"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if root.ID != rootID || root.Revision != 1 || !reflect.DeepEqual(root.ExcludedPaths, paths) {
		t.Fatalf("排除项响应错误：%s，期望 root=%s revision=1 excluded_paths=%v", body, rootID, paths)
	}
}

func requireListedExclusions(t *testing.T, application *integrationTestApplication, admin integrationTestUser, rootID string, paths []string) {
	t.Helper()
	response := application.request(t, http.MethodGet, "/api/v1/library-roots", nil, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("目录列表失败：%d %s", response.Code, response.Body.String())
	}
	var listing struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	for _, item := range listing.Items {
		var root struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &root); err != nil {
			t.Fatal(err)
		}
		if root.ID == rootID {
			requireExclusionRootJSON(t, item, rootID, paths)
			return
		}
	}
	t.Fatalf("目录列表缺少 %s：%s", rootID, response.Body.String())
}
