package governance

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

var ErrArtistPreviewPreparing = errors.New("artist preview preparing")

type ArtistScopeGroup struct {
	GroupID    string   `json:"group_id"`
	Revision   int64    `json:"revision"`
	ReleaseIDs []string `json:"release_ids"`
}
type ArtistExcludedScope struct {
	ReleaseID *string `json:"release_id,omitempty"`
	TrackID   *string `json:"track_id,omitempty"`
	KeyID     string  `json:"key_id"`
	ArtistID  string  `json:"artist_id"`
}

type ArtistScopeSummary struct {
	Mode               string                `json:"mode"`
	NameKeyIDs         []string              `json:"name_key_ids"`
	Groups             []ArtistScopeGroup    `json:"groups"`
	ReleaseIDs         []string              `json:"release_ids"`
	NameCount          int                   `json:"name_count"`
	ReleaseCount       int                   `json:"release_count"`
	TrackCount         int                   `json:"track_count"`
	Roles              []string              `json:"roles"`
	ExcludedScopeCount int                   `json:"excluded_scope_count"`
	ExcludedScopes     []ArtistExcludedScope `json:"excluded_scopes"`
}
type ArtistSplitPreview struct {
	ArtistID string             `json:"artist_id"`
	Revision int64              `json:"revision"`
	Basis    string             `json:"basis"`
	Scope    ArtistScopeSummary `json:"scope"`
	Limits   map[string]int     `json:"limits"`
}
type artistScopeFact struct{ ReleaseID, TrackID, KeyID, NameID, Role, ArtistID, Status string }
type artistSplitPlan struct {
	preview     ArtistSplitPreview
	bindings    []artistBindingState
	assignments []artistAssignmentState
	releases    []string
}

// PreviewArtistSplit 在一个只读快照里计算依据；不创建审批对象，不取得业务写锁。
func PreviewArtistSplit(ctx context.Context, db *sql.DB, id string, scope ArtistSplitScope) (ArtistSplitPreview, error) {
	if !uuid.Valid(id) {
		return ArtistSplitPreview{}, operations.Invalid("invalid_id", "艺人标识无效")
	}
	id = strings.ToLower(id)
	if err := scope.Normalize(); err != nil {
		return ArtistSplitPreview{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ArtistSplitPreview{}, err
	}
	defer tx.Rollback()
	ready, err := artistgraph.Ready(ctx, tx)
	if err != nil {
		return ArtistSplitPreview{}, err
	}
	if !ready {
		return ArtistSplitPreview{}, ErrArtistPreviewPreparing
	}
	artists, err := loadArtistIdentities(ctx, tx, []string{id})
	if err != nil {
		return ArtistSplitPreview{}, err
	}
	a, ok := artists[id]
	if !ok {
		return ArtistSplitPreview{}, operations.NotFound("not_found", "艺人不存在")
	}
	if err := checkArtist(a, a.Revision, false); err != nil {
		return ArtistSplitPreview{}, err
	}
	plan, err := buildArtistSplitPlan(ctx, tx, a, scope)
	if err != nil {
		return ArtistSplitPreview{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtistSplitPreview{}, err
	}
	return plan.preview, nil
}

func buildArtistSplitPlan(ctx context.Context, q operations.Queryer, artist artistIdentity, scope ArtistSplitScope) (artistSplitPlan, error) {
	plan := artistSplitPlan{bindings: []artistBindingState{}, assignments: []artistAssignmentState{}}
	summary := ArtistScopeSummary{Mode: scope.Mode, NameKeyIDs: scope.NameKeyIDs, NameCount: len(scope.NameKeyIDs), Groups: []ArtistScopeGroup{}, ReleaseIDs: []string{}, Roles: []string{}, ExcludedScopes: []ArtistExcludedScope{}}
	// 完整的键路由进入依据；恢复旧预览不能偷偷抢走已经分给第三人的全局名字。
	rows, err := q.QueryContext(ctx, `SELECT k.id::text,COALESCE(b.artist_id,k.initial_artist_id)::text,b.operation_id::text,
 COALESCE(o.redirect_artist_id,b.artist_id,k.initial_artist_id)::text
 FROM artist_name_keys k LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 LEFT JOIN artist_overrides o ON o.artist_id=COALESCE(b.artist_id,k.initial_artist_id)
 WHERE k.id=ANY($1::text[]::uuid[]) ORDER BY k.id`, scope.NameKeyIDs)
	if err != nil {
		return plan, err
	}
	keyBindings := []artistBindingState{}
	keyCanonical := map[string]string{}
	for rows.Next() {
		var b artistBindingState
		var canonical string
		if err := rows.Scan(&b.KeyID, &b.ArtistID, &b.OperationID, &canonical); err != nil {
			rows.Close()
			return plan, err
		}
		keyBindings = append(keyBindings, b)
		keyCanonical[b.KeyID] = canonical
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	if len(keyBindings) != len(scope.NameKeyIDs) {
		return plan, operations.NotFound("not_found", "名字不存在")
	}
	if scope.Mode == "aliases" {
		for _, b := range keyBindings {
			if keyCanonical[b.KeyID] != artist.ID {
				return plan, artistScopeChanged()
			}
			plan.bindings = append(plan.bindings, b)
		}
	}
	selectedReleases := []string{}
	if scope.Mode == "albums" {
		rows, err = q.QueryContext(ctx, `SELECT g.id::text,g.edit_revision,r.id::text FROM release_groups g
   LEFT JOIN releases r ON r.group_id=g.id WHERE g.id=ANY($1::text[]::uuid[]) ORDER BY g.id,r.id`, scope.GroupIDs)
		if err != nil {
			return plan, err
		}
		for rows.Next() {
			var gid string
			var revision int64
			var rid *string
			if err := rows.Scan(&gid, &revision, &rid); err != nil {
				rows.Close()
				return plan, err
			}
			if len(summary.Groups) == 0 || summary.Groups[len(summary.Groups)-1].GroupID != gid {
				summary.Groups = append(summary.Groups, ArtistScopeGroup{GroupID: gid, Revision: revision, ReleaseIDs: []string{}})
			}
			if rid != nil {
				selectedReleases = append(selectedReleases, *rid)
				summary.Groups[len(summary.Groups)-1].ReleaseIDs = append(summary.Groups[len(summary.Groups)-1].ReleaseIDs, *rid)
			}
			if len(selectedReleases) > maxArtistReleases {
				rows.Close()
				return plan, artistScopeTooLarge()
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return plan, err
		}
		if len(summary.Groups) != len(scope.GroupIDs) {
			return plan, operations.NotFound("not_found", "专辑不存在")
		}
		if len(selectedReleases) == 0 {
			return plan, artistScopeChanged()
		}
		sort.Strings(selectedReleases)
	}
	// 不使用短命 credit_id；完全相同的重扫不会仅因生成新 UUID 或时间而让预览过期。
	rows, err = q.QueryContext(ctx, `SELECT COALESCE(e.release_id,m.release_id)::text,COALESCE(e.track_id::text,''),n.key_id::text,n.id::text,e.role,e.artist_id::text,COALESCE(t.source_status,'release')
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id LEFT JOIN tracks t ON t.id=e.track_id LEFT JOIN media m ON m.id=t.medium_id
 WHERE n.key_id=ANY($1::text[]::uuid[]) AND ($2::boolean OR COALESCE(e.release_id,m.release_id)=ANY($3::text[]::uuid[]))
 ORDER BY 1,2,3,4,5,6 LIMIT 1000001`, scope.NameKeyIDs, scope.Mode == "aliases", selectedReleases)
	if err != nil {
		return plan, err
	}
	facts := []artistScopeFact{}
	releases := map[string]bool{}
	roles := map[string]bool{}
	trackPairs := map[string]artistAssignmentState{}
	releasePairs := map[string]artistAssignmentState{}
	matchedKeys := map[string]bool{}
	for rows.Next() {
		var f artistScopeFact
		if err := rows.Scan(&f.ReleaseID, &f.TrackID, &f.KeyID, &f.NameID, &f.Role, &f.ArtistID, &f.Status); err != nil {
			rows.Close()
			return plan, err
		}
		facts = append(facts, f)
		if len(facts) > 1000000 {
			rows.Close()
			return plan, artistScopeTooLarge()
		}
		if f.ArtistID != artist.ID {
			continue
		}
		matchedKeys[f.KeyID] = true
		releases[f.ReleaseID] = true
		roles[f.Role] = true
		if f.TrackID != "" {
			tid := f.TrackID
			trackPairs[tid+f.KeyID] = artistAssignmentState{TrackID: &tid, KeyID: f.KeyID, ArtistID: artist.ID}
		}
		rid := f.ReleaseID
		releasePairs[rid+f.KeyID] = artistAssignmentState{ReleaseID: &rid, KeyID: f.KeyID, ArtistID: artist.ID}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	participants, err := loadArtistScopeParticipants(ctx, q, artist.ID, scope.NameKeyIDs, selectedReleases)
	if err != nil {
		return plan, err
	}
	if scope.Mode == "albums" {
		for _, key := range scope.NameKeyIDs {
			if !matchedKeys[key] {
				return plan, artistScopeChanged()
			}
		}
	}
	// 所有相关显式覆盖都进入摘要，包括不能移动的第三人覆盖。
	rows, err = q.QueryContext(ctx, `SELECT a.release_id::text,a.track_id::text,a.key_id::text,a.artist_id::text,a.operation_id::text,
 COALESCE(o.redirect_artist_id,a.artist_id)::text
 FROM artist_assignment_overrides a LEFT JOIN artist_overrides o ON o.artist_id=a.artist_id
 LEFT JOIN tracks t ON t.id=a.track_id LEFT JOIN media m ON m.id=t.medium_id
 WHERE a.key_id=ANY($1::text[]::uuid[]) AND ($2::boolean OR COALESCE(a.release_id,m.release_id)=ANY($3::text[]::uuid[]))
 ORDER BY a.release_id NULLS FIRST,a.track_id NULLS FIRST,a.key_id LIMIT 10001`, scope.NameKeyIDs, scope.Mode == "aliases", selectedReleases)
	if err != nil {
		return plan, err
	}
	allAssignments := []artistAssignmentState{}
	for rows.Next() {
		var a artistAssignmentState
		var canonical string
		if err := rows.Scan(&a.ReleaseID, &a.TrackID, &a.KeyID, &a.ArtistID, &a.OperationID, &canonical); err != nil {
			rows.Close()
			return plan, err
		}
		allAssignments = append(allAssignments, a)
		if len(allAssignments) > maxArtistOverrides {
			rows.Close()
			return plan, artistScopeTooLarge()
		}
		if canonical != artist.ID {
			summary.ExcludedScopeCount++
			summary.ExcludedScopes = append(summary.ExcludedScopes, ArtistExcludedScope{ReleaseID: a.ReleaseID, TrackID: a.TrackID, KeyID: a.KeyID, ArtistID: canonical})
			if a.ReleaseID != nil {
				delete(releasePairs, *a.ReleaseID+a.KeyID)
			}
			if a.TrackID != nil {
				delete(trackPairs, *a.TrackID+a.KeyID)
			}
			continue
		}
		if scope.Mode == "aliases" {
			plan.assignments = append(plan.assignments, a)
		} else if a.TrackID != nil {
			if _, ok := trackPairs[*a.TrackID+a.KeyID]; ok {
				trackPairs[*a.TrackID+a.KeyID] = a
			}
		} else {
			if _, ok := releasePairs[*a.ReleaseID+a.KeyID]; ok {
				releasePairs[*a.ReleaseID+a.KeyID] = a
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	if scope.Mode == "albums" {
		for _, a := range releasePairs {
			if a.OperationID == nil && keyCanonical[a.KeyID] != artist.ID {
				summary.ExcludedScopeCount++
				summary.ExcludedScopes = append(summary.ExcludedScopes, ArtistExcludedScope{ReleaseID: a.ReleaseID, KeyID: a.KeyID, ArtistID: keyCanonical[a.KeyID]})
				continue
			}
			plan.assignments = append(plan.assignments, a)
		}
		for _, a := range trackPairs {
			plan.assignments = append(plan.assignments, a)
		}
		if len(plan.assignments) == 0 {
			return plan, artistScopeChanged()
		}
	}
	if len(plan.assignments) > maxArtistOverrides || len(summary.ExcludedScopes) > maxArtistOverrides {
		return plan, artistScopeTooLarge()
	}
	sort.Slice(plan.assignments, func(i, j int) bool {
		return assignmentOrder(plan.assignments[i]) < assignmentOrder(plan.assignments[j])
	})

	sort.Slice(summary.ExcludedScopes, func(i, j int) bool {
		a, b := summary.ExcludedScopes[i], summary.ExcludedScopes[j]
		return assignmentOrder(artistAssignmentState{ReleaseID: a.ReleaseID, TrackID: a.TrackID, KeyID: a.KeyID}) < assignmentOrder(artistAssignmentState{ReleaseID: b.ReleaseID, TrackID: b.TrackID, KeyID: b.KeyID})
	})
	for id := range releases {
		plan.releases = append(plan.releases, id)
	}
	sort.Strings(plan.releases)
	summary.ReleaseIDs = append(summary.ReleaseIDs, plan.releases...)
	summary.ReleaseCount = len(plan.releases)
	summary.TrackCount = len(participants)
	for role := range roles {
		summary.Roles = append(summary.Roles, role)
	}
	sort.Strings(summary.Roles)

	// 发行的当前归组与人工版本也属于预览范围，扫描时间不是依据。
	type scopeRelease struct {
		ID, GroupID string
		Revision    int64
	}
	releaseStates := []scopeRelease{}
	basisReleaseSet := map[string]bool{}
	for _, id := range selectedReleases {
		basisReleaseSet[id] = true
	}
	for _, id := range plan.releases {
		basisReleaseSet[id] = true
	}
	basisReleaseIDs := make([]string, 0, len(basisReleaseSet))
	for id := range basisReleaseSet {
		basisReleaseIDs = append(basisReleaseIDs, id)
	}
	if len(basisReleaseIDs) > 0 {
		rows, err = q.QueryContext(ctx, `SELECT id::text,group_id::text,edit_revision FROM releases WHERE id=ANY($1::text[]::uuid[]) ORDER BY id`, basisReleaseIDs)
		if err != nil {
			return plan, err
		}
		for rows.Next() {
			var r scopeRelease
			if err := rows.Scan(&r.ID, &r.GroupID, &r.Revision); err != nil {
				rows.Close()
				return plan, err
			}
			releaseStates = append(releaseStates, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return plan, err
		}
	}
	basis := operations.Fingerprint(struct {
		Artist       artistIdentity
		Scope        ArtistSplitScope
		Groups       []ArtistScopeGroup
		Keys         []artistBindingState
		Canonical    map[string]string
		Assignments  []artistAssignmentState
		Facts        []artistScopeFact
		Participants []artistScopeParticipant
		Releases     []scopeRelease
	}{artist, scope, summary.Groups, keyBindings, keyCanonical, allAssignments, facts, participants, releaseStates})
	plan.preview = ArtistSplitPreview{ArtistID: artist.ID, Revision: artist.Revision, Basis: basis, Scope: summary, Limits: map[string]int{"name_keys": maxArtistKeys, "groups": maxArtistGroups, "releases": maxArtistReleases, "tracks": maxArtistTracks, "overrides": maxArtistOverrides}}
	return plan, nil
}
func assignmentOrder(a artistAssignmentState) string {
	if a.ReleaseID != nil {
		return "r" + *a.ReleaseID + a.KeyID
	}
	return "t" + *a.TrackID + a.KeyID
}
