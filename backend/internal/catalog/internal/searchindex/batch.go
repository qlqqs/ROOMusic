package searchindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const trackRefreshChunk = 128

// RefreshTracks 用于已改变事实的显式曲目集合；调用者持 CATP，同事务完成所有 chunk。
// 每个去重后的 ID 只推进一次 revision；空集合不操作，不能传入未变化曲目代替精确变更集合。
func RefreshTracks(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		unique[strings.ToLower(id)] = struct{}{}
	}
	ids = make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := textnorm.LoadEmbedded(); err != nil {
		return err
	}
	s, err := LoadState(ctx, tx)
	if err != nil {
		return err
	}
	if !s.CurrentVersion() {
		return ErrVersionMismatch
	}
	for start := 0; start < len(ids); start += trackRefreshChunk {
		if err = refreshTrackChunk(ctx, tx, s, ids[start:min(start+trackRefreshChunk, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}

type trackMarker struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Title    string `json:"title"`
}

func refreshTrackChunk(ctx context.Context, tx *sql.Tx, s State, ids []string) error {
	rows, err := tx.QueryContext(ctx, `INSERT INTO catalog_projection_subject_state(subject_kind,subject_id,input_revision)
 SELECT 'track',id,1 FROM unnest($1::uuid[]) id
 ON CONFLICT(subject_kind,subject_id) DO UPDATE SET input_revision=catalog_projection_subject_state.input_revision+1
 RETURNING subject_id::text,input_revision,search_applied_generation,search_applied_input_revision`, ids)
	if err != nil {
		return err
	}
	markers := []trackMarker{}
	pending := []string{}
	for rows.Next() {
		var m trackMarker
		var gen, applied int64
		if err = rows.Scan(&m.ID, &m.Revision, &gen, &applied); err != nil {
			rows.Close()
			return err
		}
		// 与 Apply 相同：不覆盖已完成的同代同 revision（包括异常前置 marker 的边界）。
		if gen != s.TargetGeneration || applied != m.Revision {
			markers = append(markers, m)
			pending = append(pending, m.ID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		if err = writeTrackChunk(ctx, tx, s, pending, markers); err != nil {
			return err
		}
	}
	if s.ManifestSealed {
		_, err = tx.ExecContext(ctx, `WITH incoming AS (
 SELECT count(*) AS n FROM unnest($2::uuid[]) id WHERE NOT EXISTS(
 SELECT 1 FROM catalog_projection_work WHERE build_id=$1::uuid AND subject_kind='track' AND subject_id=id))
 UPDATE catalog_projection_state SET live_updated=live_updated+incoming.n FROM incoming
 WHERE capability='search' AND incoming.n>0`, s.BuildID, ids)
	}
	return err
}

func writeTrackChunk(ctx context.Context, tx *sql.Tx, s State, ids []string, markers []trackMarker) error {
	values, err := loadTrackValues(ctx, tx, ids)
	if err != nil {
		return err
	}
	type trackValue struct {
		TrackID string `json:"track_id"`
		value
	}
	batch := []trackValue{}
	for i := range markers {
		m := &markers[i]
		m.Title, err = normalizeValues(values[m.ID])
		if err != nil {
			return err
		}
		for _, v := range values[m.ID] {
			batch = append(batch, trackValue{m.ID, v})
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_values WHERE track_id=ANY($1::uuid[])`, ids); err != nil {
		return err
	}
	if len(batch) > 0 {
		data, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_search_values(id,track_id,field,role,position,original_value,normalized_value,short_grams,loose_value,loose_grams,normalization_version,projection_generation)
 SELECT gen_random_uuid(),v.track_id,v.field,NULLIF(v.role,''),v.position,v.original,v.normalized,v.grams,NULLIF(v.loose,''),v.loose_grams,$2,$3
 FROM jsonb_to_recordset($1::jsonb) AS v(track_id uuid,field text,role text,position integer,original text,normalized text,grams text[],loose text,loose_grams text[])
 WHERE v.normalized<>''`, string(data), s.TextnormVersion, s.TargetGeneration)
		if err != nil {
			return err
		}
	}
	data, err := json.Marshal(markers)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_subject_state s SET search_applied_generation=$2,
 search_applied_input_revision=m.revision,search_title=m.title FROM jsonb_to_recordset($1::jsonb) AS m(id uuid,revision bigint,title text)
 WHERE s.subject_kind='track' AND s.subject_id=m.id`, string(data), s.TargetGeneration)
	return err
}

func loadTrackValues(ctx context.Context, tx *sql.Tx, ids []string) (map[string][]value, error) {
	values := make(map[string][]value, len(ids))
	rows, err := tx.QueryContext(ctx, `SELECT id::text,title,artist,COALESCE(work_title,''),COALESCE(work_catalog,'')
 FROM tracks WHERE id=ANY($1::uuid[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, title, artist, workTitle, workCatalog string
		if err = rows.Scan(&id, &title, &artist, &workTitle, &workCatalog); err != nil {
			rows.Close()
			return nil, err
		}
		values[id] = appendValue(values[id], "track_title", "", title, 1)
		values[id] = appendValue(values[id], "track_artist", "", artist, 1)
		values[id] = appendValue(values[id], "work_title", "", workTitle, 1)
		values[id] = appendValue(values[id], "work_catalog", "", workCatalog, 1)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT track_id::text,role,name,position FROM track_credits WHERE track_id=ANY($1::uuid[]) ORDER BY track_id,role,position,id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, role, name string
		var position int
		if err = rows.Scan(&id, &role, &name, &position); err != nil {
			rows.Close()
			return nil, err
		}
		values[id], err = appendCredit(values[id], role, name, position)
		if err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	return values, err
}
