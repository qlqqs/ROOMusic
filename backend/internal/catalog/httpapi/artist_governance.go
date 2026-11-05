package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ArtistGovernanceHandlers 由 app 单独装配收藏解析端口，不给扫描或普通读取增加写能力。
type ArtistGovernanceHandlers struct {
	base     *Handlers
	commands *governance.ArtistGovernance
}

func NewArtistGovernance(database *sql.DB, guard *identity.Guard, logger *slog.Logger, resolution governance.ArtistResolutionRefresher) *ArtistGovernanceHandlers {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ArtistGovernanceHandlers{base: &Handlers{database: database, guard: guard, logger: logger}, commands: governance.NewArtistGovernance(resolution)}
}
func (h *ArtistGovernanceHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/artists/{id}/merge", h.merge)
	mux.HandleFunc("POST /api/v1/artists/{id}/split-preview", h.splitPreview)
	mux.HandleFunc("POST /api/v1/artists/{id}/split", h.split)
	mux.HandleFunc("PATCH /api/v1/artists/{id}/name", h.rename)
	mux.HandleFunc("POST /api/v1/artists/{id}/candidate-ignore", h.ignore)
	mux.HandleFunc("GET /api/v1/artist-candidates", h.candidates)
}
func (h *ArtistGovernanceHandlers) authorize(w http.ResponseWriter, r *http.Request, write bool) (actorID, id, key string, ok bool) {
	if !h.base.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, allowed := h.base.guard.RequireAdmin(w, r)
	if !allowed {
		return
	}
	actorID = actor.ID
	id = r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "艺人标识无效")
		return
	}
	id = strings.ToLower(id)
	if write {
		key, allowed = operations.NormalizeKey(r.Header.Get("Idempotency-Key"))
		if !allowed {
			httpserver.WriteError(w, r, 400, "invalid_input", "请求需要 Idempotency-Key（UUID）")
			return
		}
	}
	ok = true
	return
}
func decodeArtistCommand(w http.ResponseWriter, r *http.Request, destination any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !utf8.Valid(raw) {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效或超过预算")
		return false
	}
	if err := uniqueArtistJSON(raw); err != nil {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return false
	}
	// auto 是明确清除意图，不接受带 null 的 name 伪装成没有字段。
	if rename, ok := destination.(*governance.ArtistRenameRequest); ok && rename.Mode == "auto" {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["name"]; exists {
			httpserver.WriteError(w, r, 400, "invalid_input", "恢复自动名称不能带名称")
			return false
		}
	}
	return true
}
func uniqueArtistJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected object")
	}
	if err := artistJSONContainer(d, '{'); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func artistJSONContainer(d *json.Decoder, kind json.Delim) error {
	keys := map[string]bool{}
	for d.More() {
		if kind == '{' {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate JSON key")
			}
			keys[key] = true
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter != '{' && delimiter != '[' {
				return errors.New("invalid JSON")
			}
			if err := artistJSONContainer(d, delimiter); err != nil {
				return err
			}
		}
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if (kind == '{' && end != json.Delim('}')) || (kind == '[' && end != json.Delim(']')) {
		return errors.New("invalid JSON")
	}
	return nil
}
func (h *ArtistGovernanceHandlers) run(w http.ResponseWriter, r *http.Request, actor, key, kind, fingerprint string, execute operations.Executor) {
	result, err := operations.Run(r.Context(), h.base.database, operations.Spec{ActorID: actor, Key: key, Type: kind, Fingerprint: fingerprint, RequestID: r.Header.Get("X-Request-ID")}, execute)
	if err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result.Response)
}
func (h *ArtistGovernanceHandlers) merge(w http.ResponseWriter, r *http.Request) {
	actor, id, key, ok := h.authorize(w, r, true)
	if !ok {
		return
	}
	var command governance.ArtistMergeRequest
	if !decodeArtistCommand(w, r, &command) {
		return
	}
	command.ArtistID = id
	if err := command.Normalize(); err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	h.run(w, r, actor, key, operations.TypeArtistMerge, command.Fingerprint(), h.commands.MergeExecutor(command))
}
func (h *ArtistGovernanceHandlers) split(w http.ResponseWriter, r *http.Request) {
	actor, id, key, ok := h.authorize(w, r, true)
	if !ok {
		return
	}
	var command governance.ArtistSplitRequest
	if !decodeArtistCommand(w, r, &command) {
		return
	}
	command.ArtistID = id
	if err := command.Normalize(); err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	h.run(w, r, actor, key, operations.TypeArtistSplit, command.Fingerprint(), h.commands.SplitExecutor(command))
}
func (h *ArtistGovernanceHandlers) rename(w http.ResponseWriter, r *http.Request) {
	actor, id, key, ok := h.authorize(w, r, true)
	if !ok {
		return
	}
	var command governance.ArtistRenameRequest
	if !decodeArtistCommand(w, r, &command) {
		return
	}
	command.ArtistID = id
	if err := command.Normalize(); err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	h.run(w, r, actor, key, operations.TypeArtistRename, command.Fingerprint(), h.commands.RenameExecutor(command))
}
func (h *ArtistGovernanceHandlers) ignore(w http.ResponseWriter, r *http.Request) {
	actor, id, key, ok := h.authorize(w, r, true)
	if !ok {
		return
	}
	var command governance.ArtistCandidateIgnoreRequest
	if !decodeArtistCommand(w, r, &command) {
		return
	}
	command.ArtistID = id
	if err := command.Normalize(); err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	h.run(w, r, actor, key, operations.TypeArtistCandidateIgnore, command.Fingerprint(), h.commands.CandidateIgnoreExecutor(command))
}
func (h *ArtistGovernanceHandlers) splitPreview(w http.ResponseWriter, r *http.Request) {
	_, id, _, ok := h.authorize(w, r, false)
	if !ok {
		return
	}
	var scope governance.ArtistSplitScope
	if !decodeArtistCommand(w, r, &scope) {
		return
	}
	if err := scope.Normalize(); err != nil {
		h.base.writeOperationError(w, r, err)
		return
	}
	preview, err := governance.PreviewArtistSplit(r.Context(), h.base.database, id, scope)
	if err != nil {
		h.readError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, preview)
}
func (h *ArtistGovernanceHandlers) candidates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.base.guard.RequireAdmin(w, r); !ok {
		return
	}
	page, size, err := httpserver.ParsePagination(r, 50, 100)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
		return
	}
	result, err := governance.ListArtistCandidates(r.Context(), h.base.database, page, size)
	if err != nil {
		h.readError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func (h *ArtistGovernanceHandlers) readError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, governance.ErrArtistPreviewPreparing) {
		httpserver.WriteError(w, r, 503, "artist_catalog_preparing", "艺人资料正在整理")
		return
	}
	var failure *operations.Failure
	if errors.As(err, &failure) {
		h.base.writeOperationError(w, r, err)
		return
	}
	h.base.logger.Error("read artist governance", "request_id", r.Header.Get("X-Request-ID"), "error", err)
	httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
}
