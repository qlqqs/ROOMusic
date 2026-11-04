// Package httpapi 是播放历史的 REST 传输层：记录、最近发行、按曲目历史、队列候选与删除自己的历史。
// 所有路由都只作用于当前会话用户，路径与请求体里没有用户 ID，管理员也只能操作自己的记录；
// handler 只做同源校验、鉴权、解码与响应映射，SQL 与规则在 listening。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Handlers 汇集播放历史的 HTTP handler。
type Handlers struct {
	guard   *identity.Guard
	service *listening.Service
}

// New 返回播放历史 handler。
func New(guard *identity.Guard, service *listening.Service) *Handlers {
	return &Handlers{guard: guard, service: service}
}

// Register 在 mux 上注册播放历史路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/me/plays", handlers.record)
	mux.HandleFunc("GET /api/v1/me/plays/recent-releases", handlers.recentReleases)
	mux.HandleFunc("DELETE /api/v1/me/plays", handlers.clear)
	mux.HandleFunc("GET /api/v1/me/plays/tracks", handlers.tracks)
	mux.HandleFunc("GET /api/v1/me/plays/queue", handlers.queue)
	mux.HandleFunc("DELETE /api/v1/me/plays/tracks/{track_id}", handlers.deleteTrack)
}

// playRequest 用指针区分“缺失”与零值：缺失的字段是 invalid_input，不会被当成 0。
type playRequest struct {
	TrackID       *string  `json:"track_id"`
	PlayedSeconds *float64 `json:"played_seconds"`
}

// record 记录当前用户的一次播放。检查顺序：同源（403）→ 登录（401）→ 幂等键（400）→ JSON（400）→ 字段（400）→
// 曲目（404）→ 门槛（400 play_too_short）→ 写入（201，重放同样 201）。同键不同请求是 409，数据库失败是 503。
func (handlers *Handlers) record(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !uuid.Valid(key) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "请求需要有效的 Idempotency-Key")
		return
	}
	var input playRequest
	if !httpserver.DecodeJSON(responseWriter, request, &input) {
		return
	}
	if input.TrackID == nil || !uuid.Valid(*input.TrackID) || input.PlayedSeconds == nil || !listening.ValidPlayedSeconds(*input.PlayedSeconds) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "播放记录的曲目或时长无效")
		return
	}
	recorded, err := handlers.service.Record(request.Context(), user.ID, key, *input.TrackID, *input.PlayedSeconds)
	switch {
	case err == nil:
		httpserver.WriteJSON(responseWriter, http.StatusCreated, recorded)
	case errors.Is(err, listening.ErrInvalidPlay):
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "播放记录的曲目或时长无效")
	case errors.Is(err, listening.ErrTrackNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "曲目不存在")
	case errors.Is(err, listening.ErrPlayTooShort):
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "play_too_short", "播放时间不够，不计入播放历史")
	case errors.Is(err, listening.ErrIdempotencyConflict):
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "idempotency_conflict", "幂等键已用于另一次播放")
	default:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法记录播放")
	}
}

// recentReleases 返回当前用户最近播放的发行（最多 8 个）与是否有任何历史。
func (handlers *Handlers) recentReleases(responseWriter http.ResponseWriter, request *http.Request) {
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	recent, err := handlers.service.RecentReleases(request.Context(), user.ID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取最近播放")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, recent)
}

// clear 清除当前用户的全部播放历史，返回删除的条数；没有记录时是 0，所以重复调用安全。
func (handlers *Handlers) clear(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	deleted, err := handlers.service.Clear(request.Context(), user.ID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法清除播放历史")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]int64{"deleted": deleted})
}
