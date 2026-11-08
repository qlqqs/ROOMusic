package ingest

import "context"

// 本轮读取失败、已有曲目需要原样保留的来源范围。
const (
	HeldFile      = "file"
	HeldDirectory = "directory"
	HeldCueSheet  = "cue_sheet"
)

// heldSourceMatches 同时用于身份配对排除与观察时间更新，t 为 tracks 别名。
const heldSourceMatches = `EXISTS (SELECT 1 FROM scan_held_sources h
 WHERE h.scan_run_id=$1::uuid AND h.root_id=$2::uuid AND (
 (h.kind='file' AND t.relative_path=h.relative_path) OR
 (h.kind='directory' AND left(t.relative_path,length(h.relative_path)+1)=h.relative_path||'/') OR
 (h.kind='cue_sheet' AND t.cue_sheet_path=h.relative_path)))`

// HoldSource 登记本轮保住的来源；重复登记不新增行。
func HoldSource(ctx context.Context, db Execer, scanID, rootID, kind, relativePath string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO scan_held_sources(scan_run_id,root_id,kind,relative_path)
 VALUES($1::uuid,$2::uuid,$3,$4) ON CONFLICT DO NOTHING`, scanID, rootID, kind, relativePath)
	return err
}

// TouchHeldSources 只更新 present 曲目的观察时间，不改变元数据或恢复 missing 曲目。
func TouchHeldSources(ctx context.Context, db Execer, scanID, rootID string) (int64, error) {
	result, err := db.ExecContext(ctx, `UPDATE tracks t SET observed_at=NOW()
 WHERE t.source_root_id=$2::uuid AND t.source_status='present' AND `+heldSourceMatches, scanID, rootID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RootHasPresentTracks 为扫描空根保护查询既有来源。
func RootHasPresentTracks(ctx context.Context, db queryer, rootID string) (bool, error) {
	var present bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tracks WHERE source_root_id=$1::uuid AND source_status='present')`, rootID).Scan(&present)
	return present, err
}
