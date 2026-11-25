package catalog

import "context"

// PersonalTargetKind 将客户端给出的稳定身份映射到个人关系的目标类型，未知身份返回空串。
func (s *Store) PersonalTargetKind(ctx context.Context, id string) (string, error) {
	var kind string
	err := s.database.QueryRowContext(ctx, `SELECT CASE
		WHEN EXISTS (SELECT FROM tracks WHERE id=$1::uuid) THEN 'track'
		WHEN EXISTS (SELECT FROM release_groups WHERE id=$1::uuid) THEN 'album'
		WHEN EXISTS (SELECT FROM artists WHERE id=$1::uuid) THEN 'artist'
		ELSE '' END`, id).Scan(&kind)
	return kind, err
}
