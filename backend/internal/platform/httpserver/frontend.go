package httpserver

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// 前端的缓存策略。assets/ 下的文件名带内容哈希（Vite 构建），内容变化就换名，所以可以长期缓存；
// 入口页与其他根目录文件的名字固定，必须每次验证才能拿到新版本。
const (
	immutableAssetCacheControl = "public, max-age=31536000, immutable"
	revalidateCacheControl     = "no-cache"
)

// Frontend 提供前端单页应用：
//   - assets/ 下存在的文件原样返回并长期缓存；缺失的资源与 assets/ 目录本身返回 404，不列目录；
//   - 其他存在的文件（licenses/ 下的许可文本等）原样返回，每次验证；
//   - 其余路径（含 / 与根目录以外的目录）回落到 index.html，每次验证，由前端路由决定显示什么。
func Frontend(webAssets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webAssets))
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestedPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestedPath == "." || requestedPath == "" {
			serveIndex(responseWriter, webAssets)
			return
		}
		// 先清理路径再判断前缀：/assets/../licenses/x.txt 清理后不在 assets/ 里，不能因为字面上带着 assets/ 就被长期缓存。
		inAssets := requestedPath == "assets" || strings.HasPrefix(requestedPath, "assets/")
		info, statError := fs.Stat(webAssets, requestedPath)
		if statError != nil || info.IsDir() {
			if inAssets {
				http.NotFound(responseWriter, request)
				return
			}
			serveIndex(responseWriter, webAssets)
			return
		}
		if inAssets {
			responseWriter.Header().Set("Cache-Control", immutableAssetCacheControl)
		} else {
			responseWriter.Header().Set("Cache-Control", revalidateCacheControl)
		}
		request.URL.Path = "/" + requestedPath
		fileServer.ServeHTTP(responseWriter, request)
	})
}

func serveIndex(responseWriter http.ResponseWriter, webAssets fs.FS) {
	indexHTML, err := fs.ReadFile(webAssets, "index.html")
	if err != nil {
		http.Error(responseWriter, "frontend unavailable", http.StatusServiceUnavailable)
		return
	}
	responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
	responseWriter.Header().Set("Cache-Control", revalidateCacheControl)
	_, _ = responseWriter.Write(indexHTML)
}
