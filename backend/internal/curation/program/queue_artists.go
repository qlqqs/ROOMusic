package program

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

func (w *Worker) creditedTargets(ctx context.Context, field string, ids []string) ([]string, error) {
	query := `SELECT DISTINCT release_id FROM credits WHERE artist_id=ANY($1::uuid[]) ORDER BY release_id`
	if field == "release_id" {
		query = `SELECT DISTINCT artist_id FROM credits WHERE release_id=ANY($1::uuid[]) ORDER BY artist_id`
	}
	rows, err := w.db.QueryContext(ctx, curation.ArtistReleaseCreditsSQL+query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		targets = append(targets, id)
	}
	return targets, rows.Err()
}

func (w *Worker) refreshLinkPriorities(ctx context.Context, artists []string) error {
	ids, err := w.creditedTargets(ctx, "artist_id", artists)
	if err != nil || len(ids) == 0 {
		return err
	}
	missing, err := catalog.CurationMissingArtistLinks(ctx, w.db, ids)
	if err != nil {
		return err
	}
	var unlinked []string
	for _, id := range ids {
		if missing[id] {
			unlinked = append(unlinked, id)
		}
	}
	_, err = w.db.ExecContext(ctx, `UPDATE curation_work_states SET queue_artist_unlinked=target_id=ANY(COALESCE($2::uuid[],'{}'))
 WHERE subject='release' AND target_id=ANY($1::uuid[]) AND queue_artist_unlinked IS DISTINCT FROM (target_id=ANY(COALESCE($2::uuid[],'{}')))`, ids, unlinked)
	return err
}

func (w *Worker) refreshArtistQueue(ctx context.Context, artistID string) error {
	ids := []string{artistID}
	if err := w.refreshLinkPriorities(ctx, ids); err != nil {
		return err
	}
	return w.refreshArtists(ctx, ids)
}

func (w *Worker) refreshArtists(ctx context.Context, selected []string) error {
	providers, err := w.artistProviders(ctx)
	if err != nil {
		return err
	}
	var after string
	for {
		if err := w.ready(ctx); err != nil {
			return err
		}
		pageSQL := artistMediaCandidatesSQL
		args := []any{after, pageSize}
		if len(selected) > 0 {
			pageSQL = `SELECT id FROM artists WHERE id=ANY($1::uuid[])`
			args = []any{selected}
		}
		// 每页的缺口、已有状态及其关联发行来自同一份快照。
		tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return err
		}
		page, err := w.projectArtistPage(ctx, tx, pageSQL, providers, args...)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if len(selected) > 0 || len(page) < pageSize {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

func (w *Worker) projectArtistPage(ctx context.Context, tx *sql.Tx, pageSQL string, providers artistProviders, args ...any) ([]artistEvidence, error) {
	if _, err := tx.ExecContext(ctx, "SET LOCAL jit=off"); err != nil {
		return nil, err
	}
	page, err := artistMediaPage(ctx, tx, pageSQL, args...)
	if err != nil || len(page) == 0 {
		return page, err
	}
	ids := make([]string, len(page))
	for i, e := range page {
		ids[i] = e.ID
	}
	states, err := curation.LoadWorkStates(ctx, tx, "artist_media", ids)
	if err != nil {
		return nil, err
	}
	parents, err := artistQueueParents(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	var projected []queueProjection
	var completed []mediaRecovery
	for _, e := range page {
		state := states[e.ID]
		rank := min(e.ImageRank, e.BioRank)
		want := rank < catalog.ArtistMediaLicensedRank
		if e.NoLink {
			want = e.hasCredits && (providers.namedPlugin && rank < catalog.ArtistMediaPluginRank ||
				providers.active["web_search"] && e.resolution() == "")
		}
		missingItem := e.NoLink && providers.active["web_search"] && e.resolution() == "" && e.pendingStatus == ""
		needs := !e.waitingForRecognition && (e.shouldClose() || want && (missingItem || needsWork("artist_media", state, digest([]any{e, providers.digest}), w.opts.Now(), false)))
		if !want && rank > 0 && (!e.NoLink || e.hasCredits) {
			item := mediaRecovery{Subject: "artist_media", ID: e.ID, Release: parents[e.ID], state: state, input: digest([]any{e, providers.digest}), imageRank: e.ImageRank, bioRank: e.BioRank}
			if e.shouldClose() {
				item.resolution = e.resolution()
			}
			completed = append(completed, item)
		}
		if !needs && state.TargetID == "" {
			continue
		}
		parent := parents[e.ID]
		if !want && !e.shouldClose() {
			parent = ""
		}
		projected = append(projected, queueProjection{Subject: "artist_media", ID: e.ID, Release: parent, Need: needs})
	}
	return page, w.saveMediaProjection(ctx, tx, projected, completed, artistMediaBindings)
}

func artistQueueParents(ctx context.Context, tx *sql.Tx, ids []string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, curation.ArtistReleaseCreditsSQL+`, visible AS NOT MATERIALIZED (`+catalog.VisibleReleaseIDsSQL+`), ranked AS (
 SELECT c.artist_id,c.release_id,stable.id IS NOT NULL stable,`+queueRankSQL+` rank,
 r.queue_gap,r.queue_artist_unlinked,r.queue_added_at
 FROM credits c JOIN visible ON visible.id=c.release_id JOIN curation_work_states r ON r.subject='release' AND r.target_id=c.release_id
 CROSS JOIN curation_settings settings
 LEFT JOIN curation_work_states s ON s.subject='artist_media' AND s.target_id=c.artist_id
 LEFT JOIN curation_stable_releases stable ON stable.id=c.release_id
 WHERE settings.id=1 AND c.artist_id=ANY($1::uuid[])
 ) SELECT DISTINCT ON (artist_id) artist_id,release_id FROM ranked ORDER BY artist_id,stable DESC,rank,
 CASE WHEN rank=3 THEN queue_gap END DESC,CASE WHEN rank=4 THEN queue_artist_unlinked END DESC,queue_added_at DESC,release_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parents := map[string]string{}
	for rows.Next() {
		var artist, release string
		if err = rows.Scan(&artist, &release); err != nil {
			return nil, err
		}
		parents[artist] = release
	}
	return parents, rows.Err()
}
