package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) listFolders(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	query := request.URL.Query()
	var listing library.FolderListing
	var err error
	if query.Has("root_id") {
		rootID := query.Get("root_id")
		if !uuid.Valid(rootID) {
			httpserver.WriteError(responseWriter, request, 400, "invalid_id", "目录标识无效")
			return
		}
		root, loadErr := library.LoadRootLocation(request.Context(), handlers.database, strings.ToLower(rootID))
		if errors.Is(loadErr, library.ErrRootNotFound) {
			httpserver.WriteError(responseWriter, request, 404, "not_found", "目录不存在")
			return
		}
		if loadErr != nil {
			httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
			return
		}
		listing, err = library.ListFoldersWithin(query.Get("path"), root.Path, handlers.allowedRoots, 2000)
	} else {
		listing, err = library.ListFolders(query.Get("path"), handlers.allowedRoots, 2000)
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, 400, "invalid_library_path", "打不开这个文件夹")
		return
	}

	paths := make([]string, 0, len(listing.Items)+1)
	paths = append(paths, listing.Path)
	for _, item := range listing.Items {
		paths = append(paths, item.Path)
	}
	rows, err := handlers.database.QueryContext(request.Context(), "SELECT path FROM library_roots WHERE path = ANY($1::text[])", paths)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	registered := make(map[string]bool, len(paths))
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			httpserver.WriteError(responseWriter, request, 503, "database_error", "无法读取目录")
			return
		}
		registered[path] = true
	}
	if err := rows.Err(); err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_error", "无法读取目录")
		return
	}
	listing.Registered = registered[listing.Path]
	for index := range listing.Items {
		listing.Items[index].Registered = registered[listing.Items[index].Path]
	}
	httpserver.WriteJSON(responseWriter, 200, listing)
}
