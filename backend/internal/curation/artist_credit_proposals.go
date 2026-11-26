package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type ProposalLocalCredit struct {
	ArtistID string `json:"artist_id"`
	Role     string `json:"role"`
	TrackID  string `json:"track_id,omitempty"`
	Position int    `json:"position"`
	Name     string `json:"name"`
}

type ProposalExternalCredit struct {
	Country        string   `json:"country,omitempty"`
	Disambiguation string   `json:"disambiguation,omitempty"`
	Provider       string   `json:"provider"`
	ArtistID       string   `json:"artist_id"`
	CreditName     string   `json:"credit_name"`
	PrimaryName    string   `json:"primary_name"`
	Aliases        []string `json:"aliases"`
}

type ProposalCreditBasis struct {
	Level       string                       `json:"level"`
	ExternalID  string                       `json:"external_id"`
	OperationID string                       `json:"operation_id"`
	Rule        string                       `json:"rule"`
	Roles       []catalog.CurationCreditRole `json:"role_evidence"`
}

type ArtistCreditProposal struct {
	PairLow        string                 `json:"pair_low,omitempty"`
	PairHigh       string                 `json:"pair_high,omitempty"`
	Kind           string                 `json:"kind"`
	ID             string                 `json:"proposal_id"`
	ReleaseID      string                 `json:"release_id"`
	IdentityDigest string                 `json:"identity_digest"`
	Local          ProposalLocalCredit    `json:"local"`
	External       ProposalExternalCredit `json:"external"`
	Basis          ProposalCreditBasis    `json:"basis"`
}

// 逐项核对当前外部对应，不能用同一操作写下的另一项对应代替来源。
const proposalSourceExists = `EXISTS (
 SELECT FROM external_links l WHERE l.operation_id=p.operation_id AND l.source='musicbrainz'
 AND l.external_id=p.evidence->'basis'->>'external_id'
 AND (l.object_type,l.object_id)=(CASE p.evidence->'basis'->>'level'
     WHEN 'release_group' THEN 'release_group_match' WHEN 'recording' THEN 'track' ELSE 'release' END,
     CASE p.evidence->'basis'->>'level' WHEN 'recording' THEN (p.evidence->'local'->>'track_id')::uuid ELSE p.release_id END)
 AND NOT EXISTS(SELECT FROM operations undo WHERE undo.reverts_operation_id=p.operation_id))`

// OpenArtistCreditProposals 按本次识别摘要读取仍然适用的待核实署名。
func (w *Writer) OpenArtistCreditProposals(ctx context.Context, releaseID, identityDigest string) ([]ArtistCreditProposal, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT p.evidence FROM curation_artist_credit_proposals p
 JOIN curation_work_states s ON s.subject='release' AND s.target_id=p.release_id AND s.identity_digest=p.identity_digest
 WHERE p.release_id=$1 AND p.identity_digest=$2 AND p.status='open' AND `+proposalSourceExists+` ORDER BY p.id`, releaseID, identityDigest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var proposals []ArtistCreditProposal
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p ArtistCreditProposal
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	return proposals, rows.Err()
}

// SaveArtistCreditProposals 在识别结论事务内重新核对来源操作及当前署名。
func SaveArtistCreditProposals(ctx context.Context, tx *sql.Tx, releaseID, digest string, proposals []ArtistCreditProposal) error {
	_, err := tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals p SET status='stale'
 WHERE release_id=$1 AND status='open' AND (identity_digest<>$2 OR NOT `+proposalSourceExists+`)`, releaseID, digest)
	if err != nil || len(proposals) == 0 {
		return err
	}
	credits, err := catalog.LoadCurationArtistCredits(ctx, tx, releaseID)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		group := credits.Tracks[p.Local.TrackID]
		switch p.Local.Role {
		case "release_artist":
			group = credits.ReleaseArtists
		case "album_artist":
			group = credits.AlbumArtists
		}
		pos := p.Local.Position
		if pos < 1 || pos > len(group) || group[pos-1].ArtistID != p.Local.ArtistID || group[pos-1].Name != p.Local.Name {
			continue
		}
		subject, objectID := "release", releaseID
		switch p.Basis.Level {
		case "release_group":
			subject = "release_group_match"
		case "recording":
			subject, objectID = "track", p.Local.TrackID
		}
		err := tx.QueryRowContext(ctx, `SELECT l.operation_id::text FROM external_links l JOIN operations o ON o.id=l.operation_id AND o.operation_type='external_link'
   WHERE l.source='musicbrainz' AND l.object_type=$1 AND l.object_id=$2 AND l.external_id=$3 AND NOT EXISTS(SELECT FROM operations r WHERE r.reverts_operation_id=o.id)
   FOR SHARE OF l`, subject, objectID, p.Basis.ExternalID).Scan(&p.Basis.OperationID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		p.Kind, p.ReleaseID, p.IdentityDigest = "artist_credit_identity", releaseID, digest
		p.ID = DerivedKey(releaseID, strings.Join([]string{p.Kind, p.Local.TrackID, p.Local.Role, strconv.Itoa(pos), p.Local.ArtistID, p.External.ArtistID, digest}, "/"))
		p.PairLow, p.PairHigh = namePair(p.Local.Name, p.External.CreditName)
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO curation_artist_credit_proposals(id,release_id,identity_digest,operation_id,evidence) SELECT $1,$2,$3,$4,$5::jsonb WHERE NOT EXISTS(SELECT FROM curation_artist_negative_pairs WHERE name_low=$5::jsonb->>'pair_low' AND name_high=$5::jsonb->>'pair_high')
  ON CONFLICT(id) DO UPDATE SET operation_id=excluded.operation_id,evidence=excluded.evidence,status='open'
  WHERE curation_artist_credit_proposals.status IN ('stale','open')`, p.ID, releaseID, digest, p.Basis.OperationID, raw)
		if err != nil {
			return err
		}
	}
	return nil
}
