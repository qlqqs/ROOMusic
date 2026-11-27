package catalog

import (
	"context"
	"database/sql"
)

// LibraryFileOwner 是扫描后文件对应的发行和曲目位置。附件没有曲目位置。
type LibraryFileOwner struct {
	ReleaseID    string `json:"release_id"`
	ReleaseTitle string `json:"release_title"`
	DiscNumber   int    `json:"disc_number,omitempty"`
	TrackNumber  int    `json:"track_number,omitempty"`
}

// LoadLibraryFileOwners 批量读取同一登记根内已扫描的文件归属。CUE 与父音频都指向分轨。
func (s *Store) LoadLibraryFileOwners(ctx context.Context, rootID string, trackPaths, attachmentPaths []string) (map[string][]LibraryFileOwner, error) {
	result := make(map[string][]LibraryFileOwner)
	if len(trackPaths) == 0 && len(attachmentPaths) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, `
WITH owners AS (
    SELECT DISTINCT p.path, m.release_id, t.disc_number, t.position
    FROM tracks t
    JOIN media m ON m.id=t.medium_id
    CROSS JOIN LATERAL (VALUES (t.relative_path), (t.cue_sheet_path), (t.cue_parent_relative_path)) p(path)
    WHERE t.source_root_id=$1::uuid AND t.source_status='present'
      AND (t.relative_path=ANY($2::text[]) OR t.cue_sheet_path=ANY($2::text[]) OR t.cue_parent_relative_path=ANY($2::text[]))
      AND p.path=ANY($2::text[])
    UNION ALL
    SELECT a.relative_path, r.id, NULL::integer, NULL::integer
    FROM release_attachments a JOIN releases r ON r.id=a.release_id
    WHERE r.source_root_id=$1::uuid AND a.relative_path=ANY($3::text[])
)
SELECT o.path, r.id::text, r.title, o.disc_number, o.position
FROM owners o JOIN release_effective r ON r.id=o.release_id
ORDER BY o.path, r.id, o.disc_number, o.position`, rootID, trackPaths, attachmentPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var item LibraryFileOwner
		var disc, track sql.NullInt32
		if err := rows.Scan(&path, &item.ReleaseID, &item.ReleaseTitle, &disc, &track); err != nil {
			return nil, err
		}
		item.DiscNumber, item.TrackNumber = int(disc.Int32), int(track.Int32)
		result[path] = append(result[path], item)
	}
	return result, rows.Err()
}
