package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// ArtistResolutionRefresher 由 app 注入个人收藏能力；治理不访问 personal 私有表。
type ArtistResolutionRefresher interface {
	RefreshArtistResolution(context.Context, *sql.Tx, map[string]string) error
}

// ArtistGovernance 集中治理所需的跨能力端口，没有全局可变服务。
type ArtistGovernance struct{ resolution ArtistResolutionRefresher }

func NewArtistGovernance(resolution ArtistResolutionRefresher) *ArtistGovernance {
	return &ArtistGovernance{resolution: resolution}
}

func lockArtistCatalog(ctx context.Context, tx *sql.Tx) error {
	if err := writegate.Lock(ctx, tx); err != nil {
		return err
	}
	ready, err := artistgraph.Ready(ctx, tx)
	if err != nil {
		return err
	}
	if !ready {
		return operations.Conflict("artist_catalog_not_ready", "艺人资料正在整理，请稍后再试")
	}
	return nil
}

type artistIdentity struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Revision            int64   `json:"revision"`
	DisplayName         *string `json:"display_name"`
	RedirectArtistID    *string `json:"redirect_artist_id"`
	NameOperationID     *string `json:"name_operation_id"`
	RedirectOperationID *string `json:"redirect_operation_id"`
}

func loadArtistIdentities(ctx context.Context, q operations.Queryer, ids []string) (map[string]artistIdentity, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id::text,COALESCE(p.effective_display_name,a.initial_name),a.edit_revision,
 o.display_name,o.redirect_artist_id::text,o.name_operation_id::text,o.redirect_operation_id::text
 FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id LEFT JOIN artist_overrides o ON o.artist_id=a.id
 WHERE a.id=ANY($1::text[]::uuid[]) ORDER BY a.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]artistIdentity{}
	for rows.Next() {
		var a artistIdentity
		if err := rows.Scan(&a.ID, &a.Name, &a.Revision, &a.DisplayName, &a.RedirectArtistID, &a.NameOperationID, &a.RedirectOperationID); err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}
func lockArtists(ctx context.Context, tx *sql.Tx, ids ...string) (map[string]artistIdentity, error) {
	sort.Strings(ids)
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM artists WHERE id=ANY($1::text[]::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result, err := loadArtistIdentities(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			return nil, operations.NotFound("not_found", "艺人不存在")
		}
	}
	return result, nil
}
func checkArtist(a artistIdentity, revision int64, allowRedirect bool) error {
	if a.Revision != revision {
		return operations.Conflict("revision_conflict", "艺人刚被修改过，请刷新后再试")
	}
	if !allowRedirect && a.RedirectArtistID != nil {
		return operations.Conflict("artist_redirected", "艺人已合并，请刷新后再试")
	}
	return nil
}

type artistBindingState struct {
	KeyID       string  `json:"key_id"`
	ArtistID    string  `json:"artist_id"`
	OperationID *string `json:"operation_id"`
}
type artistAssignmentState struct {
	ReleaseID   *string `json:"release_id"`
	TrackID     *string `json:"track_id"`
	KeyID       string  `json:"key_id"`
	ArtistID    string  `json:"artist_id"`
	OperationID *string `json:"operation_id"`
}
type artistIgnoreState struct {
	ArtistKeyPair
	OperationID *string `json:"operation_id"`
}
type artistRecordState struct {
	SchemaVersion int                     `json:"schema_version"`
	Artists       []artistIdentity        `json:"artists"`
	Bindings      []artistBindingState    `json:"bindings"`
	Assignments   []artistAssignmentState `json:"assignments"`
	Ignores       []artistIgnoreState     `json:"ignores,omitempty"`
	Changes       any                     `json:"changes,omitempty"`
}

func emptyArtistState() artistRecordState {
	return artistRecordState{SchemaVersion: 1, Artists: []artistIdentity{}, Bindings: []artistBindingState{}, Assignments: []artistAssignmentState{}}
}
func boundedArtistState(state artistRecordState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw) > 8*1024*1024 {
		return artistScopeTooLarge()
	}
	return nil
}

// loadArtistMappings 读取当前实际归属于 artist 的所有持久名字路由和作用域，记录旧身份而非只存数量。
func loadArtistMappings(ctx context.Context, q operations.Queryer, id string) (artistRecordState, error) {
	state := emptyArtistState()
	rows, err := q.QueryContext(ctx, `SELECT k.id::text,COALESCE(b.artist_id,k.initial_artist_id)::text,b.operation_id::text
 FROM artist_name_keys k LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 LEFT JOIN artist_overrides o ON o.artist_id=COALESCE(b.artist_id,k.initial_artist_id)
 WHERE COALESCE(o.redirect_artist_id,b.artist_id,k.initial_artist_id)=$1::uuid ORDER BY k.id LIMIT 1001`, id)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var b artistBindingState
		if err := rows.Scan(&b.KeyID, &b.ArtistID, &b.OperationID); err != nil {
			rows.Close()
			return state, err
		}
		state.Bindings = append(state.Bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	if len(state.Bindings) > maxArtistBindings {
		return state, artistScopeTooLarge()
	}
	rows, err = q.QueryContext(ctx, `SELECT a.release_id::text,a.track_id::text,a.key_id::text,a.artist_id::text,a.operation_id::text
 FROM artist_assignment_overrides a LEFT JOIN artist_overrides o ON o.artist_id=a.artist_id
 WHERE COALESCE(o.redirect_artist_id,a.artist_id)=$1::uuid ORDER BY a.release_id NULLS FIRST,a.track_id NULLS FIRST,a.key_id LIMIT 10001`, id)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var a artistAssignmentState
		if err := rows.Scan(&a.ReleaseID, &a.TrackID, &a.KeyID, &a.ArtistID, &a.OperationID); err != nil {
			rows.Close()
			return state, err
		}
		state.Assignments = append(state.Assignments, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	if len(state.Assignments) > maxArtistOverrides {
		return state, artistScopeTooLarge()
	}
	return state, nil
}
func affectedArtistReleases(ctx context.Context, q operations.Queryer, ids []string) ([]string, int, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT COALESCE(e.release_id,m.release_id)::text
 FROM artist_effective_credits e LEFT JOIN tracks t ON t.id=e.track_id LEFT JOIN media m ON m.id=t.medium_id
 WHERE e.artist_id=ANY($1::text[]::uuid[]) ORDER BY 1 LIMIT 100001`, ids)
	if err != nil {
		return nil, 0, err
	}
	var releases []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		releases = append(releases, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if len(releases) > maxArtistTracks {
		return nil, 0, artistScopeTooLarge()
	}
	tracks, err := countArtistParticipants(ctx, q, ids, nil, nil)
	return releases, tracks, err
}
func refreshGovernedRelease(ctx context.Context, tx *sql.Tx, id string) error {
	if err := artistgraph.Refresh(ctx, tx, []string{id}); err != nil {
		return err
	}
	return searchindex.RefreshRelease(ctx, tx, id)
}
func (g *ArtistGovernance) refresh(ctx context.Context, tx *sql.Tx, releases []string, artistIDs []string, mapping map[string]string) error {
	if err := artistgraph.Refresh(ctx, tx, releases); err != nil {
		return err
	}
	if err := artistgraph.RefreshProfiles(ctx, tx, artistIDs); err != nil {
		return err
	}
	// 艺人治理不改发行文本；别名搜索由 profiles 的搜索投影和有效署名联结传播。
	if len(mapping) > 0 {
		if g.resolution == nil {
			return errors.New("artist resolution refresher is required")
		}
		if err := g.resolution.RefreshArtistResolution(ctx, tx, mapping); err != nil {
			return err
		}
	}
	return nil
}
func incrementArtists(ctx context.Context, tx *sql.Tx, ids []string) error {
	_, err := tx.ExecContext(ctx, `UPDATE artists SET edit_revision=edit_revision+1 WHERE id=ANY($1::text[]::uuid[])`, ids)
	return err
}
func artistNameSnapshot(a artistIdentity) map[string]string {
	return map[string]string{"id": a.ID, "name": a.Name}
}
func artistTargets(ids ...string) []operations.Target {
	out := make([]operations.Target, 0, len(ids))
	for _, id := range ids {
		out = append(out, operations.Target{Type: operations.TargetArtist, ID: id})
	}
	return out
}
