package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ArtistAutoMerge struct {
	A, B, ExternalID string
	Source           Source
	BatchID, Key     string
}

func (w *Writer) WithArtists(g *governance.ArtistGovernance) *Writer {
	w.artists = g
	return w
}

// MusicBrainzSpecialArtist 排除用于汇总多人或缺失署名的公共身份。
func MusicBrainzSpecialArtist(id string) bool {
	switch strings.ToLower(id) {
	case "89ad4ac3-39f7-470e-963a-56509c546377", // Various Artists
		"125ec42a-7229-4250-afc5-e057484327fe": // [unknown]
		return true
	}
	return false
}

type ArtistMergeApplied struct {
	OperationID string `json:"operation_id"`
	ArtistID    string `json:"artist_id"`
}

func (w *Writer) MergeArtists(ctx context.Context, r ArtistAutoMerge) (ArtistMergeApplied, error) {
	a, b := strings.ToLower(r.A), strings.ToLower(r.B)
	if a > b {
		a, b = b, a
	}
	if !uuid.Valid(a) || !uuid.Valid(b) || !validExternalID(r.ExternalID) || r.Source.Kind != "source" || r.Source.Provider != "musicbrainz" {
		return ArtistMergeApplied{}, operations.Invalid("invalid_input", "艺人、外部编号或来源无效")
	}
	if MusicBrainzSpecialArtist(r.ExternalID) {
		return ArtistMergeApplied{}, operations.Conflict("no_strong_evidence", "特殊用途艺人不能作为合并依据")
	}
	fp := operations.Fingerprint(map[string]any{"type": operations.TypeArtistMerge, "pair": []string{a, b}, "external_id": r.ExternalID, "provider": r.Source.Provider, "rule": "same_mb_artist/1"})
	spec, err := w.sourceSpec(ctx, operations.TypeArtistMerge, a, fp, r.Source, r.BatchID, r.Key, "", "", "")
	if err != nil {
		return ArtistMergeApplied{}, err
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		if err := checkArtistEvidence(ctx, tx, a, b, r.ExternalID); err != nil {
			return err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return err
		}
		return checkArtistSeparation(ctx, tx, a, b)
	}
	result, err := operations.Run(ctx, w.db, spec, w.artists.CurationMergeExecutor(a, b, check))
	if err != nil {
		return ArtistMergeApplied{}, err
	}
	var out ArtistMergeApplied
	err = json.Unmarshal(result.Response, &out)
	return out, err
}

const artistClosuresSQL = `WITH roots(id) AS (VALUES ($1::uuid),($2::uuid)), members(id,root) AS (
 SELECT id,id FROM roots
 UNION ALL SELECT o.artist_id,o.redirect_artist_id FROM artist_overrides o JOIN roots r ON o.redirect_artist_id=r.id)`

func checkArtistEvidence(ctx context.Context, tx *sql.Tx, a, b, externalID string) error {
	strong, err := artistEvidence(ctx, tx, a, b, externalID)
	if err == nil && !strong {
		return operations.Conflict("no_strong_evidence", "两位艺人缺少唯一且一致的 MusicBrainz 对应")
	}
	return err
}
func artistEvidence(ctx context.Context, q operations.Queryer, a, b, externalID string) (bool, error) {
	var strong bool
	err := q.QueryRowContext(ctx, artistClosuresSQL+`, links AS MATERIALIZED (
 SELECT m.root,l.object_id,l.external_id,l.operation_id FROM members m JOIN external_links l ON l.object_id=m.id
 WHERE l.source='musicbrainz' AND l.object_type='artist' FOR SHARE OF l)
 SELECT NOT EXISTS(SELECT 1 FROM links WHERE external_id<>$3) AND
 (SELECT count(DISTINCT l.root) FROM links l JOIN operations o ON o.id=l.operation_id
 WHERE l.external_id=$3 AND o.operation_type='external_link' AND o.target_type='release' AND o.after_state->>'source'='musicbrainz'
 AND o.after_state->'links' @> jsonb_build_array(jsonb_build_object('object_type','artist','object_id',l.object_id,'external_id',l.external_id))
 AND NOT EXISTS(SELECT 1 FROM operations r WHERE r.reverts_operation_id=o.id))=2`, a, b, externalID).Scan(&strong)
	return strong, err
}

func checkArtistSeparation(ctx context.Context, tx operations.Queryer, a, b string) error {
	var suppressed, manual, together bool
	err := tx.QueryRowContext(ctx, artistClosuresSQL+`, routed_keys AS (
 SELECT k.id,m.root FROM artist_name_keys k
 LEFT JOIN artist_name_bindings binding ON binding.key_id=k.id
 JOIN members m ON m.id=COALESCE(binding.artist_id,k.initial_artist_id))
 SELECT EXISTS(SELECT 1 FROM members p JOIN curation_suppressions s ON s.target_id=p.id
 JOIN members q ON q.id::text=s.value->>'artist_id' AND q.root<>p.root
 WHERE s.target_type='artist' AND s.subject='artist_merge'),
 EXISTS(SELECT 1 FROM members p JOIN operations_artist_targets tp ON tp.artist_id=p.id
 JOIN operations o ON o.id=tp.operation_id LEFT JOIN operations original ON original.id=o.reverts_operation_id
 WHERE (o.operation_type='artist_split' OR (o.operation_type='revert' AND original.operation_type='artist_merge'))
 AND EXISTS(SELECT 1 FROM operations_artist_targets tq JOIN members q ON q.id=tq.artist_id
 WHERE tq.operation_id=o.id AND q.root<>p.root))
 OR EXISTS(SELECT 1 FROM artist_candidate_ignores i JOIN routed_keys p ON p.id=i.left_key_id
 JOIN routed_keys q ON q.id=i.right_key_id WHERE p.root<>q.root),
 EXISTS (WITH credits AS (
 SELECT c.track_id,c.layer,c.position,m.root FROM artist_credits c JOIN tracks t ON t.id=c.track_id JOIN media medium ON medium.id=t.medium_id
 JOIN artist_names n ON n.id=c.name_id JOIN artist_name_keys k ON k.id=n.key_id
 LEFT JOIN artist_assignment_overrides ta ON ta.track_id=t.id AND ta.key_id=k.id
 LEFT JOIN artist_assignment_overrides ra ON ra.release_id=medium.release_id AND ra.key_id=k.id
 LEFT JOIN artist_name_bindings binding ON binding.key_id=k.id
 JOIN members m ON m.id=COALESCE(ta.artist_id,ra.artist_id,binding.artist_id,k.initial_artist_id)
 WHERE c.role='track_artist')
 SELECT FROM credits x JOIN credits y USING(track_id,layer) WHERE x.root<>y.root AND x.position<>y.position)`, a, b).Scan(&suppressed, &manual, &together)
	if err != nil {
		return err
	}
	if together {
		return operations.Conflict("no_strong_evidence", "两位艺人曾在同一曲目并列署名")
	}
	if suppressed {
		return operations.Conflict("nothing_to_apply", "这两位艺人已标记不再自动合并")
	}
	if manual {
		return operations.Conflict("manual_structure", "这两位艺人已有分开决定")
	}
	return nil
}

func (w *Writer) SuppressArtistMerge(ctx context.Context, tx *sql.Tx, targetID, sourceID, revertID, actorID string) error {
	first, second := min(targetID, sourceID), max(targetID, sourceID)
	var name string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.effective_display_name,a.initial_name)
 FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id WHERE a.id=$1`, second).Scan(&name)
	if err != nil {
		return err
	}
	return insertSuppression(ctx, tx, "artist", first, "artist_merge", hashExternalValue(second), map[string]string{"artist_id": second, "name": name}, revertID, actorID)
}
