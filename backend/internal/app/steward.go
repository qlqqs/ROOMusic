package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/backup"
	backuptools "github.com/qlqq/roomusic/backend/internal/backup/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	dsptools "github.com/qlqq/roomusic/backend/internal/dspsettings/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identitytools "github.com/qlqq/roomusic/backend/internal/identity/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/library"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/listening"
	listeningtools "github.com/qlqq/roomusic/backend/internal/listening/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/personal"
	personaltools "github.com/qlqq/roomusic/backend/internal/personal/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	diagnostictools "github.com/qlqq/roomusic/backend/internal/platform/diagnostics/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/playback"
	playbacktools "github.com/qlqq/roomusic/backend/internal/playback/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	stewardapi "github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	stewardmcp "github.com/qlqq/roomusic/backend/internal/steward/mcp"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
	"github.com/qlqq/roomusic/backend/internal/zones"
	zonestools "github.com/qlqq/roomusic/backend/internal/zones/stewardtools"
)

type stewardConfig struct {
	music        *musicbrainz.Client
	covers       *coverartarchive.Client
	diagnostics  *diagnostics.Service
	backups      *backup.Service
	allowedRoots []string
	audio        *playback.Service
	progress     playback.AnalysisProgressProvider
	dsp          *dspsettings.Service
}

func newInstanceSecrets(dataDirectory string) *secret.Store {
	return secret.New(filepath.Join(dataDirectory, "steward", "instance.key"))
}

// registerSteward 在接受请求前恢复会话状态；失败时记录日志并让管家接口返回 503。
func registerSteward(lifecycle context.Context, mux *http.ServeMux, database *sql.DB, guard *identity.Guard, secrets *secret.Store, logger *slog.Logger, scans *library.Service, kinds []operations.Kind, playback *zones.Service, history *listening.Service, personalLibrary *personal.Service, configurations ...stewardConfig) *runtime.Supervisor {
	store := runtime.NewStore(database).WithOperationLookup(func(ctx context.Context, tx *sql.Tx, userID, key string) (runtime.RecordedOperation, error) {
		result, err := operations.LookupResult(ctx, tx, userID, key)
		if errors.Is(err, operations.ErrNotFound) {
			return runtime.RecordedOperation{}, nil
		}
		return runtime.RecordedOperation{ID: result.OperationID, Output: result.Response}, err
	})
	if database != nil {
		ctx, cancel := context.WithTimeout(lifecycle, 10*time.Second)
		count, err := store.ResetRunningConversations(ctx)
		cancel()
		if err != nil {
			logger.Error("管家会话状态恢复失败", "event", "steward.recovery.failed", "message", "管家会话状态恢复失败", "module", "steward")
			unavailable := func(w http.ResponseWriter, r *http.Request) {
				httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "管家会话状态恢复失败")
			}
			for _, prefix := range []string{"/api/v1/me/steward", "/api/v1/admin/steward"} {
				mux.HandleFunc(prefix, unavailable)
				mux.HandleFunc(prefix+"/", unavailable)
			}
			mux.HandleFunc("GET /api/v1/admin/curation/conversations", unavailable)
			mux.HandleFunc("/api/v1/mcp", unavailable)
			return nil
		}
		logger.Info("管家会话状态已恢复", "event", "steward.recovery.completed", "message", "管家会话状态已恢复", "module", "steward", "conversations", count)
	}
	webService := web.New(store, secrets, func(ctx context.Context, user identity.User) (*http.Client, error) {
		return stewardapi.OutboundClient(ctx, user, store)
	}, logger)
	memories := memory.NewStore(database)
	registry := tool.NewRegistry()
	var supervisor *runtime.Supervisor
	definitions := catalogtools.StewardTools(catalog.NewStore(database), governance.NewStore(database))
	definitions = append(definitions, governancetools.StewardTools(database, artistResolutionAdapter{})...)
	definitions = append(definitions, operationtools.RevertTools(database, kinds...)...)
	definitions = append(definitions, librarytools.StewardTools(scans)...)
	if len(configurations) > 0 {
		configuration := configurations[0]
		if configuration.covers != nil {
			webService.WithCovers(configuration.covers)
		}
		if configuration.music != nil {
			webService.WithMusic(configuration.music)
		}
		definitions = append(definitions, librarytools.ConfigTools(scans, configuration.allowedRoots, logger)...)
		definitions = append(definitions, diagnostictools.StewardTools(configuration.diagnostics)...)
		definitions = append(definitions, backuptools.StewardTools(configuration.backups, database, logger)...)
		definitions = append(definitions, identitytools.StewardTools(database, logger, func(userID string) { supervisor.CancelUser(userID) })...)
		definitions = append(definitions, playbacktools.StewardTools(configuration.audio, catalog.NewStore(database), configuration.progress)...)
		definitions = append(definitions, zonestools.SettingsTools(playback, logger)...)
		definitions = append(definitions, dsptools.StewardTools(configuration.dsp, playback, logger)...)
	}
	definitions = append(definitions, operationtools.StewardTools(database, kinds...)...)
	definitions = append(definitions, zonestools.StewardTools(playback, catalog.NewStore(database))...)
	definitions = append(definitions, listeningtools.StewardTools(history)...)
	definitions = append(definitions, personaltools.StewardTools(personalLibrary)...)
	definitions = append(definitions, webService.StewardTools()...)
	definitions = append(definitions, memories.StewardTools()...)
	for _, definition := range definitions {
		definition = withCurrentStewardUser(definition, guard, store)
		if definition.Scope == tool.ScopeShared {
			handler := definition.Handler
			definition.Handler = func(ctx context.Context, caller tool.Caller, input json.RawMessage) (json.RawMessage, error) {
				if caller.ReviewID != "" {
					ctx = operations.WithReviewOrigin(ctx, caller.ReviewID, caller.ReviewDecision)
				}
				return handler(ctx, caller, input)
			}
		}
		if err := registry.Register(definition); err != nil {
			// 工具合同是编译期装配；重复名称或非法 schema 属于程序错误。
			panic(err)
		}
	}
	runner := runtime.NewRunner(store, registry, logger).WithMemories(memories).WithReviewEvidence(func(ctx context.Context, caller tool.Caller, item runtime.PlanItem) (runtime.ReviewEvidenceFacts, error) {
		input, err := reviewTargetInput(ctx, database, item)
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		targets, err := catalogtools.ReadReviewTargets(ctx, catalog.NewStore(database), governance.NewStore(database), input)
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		filters := make([]operations.Target, 0, len(targets))
		for _, target := range targets {
			filters = append(filters, operations.Target{Type: target.Type, ID: target.ID})
		}
		if len(filters) == 0 {
			return runtime.ReviewEvidenceFacts{}, runtime.ErrReviewUnavailable
		}
		// 最近操作限定为公开列表第一页的 20 条；不外发操作正文或操作者。
		page, err := operations.List(ctx, database, operations.ListParams{Targets: filters, Page: 1, PageSize: 20})
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		external, err := reviewExternal(ctx, database, targets)
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		raw, err := json.Marshal(targets)
		return runtime.ReviewEvidenceFacts{Targets: raw, External: external, RecentOperationCount: len(page.Items)}, err
	})
	supervisor = runtime.NewSupervisor(lifecycle, runner, logger)
	tokens := token.NewStore(database, guard)
	mux.Handle("/api/v1/mcp", stewardmcp.New(guard, tokens, store, registry, logger))
	stewardapi.New(guard, store, secrets, runner, logger).WithSupervisor(supervisor).WithMemories(memories).WithWeb(webService).WithTokens(tokens).WithCredentialCheck(func(r *http.Request, user identity.User) (bool, error) {
		cookie, err := r.Cookie(identity.SessionCookieName)
		if err != nil || cookie.Value == "" {
			return false, nil
		}
		// 现有 Guard 会丢弃查询错误分类；长流复查保留错误，以便数据库暂时不可用时下次再查。
		var valid bool
		err = database.QueryRowContext(r.Context(), `SELECT EXISTS (
 SELECT 1 FROM sessions s JOIN users u ON u.id=s.user_id
 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > NOW()
 AND u.disabled_at IS NULL AND u.id=$2::uuid AND u.role=$3)`, identity.HashSessionToken(cookie.Value), user.ID, user.Role).Scan(&valid)
		return valid, err
	}).Register(mux)
	return supervisor
}

// withCurrentStewardUser 在预览及实际调用前重读身份，覆盖长对话、确认后执行与 MCP。
// 注册表先做入口快照检查，此处再按最新角色检查并把新身份传给业务工具。
func withCurrentStewardUser(definition tool.Definition, guard *identity.Guard, store *runtime.Store) tool.Definition {
	refresh := func(ctx context.Context, caller tool.Caller) (tool.Caller, error) {
		user, err := guard.EnabledUser(ctx, caller.User.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return caller, &tool.Error{Code: tool.CodePermissionDenied, Message: "成员已停用或不存在"}
		}
		if err != nil {
			if ctx.Err() != nil {
				return caller, ctx.Err()
			}
			return caller, &tool.Error{Code: "database_unavailable", Message: "暂时无法核对成员身份"}
		}
		caller.User = user
		if !definition.MinRole.Allows(user) {
			return caller, &tool.Error{Code: tool.CodePermissionDenied, Message: "无权调用此工具"}
		}
		if user.Role != "admin" {
			enabled, err := store.GetMembersEnabled(ctx)
			if err != nil {
				return caller, &tool.Error{Code: "database_unavailable", Message: "暂时无法核对管家权限"}
			}
			if !enabled {
				return caller, &tool.Error{Code: tool.CodePermissionDenied, Message: "成员管家功能已关闭"}
			}
		}
		if definition.Available != nil && !definition.Available(ctx, caller) {
			return caller, &tool.Error{Code: tool.CodePermissionDenied, Message: "此工具当前不可用"}
		}
		return caller, nil
	}
	handler := definition.Handler
	definition.Handler = func(ctx context.Context, caller tool.Caller, input json.RawMessage) (json.RawMessage, error) {
		current, err := refresh(ctx, caller)
		if err != nil {
			return nil, err
		}
		return handler(ctx, current, input)
	}
	if preview := definition.Preview; preview != nil {
		definition.Preview = func(ctx context.Context, caller tool.Caller, input json.RawMessage) (tool.Preview, error) {
			current, err := refresh(ctx, caller)
			if err != nil {
				return tool.Preview{}, err
			}
			return preview(ctx, current, input)
		}
	}
	return definition
}

// reviewTargetInput 将工具的对象标识投影到目录审查读取入口。
func reviewTargetInput(ctx context.Context, db *sql.DB, item runtime.PlanItem) (json.RawMessage, error) {
	switch item.Tool {
	case "resolve_release_match":
		var input struct {
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(item.Input, &input); err != nil {
			return nil, err
		}
		var releaseID string
		err := db.QueryRowContext(ctx, `SELECT target_id::text FROM curation_pending_items WHERE id=$1::uuid AND target_type='release'`, input.ItemID).Scan(&releaseID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, runtime.ErrReviewUnavailable
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"release_id": releaseID})
	default:
		if !catalogtools.SupportsReviewEvidence(item.Tool) {
			return nil, runtime.ErrReviewEvidenceUnsupported
		}
		return item.Input, nil
	}
}

type reviewLink struct {
	ObjectType string `json:"object_type"`
	ExternalID string `json:"external_id"`
	Method     string `json:"method"`
	Confidence string `json:"confidence"`
}

type externalRelease struct {
	ReleaseID string          `json:"release_id"`
	Links     []reviewLink    `json:"links"`
	Pending   json.RawMessage `json:"pending"`
}

func reviewExternal(ctx context.Context, db *sql.DB, targets []catalogtools.ReviewTarget) (json.RawMessage, error) {
	data := []externalRelease{}
	for _, target := range targets {
		if target.Type != "release" {
			continue
		}
		release, err := reviewExternalRelease(ctx, db, target.ID)
		if err != nil {
			return nil, err
		}
		data = append(data, release)
	}
	return json.Marshal(struct {
		Source string            `json:"source"`
		Data   []externalRelease `json:"data"`
	}{"program_untrusted_external", data})
}

// 外部证据只保留审查所需的对应与候选字段，具体写入记录和程序原始证据留在本地。
func reviewExternalRelease(ctx context.Context, db *sql.DB, id string) (externalRelease, error) {
	release := externalRelease{ReleaseID: id, Links: []reviewLink{}}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return release, err
	}
	defer tx.Rollback()
	var groupID string
	var tracks []byte
	err = tx.QueryRowContext(ctx, `SELECT r.group_id::text,ARRAY_TO_JSON(ARRAY(
        SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=r.id ORDER BY t.id))
        FROM releases r WHERE r.id=$1`, id).Scan(&groupID, &tracks)
	if err != nil {
		return release, err
	}
	var trackIDs []string
	if err := json.Unmarshal(tracks, &trackIDs); err != nil {
		return release, err
	}
	for _, group := range []struct {
		kind string
		ids  []string
	}{
		{"release", []string{id}}, {"release_group", []string{groupID}}, {"track", trackIDs},
	} {
		links, err := curation.LinksOf(ctx, tx, group.kind, group.ids)
		if err != nil {
			return release, err
		}
		for _, link := range links {
			release.Links = append(release.Links, reviewLink{link.ObjectType, link.ExternalID, link.Method, link.Confidence})
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'kind',p.kind,'conflicts',p.conflicts,'candidates',(
          SELECT COALESCE(jsonb_agg(jsonb_build_object(
            'external_id',c->'external_id','title',c->'title','artist',c->'artist','date',c->'date',
            'country',c->'country','track_count',c->'track_count','method',c->'method','score',c->'score')
            ORDER BY ord),'[]'::jsonb) FROM jsonb_array_elements(p.candidates) WITH ORDINALITY AS candidate(c,ord)
        )) ORDER BY p.kind,p.id),'[]'::jsonb)
        FROM curation_pending_items p WHERE p.target_id=$1 AND p.status<>'resolved'`, id).Scan(&release.Pending)
	if err != nil {
		return release, err
	}
	return release, tx.Commit()
}
