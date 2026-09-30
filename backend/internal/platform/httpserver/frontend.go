package httpserver

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Frontend 提供前端单页应用：存在的文件原样返回，缺失的 assets/ 资源返回 404，其余路径回落到 index.html。
func Frontend(webAssets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webAssets))
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestedPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestedPath == "." || requestedPath == "" {
			serveIndex(responseWriter, webAssets)
			return
		}
		if _, statError := fs.Stat(webAssets, requestedPath); statError != nil {
			if strings.HasPrefix(requestedPath, "assets/") {
				http.NotFound(responseWriter, request)
				return
			}
			serveIndex(responseWriter, webAssets)
			return
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
	_, _ = responseWriter.Write(indexHTML)
}
