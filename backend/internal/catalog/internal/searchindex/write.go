package searchindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// RefreshRelease 与 RefreshTrack 只在调用方取得 CATP、写好事实后使用；不另开事务。
func RefreshRelease(ctx context.Context, tx *sql.Tx, id string) error {
	return refreshChanged(ctx, tx, "release", id)
}
func RefreshTrack(ctx context.Context, tx *sql.Tx, id string) error {
	return refreshChanged(ctx, tx, "track", id)
}
func refreshChanged(ctx context.Context, tx *sql.Tx, kind, id string) error {
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_projection_subject_state(subject_kind,subject_id,input_revision)
  VALUES($1,$2::uuid,1) ON CONFLICT(subject_kind,subject_id) DO UPDATE
  SET input_revision=catalog_projection_subject_state.input_revision+1`, kind, id); err != nil {
		return err
	}
	_, err = Apply(ctx, tx, s, kind, id)
	if err != nil {
		return err
	}
	if s.ManifestSealed {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET live_updated=live_updated+1 WHERE capability='search'
   AND NOT EXISTS (SELECT 1 FROM catalog_projection_work WHERE build_id=$1::uuid AND subject_kind=$2 AND subject_id=$3::uuid)`, s.BuildID, kind, id)
	}
	return err
}

// Apply 在锁内重读当前事实；已完成的同代同 revision 绝不重复覆盖。
// release 任务只包含五个发行字段，曲目始终各有独立任务，不展开整个发行。
func Apply(ctx context.Context, tx *sql.Tx, s State, kind, id string) (written bool, err error) {
	if err = textnorm.LoadEmbedded(); err != nil {
		return false, err
	}
	if !s.CurrentVersion() {
		return false, ErrVersionMismatch
	}
	if kind != "release" && kind != "track" {
		return false, errors.New("unknown projection subject")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_projection_subject_state(subject_kind,subject_id)
  VALUES($1,$2::uuid) ON CONFLICT DO NOTHING`, kind, id); err != nil {
		return false, err
	}
	var revision, appliedGeneration, appliedRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT input_revision,search_applied_generation,search_applied_input_revision
  FROM catalog_projection_subject_state WHERE subject_kind=$1 AND subject_id=$2::uuid`, kind, id).
		Scan(&revision, &appliedGeneration, &appliedRevision); err != nil {
		return false, err
	}
	if appliedGeneration == s.TargetGeneration && appliedRevision == revision {
		return false, nil
	}
	values, err := loadValues(ctx, tx, kind, id)
	if err != nil {
		return false, err
	}
	sortTitle, err := normalizeValues(values)
	if err != nil {
		return false, err
	}
	column := "release_id"
	if kind == "track" {
		column = "track_id"
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_values WHERE `+column+`=$1::uuid`, id); err != nil {
		return false, err
	}
	if len(values) > 0 {
		data, encodeErr := json.Marshal(values)
		if encodeErr != nil {
			return false, encodeErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_search_values(id,`+column+`,field,role,position,original_value,normalized_value,short_grams,normalization_version,projection_generation)
   SELECT gen_random_uuid(),$1::uuid,v.field,NULLIF(v.role,''),v.position,v.original,v.normalized,v.grams,$3,$4
   FROM jsonb_to_recordset($2::jsonb) AS v(field text,role text,position integer,original text,normalized text,grams text[])
   WHERE v.normalized<>''`, id, string(data), s.TextnormVersion, s.TargetGeneration)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_subject_state SET search_applied_generation=$3,search_applied_input_revision=$4,search_title=$5
  WHERE subject_kind=$1 AND subject_id=$2::uuid`, kind, id, s.TargetGeneration, revision, sortTitle)
	return true, err
}

type value struct {
	Field      string   `json:"field"`
	Role       string   `json:"role"`
	Position   int      `json:"position"`
	Original   string   `json:"original"`
	Normalized string   `json:"normalized"`
	Grams      []string `json:"grams"`
}

func loadValues(ctx context.Context, tx *sql.Tx, kind, id string) ([]value, error) {
	values := []value{}
	add := func(field, role, raw string, position int) {
		values = appendValue(values, field, role, raw, position)
	}
	if kind == "release" {
		var title, artist, albumArtist, label, number string
		err := tx.QueryRowContext(ctx, `SELECT title,artist,COALESCE(album_artist,''),COALESCE(label,''),COALESCE(catalog_number,'')
   FROM release_effective WHERE id=$1::uuid`, id).Scan(&title, &artist, &albumArtist, &label, &number)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		add("release_title", "", title, 1)
		add("release_artist", "", artist, 1)
		add("album_artist", "", albumArtist, 1)
		add("label", "", label, 1)
		add("catalog_number", "", number, 1)
		return values, nil
	}
	var title, artist string
	err := tx.QueryRowContext(ctx, `SELECT title,artist FROM tracks WHERE id=$1::uuid`, id).Scan(&title, &artist)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	add("track_title", "", title, 1)
	add("track_artist", "", artist, 1)
	rows, err := tx.QueryContext(ctx, `SELECT role,name,position FROM track_credits WHERE track_id=$1::uuid ORDER BY role,position,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var role, name string
		var position int
		if err := rows.Scan(&role, &name, &position); err != nil {
			return nil, err
		}
		values, err = appendCredit(values, role, name, position)
		if err != nil {
			return nil, err
		}
	}
	return values, rows.Err()
}
