package catalog

import "context"

type MatchName struct {
	TargetType       string `json:"target_type"`
	TargetID         string `json:"target_id"`
	Field            string `json:"field"`
	LocalValue       string `json:"local_value"`
	EffectiveValue   string `json:"effective_value"`
	MusicBrainzValue string `json:"musicbrainz_value"`
	BasisOperationID string `json:"basis_operation_id"`
	ExternalID       string `json:"external_id"`
	Revision         int64  `json:"revision"`
	CanAdopt         bool   `json:"can_adopt"`
}

func (s *Store) MatchNames(ctx context.Context, release string) ([]MatchName, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT n.target_type,n.target_id,n.field,
 coalesce(CASE WHEN n.target_type='release' THEN CASE n.field WHEN 'title' THEN r.title ELSE coalesce(nullif(r.album_artist,''),r.artist) END
 ELSE CASE n.field WHEN 'title' THEN t.title ELSE t.artist END END,''),
 coalesce(CASE WHEN n.target_type='release' THEN CASE n.field WHEN 'title' THEN re.title ELSE coalesce(nullif(re.album_artist,''),re.artist) END
 ELSE CASE n.field WHEN 'title' THEN te.title ELSE te.artist END END,''),n.mb_value,n.basis_operation_id,n.external_id,
 CASE n.target_type WHEN 'release' THEN re.edit_revision ELSE te.edit_revision END
 FROM curation_match_names n JOIN external_links l ON l.id=n.basis_link_id AND l.operation_id=n.basis_operation_id AND l.external_id=n.external_id
 JOIN releases r ON r.id=n.release_id JOIN release_effective re ON re.id=r.id
 LEFT JOIN tracks t ON n.target_type='track' AND t.id=n.target_id LEFT JOIN media d ON d.id=t.medium_id AND d.release_id=r.id LEFT JOIN track_effective te ON te.id=t.id
 WHERE n.release_id=$1 AND (n.target_type='release' OR d.id IS NOT NULL)
 ORDER BY (n.target_type='track'),d.position,t.position,n.field`, release)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MatchName{}
	for rows.Next() {
		var n MatchName
		if err := rows.Scan(&n.TargetType, &n.TargetID, &n.Field, &n.LocalValue, &n.EffectiveValue, &n.MusicBrainzValue, &n.BasisOperationID, &n.ExternalID, &n.Revision); err != nil {
			return nil, err
		}
		n.CanAdopt = n.EffectiveValue != n.MusicBrainzValue
		result = append(result, n)
	}
	return result, rows.Err()
}
