// Package httpapi 是曲目播放的 REST 传输层：格式表、播放前预检与音频交付。
// handler 只做鉴权、参数解析与响应映射；来源核对与安全打开在 playback，已建立传输的停机中止由 playback 的读取器执行。
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback"
)

// Handlers 汇集播放相关的 HTTP handler。
type Handlers struct {
	guard     *identity.Guard
	service   *playback.Service
	lifecycle context.Context
	logger    *slog.Logger
	progress  playback.AnalysisProgressProvider
}

// Options 注入后台分析进度。
type Options struct {
	AnalysisProgress playback.AnalysisProgressProvider
}

// New 返回播放 handler。lifecycle 结束（进程关停）时所有已建立的音频传输都会中止。
func New(guard *identity.Guard, service *playback.Service, lifecycle context.Context, logger *slog.Logger, options Options) *Handlers {
	return &Handlers{
		guard: guard, service: service, lifecycle: lifecycle, logger: logger,
		progress: options.AnalysisProgress,
	}
}

// Register 在 mux 上注册播放路由。GET 路由同时匹配 HEAD。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/playback/formats", handlers.formats)
	mux.HandleFunc("GET /api/v1/audio-analysis/progress", handlers.analysisProgress)
	mux.HandleFunc("GET /api/v1/tracks/{id}/playback", handlers.preflight)
	mux.HandleFunc("GET /api/v1/tracks/{id}/audio", handlers.audio)
}

type formatDTO struct {
	SourceKind string `json:"source_kind"`
	Codec      string `json:"codec"`
	// Delivery 是交付方式：original 直出原文件，derived 经服务端处理；MIMEType 与 Codecs 描述交给浏览器的表示。
	Delivery string  `json:"delivery"`
	MIMEType string  `json:"mime_type"`
	Codecs   *string `json:"codecs"`
}

type formatsResponse struct {
	Items []formatDTO `json:"items"`
}

type representationDTO struct {
	Kind      string  `json:"kind"`
	URL       string  `json:"url"`
	MIMEType  string  `json:"mime_type"`
	Codecs    *string `json:"codecs"`
	SizeBytes int64   `json:"size_bytes"`
}

type preflightResponse struct {
	TrackID        string            `json:"track_id"`
	Representation representationDTO `json:"representation"`
}

// codecsValue 把空的 codecs 参数表示为 JSON null。
func codecsValue(codecs string) *string {
	if codecs == "" {
		return nil
	}
	return &codecs
}

func (handlers *Handlers) formats(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	table := handlers.service.Formats()
	items := make([]formatDTO, 0, len(table))
	for _, format := range table {
		items = append(items, formatDTO{
			SourceKind: format.SourceKind, Codec: format.Codec, Delivery: string(format.Delivery),
			MIMEType: format.MIMEType, Codecs: codecsValue(format.Codecs),
		})
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, formatsResponse{Items: items})
}

// preflight 执行与音频接口相同的检查（含打开文件、比对版本、识别容器），返回将要交付的表示。
// 媒体元素读不到错误正文，所以前端先预检：出声之前就能得到确切原因，音频失败后也能再次预检来分类。
func (handlers *Handlers) preflight(responseWriter http.ResponseWriter, request *http.Request) {
	representation, ok := handlers.prepare(responseWriter, request)
	if !ok {
		return
	}
	defer representation.File.Close()
	httpserver.WriteJSON(responseWriter, http.StatusOK, preflightResponse{
		TrackID: representation.TrackID,
		Representation: representationDTO{
			Kind:      string(representation.Format.Delivery),
			URL:       "/api/v1/tracks/" + representation.TrackID + "/audio",
			MIMEType:  representation.Format.MIMEType,
			Codecs:    codecsValue(representation.Format.Codecs),
			SizeBytes: representation.Size,
		},
	})
}

// audio 交付可直接播放的原文件。
// 检查顺序固定：401 → 400 → 404 → 409 → Range（416）。
func (handlers *Handlers) audio(responseWriter http.ResponseWriter, request *http.Request) {
	prepared, ok := handlers.prepare(responseWriter, request)
	if !ok {
		return
	}
	defer prepared.File.Close()
	entityTag := httpserver.EntityTag(prepared.ETag)

	// http.ServeContent 对多范围会生成 multipart，对语法错误只写纯文本 416；先自行预判，使 416 总是统一的 JSON 信封。
	// If-Range 不命中当前强校验值时 Range 本应被整体忽略（返回完整的 200），这里不能先拒绝。
	rangeHeader := request.Header.Get("Range")
	if ifRange := request.Header.Get("If-Range"); ifRange != "" && !ifRangeMatches(ifRange, entityTag) {
		rangeHeader = ""
	}
	if status := playback.CheckRange(rangeHeader, prepared.Size); status == playback.RangeInvalid || status == playback.RangeUnsatisfiable {
		responseWriter.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", prepared.Size))
		httpserver.WriteError(responseWriter, request, http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable", "请求的范围无效")
		return
	}

	header := responseWriter.Header()
	header.Set("Content-Type", prepared.Format.MIMEType)
	// 受保护的音频不得进入共享缓存或浏览器的离线缓存；no-store 不能追溯删除已交付的字节，合同如实说明。
	header.Set("Cache-Control", "private, no-store")
	header.Set("ETag", entityTag)
	header.Set("X-Content-Type-Options", "nosniff")
	reader := playback.NewLifecycleReader(prepared.File, handlers.lifecycle)
	// 修改时间传零值：不发 Last-Modified，只用强 ETag 做条件请求与 If-Range。响应头发出之后出错只会中止传输。
	http.ServeContent(responseWriter, request, "", time.Time{}, reader)
	if reader.Aborted() {
		handlers.logAborted(request, prepared.TrackID)
	}
}

// authorize 完成鉴权与标识校验，返回当前用户与曲目 ID；失败时已写出响应并返回 false。
func (handlers *Handlers) authorize(responseWriter http.ResponseWriter, request *http.Request) (identity.User, string, bool) {
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return identity.User{}, "", false
	}
	trackID := request.PathValue("id")
	if !uuid.Valid(trackID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "曲目标识无效")
		return identity.User{}, "", false
	}
	return user, trackID, true
}

// prepare 完成鉴权、标识校验与来源核对；失败时已写出响应并返回 false。成功时调用方负责关闭文件。
func (handlers *Handlers) prepare(responseWriter http.ResponseWriter, request *http.Request) (*playback.Prepared, bool) {
	_, trackID, ok := handlers.authorize(responseWriter, request)
	if !ok {
		return nil, false
	}
	prepared, err := handlers.service.Prepare(request.Context(), trackID)
	if err != nil {
		handlers.writePrepareError(responseWriter, request, err)
		return nil, false
	}
	return prepared, true
}

// writePrepareError 把 Prepare 的错误映射为稳定的错误码与可展示的文案。文案固定，不含路径或底层错误。
func (handlers *Handlers) writePrepareError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, playback.ErrTrackNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "曲目不存在")
	case errors.Is(err, playback.ErrRootDisabled):
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "root_disabled", "曲目所在的目录已停用")
	case errors.Is(err, playback.ErrNotDirectlyPlayable):
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "not_directly_playable", "这首曲目不能直接播放")
	case errors.Is(err, playback.ErrSourceUnavailable):
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "source_unavailable", "音频文件无法读取")
	case errors.Is(err, playback.ErrSourceChanged):
		httpserver.WriteError(responseWriter, request, http.StatusConflict, "source_changed", "音频文件在上次扫描后有变化，请重新扫描")
	case errors.Is(err, playback.ErrDatabase):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "暂时无法读取曲目")
	default:
		handlers.logger.Error("prepare playback failed", "event", "playback.prepare.failed", "request_id", request.Header.Get("X-Request-ID"), "error", err)
		httpserver.WriteError(responseWriter, request, http.StatusInternalServerError, "internal", "服务内部错误")
	}
}

// logAborted 记录停机中止；只含曲目 ID、请求 ID 与 shutdown 原因，不含路径。客户端先断开时不记录。
func (handlers *Handlers) logAborted(request *http.Request, trackID string) {
	if request.Context().Err() != nil {
		return
	}
	handlers.logger.Info("playback stream aborted", "event", "playback.stream.aborted",
		"request_id", request.Header.Get("X-Request-ID"), "track_id", trackID, "reason", "shutdown")
}

// ifRangeMatches 按 RFC 9110 判断 If-Range 是否命中当前表示：只有与当前强校验值逐字相等的实体标签命中，
// 弱标签、日期（响应没有 Last-Modified）与无法解析的值都不命中。
func ifRangeMatches(value, entityTag string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, `"`) && value == entityTag
}
