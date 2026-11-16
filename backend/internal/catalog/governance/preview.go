package governance

import (
	"context"
	"encoding/json"
)

// ArtistNamingPreview 返回修改主名时的有效名称和自动名称，只读取状态。
func (s *Store) ArtistNamingPreview(ctx context.Context, id string) (current, automatic string, err error) {
	err = s.database.QueryRowContext(ctx, `SELECT COALESCE(p.effective_display_name,a.initial_name),COALESCE(p.auto_display_name,a.initial_name) FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id WHERE a.id=$1::uuid`, id).Scan(&current, &automatic)
	return
}

// CandidateIgnorePreview 按规范化名字键对返回当前忽略状态。
func (s *Store) CandidateIgnorePreview(ctx context.Context, pairs []ArtistKeyPair) ([]bool, error) {
	raw, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	rows, err := s.database.QueryContext(ctx, `SELECT i.operation_id IS NOT NULL FROM jsonb_array_elements($1::jsonb) WITH ORDINALITY AS p(value,position) LEFT JOIN artist_candidate_ignores i ON i.left_key_id=(p.value->>'left_key_id')::uuid AND i.right_key_id=(p.value->>'right_key_id')::uuid ORDER BY p.position`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []bool{}
	for rows.Next() {
		var value bool
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
