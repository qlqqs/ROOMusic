package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// seedArtwork 经真实的 artwork.Store 为一个发行版本写入受管封面（文件加 release_artworks 关联），返回资源标识与字节。
func seedArtwork(t *testing.T, application *integrationTestApplication) (string, []byte) {
	t.Helper()
	rootID := uuid.New()
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, filepath.Join(application.allowedRoot, "artwork-library"))
	releaseID := createCatalogReleaseFixture(t, application, rootID, "封面专辑", "封面艺术家", "artwork", true)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatalf("encode artwork fixture: %v", err)
	}
	data := encoded.Bytes()
	if err := artwork.NewStore(application.dataDirectory).Save(context.Background(), application.database, releaseID, data, "image/png", "embedded"); err != nil {
		t.Fatalf("save artwork fixture: %v", err)
	}
	return fmt.Sprintf("%x.png", sha256.Sum256(data)), data
}

func requireArtworkCacheHeaders(t *testing.T, response *httptest.ResponseRecorder, resourceID string) {
	t.Helper()
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q, want private, no-cache", got)
	}
	if got := response.Header().Get("ETag"); got != `"`+resourceID+`"` {
		t.Fatalf("ETag = %q, want %q", got, `"`+resourceID+`"`)
	}
}

func TestPostgreSQLArtworkCachingUsesETagAndRevalidation(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	resourceID, data := seedArtwork(t, application)
	path := "/api/v1/artworks/" + resourceID
	etag := `"` + resourceID + `"`
	get := func(user *integrationTestUser, ifNoneMatch string) *httptest.ResponseRecorder {
		headers := map[string]string{}
		if ifNoneMatch != "" {
			headers["If-None-Match"] = ifNoneMatch
		}
		return application.request(t, http.MethodGet, path, nil, user, headers)
	}

	// 未登录：即使带着匹配的 ETag 也是 401，不会因为“没有变化”泄露资源是否存在。
	anonymous := get(nil, etag)
	requireAPIError(t, anonymous, http.StatusUnauthorized, "unauthorized")
	if anonymous.Header().Get("ETag") != "" || anonymous.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("a 401 must carry no validator and Cache-Control: no-store, got ETag %q and Cache-Control %q", anonymous.Header().Get("ETag"), anonymous.Header().Get("Cache-Control"))
	}

	// 200：字节、MIME 与验证式缓存头。
	first := get(&user, "")
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), data) {
		t.Fatalf("first read: HTTP %d, %d bytes (want %d)", first.Code, first.Body.Len(), len(data))
	}
	if got := first.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	requireArtworkCacheHeaders(t, first, resourceID)

	// 删掉文件：匹配的 If-None-Match 仍是 304（证明没有读文件），没有校验值的请求才发现文件不见了。
	if err := os.Remove(filepath.Join(application.dataDirectory, "artwork", resourceID)); err != nil {
		t.Fatalf("remove artwork file: %v", err)
	}
	for _, value := range []string{etag, "W/" + etag, `"unrelated", ` + etag, `"unrelated",` + etag, "*"} {
		response := get(&user, value)
		if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
			t.Fatalf("If-None-Match %s: HTTP %d with %d body bytes, want 304 without a body", value, response.Code, response.Body.Len())
		}
		requireArtworkCacheHeaders(t, response, resourceID)
	}
	requireAPIError(t, get(&user, ""), http.StatusNotFound, "not_found")
	requireAPIError(t, get(&user, `"another-resource.png"`), http.StatusNotFound, "not_found")

	// 不存在的资源：即使带着看似匹配的 ETag 也是 404，不是 304。
	missing := application.request(t, http.MethodGet, "/api/v1/artworks/missing.png", nil, &user, map[string]string{"If-None-Match": `"missing.png"`})
	requireAPIError(t, missing, http.StatusNotFound, "not_found")
	if missing.Header().Get("ETag") != "" || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("a 404 must carry no validator and Cache-Control: no-store, got ETag %q and Cache-Control %q", missing.Header().Get("ETag"), missing.Header().Get("Cache-Control"))
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/artworks/missing.png", nil, &user, map[string]string{"If-None-Match": "*"}), http.StatusNotFound, "not_found")

	// 会话被撤销（例如退出登录）：重新验证得到 401，浏览器不能再把缓存里的封面当作仍然有效。
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, user.id)
	requireAPIError(t, get(&user, etag), http.StatusUnauthorized, "unauthorized")
}

func TestPostgreSQLArtworkServesFreshBytesWhenTheTagDiffers(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	resourceID, data := seedArtwork(t, application)
	response := application.request(t, http.MethodGet, "/api/v1/artworks/"+resourceID, nil, &user, map[string]string{"If-None-Match": `"some-older-version.png"`})
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), data) {
		t.Fatalf("a different tag must get the full response: HTTP %d, %d bytes", response.Code, response.Body.Len())
	}
	requireArtworkCacheHeaders(t, response, resourceID)
}

func TestPostgreSQLArtworkRejectsUnsafeStorageKeys(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	rootID := uuid.New()
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, filepath.Join(application.allowedRoot, "unsafe-library"))
	// 这些键都不是 Save 会写出的形状：带引号会破坏 ETag 的语法，带空格或非 ASCII 字符不是内容寻址的文件名。
	for index, key := range []string{`bad"key.png`, "with space.png", "名字.png", `back\slash.png`} {
		releaseID := createCatalogReleaseFixture(t, application, rootID, fmt.Sprintf("不安全封面 %d", index), "艺术家", fmt.Sprintf("unsafe-%d", index), true)
		mustExec(t, application, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,'image/png',1,1,$3,'embedded')`, releaseID, fmt.Sprintf("%064d", index), key)
		for _, ifNoneMatch := range []string{"", "*"} {
			headers := map[string]string{}
			if ifNoneMatch != "" {
				headers["If-None-Match"] = ifNoneMatch
			}
			response := application.request(t, http.MethodGet, "/api/v1/artworks/"+url.PathEscape(key), nil, &user, headers)
			requireAPIError(t, response, http.StatusNotFound, "not_found")
			if response.Header().Get("ETag") != "" {
				t.Fatalf("unsafe key %q produced an ETag: %q", key, response.Header().Get("ETag"))
			}
		}
	}
}

func TestPostgreSQLJSONResponsesAreNotCacheable(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	for _, testCase := range []struct {
		name   string
		method string
		path   string
		user   *integrationTestUser
		status int
	}{
		{"current user", http.MethodGet, "/api/v1/auth/me", &user, http.StatusOK},
		{"catalog list", http.MethodGet, "/api/v1/releases", &user, http.StatusOK},
		{"active scan", http.MethodGet, "/api/v1/scans/active", &user, http.StatusOK},
		{"setup status", http.MethodGet, "/api/v1/setup/status", nil, http.StatusOK},
		{"unauthenticated error", http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized},
		{"forbidden error", http.MethodGet, "/api/v1/users", &user, http.StatusForbidden},
		{"not found error", http.MethodGet, "/api/v1/missing", &user, http.StatusNotFound},
		{"method not allowed error", http.MethodPut, "/api/v1/users", &user, http.StatusMethodNotAllowed},
		{"liveness", http.MethodGet, "/healthz", nil, http.StatusOK},
	} {
		response := application.request(t, testCase.method, testCase.path, nil, testCase.user, nil)
		if response.Code != testCase.status {
			t.Fatalf("%s: HTTP %d, want %d: %s", testCase.name, response.Code, testCase.status, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q, want no-store", testCase.name, got)
		}
	}
}
