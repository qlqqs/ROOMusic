package main

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func productionFrontendTestAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>ROOMusic</title>")},
		"assets/index-AbC12345.js": {Data: []byte("console.log('ROOMusic')")},
		"licenses/README.txt":      {Data: []byte("licenses readme")},
		"licenses/lora-OFL.txt":    {Data: []byte("OFL text")},
	}
}

func TestProductionFrontendServesRootFallbackAndAsset404(t *testing.T) {
	handler := httpserver.Frontend(productionFrontendTestAssets())
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
	assets := frontendSub()
	if assets == nil {
		t.Skip("前端尚未构建，跳过真实内嵌 web/dist 的哈希检查")
	}
	files := 0
	err := fs.WalkDir(assets, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
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
		t.Fatal("内嵌 assets/ 为空，请运行前端构建以生成 web/dist")
	}
}

func TestProductionFrontendCachePolicy(t *testing.T) {
	handler := httpserver.Frontend(productionFrontendTestAssets())
	for _, testCase := range []struct {
		target       string
		status       int
		cacheControl string
	}{
		{"/assets/index-AbC12345.js", http.StatusOK, "public, max-age=31536000, immutable"},
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

func TestMissingFrontendServesUnavailableAndPreservesApplicationRoutes(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		assets fs.FS
	}{
		{"nil", nil},
		{"without_index", fstest.MapFS{"assets/index-AbC12345.js": {Data: []byte("app")}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			application := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Test-Application", "handled")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(r.Method + " " + r.URL.Path))
			})
			handler := frontendHandler(testCase.assets, application, logger)
			if count := strings.Count(logs.String(), `"event":"platform.frontend.missing"`); count != 1 {
				t.Fatalf("启动时缺产物日志应有一条，实际 %d 条：%s", count, logs.String())
			}
			for _, target := range []string{"/", "/releases/example", "/assets/index-AbC12345.js", "/api-example"} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
				if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "前端未构建") {
					t.Errorf("GET %s: 应返回 503 和中文提示，实际 HTTP %d: %s", target, recorder.Code, recorder.Body.String())
				}
				if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("GET %s: Cache-Control = %q，期望 no-store", target, got)
				}
				if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("GET %s: Content-Type = %q，期望中文纯文本", target, got)
				}
				if recorder.Header().Get("X-Test-Application") != "" {
					t.Errorf("GET %s: 前端请求意外进入应用", target)
				}
			}
			for _, request := range []struct{ method, target string }{
				{http.MethodGet, "/api"},
				{http.MethodGet, "/api/"},
				{http.MethodGet, "/api/v1/setup/status"},
				{http.MethodPost, "/api/v1/example"},
				{http.MethodGet, "/healthz"},
				{http.MethodGet, "/readyz"},
			} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(request.method, request.target, nil))
				if recorder.Code != http.StatusAccepted || recorder.Header().Get("X-Test-Application") != "handled" || recorder.Body.String() != request.method+" "+request.target {
					t.Errorf("%s %s: 未保留应用响应，实际 HTTP %d: %s", request.method, request.target, recorder.Code, recorder.Body.String())
				}
			}
			if count := strings.Count(logs.String(), `"event":"platform.frontend.missing"`); count != 1 {
				t.Errorf("请求后缺产物日志应仍有一条，实际 %d 条：%s", count, logs.String())
			}
		})
	}
}
