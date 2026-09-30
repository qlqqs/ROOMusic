package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// TxBeginner 是写入 Release 图所需的最小数据库能力：每个候选使用独立短事务。
type TxBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Execer 是在调用方已持有的事务里执行 SQL 所需的最小能力。
type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// PersistCandidate 用一个独立短事务把整理后的候选写入 Release 图：Release 与其分组、Medium、Track、
// 逐字段观察、署名、字段决定与分组证据。Release 按 candidate_anchor 查找并更新；每条 Track 沿用还是新建，
// 由 PlanStagedRoot 写在暂存行上的规划决定，因此调用前必须已完成规划。成功时返回 Release ID；封面不在本事务内，
// 由调用方在提交后处理。
func PersistCandidate(ctx context.Context, database TxBeginner, scanID, rootID string, candidate OrganizedCandidate) (releaseID string, err error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	plans, err := loadTrackPlans(ctx, tx, scanID, rootID, candidate)
	if err != nil {
		return "", err
	}
	anchor := CandidateIdentity(rootID, candidate.Anchor)
	var groupID string
	err = tx.QueryRowContext(ctx, "SELECT id::text,group_id::text FROM releases WHERE source_root_id=$1::uuid AND candidate_anchor=$2", rootID, anchor).Scan(&releaseID, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		groupID, releaseID = uuid.New(), uuid.New()
		if _, err = tx.ExecContext(ctx, "INSERT INTO release_groups(id) VALUES($1::uuid)", groupID); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind,album_artist,year,source_type,media_type,genre,catalog_number,edition,label,barcode)
				VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, releaseID, groupID, candidate.Title, candidate.Artist, rootID, candidate.Anchor.Scope, anchor, string(candidate.Anchor.Kind), candidate.AlbumArtist, releaseYear(candidate), nullableString(candidate.SourceType), nullableString(candidate.MediaType), nullableString(candidate.Genre), nullableString(candidate.CatalogNumber), nullableString(candidate.Edition), nullableString(candidate.Label), nullableString(candidate.Barcode))
		}
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE releases SET title=$1,artist=$2,relative_directory=$3,candidate_kind=$4,album_artist=$5,year=$6,source_type=$7,media_type=$8,genre=$9,catalog_number=$10,edition=$11,label=$12,barcode=$13 WHERE id=$14::uuid`, candidate.Title, candidate.Artist, candidate.Anchor.Scope, string(candidate.Anchor.Kind), candidate.AlbumArtist, releaseYear(candidate), nullableString(candidate.SourceType), nullableString(candidate.MediaType), nullableString(candidate.Genre), nullableString(candidate.CatalogNumber), nullableString(candidate.Edition), nullableString(candidate.Label), nullableString(candidate.Barcode), releaseID)
	}
	if err != nil {
		return "", err
	}

	discs := make([]int, 0, len(candidate.Mediums))
	for disc := range candidate.Mediums {
		discs = append(discs, disc)
	}
	sort.Ints(discs)
	for _, disc := range discs {
		var mediumID string
		err = tx.QueryRowContext(ctx, "SELECT id::text FROM media WHERE release_id=$1::uuid AND position=$2", releaseID, disc).Scan(&mediumID)
		if errors.Is(err, sql.ErrNoRows) {
			mediumID = uuid.New()
			_, err = tx.ExecContext(ctx, "INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,$3)", mediumID, releaseID, disc)
		}
		if err != nil {
			return "", err
		}
		for _, placedTrack := range candidate.Mediums[disc] {
			observation := placedTrack.Observation
			observation.RelativePath = NormalizedPath(observation.RelativePath)
			plan, planned := plans[observation.RelativePath]
			if !planned {
				return "", errUnplannedObservation
			}
			var trackID string
			if trackID, err = persistTrack(ctx, tx, scanID, rootID, mediumID, disc, placedTrack, observation, plan); err != nil {
				return "", err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM track_observations WHERE track_id=$1::uuid", trackID); err != nil {
				return "", err
			}
			for _, field := range observation.fieldObservations() {
				if _, err = tx.ExecContext(ctx, "INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,NOW())", trackID, scanID, field.Name, field.Value, field.SourceKind, field.Inferred); err != nil {
					return "", err
				}
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM track_credits WHERE track_id=$1::uuid", trackID); err != nil {
				return "", err
			}
			for position, credit := range metadata.CanonicalCredits(observation.Credits) {
				if _, err = tx.ExecContext(ctx, "INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,$4)", trackID, credit.Role, credit.Name, position+1); err != nil {
					return "", err
				}
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM media WHERE release_id=$1::uuid AND NOT EXISTS (
		SELECT 1 FROM tracks WHERE tracks.medium_id=media.id)`, releaseID); err != nil {
		return "", err
	}

	if _, err = tx.ExecContext(ctx, "DELETE FROM release_field_decisions WHERE release_id=$1::uuid", releaseID); err != nil {
		return "", err
	}
	for _, decision := range candidate.Decisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason,scan_run_id)
			VALUES($1::uuid,$2,to_jsonb($3::text),$4,$5,$6,$7,to_jsonb($8::text[]),$9,$10::uuid)`, releaseID, decision.Field, decision.Value, decision.Source, decision.Confidence, decision.Action, decision.RuleID, decision.Candidates, decision.Reason, scanID); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM release_credits WHERE release_id=$1::uuid", releaseID); err != nil {
		return "", err
	}
	if candidate.AlbumArtist != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO release_credits(release_id,role,name,position) VALUES($1::uuid,'album_artist',$2,1)", releaseID, candidate.AlbumArtist); err != nil {
			return "", err
		}
	}
	refs := append([]string(nil), candidate.GroupingRefs...)
	if len(refs) == 0 {
		for _, disc := range discs {
			for _, track := range candidate.Mediums[disc] {
				refs = append(refs, NormalizedPath(track.Observation.RelativePath))
			}
		}
		sort.Strings(refs)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason,scan_run_id)
		VALUES($1::uuid,$2,$3,to_jsonb($4::text[]),$5,$6::uuid)
		ON CONFLICT(release_id) DO UPDATE SET candidate_kind=EXCLUDED.candidate_kind,rule_id=EXCLUDED.rule_id,source_refs=EXCLUDED.source_refs,reason=EXCLUDED.reason,scan_run_id=EXCLUDED.scan_run_id,observed_at=NOW()`, releaseID, candidate.Anchor.Kind, "organizer_v2", refs, strings.Join(candidate.Attention, ";"), scanID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	committed = true
	return releaseID, nil
}

// ArtworkObservation 返回提供封面的来源：优先取带内嵌封面的曲目，否则取按碟序排列的第一首曲目
// （调用方再回退到该曲目所在文件夹的封面）。没有任何曲目时 ok 为 false。
func (candidate OrganizedCandidate) ArtworkObservation() (SourceObservation, bool) {
	discs := make([]int, 0, len(candidate.Mediums))
	for disc := range candidate.Mediums {
		discs = append(discs, disc)
	}
	sort.Ints(discs)
	for _, disc := range discs {
		for _, track := range candidate.Mediums[disc] {
			observation := track.Observation
			if len(observation.Artwork) > 0 {
				return observation, true
			}
		}
	}
	for _, disc := range discs {
		if len(candidate.Mediums[disc]) > 0 {
			return candidate.Mediums[disc][0].Observation, true
		}
	}
	return SourceObservation{}, false
}

func releaseYear(candidate OrganizedCandidate) any {
	// Release 核心列必须与 organizer 已保存的字段决定一致，不能在持久化层把
	// inferred 年份重新计票并覆盖权威 tag/CUE 证据。
	for _, decision := range candidate.Decisions {
		if decision.Field != "year" {
			continue
		}
		year, err := strconv.Atoi(decision.Value)
		if err == nil && year > 0 {
			return year
		}
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableCueFrame(value int, present bool) any {
	if !present || value < 0 {
		return nil
	}
	return value
}

// MarkMissingSources 把本次扫描开始前最后一次观察到、此后未再出现的曲目标记为 missing。
// 只有以 succeeded 终态完成的扫描才允许调用：不完整的扫描不能做负向对账。
func MarkMissingSources(ctx context.Context, database Execer, scanID string, rootIDs []string) error {
	for _, rootID := range rootIDs {
		if _, err := database.ExecContext(ctx, "UPDATE tracks SET source_status='missing' WHERE source_root_id=$1::uuid AND observed_at < (SELECT started_at FROM scan_runs WHERE id=$2::uuid)", rootID, scanID); err != nil {
			return err
		}
	}
	return nil
}
