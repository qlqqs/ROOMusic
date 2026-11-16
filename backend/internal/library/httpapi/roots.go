package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) listRoots(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	roots, err := handlers.service.ListRoots(request.Context())
	if err != nil {
		handlers.writeRootOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]any{"items": roots})
}

func (handlers *Handlers) addRoot(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	actor, _ := handlers.guard.Authenticate(request)
	key := operationKey(request)
	var input struct {
		Path string `json:"path"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &input) {
		return
	}
	result, err := handlers.service.AddRoot(request.Context(), library.RootOperation{ActorID: actor.ID, IdempotencyKey: key, RequestID: request.Header.Get("X-Request-ID")}, input.Path, handlers.allowedRoots)
	handlers.writeRootOperationResult(responseWriter, request, result, err)
}
