// Package httpapi 是受治理操作的 REST 传输层：操作记录列表与撤销。handler 只做参数解析、鉴权和响应映射；
// 记录与共同写入流程在 operations，每种操作类型的撤销与展示由 app 注册的 operations.Kind 提供，所以这里不导入任何业务包。
package httpapi

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 操作记录列表的页大小：默认 50、上限 100。
const (
	defaultOperationPageSize = 50
	maxOperationPageSize     = 100
)

// Handlers 汇集操作记录相关的 HTTP handler。
type Handlers struct {
	database *sql.DB
	guard    *identity.Guard
	kinds    map[string]operations.Kind
	logger   *slog.Logger
}

// New 返回操作记录 handler；kinds 是各操作类型的撤销与展示能力（每个类型一个）。logger 记录写入失败的细节，可以为 nil。
func New(database *sql.DB, guard *identity.Guard, logger *slog.Logger, kinds ...operations.Kind) *Handlers {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	registered := make(map[string]operations.Kind, len(kinds))
	for _, kind := range kinds {
		registered[kind.Type()] = kind
	}
	return &Handlers{database: database, guard: guard, kinds: registered, logger: logger}
}

// Register 在 mux 上注册操作记录路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/operations", handlers.listOperations)
	mux.HandleFunc("POST /api/v1/operations/{id}/revert", handlers.revertOperation)
}

type actorItem struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// operationItem 是列表里的一条记录：谁、何时、哪个目标（当前的状态）、改了什么、撤销关系，以及现在能不能撤销。
// 目标是发行的记录给 release、album 为 null；目标是专辑的记录反过来。不含请求体、前后状态原文、路径与密钥。
type operationItem struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	CreatedAt  time.Time               `json:"created_at"`
	Actor      actorItem               `json:"actor"`
	Release    *operations.ReleaseInfo `json:"release"`
	Album      *operations.AlbumInfo   `json:"album"`
	Changes    any                     `json:"changes"`
	Reverts    *string                 `json:"reverts"`
	RevertedBy *string                 `json:"reverted_by"`
	Revertible bool                    `json:"revertible"`
}

func optionalID(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// listOperations 按时间倒序分页返回操作记录（管理员），可按发行筛选（release_id）、按专辑筛选（group_id）；
// 两个都给时取并集，即这个版本的记录加上这张专辑的记录。检查顺序是 401、403、400（分页、release_id、group_id）、200。
func (handlers *Handlers) listOperations(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	page, pageSize, err := httpserver.ParsePagination(request, defaultOperationPageSize, maxOperationPageSize)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	params := operations.ListParams{Page: page, PageSize: pageSize}
	if releaseID := request.URL.Query().Get("release_id"); releaseID != "" {
		if !uuid.Valid(releaseID) {
			httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
			return
		}
		params.Targets = append(params.Targets, operations.Target{Type: operations.TargetRelease, ID: strings.ToLower(releaseID)})
	}
	if groupID := request.URL.Query().Get("group_id"); groupID != "" {
		if !uuid.Valid(groupID) {
			httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "专辑标识无效")
			return
		}
		params.Targets = append(params.Targets, operations.Target{Type: operations.TargetReleaseGroup, ID: strings.ToLower(groupID)})
	}

	listed, err := operations.List(request.Context(), handlers.database, params)
	if err != nil {
		handlers.writeListError(responseWriter, request, err)
		return
	}
	// 每种类型一次批量查询：撤销记录的状态与它所撤销的操作同类型。
	byType := map[string][]operations.Record{}
	actorIDs := make([]string, 0, len(listed.Items))
	for _, record := range listed.Items {
		byType[record.ChangeType()] = append(byType[record.ChangeType()], record)
		actorIDs = append(actorIDs, record.ActorID)
	}
	descriptions := map[string]operations.Description{}
	for changeType, records := range byType {
		kind, registered := handlers.kinds[changeType]
		if !registered {
			continue
		}
		described, err := kind.Describe(request.Context(), handlers.database, records)
		if err != nil {
			handlers.writeListError(responseWriter, request, err)
			return
		}
		for id, description := range described {
			descriptions[id] = description
		}
	}
	names, err := handlers.guard.Usernames(request.Context(), actorIDs)
	if err != nil {
		handlers.writeListError(responseWriter, request, err)
		return
	}

	items := make([]operationItem, 0, len(listed.Items))
	for _, record := range listed.Items {
		description := descriptions[record.ID]
		items = append(items, operationItem{
			ID: record.ID, Type: record.Type, CreatedAt: record.CreatedAt,
			Actor:   actorItem{ID: record.ActorID, Username: names[record.ActorID]},
			Release: description.Release, Album: description.Album, Changes: description.Changes,
			Reverts: optionalID(record.RevertsID), RevertedBy: optionalID(record.RevertedByID),
			// 撤销记录不能再被撤销，已被撤销的操作也不能再撤销；其余由类型判断目标现在是否还能撤销。
			Revertible: description.Revertible && record.Type != operations.TypeRevert && record.RevertedByID == "",
		})
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": listed.Total},
	})
}

func (handlers *Handlers) writeListError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	handlers.logger.Error("list operations", "request_id", request.Header.Get("X-Request-ID"), "error", err)
	status, code, message := http.StatusServiceUnavailable, "database_error", "无法读取操作记录"
	if errors.Is(err, operations.ErrQuery) {
		code, message = "database_unavailable", "服务暂不可用"
	}
	httpserver.WriteError(responseWriter, request, status, code, message)
}

// revertOperation 撤销一条操作（管理员、同源、Idempotency-Key，没有请求体）。撤销是一次新的操作：按记录的类型找到注册的 Kind，
// 在 operations.Run 的事务里恢复之前的状态并写一条 revert 记录。检查顺序是同源 403、401/403、400（ID、幂等键）、
// 404（记录不存在）、之后由事务内的检查给出 409 not_revertible、already_reverted、revert_conflict 或 404（目标不可见）。
func (handlers *Handlers) revertOperation(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(responseWriter, request)
	if !ok {
		return
	}
	operationID := request.PathValue("id")
	if !uuid.Valid(operationID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "操作记录标识无效")
		return
	}
	operationID = strings.ToLower(operationID)
	key, ok := operations.NormalizeKey(request.Header.Get("Idempotency-Key"))
	if !ok {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "请求需要 Idempotency-Key（UUID）")
		return
	}
	// 事务外先读一次记录，只用来按类型找到 Kind；是否已被撤销等判断都在事务内重新做（锁住原记录之后）。
	original, err := operations.Load(request.Context(), handlers.database, operationID)
	if err != nil {
		if errors.Is(err, operations.ErrNotFound) {
			httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "操作记录不存在")
			return
		}
		handlers.writeListError(responseWriter, request, err)
		return
	}
	kind, registered := handlers.kinds[original.Type]
	if !registered {
		message := "这类操作不支持撤销"
		if original.Type == operations.TypeRevert {
			message = "撤销不能再被撤销；需要的话请重新修改"
		}
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "not_revertible", message)
		return
	}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeRevert, Key: key,
		Fingerprint: operations.Fingerprint(map[string]any{"type": operations.TypeRevert, "operation_id": operationID}),
		RequestID:   request.Header.Get("X-Request-ID"),
	}, operations.RevertExecutor(kind, operationID))
	if err != nil {
		status, code, message := operations.Problem(err)
		if status >= http.StatusInternalServerError {
			handlers.logger.Error("revert operation failed", "request_id", request.Header.Get("X-Request-ID"), "status", status, "error", err)
		}
		httpserver.WriteError(responseWriter, request, status, code, message)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}
