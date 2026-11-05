package ingest

import (
	"context"
	"database/sql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func artistPreviousReleases(ctx context.Context, tx *sql.Tx, rootID string, candidate OrganizedCandidate, plans map[string]trackPlan) ([]string, error) {
	ids, identities := []string{}, []string{}
	for _, p := range plans {
		if p.TrackID != "" {
			ids = append(ids, p.TrackID)
		}
	}
	for _, track := range candidate.orderedTracks() {
		identities = append(identities, SourceIdentityForObservation(rootID, track.Observation))
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.id=ANY($1::uuid[]) OR t.source_identity=ANY($2::text[])`, ids, identities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func persistArtistEvidence(ctx context.Context, tx *sql.Tx, trackID string, evidence []metadata.ArtistTagEvidence) error {
	if err := metadata.ValidateArtistTagEvidence(evidence); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_tag_evidence WHERE track_id=$1::uuid`, trackID); err != nil {
		return err
	}
	for _, e := range evidence {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind)
 VALUES($1::uuid,$2,$3,$4,$5,$6)`, trackID, e.Role, e.TagKey, e.OccurrenceIndex, e.RawValue, e.SourceKind); err != nil {
			return err
		}
	}
	return nil
}
func refreshMissingArtists(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	releases := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		releases = append(releases, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return artistgraph.Refresh(ctx, tx, releases)
}
