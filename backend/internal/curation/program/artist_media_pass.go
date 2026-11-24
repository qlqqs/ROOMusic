package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type artistEvidence struct {
	ID, Name                           string
	MBIDs, WikidataIDs, Names, Markers []string
	ImageRank, BioRank                 int
	Albums, Tracks                     json.RawMessage
	NoLink                             bool
	hasCredits                         bool
	pendingStatus                      string
}

func (e artistEvidence) resolution() string {
	switch {
	case !e.NoLink:
		return "program_matched"
	case !e.hasCredits:
		return "artist_out_of_scope"
	case e.ImageRank > 0 && e.BioRank > 0:
		return "filled"
	}
	return ""
}

// 模型可能已提交最后一项资料并正准备完成自己的认领。释放后遗留的齐全项仍由程序收尾。
func (e artistEvidence) shouldClose() bool {
	reason := e.resolution()
	return e.pendingStatus != "" && reason != "" && !(reason == "filled" && e.pendingStatus == "in_progress")
}

const primaryArtistRolesSQL = `('release_artist','album_artist','track_artist')`

const artistMediaCandidatesSQL = `SELECT a.id FROM artists a
WHERE a.id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
  AND NOT EXISTS (SELECT FROM artist_overrides o WHERE o.artist_id=a.id AND o.redirect_artist_id IS NOT NULL)
  AND (
    EXISTS (SELECT FROM artist_media_family f JOIN external_links l ON l.object_id=f.artist_id
      WHERE f.canonical_id=a.id AND l.object_type='artist' AND l.source IN ('musicbrainz','wikidata'))
    OR (
      (NOT EXISTS (SELECT FROM artist_media_family f JOIN (` + catalog.ArtistImageRowsSQL + `) m USING(artist_id) WHERE f.canonical_id=a.id)
        OR NOT EXISTS (SELECT FROM artist_media_family f JOIN (` + catalog.ArtistBioRowsSQL + `) m USING(artist_id) WHERE f.canonical_id=a.id))
      AND EXISTS (SELECT FROM artist_media_family f JOIN artist_effective_credits c USING(artist_id)
        WHERE f.canonical_id=a.id AND c.role IN ` + primaryArtistRolesSQL + `
        AND (EXISTS (SELECT FROM tracks t WHERE t.id=c.track_id AND t.source_status='present')
          OR EXISTS (SELECT FROM media m JOIN tracks t ON t.medium_id=m.id
            WHERE m.release_id=c.release_id AND t.source_status='present')))
    )
  )
ORDER BY a.id LIMIT $2`

// 分页限定家族范围后再展开主署名，仅把名称、年份和资料缺口投影到待定项。
func artistMediaPage(ctx context.Context, db catalog.Queryer, pageSQL string, args ...any) ([]artistEvidence, error) {
	rows, err := db.QueryContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, page(canonical_id) AS MATERIALIZED (`+pageSQL+`),
	family AS MATERIALIZED (SELECT f.* FROM artist_media_family f JOIN page USING(canonical_id)),
	`+curation.ArtistMediaLinksCTE+`, primary_credits AS MATERIALIZED (
		SELECT f.canonical_id,c.track_id,c.release_id FROM family f JOIN artist_effective_credits c USING(artist_id)
		WHERE c.role IN `+primaryArtistRolesSQL+`
		AND NOT EXISTS (SELECT FROM media_links l WHERE l.canonical_id=f.canonical_id)
	), credited_tracks AS MATERIALIZED (
		SELECT c.canonical_id,t.id,m.release_id FROM primary_credits c JOIN tracks t ON t.id=c.track_id
		JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'
		UNION SELECT c.canonical_id,t.id,m.release_id FROM primary_credits c JOIN media m ON m.release_id=c.release_id
		JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present'
	)
	SELECT p.canonical_id::text, COALESCE(profile.effective_display_name,a.initial_name),
		to_jsonb(ARRAY(SELECT external_id FROM media_links l WHERE l.canonical_id=p.canonical_id AND l.source='musicbrainz' GROUP BY external_id ORDER BY external_id)),
		to_jsonb(ARRAY(SELECT external_id FROM media_links l WHERE l.canonical_id=p.canonical_id AND l.source='wikidata' GROUP BY external_id ORDER BY external_id)),
		COALESCE((SELECT max(`+catalog.ArtistMediaRankSQL+`) FROM family f JOIN (`+catalog.ArtistImageRowsSQL+`) m USING(artist_id) WHERE f.canonical_id=p.canonical_id),0),
		COALESCE((SELECT max(`+catalog.ArtistMediaRankSQL+`) FROM family f JOIN (`+catalog.ArtistBioRowsSQL+`) m USING(artist_id) WHERE f.canonical_id=p.canonical_id),0),
		to_jsonb(ARRAY(SELECT DISTINCT s.subject||':'||s.value_hash FROM family f JOIN curation_suppressions s ON s.target_id=f.artist_id
			WHERE f.canonical_id=p.canonical_id AND s.target_type='artist' AND s.subject IN ('artist_image','artist_bio','external_link') ORDER BY 1)),
		to_jsonb(ARRAY(SELECT DISTINCT name FROM (
			SELECT COALESCE(profile.effective_display_name,a.initial_name) AS name
			UNION ALL SELECT n.original_value FROM family f JOIN artist_search_names n USING(artist_id) WHERE f.canonical_id=p.canonical_id
		) names ORDER BY name)),
		(SELECT COALESCE(jsonb_agg(to_jsonb(sample) ORDER BY name,year),'[]') FROM (
			SELECT DISTINCT r.title AS name,r.year FROM credited_tracks c JOIN release_effective r ON r.id=c.release_id
			WHERE c.canonical_id=p.canonical_id ORDER BY name,year LIMIT 8
		) sample),
		to_jsonb(ARRAY(SELECT DISTINCT t.title FROM credited_tracks c JOIN track_effective t ON t.id=c.id
			WHERE c.canonical_id=p.canonical_id ORDER BY t.title LIMIT 12)),
		EXISTS(SELECT FROM credited_tracks c WHERE c.canonical_id=p.canonical_id),
		COALESCE((SELECT status FROM curation_pending_items pi WHERE pi.artist_id=p.canonical_id
			AND pi.target_type='artist' AND pi.kind='artist_identity' AND pi.status<>'resolved'),'')
	FROM page p JOIN artists a ON a.id=p.canonical_id LEFT JOIN artist_profiles profile ON profile.artist_id=p.canonical_id
	ORDER BY p.canonical_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []artistEvidence
	for rows.Next() {
		var e artistEvidence
		var ids, qids, markers, names []byte
		if err := rows.Scan(&e.ID, &e.Name, &ids, &qids, &e.ImageRank, &e.BioRank, &markers, &names, &e.Albums, &e.Tracks, &e.hasCredits, &e.pendingStatus); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ids, &e.MBIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(qids, &e.WikidataIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(markers, &e.Markers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(names, &e.Names); err != nil {
			return nil, err
		}
		e.NoLink = len(e.MBIDs) == 0 && len(e.WikidataIDs) == 0
		page = append(page, e)
	}
	return page, rows.Err()
}

type artistProviders struct {
	active      map[string]bool
	namedPlugin bool
	digest      string
}

func (w *Worker) artistProviders(ctx context.Context) (artistProviders, error) {
	settings, err := w.sources.List(ctx)
	if err != nil {
		return artistProviders{}, err
	}
	active := map[string]bool{}
	var state []string
	var namedPlugin bool
	for _, s := range settings {
		switch s.ID {
		case "musicbrainz", "wikidata", "commons", "wikipedia", "fanarttv", "theaudiodb", "web_search":
		default:
			if !strings.HasPrefix(s.ID, "plugin:") || !slices.Contains(s.Capabilities, sources.ArtistImageLookup) && !slices.Contains(s.Capabilities, sources.ArtistBioLookup) {
				continue
			}
		}
		active[s.ID] = s.Active
		if s.ID == "web_search" {
			for _, requirement := range s.Requirements {
				if requirement.Ready {
					state = append(state, s.ID+"/"+requirement.Kind)
				} else {
					active[s.ID] = false
				}
			}
		}
		if strings.HasPrefix(s.ID, "plugin:") && s.Active {
			namedPlugin = true
		}
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
	err = w.db.QueryRowContext(ctx, `SELECT GREATEST((SELECT max(updated_at) FROM curation_source_keys WHERE source IN ('fanarttv','theaudiodb') OR source LIKE 'plugin:%'),(SELECT max(updated_at) FROM curation_source_settings WHERE source LIKE 'plugin:%' AND enabled))`).Scan(&changed)
	return artistProviders{active: active, namedPlugin: namedPlugin, digest: digest([]any{state, changed})}, err
}

func (w *Worker) walkArtistMedia(ctx context.Context, priority func() error) error {
	if err := w.closeInactiveArtistItems(ctx, priority); err != nil {
		return err
	}
	var after string
	for {
		if err := priority(); err != nil {
			return err
		}
		page, err := artistMediaPage(ctx, w.db, artistMediaCandidatesSQL, after, pageSize)
		if err != nil || len(page) == 0 {
			return err
		}
		ids := make([]string, 0, len(page))
		for _, e := range page {
			ids = append(ids, e.ID)
		}
		after = page[len(page)-1].ID
		providers, err := w.artistProviders(ctx)
		if err != nil {
			return err
		}
		states, err := curation.LoadWorkStates(ctx, w.db, "artist_media", ids)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := priority(); err != nil {
				return err
			}
			if err := w.processArtistMedia(ctx, e, providers, states[e.ID]); err != nil {
				return err
			}
		}
	}
}

// 资料补齐、失去主署名或并入后，仍需收尾此前已创建的待定项。
func (w *Worker) closeInactiveArtistItems(ctx context.Context, priority func() error) error {
	var after string
	for {
		if err := priority(); err != nil {
			return err
		}
		page, err := artistMediaPage(ctx, w.db, `SELECT artist_id FROM curation_pending_items
			WHERE target_type='artist' AND kind='artist_identity' AND status<>'resolved'
			AND artist_id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
			ORDER BY artist_id LIMIT $2`, after, pageSize)
		if err != nil || len(page) == 0 {
			return err
		}
		for _, e := range page {
			if e.shouldClose() {
				if err := w.finishArtistState(ctx, e, artistProviders{}, nil); err != nil {
					return err
				}
			}
		}
		after = page[len(page)-1].ID
	}
}

func (w *Worker) processArtistMedia(ctx context.Context, e artistEvidence, providers artistProviders, state curation.WorkState) error {
	const subject = "artist_media"
	if e.shouldClose() {
		if err := w.finishArtistState(ctx, e, providers, nil); err != nil {
			return err
		}
	}
	if e.NoLink {
		canUpgrade := providers.namedPlugin && min(e.ImageRank, e.BioRank) < catalog.ArtistMediaPluginRank
		canQueue := providers.active["web_search"] && e.resolution() == ""
		if !e.hasCredits || !canUpgrade && !canQueue {
			return nil
		}
	} else if e.ImageRank >= catalog.ArtistMediaLicensedRank && e.BioRank >= catalog.ArtistMediaLicensedRank {
		return nil
	}
	input := digest([]any{e, providers.digest})
	// 模型写入一项资料后结束原项，程序用当前缺口和摘要建立下一项。
	missingItem := e.NoLink && providers.active["web_search"] && e.resolution() == "" && e.pendingStatus == ""
	if !missingItem && !needsWork(subject, state, input, w.opts.Now(), false) {
		return nil
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.rotateBatch(ctx); err != nil {
		return err
	}
	if e.ImageRank < catalog.ArtistMediaLicensedRank && e.BioRank < catalog.ArtistMediaLicensedRank && w.batch.Operations >= 499 {
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
		if e.ImageRank == 0 && e.BioRank == 0 {
			state.Status = "no_match"
		}
	}
	if e.NoLink && (e.pendingStatus != "" || providers.active["web_search"]) {
		return w.finishArtistState(ctx, e, providers, &state)
	}
	return curation.SaveWorkState(ctx, w.db, state)
}

// 建项或收尾时在目录写锁内重读。资料写入、认领和当前批次外的人工改动可能发生在插件查询期间。
func (w *Worker) finishArtistState(ctx context.Context, observed artistEvidence, p artistProviders, state *curation.WorkState) error {
	if state != nil && p.active["web_search"] && observed.resolution() == "" {
		if err := w.sources.Check(ctx, "web_search"); err != nil {
			return err
		}
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		return err
	}
	page, err := artistMediaPage(ctx, tx, `SELECT $1::uuid`, observed.ID)
	if err != nil {
		return err
	}
	if len(page) != 1 {
		return errReleaseChanged
	}
	e := page[0]
	if reason := e.resolution(); reason != "" {
		if e.shouldClose() {
			if err := curation.ResolvePendingItems(ctx, tx, e.ID, "artist_identity", reason); err != nil {
				return err
			}
		}
	} else if state != nil && p.active["web_search"] {
		input := digest([]any{e, p.digest})
		evidence := map[string]any{"names": e.Names, "albums": e.Albums, "tracks": e.Tracks, "gaps": map[string]bool{"image": e.ImageRank == 0, "bio": e.BioRank == 0}}
		item := curation.PendingItem{TargetType: "artist", ArtistID: e.ID, Kind: "artist_identity", RuleVersion: StoredRuleVersion, InputDigest: input, Evidence: jsonValue(evidence)}
		if err := curation.UpsertPendingItem(ctx, tx, item); err != nil {
			return err
		}
	}
	if state != nil {
		if state.InputDigest != digest([]any{e, p.digest}) {
			return errReleaseChanged
		}
		if err := curation.SaveWorkState(ctx, tx, *state); err != nil {
			return err
		}
	}
	return tx.Commit()
}
