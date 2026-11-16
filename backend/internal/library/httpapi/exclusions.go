package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) addExclusion(w http.ResponseWriter, r *http.Request) {
	handlers.changeExclusion(w, r, false)
}

func (handlers *Handlers) removeExclusion(w http.ResponseWriter, r *http.Request) {
	handlers.changeExclusion(w, r, true)
}

func (handlers *Handlers) changeExclusion(w http.ResponseWriter, r *http.Request, remove bool) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := rootIDFromPath(w, r)
	if !ok {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if remove {
		input.Path = r.URL.Query().Get("path")
		if err := library.ValidateExclusionRelativePath(input.Path); err != nil {
			httpserver.WriteError(w, r, 400, "invalid_input", "排除路径无效")
			return
		}
	} else if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	operation := library.RootOperation{ActorID: actor.ID, IdempotencyKey: operationKey(r), RequestID: r.Header.Get("X-Request-ID")}
	var result library.RootOperationResult
	var err error
	if remove {
		result, err = handlers.service.RemoveExclusion(r.Context(), operation, id, input.Path)
	} else {
		result, err = handlers.service.AddExclusion(r.Context(), operation, id, input.Path, handlers.allowedRoots)
	}
	handlers.writeRootOperationResult(w, r, result, err)
}
