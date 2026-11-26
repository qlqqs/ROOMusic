package program

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type projectionOptions struct {
	now                      time.Time
	fingerprints             bool
	coverChange, lyricChange sql.NullTime
}

func (w *Worker) projectionOptions(ctx context.Context) (projectionOptions, error) {
	opts := projectionOptions{now: w.opts.Now()}
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return opts, err
	}
	opts.fingerprints = step != nil
	for capability, target := range map[sources.Capability]*sql.NullTime{
		sources.CoverArt: &opts.coverChange, sources.LyricsLookup: &opts.lyricChange,
	} {
		ids := w.sources.PluginIDs(capability)
		if len(ids) == 0 {
			continue
		}
		err = w.db.QueryRowContext(ctx, `SELECT GREATEST(max(s.updated_at),max(k.updated_at))
 FROM curation_source_settings s LEFT JOIN curation_source_keys k ON k.source=s.source
 WHERE s.enabled AND s.source=ANY($1::text[])`, ids).Scan(target)
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func (w *Worker) refreshQueueProjection(ctx context.Context) error {
	opts, err := w.projectionOptions(ctx)
	if err != nil {
		return err
	}
	if !opts.fingerprints {
		// 关闭 AcoustID 或移除本地工具后，识别应继续使用其余分支。重新开启时重新登记需求。
		if _, err = w.db.ExecContext(ctx, `DELETE FROM curation_work_states WHERE subject='fingerprint' AND needs_work`); err != nil {
			return err
		}
	}
	var after *catalog.CurationCursor
	for {
		if err := w.ready(ctx); err != nil {
			return err
		}
		page, err := catalog.PageCurationReleases(ctx, w.db, after, pageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		if after == nil {
			_, err = w.db.ExecContext(ctx, `UPDATE curation_settings SET queue_baseline_at=$1
 WHERE id=1 AND queue_baseline_at IS NULL`, page[0].AddedAt)
			if err != nil {
				return err
			}
		}
		if err = w.projectReleasePage(ctx, page, opts); err != nil {
			return err
		}
		after = &page[len(page)-1]
	}
	return w.refreshArtists(ctx, nil)
}

func (w *Worker) refreshReleaseQueue(ctx context.Context, releaseID string) error {
	opts, err := w.projectionOptions(ctx)
	if err != nil {
		return err
	}
	row := catalog.CurationCursor{ID: releaseID}
	if err := w.db.QueryRowContext(ctx, `SELECT r.created_at FROM releases r
 JOIN curation_eligible_releases e ON e.id=r.id WHERE r.id=$1`, releaseID).Scan(&row.AddedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if err := w.projectReleasePage(ctx, []catalog.CurationCursor{row}, opts); err != nil {
		return err
	}
	artists, err := w.creditedTargets(ctx, "release_id", []string{releaseID})
	if err != nil || len(artists) == 0 {
		return err
	}
	if err := w.refreshArtists(ctx, artists); err != nil {
		return err
	}
	return w.refreshLinkPriorities(ctx, artists)
}

func (w *Worker) projectReleasePage(ctx context.Context, page []catalog.CurationCursor, opts projectionOptions) error {
	ids := make([]string, len(page))
	for i, row := range page {
		ids[i] = row.ID
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL jit=off"); err != nil {
		return err
	}
	entries, err := catalog.LoadCurationEvidence(ctx, tx, ids, w.writer.ArtworkExists)
	if err != nil {
		return err
	}
	if err = curation.ProjectPatrolInputs(ctx, tx, ids); err != nil {
		return err
	}
	if err = curation.ProjectLocalItems(ctx, tx, ids, entries); err != nil {
		return err
	}
	missing, err := catalog.CurationMissingArtistLinks(ctx, tx, ids)
	if err != nil {
		return err
	}
	releases, err := curation.LoadWorkStates(ctx, tx, "release", ids)
	if err != nil {
		return err
	}
	artworks, err := curation.LoadWorkStates(ctx, tx, "artwork", ids)
	if err != nil {
		return err
	}
	var tracks []string
	for _, e := range entries {
		for _, t := range e.Tracks {
			tracks = append(tracks, t.ID)
		}
	}
	lyrics, err := curation.LoadWorkStates(ctx, tx, "lyrics", tracks)
	if err != nil {
		return err
	}
	var projected []queueProjection
	var completed []mediaRecovery
	for _, row := range page {
		e, visible := entries[row.ID]
		if !visible {
			continue
		}
		state := releases[e.ID]
		d := releaseInputs(e)
		need := releaseQueueNeeds(e, state, d, opts)
		class := e.Classification()
		projected = append(projected, queueProjection{
			Subject: "release", ID: e.ID, Release: e.ID, Class: class.Class,
			HasID: e.HasSearchID(), Gap: class.GapOrder[:], Unlinked: missing[e.ID],
			Added: &row.AddedAt, Identity: d.identity, Need: need,
		})
		cover := subjectEvidence("artwork", e, state)
		coverDigest := ArtworkDigest(cover)
		if e.ExternalArtwork && e.ArtworkFile && !e.LocalArtwork {
			completed = append(completed, mediaRecovery{Subject: "artwork", ID: e.ID, Release: e.ID, input: coverDigest, state: artworks[e.ID]})
		}
		projected = appendMedium(projected, "artwork", e.ID, e.ID, artworkGap(cover), coverDigest, artworks[e.ID], opts.coverChange, opts.now)
		for _, t := range e.Tracks {
			input := LyricsDigest(e, t)
			if t.ExternalLyrics && !t.LocalLyrics {
				completed = append(completed, mediaRecovery{Subject: "lyrics", ID: t.ID, Release: e.ID, input: input, state: lyrics[t.ID]})
			}
			projected = appendMedium(projected, "lyrics", t.ID, e.ID, lyricsGap(t), input, lyrics[t.ID], opts.lyricChange, opts.now)
		}
	}
	if err := w.saveMediaProjection(ctx, tx, projected, completed, releaseMediaBindings); err != nil {
		return err
	}
	return tx.Commit()
}

func releaseQueueNeeds(e catalog.CurationEvidence, state curation.WorkState, d releaseDigests, opts projectionOptions) bool {
	if len(e.StaleAuto) > 0 {
		return true
	}
	if state.Status == "no_usable_candidates" && state.MatchRuleVersion == MatchRuleVersion {
		review, err := previousReview(state)
		if err == nil && review.BlockDigest == blockedLinkDigest(e) {
			return false
		}
	}
	if state.InputDigest != d.full || state.IdentityDigest != d.identity ||
		state.GapDigest != d.gap || state.LocalRuleVersion != LocalRuleVersion || state.MatchRuleVersion != MatchRuleVersion {
		return true
	}
	tracks, artists, merges := matchBackfills(state, e)
	return tracks || artists || merges || needsWork("release", state, d.identity, opts.now, opts.fingerprints)
}

type queueProjection struct {
	Subject  string                `json:"subject"`
	ID       string                `json:"id"`
	Release  string                `json:"parent_id,omitempty"`
	Need     bool                  `json:"need"`
	Class    catalog.CurationClass `json:"class,omitempty"`
	HasID    bool                  `json:"has_id"`
	Gap      []int                 `json:"gap,omitempty"`
	Unlinked bool                  `json:"unlinked"`
	Added    *time.Time            `json:"added,omitempty"`
	Identity string                `json:"identity_digest,omitempty"`
}

func appendMedium(rows []queueProjection, subject, id, releaseID string, gap bool, input string, state curation.WorkState, plugins sql.NullTime, now time.Time) []queueProjection {
	if !gap && state.TargetID == "" {
		return rows
	}
	if !gap {
		releaseID = ""
	}
	need := gap && (needsWork(subject, state, input, now, false) || plugins.Time.After(state.UpdatedAt))
	return append(rows, queueProjection{Subject: subject, ID: id, Release: releaseID, Need: need})
}

// 同一页的发行、曲目共用一次往返，仅写有变化的投影。身份结论和工作代次由执行者维护。
func saveQueueProjection(ctx context.Context, tx *sql.Tx, rows []queueProjection) error {
	if len(rows) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO curation_work_states AS s
 (subject,target_id,rule_version,input_digest,status,queue_release_id,queue_class,queue_has_id,queue_gap,queue_artist_unlinked,queue_added_at,queue_identity,needs_work)
 SELECT p.subject,p.id,CASE p.subject WHEN 'release' THEN $2::smallint ELSE $3::smallint END,'','pending',
 p.parent_id,p.class,p.has_id,p.gap,p.unlinked,p.added,p.identity_digest,p.need
 FROM jsonb_to_recordset($1::jsonb) AS p(subject text,id uuid,parent_id uuid,class text,has_id boolean,gap integer[],unlinked boolean,added timestamptz,identity_digest text,need boolean)
 ON CONFLICT(subject,target_id) DO UPDATE SET queue_release_id=excluded.queue_release_id,queue_class=excluded.queue_class,
 queue_has_id=excluded.queue_has_id,queue_gap=excluded.queue_gap,queue_artist_unlinked=excluded.queue_artist_unlinked,
 queue_added_at=excluded.queue_added_at,queue_identity=excluded.queue_identity,needs_work=excluded.needs_work OR s.manual_requested_at IS NOT NULL
 WHERE (s.queue_release_id,s.queue_class,s.queue_has_id,s.queue_gap,s.queue_artist_unlinked,s.queue_added_at,s.queue_identity,s.needs_work)
 IS DISTINCT FROM (excluded.queue_release_id,excluded.queue_class,excluded.queue_has_id,excluded.queue_gap,excluded.queue_artist_unlinked,excluded.queue_added_at,excluded.queue_identity,
 excluded.needs_work OR s.manual_requested_at IS NOT NULL)`, jsonValue(rows), MatchRuleVersion, resourceRuleVersion)
	return err
}
