package main

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func TestProductionFrontendServesRootFallbackAndAsset404(t *testing.T) {
	handler := httpserver.Frontend(frontendSub())
	for _, requestPath := range []string{"/", "/releases/example"} {
		responseRecorder := httptest.NewRecorder()
		handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if responseRecorder.Code != http.StatusOK {
			t.Fatalf("expected frontend path %q to return 200, got %d", requestPath, responseRecorder.Code)
		}
		if !bytes.Contains(responseRecorder.Body.Bytes(), []byte("ROOMusic")) {
			t.Fatalf("frontend path %q did not serve the embedded index", requestPath)
		}
	}

	responseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing production asset returned %d instead of 404", responseRecorder.Code)
	}
}

// 内嵌的 assets/ 以 immutable 长期缓存：文件名必须带内容哈希（Vite 构建的 [name]-[hash].[ext]，哈希是 8 个 base64url 字符），
// 内容变化才会换名。不带哈希的文件放进 assets/ 会让浏览器永远拿到旧版本，所以这里守住这一点。
var hashedAssetName = regexp.MustCompile(`^.+-([A-Za-z0-9_-]{8})\.[A-Za-z0-9]+$`)

func TestEmbeddedAssetsAreContentHashed(t *testing.T) {
	files := 0
	err := fs.WalkDir(frontendSub(), "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		files++
		match := hashedAssetName.FindStringSubmatch(path.Base(name))
		// 真正的哈希几乎总含数字或大写字母；全小写字母的 8 字符段更像一个普通单词。
		if match == nil || !strings.ContainsAny(match[1], "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("assets/ 下的 %s 没有内容哈希段，不能以 immutable 缓存", name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
	if files == 0 {
		t.Fatal("embedded assets/ is empty; run the frontend build and commit backend/cmd/roomusic/web")
	}
}

func TestProductionFrontendCachePolicy(t *testing.T) {
	assets := frontendSub()
	handler := httpserver.Frontend(assets)
	var anyAsset string
	if err := fs.WalkDir(assets, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && anyAsset == "" {
			anyAsset = name
		}
		return walkErr
	}); err != nil || anyAsset == "" {
		t.Fatalf("no embedded asset to request: %v", err)
	}
	for _, testCase := range []struct {
		target       string
		status       int
		cacheControl string
	}{
		{"/" + anyAsset, http.StatusOK, "public, max-age=31536000, immutable"},
		{"/", http.StatusOK, "no-cache"},
		{"/albums/example", http.StatusOK, "no-cache"},
		{"/licenses/README.txt", http.StatusOK, "no-cache"},
		{"/assets/", http.StatusNotFound, ""},
		{"/licenses/", http.StatusOK, "no-cache"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.target, nil))
		if recorder.Code != testCase.status || recorder.Header().Get("Cache-Control") != testCase.cacheControl {
			t.Errorf("GET %s: HTTP %d with Cache-Control %q, want HTTP %d with %q", testCase.target, recorder.Code, recorder.Header().Get("Cache-Control"), testCase.status, testCase.cacheControl)
		}
		if testCase.target == "/licenses/" && strings.Contains(recorder.Body.String(), "OFL.txt") {
			t.Errorf("GET /licenses/ lists the directory: %s", recorder.Body.String())
		}
	}
}
