package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

type artistEvidence struct {
	ID         string
	MBIDs      []string
	Image, Bio bool
	Markers    []string
}

// 先按有效艺人分页，同一页一次取齐被并入身份的对应、已有资料和撤销标记。
func (w *Worker) artistMediaPage(ctx context.Context, after string) ([]artistEvidence, error) {
	rows, err := w.db.QueryContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, page AS (
        SELECT DISTINCT f.canonical_id FROM external_links l JOIN artist_media_family f ON f.artist_id=l.object_id
        WHERE l.source='musicbrainz' AND l.object_type='artist' AND f.canonical_id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
        ORDER BY f.canonical_id LIMIT $2
    ), family AS MATERIALIZED (
        SELECT f.canonical_id,f.artist_id FROM artist_media_family f JOIN page p USING(canonical_id)
    )
    SELECT p.canonical_id::text,
        (SELECT jsonb_agg(DISTINCT external_id ORDER BY external_id) FROM family f JOIN external_links l ON l.object_id=f.artist_id
         WHERE f.canonical_id=p.canonical_id AND l.source='musicbrainz' AND l.object_type='artist'),
        EXISTS(SELECT FROM family f JOIN artist_external_artworks a USING(artist_id) WHERE f.canonical_id=p.canonical_id),
        EXISTS(SELECT FROM family f JOIN artist_external_bios b USING(artist_id) WHERE f.canonical_id=p.canonical_id),
        (SELECT COALESCE(jsonb_agg(DISTINCT s.subject||':'||s.value_hash ORDER BY s.subject||':'||s.value_hash),'[]') FROM family f
         JOIN curation_suppressions s ON s.target_type='artist' AND s.target_id=f.artist_id
         WHERE f.canonical_id=p.canonical_id AND s.subject IN ('artist_image','artist_bio'))
    FROM page p ORDER BY p.canonical_id`, after, pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []artistEvidence
	for rows.Next() {
		var e artistEvidence
		var ids, markers []byte
		if err := rows.Scan(&e.ID, &ids, &e.Image, &e.Bio, &markers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ids, &e.MBIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(markers, &e.Markers); err != nil {
			return nil, err
		}
		page = append(page, e)
	}
	return page, rows.Err()
}

type artistProviders struct {
	active map[string]bool
	digest string
}

func (w *Worker) artistProviders(ctx context.Context) (artistProviders, error) {
	settings, err := w.sources.List(ctx)
	if err != nil {
		return artistProviders{}, err
	}
	active := map[string]bool{}
	var state []string
	for _, s := range settings {
		switch s.ID {
		case "musicbrainz", "wikidata", "commons", "wikipedia", "fanarttv", "theaudiodb":
		default:
			continue
		}
		active[s.ID] = s.Active
		if s.Enabled {
			state = append(state, s.ID)
		}
		if s.InactiveReason != nil {
			state = append(state, s.ID+"/"+*s.InactiveReason)
		}
		for _, k := range s.Keys {
			if k.Configured {
				state = append(state, s.ID+"/"+k.Name)
			}
			if k.Status == "invalid" {
				state = append(state, s.ID+"/"+k.Name+"/invalid")
			}
		}
	}
	// 换上尾号相同的新密钥也应重试；摘要只需要修改时间。
	var changed sql.NullTime
	err = w.db.QueryRowContext(ctx, `SELECT max(updated_at) FROM curation_source_keys WHERE source IN ('fanarttv','theaudiodb')`).Scan(&changed)
	return artistProviders{active, digest([]any{state, changed})}, err
}

func (w *Worker) walkArtistMedia(ctx context.Context, priority func() error) error {
	var after string
	for {
		if err := priority(); err != nil {
			return err
		}
		page, err := w.artistMediaPage(ctx, after)
		if err != nil || len(page) == 0 {
			return err
		}
		ids := make([]string, 0, len(page))
		for _, e := range page {
			if !e.Image || !e.Bio {
				ids = append(ids, e.ID)
			}
		}
		after = page[len(page)-1].ID
		if len(ids) == 0 {
			continue
		}
		providers, err := w.artistProviders(ctx)
		if err != nil {
			return err
		}
		states, err := curation.LoadWorkStates(ctx, w.db, "artist_media", ids)
		if err != nil {
			return err
		}
		for _, e := range page {
			if e.Image && e.Bio {
				continue
			}
			if err := priority(); err != nil {
				return err
			}
			if err := w.processArtistMedia(ctx, e, providers, states[e.ID]); err != nil {
				return err
			}
		}
	}
}

func (w *Worker) processArtistMedia(ctx context.Context, e artistEvidence, providers artistProviders, state curation.WorkState) error {
	const subject = "artist_media"
	input := digest([]any{e, providers.digest})
	if !needsWork(subject, state, input, w.opts.Now(), false) {
		return nil
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.rotateBatch(ctx); err != nil {
		return err
	}
	if !e.Image && !e.Bio && w.batch.Operations >= 499 {
		if err := w.closeBatch(ctx); err != nil {
			return err
		}
	}
	u := unit{w: w, subject: subject, id: e.ID, generation: state.Generation + 1}
	retry, err := w.enrichArtist(ctx, &u, &e, providers)
	if err != nil {
		return err
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	state.Subject, state.TargetID, state.RuleVersion = subject, e.ID, StoredRuleVersion
	state.InputDigest = digest([]any{e, providers.digest})
	state.Generation = u.generation
	if retry {
		delay := min(24*time.Hour, 10*time.Minute<<min(state.RetryCount, 8))
		next := w.opts.Now().Add(delay)
		state.Status, state.RetryCount, state.NextAttemptAt = "try_later", state.RetryCount+1, &next
	} else {
		state.Status, state.RetryCount, state.NextAttemptAt = "done", 0, nil
		state.Attempts++
		if !e.Image && !e.Bio {
			state.Status = "no_match"
		}
	}
	return curation.SaveWorkState(ctx, w.db, state)
}
