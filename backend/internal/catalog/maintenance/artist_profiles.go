package maintenance

import (
	"context"
	"database/sql"
)

const artistProfileGapPredicate = `NOT EXISTS (SELECT 1 FROM artist_profiles p
 WHERE p.artist_id=a.id AND p.applied_generation=$1)`

// 只发现身份，不生成派生；与发行清单共用后台一致快照，不取得 CATP。
// 已封闭的旧 build 也须幂等补此阶段，不能把 manifest_sealed 当作已具备新阶段的证明。
func discoverArtistProfiles(ctx context.Context, tx *sql.Tx, state artistState, phase string, repair bool) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_projection_phases
 WHERE build_id=$1::uuid AND phase=$2)`, state.BuildID, phase).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	predicate := "$1::bigint > 0"
	if repair {
		predicate = artistProfileGapPredicate
	}
	_, err := tx.ExecContext(ctx, `WITH inserted AS (
 INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
 SELECT $2::uuid,$3,'artist',a.id FROM artists a WHERE `+predicate+` RETURNING subject_id)
 INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total)
 SELECT $2::uuid,$3,'artist',count(*) FROM inserted`, state.TargetGeneration, state.BuildID, phase)
	return err
}
