package httpserver

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testWebAssets() fs.FS {
	return fstest.MapFS{
		"index.html":                       {Data: []byte("<!doctype html><title>ROOMusic</title>")},
		"assets/index-AbC12345.js":         {Data: []byte("console.log('app')")},
		"assets/index-Zy98xW76.css":        {Data: []byte("body{}")},
		"assets/lora-latin-Q1w2E3r4.woff2": {Data: []byte("font")},
		"licenses/README.txt":              {Data: []byte("licenses readme")},
		"licenses/lora-OFL.txt":            {Data: []byte("OFL text")},
		"robots.txt":                       {Data: []byte("User-agent: *")},
	}
}

func serve(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

const (
	immutable  = "public, max-age=31536000, immutable"
	revalidate = "no-cache"
)

func TestFrontendCacheHeaders(t *testing.T) {
	handler := Frontend(testWebAssets())
	for _, testCase := range []struct {
		name         string
		target       string
		status       int
		cacheControl string
		bodyContains string
	}{
		{"root is the index and always revalidates", "/", http.StatusOK, revalidate, "ROOMusic"},
		{"SPA route falls back to the index and revalidates", "/albums/example", http.StatusOK, revalidate, "ROOMusic"},
		{"asset scripts are immutable", "/assets/index-AbC12345.js", http.StatusOK, immutable, "console.log"},
		{"asset styles are immutable", "/assets/index-Zy98xW76.css", http.StatusOK, immutable, "body{}"},
		{"asset fonts are immutable", "/assets/lora-latin-Q1w2E3r4.woff2", http.StatusOK, immutable, "font"},
		{"license files revalidate", "/licenses/lora-OFL.txt", http.StatusOK, revalidate, "OFL text"},
		{"other root files revalidate", "/robots.txt", http.StatusOK, revalidate, "User-agent"},
		// 清理路径后不在 assets/ 里：不能因为字面上带着 assets/ 就被长期缓存。
		{"path traversal out of assets is not immutable", "/assets/../licenses/README.txt", http.StatusOK, revalidate, "licenses readme"},
		{"path traversal into assets is immutable", "/licenses/../assets/index-AbC12345.js", http.StatusOK, immutable, "console.log"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := serve(handler, http.MethodGet, testCase.target)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, testCase.status, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != testCase.cacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, testCase.cacheControl)
			}
			if !strings.Contains(response.Body.String(), testCase.bodyContains) {
				t.Fatalf("body %q does not contain %q", response.Body.String(), testCase.bodyContains)
			}
		})
	}
}

func TestFrontendIndexHasNoValidatorAndKeepsItsContentType(t *testing.T) {
	response := serve(Frontend(testWebAssets()), http.MethodGet, "/")
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	// 入口页很小，每次重新下载比维护校验值更简单：不发 ETag 与 Last-Modified。
	for _, header := range []string{"ETag", "Last-Modified"} {
		if got := response.Header().Get(header); got != "" {
			t.Fatalf("index should not carry %s, got %q", header, got)
		}
	}
}

func TestFrontendDoesNotListDirectories(t *testing.T) {
	handler := Frontend(testWebAssets())
	for _, target := range []string{"/assets", "/assets/", "/assets/./"} {
		response := serve(handler, http.MethodGet, target)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", target, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != "" {
			t.Fatalf("%s: a 404 must not carry Cache-Control, got %q", target, got)
		}
		for _, name := range []string{"index-AbC12345.js", "lora-latin"} {
			if strings.Contains(response.Body.String(), name) {
				t.Fatalf("%s: response lists the directory: %s", target, response.Body.String())
			}
		}
	}
	// 其他目录回落到入口页，由前端路由显示“页面不存在”，同样不列目录。
	for _, target := range []string{"/licenses", "/licenses/"} {
		response := serve(handler, http.MethodGet, target)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ROOMusic") {
			t.Fatalf("%s: expected the index fallback, got HTTP %d: %s", target, response.Code, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != revalidate {
			t.Fatalf("%s: Cache-Control = %q, want %q", target, got, revalidate)
		}
		if strings.Contains(response.Body.String(), "lora-OFL.txt") {
			t.Fatalf("%s: response lists the directory: %s", target, response.Body.String())
		}
	}
}

func TestFrontendMissingAssetIsNotFoundAndNeverFallsBack(t *testing.T) {
	handler := Frontend(testWebAssets())
	for _, target := range []string{"/assets/missing-AbC12345.js", "/assets/deep/missing.css", "/assets/index-AbC12345.js/extra"} {
		response := serve(handler, http.MethodGet, target)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", target, response.Code)
		}
		if strings.Contains(response.Body.String(), "ROOMusic") {
			t.Fatalf("%s: a missing asset must not fall back to the index", target)
		}
		if got := response.Header().Get("Cache-Control"); got != "" {
			t.Fatalf("%s: a 404 must not carry Cache-Control, got %q", target, got)
		}
	}
}

func TestFrontendHeadRequestsCarryTheSameCacheHeaders(t *testing.T) {
	handler := Frontend(testWebAssets())
	for target, want := range map[string]string{"/assets/index-AbC12345.js": immutable, "/": revalidate, "/licenses/lora-OFL.txt": revalidate} {
		response := serve(handler, http.MethodHead, target)
		if response.Code != http.StatusOK {
			t.Fatalf("HEAD %s: status = %d", target, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != want {
			t.Fatalf("HEAD %s: Cache-Control = %q, want %q", target, got, want)
		}
	}
}

func TestFrontendWithoutIndexReportsUnavailable(t *testing.T) {
	handler := Frontend(fstest.MapFS{"assets/app-AbC12345.js": {Data: []byte("x")}})
	response := serve(handler, http.MethodGet, "/anything")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got == immutable {
		t.Fatalf("an unavailable frontend must never be cached as immutable")
	}
}
