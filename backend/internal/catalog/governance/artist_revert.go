package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ArtistSuppressionWriter 在撤销来源为自动整理的合并时，记录两位艺人的“不再提出”决定。
type ArtistSuppressionWriter interface {
	SuppressArtistMerge(ctx context.Context, tx *sql.Tx, targetID, sourceID, revertID, actorID string) error
}

type artistReverter struct {
	governance   *ArtistGovernance
	suppressions ArtistSuppressionWriter
}

type artistRevertPlan struct {
	before, after artistRecordState
	change        *artistChangeDescription
	ids           []string
}

func sameString(a, b *string) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}

func validOptionalID(id *string) bool { return id == nil || uuid.Valid(*id) }

func decodeArtistRevert(r operations.Record, change *artistChangeDescription) *artistRevertPlan {
	if change == nil || r.Type == operations.TypeRevert || r.Type == operations.TypeArtistSplit || r.RevertedByID != "" {
		return nil
	}
	p := &artistRevertPlan{change: change}
	b, a := &p.before, &p.after
	if json.Unmarshal(r.Before, b) != nil || json.Unmarshal(r.After, a) != nil ||
		b.SchemaVersion != 1 || a.SchemaVersion != 1 || len(b.Artists) == 0 || len(b.Artists) != len(a.Artists) ||
		b.Bindings == nil || a.Bindings == nil || len(b.Bindings) != len(a.Bindings) ||
		b.Assignments == nil || a.Assignments == nil || len(b.Assignments) != len(a.Assignments) ||
		len(b.Ignores) != len(a.Ignores) || b.Artists[0].ID != r.TargetID {
		return nil
	}
	ids := map[string]bool{}
	for i, old := range b.Artists {
		new := a.Artists[i]
		if !uuid.Valid(old.ID) || old.ID != new.ID || ids[old.ID] || old.Revision < 1 || new.Revision < 1 ||
			!validOptionalID(old.RedirectArtistID) || !validOptionalID(old.RedirectOperationID) || !validOptionalID(old.NameOperationID) ||
			(old.RedirectArtistID != nil && old.RedirectOperationID == nil) {
			return nil
		}
		p.ids = append(p.ids, old.ID)
		ids[old.ID] = true
	}
	switch r.Type {
	case operations.TypeArtistMerge:
		if len(p.ids) < 2 || p.ids[1] != change.Source.ID || len(a.Ignores) != 0 || b.Artists[0].RedirectArtistID != nil ||
			b.Artists[1].RedirectArtistID != nil || a.Artists[0].RedirectArtistID != nil {
			return nil
		}
		for i := 1; i < len(p.ids); i++ {
			if !sameString(a.Artists[i].RedirectArtistID, &r.TargetID) || !sameString(a.Artists[i].RedirectOperationID, &r.ID) ||
				(i > 1 && !sameString(b.Artists[i].RedirectArtistID, &change.Source.ID)) {
				return nil
			}
		}
		keys := map[string]bool{}
		for i, old := range b.Bindings {
			new := a.Bindings[i]
			if keys[old.KeyID] || !uuid.Valid(old.KeyID) || !ids[old.ArtistID] || old.ArtistID == r.TargetID || !validOptionalID(old.OperationID) ||
				old.KeyID != new.KeyID || new.ArtistID != r.TargetID || !sameString(new.OperationID, &r.ID) {
				return nil
			}
			keys[old.KeyID] = true
		}
		for i, old := range b.Assignments {
			new := a.Assignments[i]
			if !uuid.Valid(old.KeyID) || !ids[old.ArtistID] || old.ArtistID == r.TargetID || old.OperationID == nil || !uuid.Valid(*old.OperationID) ||
				(old.ReleaseID == nil) == (old.TrackID == nil) || !validOptionalID(old.ReleaseID) || !validOptionalID(old.TrackID) ||
				!sameString(old.ReleaseID, new.ReleaseID) || !sameString(old.TrackID, new.TrackID) || old.KeyID != new.KeyID ||
				new.ArtistID != r.TargetID || !sameString(new.OperationID, &r.ID) {
				return nil
			}
			keys[old.KeyID] = true
		}
		if len(keys) != *change.NameCount {
			return nil
		}
	case operations.TypeArtistRename:
		if len(p.ids) != 1 || len(a.Bindings)+len(a.Assignments)+len(a.Ignores) != 0 ||
			!sameString(a.Artists[0].NameOperationID, &r.ID) || (b.Artists[0].DisplayName != nil && b.Artists[0].NameOperationID == nil) {
			return nil
		}
	case operations.TypeArtistCandidateIgnore:
		if len(p.ids) != 2 || p.ids[1] != change.OtherArtist.ID || len(a.Bindings)+len(a.Assignments) != 0 {
			return nil
		}
		changed := false
		for i, old := range b.Ignores {
			new := a.Ignores[i]
			if !uuid.Valid(old.LeftKeyID) || !uuid.Valid(old.RightKeyID) || old.LeftKeyID >= old.RightKeyID ||
				old.ArtistKeyPair != new.ArtistKeyPair || !validOptionalID(old.OperationID) || (new.OperationID != nil) != *change.Ignored {
				return nil
			}
			if !sameString(old.OperationID, new.OperationID) {
				if new.OperationID != nil && (old.OperationID != nil || *new.OperationID != r.ID) {
					return nil
				}
				changed = true
			}
		}
		if !changed {
			return nil
		}
	default:
		return nil
	}
	return p
}

// 按原记录的键和次序重读意图。绑定缺行时读取初始路由，忽略缺行时操作 ID 为空。
// 列表一次读取整组记录，撤销在 CATP 和身份行锁内用同样的快照判断。
const artistSnapshotSQL = `SELECT o.id, jsonb_build_object(
 'schema_version',1,
 'artists', COALESCE((SELECT jsonb_agg(to_jsonb(identity) ORDER BY item.ord)
   FROM jsonb_array_elements(o.after_state->'artists') WITH ORDINALITY item(v,ord)
   JOIN artists a ON a.id=(item.v->>'id')::uuid
   LEFT JOIN artist_profiles p ON p.artist_id=a.id LEFT JOIN artist_overrides ov ON ov.artist_id=a.id
   CROSS JOIN LATERAL (SELECT a.id,COALESCE(p.effective_display_name,a.initial_name) AS name,
     a.edit_revision AS revision,ov.display_name,ov.name_operation_id,ov.redirect_artist_id,ov.redirect_operation_id) identity),'[]'),
 'bindings', COALESCE((SELECT jsonb_agg(jsonb_build_object('key_id',k.id,'artist_id',COALESCE(b.artist_id,k.initial_artist_id),
     'operation_id',b.operation_id) ORDER BY item.ord)
   FROM jsonb_array_elements(o.after_state->'bindings') WITH ORDINALITY item(v,ord)
   JOIN artist_name_keys k ON k.id=(item.v->>'key_id')::uuid LEFT JOIN artist_name_bindings b ON b.key_id=k.id),'[]'),
 'assignments', COALESCE((SELECT jsonb_agg((to_jsonb(a)-'id') ORDER BY item.ord)
   FROM jsonb_array_elements(o.after_state->'assignments') WITH ORDINALITY item(v,ord)
   JOIN artist_assignment_overrides a ON a.key_id=(item.v->>'key_id')::uuid
     AND a.release_id IS NOT DISTINCT FROM (item.v->>'release_id')::uuid
     AND a.track_id IS NOT DISTINCT FROM (item.v->>'track_id')::uuid),'[]'),
 'ignores', COALESCE((SELECT jsonb_agg(item.v || jsonb_build_object('operation_id',i.operation_id) ORDER BY item.ord)
   FROM jsonb_array_elements(NULLIF(o.after_state->'ignores','null'::jsonb)) WITH ORDINALITY item(v,ord)
   LEFT JOIN artist_candidate_ignores i ON i.left_key_id=(item.v->>'left_key_id')::uuid
     AND i.right_key_id=(item.v->>'right_key_id')::uuid),'[]'))
 FROM operations o WHERE o.id=ANY($1::uuid[])`

const noLaterArtistOperationSQL = ` AND NOT EXISTS (SELECT 1 FROM operations r WHERE r.reverts_operation_id=o.id)
 AND NOT EXISTS (SELECT 1 FROM operations_artist_targets mine
 JOIN operations_artist_targets t ON t.artist_id=mine.artist_id AND t.operation_id<>mine.operation_id
 JOIN operations l ON l.id=t.operation_id WHERE mine.operation_id=o.id AND l.operation_type<>'revert'
 AND (l.created_at,l.id)>(o.created_at,o.id) AND NOT EXISTS(SELECT 1 FROM operations r WHERE r.reverts_operation_id=l.id))`

func (p *artistRevertPlan) matches(current artistRecordState) bool {
	a := p.after
	if len(current.Artists) != len(a.Artists) {
		return false
	}
	switch p.change.Kind {
	case operations.TypeArtistMerge:
		if current.Artists[0].RedirectArtistID != nil || !reflect.DeepEqual(current.Bindings, a.Bindings) ||
			!reflect.DeepEqual(current.Assignments, a.Assignments) {
			return false
		}
		for i := 1; i < len(a.Artists); i++ {
			if !sameString(current.Artists[i].RedirectArtistID, a.Artists[i].RedirectArtistID) ||
				!sameString(current.Artists[i].RedirectOperationID, a.Artists[i].RedirectOperationID) {
				return false
			}
		}
	case operations.TypeArtistRename:
		return sameString(current.Artists[0].DisplayName, a.Artists[0].DisplayName) && sameString(current.Artists[0].NameOperationID, a.Artists[0].NameOperationID)
	case operations.TypeArtistCandidateIgnore:
		if len(current.Ignores) != len(a.Ignores) {
			return false
		}
		for i, old := range p.before.Ignores {
			if !sameString(old.OperationID, a.Ignores[i].OperationID) && !sameString(current.Ignores[i].OperationID, a.Ignores[i].OperationID) {
				return false
			}
		}
	}
	return true
}

func artistRevertible(ctx context.Context, q operations.Queryer, plans map[string]*artistRevertPlan) (map[string]artistRecordState, error) {
	result := map[string]artistRecordState{}
	if len(plans) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(plans))
	for id := range plans {
		ids = append(ids, id)
	}
	writers := map[string][]string{}
	for id, plan := range plans {
		if previous := plan.previousWriters(); len(previous) > 0 {
			writers[id] = previous
		}
	}
	references, err := json.Marshal(writers)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, artistSnapshotSQL+noLaterArtistOperationSQL+` AND NOT EXISTS (
 SELECT 1 FROM jsonb_array_elements_text($2::jsonb->o.id::text) prior(id)
 WHERE NOT EXISTS (SELECT 1 FROM operations w WHERE w.id=prior.id::uuid))`, ids, references)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var state artistRecordState
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, err
		}
		if p := plans[id]; p.matches(state) {
			state.Changes = p.change
			result[id] = state
		}
	}
	return result, rows.Err()
}

func artistRevertConflict() error {
	return operations.Conflict("revert_conflict", "这位艺人之后又有修改，不能撤销这一条")
}

// 写回旧的 operation_id 会在提交时检查外键。先取得相应记录的 KEY SHARE，
// 避免另一笔撤销持有旧记录的 UPDATE 锁等待 CATP，同时本事务持有 CATP 等待旧记录。
func (p *artistRevertPlan) previousWriters() []string {
	ids := map[string]bool{}
	add := func(id *string) {
		if id != nil {
			ids[*id] = true
		}
	}
	switch p.change.Kind {
	case operations.TypeArtistMerge:
		for _, a := range p.before.Artists[1:] {
			add(a.RedirectOperationID)
		}
		for _, b := range p.before.Bindings {
			add(b.OperationID)
		}
		for _, a := range p.before.Assignments {
			add(a.OperationID)
		}
	case operations.TypeArtistRename:
		add(p.before.Artists[0].NameOperationID)
	case operations.TypeArtistCandidateIgnore:
		for i, pair := range p.before.Ignores {
			if !sameString(pair.OperationID, p.after.Ignores[i].OperationID) {
				add(pair.OperationID)
			}
		}
	}
	return slices.Collect(maps.Keys(ids))
}

func (k artistReverter) Revert(ctx context.Context, tx *sql.Tx, id string, r operations.Record) (operations.Outcome, error) {
	change, _ := decodeArtistChanges(r)
	p := decodeArtistRevert(r, change)
	if p != nil {
		if writers := p.previousWriters(); len(writers) > 0 {
			if _, err := tx.ExecContext(ctx, `SELECT id FROM operations WHERE id=ANY($1::uuid[]) ORDER BY id FOR KEY SHARE`, writers); err != nil {
				return operations.Outcome{}, err
			}
		}
	}
	if err := lockArtistCatalog(ctx, tx); err != nil {
		return operations.Outcome{}, err
	}
	if p == nil {
		return operations.Outcome{}, artistRevertConflict()
	}
	// lockArtists 会排序入参，快照中的主目标与来源次序不能变。
	if _, err := lockArtists(ctx, tx, slices.Clone(p.ids)...); err != nil {
		if status, _, _ := operations.Problem(err); status == 404 {
			return operations.Outcome{}, artistRevertConflict()
		}
		return operations.Outcome{}, err
	}
	candidates, err := artistRevertible(ctx, tx, map[string]*artistRevertPlan{r.ID: p})
	if err != nil {
		return operations.Outcome{}, err
	}
	current, ok := candidates[r.ID]
	if !ok {
		return operations.Outcome{}, artistRevertConflict()
	}
	var releases []string
	if r.Type != operations.TypeArtistCandidateIgnore {
		releases, _, err = affectedArtistReleases(ctx, tx, p.ids)
		if err != nil {
			return operations.Outcome{}, err
		}
	}
	var mapping map[string]string
	switch r.Type {
	case operations.TypeArtistMerge:
		err = restoreArtistMerge(ctx, tx, r)
		mapping = map[string]string{}
		for _, old := range p.before.Artists[1:] {
			resolved := old.ID
			if old.RedirectArtistID != nil {
				resolved = *old.RedirectArtistID
			}
			mapping[old.ID] = resolved
		}
	case operations.TypeArtistRename:
		old := p.before.Artists[0]
		_, err = tx.ExecContext(ctx, `UPDATE artist_overrides SET display_name=$2,name_operation_id=$3 WHERE artist_id=$1`, old.ID, old.DisplayName, old.NameOperationID)
	case operations.TypeArtistCandidateIgnore:
		err = restoreArtistIgnores(ctx, tx, p)
	}
	if err != nil {
		return operations.Outcome{}, err
	}
	if err = incrementArtists(ctx, tx, p.ids[:min(2, len(p.ids))]); err != nil {
		return operations.Outcome{}, err
	}
	if r.Type != operations.TypeArtistCandidateIgnore {
		if _, err = tx.ExecContext(ctx, `DELETE FROM artist_overrides WHERE artist_id=ANY($1::uuid[])
 AND display_name IS NULL AND name_operation_id IS NULL AND redirect_artist_id IS NULL AND redirect_operation_id IS NULL`, p.ids); err != nil {
			return operations.Outcome{}, err
		}
		if err = k.governance.refresh(ctx, tx, releases, p.ids, mapping); err != nil {
			return operations.Outcome{}, err
		}
	}
	if r.Type == operations.TypeArtistMerge && len(r.Origin) > 0 {
		var origin operations.Origin
		if err = json.Unmarshal(r.Origin, &origin); err != nil {
			return operations.Outcome{}, err
		}
		if origin.Source == "curation" {
			if k.suppressions == nil {
				return operations.Outcome{}, errors.New("artist merge suppression writer is required")
			}
			if err = k.suppressions.SuppressArtistMerge(ctx, tx, r.TargetID, p.change.Source.ID, id, operations.ActorID(ctx)); err != nil {
				return operations.Outcome{}, err
			}
		}
	}
	var originalID string
	var raw []byte
	if err := tx.QueryRowContext(ctx, artistSnapshotSQL, []string{r.ID}).Scan(&originalID, &raw); err != nil {
		return operations.Outcome{}, err
	}
	var restored artistRecordState
	if err := json.Unmarshal(raw, &restored); err != nil {
		return operations.Outcome{}, err
	}
	restored.Changes = p.change
	return operations.Outcome{TargetType: operations.TargetArtist, TargetID: r.TargetID, Targets: artistTargets(p.ids[1:]...),
		ExpectedRevision: current.Artists[0].Revision, ResultRevision: restored.Artists[0].Revision, Before: current, After: restored}, nil
}

func restoreArtistMerge(ctx context.Context, tx *sql.Tx, r operations.Record) error {
	// 所有恢复都定位原操作写下的行；无原绑定时删除覆盖，重新使用该键的初始路由。
	const bindings = `jsonb_to_recordset($1::jsonb->'bindings') b(key_id uuid,artist_id uuid,operation_id uuid)`
	queries := []string{
		`DELETE FROM artist_name_bindings a USING ` + bindings + ` WHERE a.key_id=b.key_id AND a.operation_id=$2 AND b.operation_id IS NULL`,
		`UPDATE artist_name_bindings a SET artist_id=b.artist_id,operation_id=b.operation_id FROM ` + bindings +
			` WHERE a.key_id=b.key_id AND a.operation_id=$2 AND b.operation_id IS NOT NULL`,
		`UPDATE artist_assignment_overrides a SET artist_id=b.artist_id,operation_id=b.operation_id
 FROM jsonb_to_recordset($1::jsonb->'assignments') b(release_id uuid,track_id uuid,key_id uuid,artist_id uuid,operation_id uuid)
 WHERE a.key_id=b.key_id AND a.release_id IS NOT DISTINCT FROM b.release_id AND a.track_id IS NOT DISTINCT FROM b.track_id AND a.operation_id=$2`,
		`UPDATE artist_overrides a SET redirect_artist_id=b.redirect_artist_id,redirect_operation_id=b.redirect_operation_id
 FROM jsonb_to_recordset($1::jsonb->'artists') b(id uuid,redirect_artist_id uuid,redirect_operation_id uuid)
 WHERE a.artist_id=b.id AND a.redirect_operation_id=$2`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, r.Before, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func restoreArtistIgnores(ctx context.Context, tx *sql.Tx, p *artistRevertPlan) error {
	changed := []artistIgnoreState{}
	for i, old := range p.before.Ignores {
		if !sameString(old.OperationID, p.after.Ignores[i].OperationID) {
			changed = append(changed, old)
		}
	}
	raw, err := json.Marshal(changed)
	if err != nil {
		return err
	}
	const pairs = `jsonb_to_recordset($1::jsonb) p(left_key_id uuid,right_key_id uuid,operation_id uuid)`
	query := `INSERT INTO artist_candidate_ignores(left_key_id,right_key_id,operation_id) SELECT p.left_key_id,p.right_key_id,p.operation_id FROM ` + pairs
	if *p.change.Ignored {
		query = `DELETE FROM artist_candidate_ignores i USING ` + pairs + ` WHERE i.left_key_id=p.left_key_id AND i.right_key_id=p.right_key_id`
	}
	_, err = tx.ExecContext(ctx, query, raw)
	return err
}
