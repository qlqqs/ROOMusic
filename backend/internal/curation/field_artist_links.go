package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// ReleaseArtistCredit 与字段值取自同一份发行详情，曲目署名另带已配对的录音。
type ReleaseArtistCredit struct {
	ReleaseID, RecordingID string
	Artists                []sources.Credit
}

// 记录具体对应行，撤销后即使重新连到相同编号，也不会使原依据复活。
type fieldCreditProof struct {
	ReleaseLink string       `json:"release_link"`
	TrackLink   string       `json:"track_link,omitempty"`
	Links       []LinkIntent `json:"links"`
}

// ValidArtistCreditPositions 核对整组人数、连续位置及两端身份的唯一性。
func ValidArtistCreditPositions(local []catalog.CurationArtistCredit, external []sources.Credit) bool {
	if len(local) == 0 || len(local) != len(external) {
		return false
	}
	seenLocal, seenExternal := map[string]bool{}, map[string]bool{}
	for i, c := range local {
		x := external[i].ArtistID
		if c.Position != i+1 || c.ArtistID == "" || !validExternalID(x) || seenLocal[c.ArtistID] || seenExternal[x] || MusicBrainzSpecialArtist(x) {
			return false
		}
		seenLocal[c.ArtistID], seenExternal[x] = true, true
	}
	return true
}

func fieldArtistCredit(ctx context.Context, tx *sql.Tx, source *ReleaseArtistCredit, level catalog.AutoWriterLevel, releaseID, trackID string) (*fieldCreditProof, error) {
	if source == nil || len(source.Artists) == 0 || level < catalog.LevelModelExternal {
		return nil, nil
	}
	var proof fieldCreditProof
	err := tx.QueryRowContext(ctx, `SELECT r.id,COALESCE(t.id::text,'') FROM external_links r
	LEFT JOIN external_links t ON t.source=r.source AND t.object_type='track' AND t.object_id=NULLIF($3,'')::uuid AND t.external_id=$4
	WHERE r.source='musicbrainz' AND r.object_type='release' AND r.object_id=$1 AND r.external_id=$2 AND ($3='' OR t.id IS NOT NULL)`, releaseID, source.ReleaseID, trackID, source.RecordingID).Scan(&proof.ReleaseLink, &proof.TrackLink)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	local, err := catalog.EffectiveAutoArtistCredits(ctx, tx, releaseID, trackID)
	if err != nil {
		return nil, err
	}
	if !ValidArtistCreditPositions(local, source.Artists) {
		return nil, nil
	}
	for i, c := range local {
		proof.Links = append(proof.Links, LinkIntent{ObjectType: "artist", ObjectID: c.ArtistID, ExternalID: source.Artists[i].ArtistID})
	}
	return &proof, nil
}

// 只重算本次发行的自动署名所涉及的艺人。每位艺人的其他发行、曲目仍可独立佐证同一编号。
// 新字段操作由 operations.Run 在回调返回后落库，所以把本次依据与已提交的操作一起核对。
func refreshFieldArtistLinks(ctx context.Context, tx *sql.Tx, releaseID, operationID string, incoming *fieldCreditProof) error {
	var ids []string
	if incoming != nil {
		for _, l := range incoming.Links {
			ids = append(ids, l.ObjectID)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.artist_id FROM operations o JOIN operations_artist_targets a ON a.operation_id=o.id
	WHERE o.target_type='release' AND o.target_id=$1 AND o.response ? 'artist_credit'`, releaseID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return reconcileFieldArtistLinks(ctx, tx, ids, operationID, incoming)
}

func reconcileFieldArtistLinks(ctx context.Context, tx *sql.Tx, ids []string, operationID string, incoming *fieldCreditProof) error {
	raw, err := json.Marshal(incoming)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `WITH credits AS (`+catalog.EffectiveExternalAutoCreditsSQL+`), proofs AS (
	SELECT c.*, CASE WHEN c.operation_id=NULLIF($2,'')::uuid THEN $3::jsonb ELSE o.response->'artist_credit' END proof
	FROM credits c LEFT JOIN operations o ON o.id=c.operation_id
	), candidates AS (
	SELECT c.artist_id, c.operation_id, link->>'external_id' AS external_id
	FROM proofs c JOIN external_links r ON r.id=(c.proof->>'release_link')::uuid AND r.object_id=c.release_id
	CROSS JOIN LATERAL (SELECT c.proof->'links'->(c.position-1) AS link) pos
	WHERE link->>'object_id'=c.artist_id::text AND (NOT (proof ? 'track_link') OR EXISTS(
	SELECT FROM external_links t WHERE t.id=(proof->>'track_link')::uuid))
	)
	SELECT c.artist_id,min(c.external_id),COALESCE(min(c.operation_id::text) FILTER (WHERE c.operation_id=l.operation_id),min(c.operation_id::text))
    FROM candidates c LEFT JOIN external_links l ON l.object_id=c.artist_id AND l.source='musicbrainz' AND l.object_type='artist'
    GROUP BY c.artist_id HAVING count(DISTINCT c.external_id)=1`, ids, operationID, raw)
	if err != nil {
		return err
	}
	desired := map[string]Link{}
	for rows.Next() {
		l := Link{Source: "musicbrainz", ObjectType: "artist", Method: "artist_credit", Confidence: "certain"}
		if err := rows.Scan(&l.ObjectID, &l.ExternalID, &l.OperationID); err != nil {
			rows.Close()
			return err
		}
		desired[l.ObjectID] = l
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var changed []string
	for _, id := range ids {
		l, want := desired[id]
		// 只清理归属于自动字段依据的行，人工对应和独立的扫描署名对应保留原写入者。
		result, err := tx.ExecContext(ctx, `DELETE FROM external_links l USING operations o
		WHERE l.object_type='artist' AND l.source='musicbrainz' AND l.object_id=$1 AND l.operation_id=o.id
		AND o.response ? 'artist_credit' AND (l.external_id<>$2 OR o.id IS DISTINCT FROM NULLIF($3,'')::uuid)`, id, l.ExternalID, l.OperationID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if want {
			skip, err := addUnoccupiedLink(ctx, tx, l)
			if err != nil {
				return err
			}
			if skip == "" {
				n++
			}
		}
		if n > 0 {
			changed = append(changed, id)
		}
	}
	return catalog.RefreshMergedArtistProfiles(ctx, tx, changed)
}

// 撤销补写及撤销作废都会改变有效署名，和字段恢复共用原操作事务。
type fieldCreditReverter struct{ operations.Kind }

func WithFieldArtistLinks(kind operations.Kind) operations.Kind { return fieldCreditReverter{kind} }
func (k fieldCreditReverter) Revert(ctx context.Context, tx *sql.Tx, id string, original operations.Record) (operations.Outcome, error) {
	out, err := k.Kind.Revert(ctx, tx, id, original)
	if err == nil {
		err = refreshFieldArtistLinks(ctx, tx, original.TargetID, "", nil)
	}
	return out, err
}
func (k fieldCreditReverter) PreviewRevert(ctx context.Context, q operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	return k.Kind.(operations.RevertPreviewer).PreviewRevert(ctx, q, r)
}

// CreditSplit 依附当前有效的版、组或录音对应，原署名文字仍由目录显示。
type CreditSplit struct {
	Role      string           `json:"role"`
	TrackID   string           `json:"track_id,omitempty"`
	LocalText string           `json:"local_text"`
	BasisType string           `json:"basis_type"`
	BasisID   string           `json:"basis_id"`
	Credits   []sources.Credit `json:"credits"`
}

func (w *Writer) SplitArtistCredit(ctx context.Context, request LinkRequest, split CreditSplit) error {
	id := request.ReleaseID
	fp := operations.Fingerprint(split)
	spec, err := w.sourceSpec(ctx, operations.TypeExternalLink, id, fp, request.Source, request.BatchID, request.Key, "", "", "")
	if err != nil {
		return err
	}
	_, err = operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, op string) (operations.Outcome, error) {
		var empty operations.Outcome
		release, err := governance.LockAutoRelease(ctx, tx, id)
		if err != nil {
			return empty, err
		}
		if err := checkWrite(ctx, tx, spec, "", id, 0, release.EditRevision()); err != nil {
			return empty, err
		}
		basisObject := id
		if split.TrackID != "" {
			basisObject = split.TrackID
		}
		var basis string
		err = tx.QueryRowContext(ctx, `SELECT l.id FROM external_links l WHERE l.source='musicbrainz' AND l.object_type=$1 AND l.object_id=$2 AND l.external_id=$3
    AND NOT EXISTS(SELECT FROM release_match_credit_parts p WHERE p.release_id=$4 AND p.role=$5 AND p.track_id IS NOT DISTINCT FROM NULLIF($6,'')::uuid)
    AND NOT EXISTS(SELECT FROM curation_suppressions WHERE target_type='release' AND target_id=$4 AND subject='split_credit' AND value_hash=$7)`, split.BasisType, basisObject, split.BasisID, id, split.Role, split.TrackID, fp).Scan(&basis)
		if errors.Is(err, sql.ErrNoRows) {
			return empty, operations.Conflict("nothing_to_apply", "署名没有新的拆分依据")
		}
		if err != nil {
			return empty, err
		}
		for i, c := range split.Credits {
			if strings.TrimSpace(c.Name) == "" || !validExternalID(c.ArtistID) || MusicBrainzSpecialArtist(c.ArtistID) {
				return empty, operations.Conflict("nothing_to_apply", "署名没有可识别的艺人")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO release_match_credit_parts(release_id,track_id,role,position,local_text,name,artist_mbid,basis_link_id,operation_id)
       VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8,$9)`, id, split.TrackID, split.Role, i+1, split.LocalText, c.Name, c.ArtistID, basis, op); err != nil {
				return empty, err
			}
		}
		if err = catalog.RefreshMatchCredits(ctx, tx, id); err != nil {
			return empty, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT p.position,c.artist_id,p.artist_mbid FROM release_match_credit_parts p
    JOIN artist_effective_credits c ON c.role=p.role AND c.position=p.position AND
      (c.track_id=p.track_id OR p.track_id IS NULL AND c.release_id=p.release_id)
    WHERE p.operation_id=$1 ORDER BY p.position`, op)
		if err != nil {
			return empty, err
		}
		var links []LinkIntent
		for rows.Next() {
			var pos int
			l := LinkIntent{ObjectType: "artist"}
			if err := rows.Scan(&pos, &l.ObjectID, &l.ExternalID); err != nil {
				rows.Close()
				return empty, err
			}
			links = append(links, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return empty, err
		}
		if len(links) != len(split.Credits) {
			return empty, operations.Conflict("nothing_to_apply", "署名已变化")
		}
		for i, l := range links {
			skip, err := addUnoccupiedLink(ctx, tx, Link{Source: "musicbrainz", ObjectType: "artist", ObjectID: l.ObjectID, ExternalID: l.ExternalID, Method: "split_credit", Confidence: "certain", OperationID: op})
			if err != nil {
				return empty, err
			}
			if skip == "suppressed" || skip == "occupied" {
				return empty, operations.Conflict("nothing_to_apply", "署名对应已人工决定")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE release_match_credit_parts p SET artist_link_id=l.id FROM external_links l WHERE p.operation_id=$1 AND p.position=$2
    AND l.object_type='artist' AND l.source='musicbrainz' AND l.object_id=$3 AND l.external_id=$4`, op, i+1, l.ObjectID, l.ExternalID); err != nil {
				return empty, err
			}
		}
		artistIDs := make([]string, len(links))
		for i, l := range links {
			artistIDs[i] = l.ObjectID
		}
		if err := catalog.RefreshMergedArtistProfiles(ctx, tx, artistIDs); err != nil {
			return empty, err
		}
		return externalOutcome(ctx, release, map[string]any{}, linkState{Source: "musicbrainz", Method: "split_credit", Confidence: "certain", Split: &split, Links: links})
	})
	return err
}
