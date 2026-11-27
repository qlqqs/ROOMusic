package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type groupPlacement struct {
	Path     string `json:"path"`
	File     string `json:"file"`
	Album    string `json:"album"`
	Disc     int    `json:"disc"`
	Track    int    `json:"track"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Root     string `json:"root_id"`
	CueStart *int   `json:"cue_start,omitempty"`
	CueEnd   *int   `json:"cue_end,omitempty"`
}

func (a localAlbum) checkGrouping(input *LocalGroup) []groupPlacement {
	rows, _ := a.checkedGrouping(input, nil)
	return rows
}

func (a localAlbum) checkedGrouping(input *LocalGroup, pathsByID map[string]string) ([]groupPlacement, error) {
	if !a.Group || input == nil {
		return nil, operations.Invalid("invalid_grouping", "缺少归组方案")
	}
	files := map[string][]localTrack{}
	for _, r := range a.Releases {
		if !r.Protected {
			for _, t := range r.Tracks {
				files[t.FileID] = append(files[t.FileID], t)
			}
		}
	}
	seen := map[string]bool{}
	var result []groupPlacement
	for index, album := range input.Albums {
		if len(album.Files) == 0 {
			return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("第 %d 张专辑缺少文件", index+1))
		}
		var paths []string
		for _, f := range album.Files {
			paths = append(paths, f.FileID)
		}
		slices.Sort(paths)
		key := operations.Fingerprint(paths)
		used := map[[2]int]string{}
		for _, f := range album.Files {
			tracks := files[f.FileID]
			if len(tracks) == 0 {
				return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 不在该目录的连通范围内", pathsByID[f.FileID]))
			}
			file := tracks[0].Path
			if seen[f.FileID] {
				return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 重复", file))
			}
			seen[f.FileID] = true
			slots := f.Tracks
			if len(slots) == 0 && len(tracks) == 1 {
				p := f.LocalPosition
				if p.TrackID != "" && p.TrackID != tracks[0].ID {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 的曲目 ID 不匹配", file))
				}
				p.TrackID = tracks[0].ID
				slots = []LocalPosition{p}
			} else if f.Disc != 0 || f.Track != 0 {
				return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 同时指定了文件和分轨位次", file))
			}
			if len(slots) != len(tracks) {
				return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 需要 %d 个分轨位次", file, len(tracks)))
			}
			for _, t := range tracks {
				i := slices.IndexFunc(slots, func(p LocalPosition) bool { return p.TrackID == t.ID })
				if i < 0 {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 缺少曲目 %s 的位次", file, t.ID))
				}
				p := slots[i]
				pos := [2]int{p.Disc, p.Track}
				if p.Disc < 1 || p.Track < 1 {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 的碟号和曲号必须为正整数", file))
				}
				if prior, ok := used[pos]; ok {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("第 %d 张专辑的碟 %d 曲 %d 重复：%q、%q", index+1, p.Disc, p.Track, prior, t.SourcePath))
				}
				used[pos] = t.SourcePath
				if t.Track > 0 && t.Observations["track_number"].Source != "model_inferred" && t.Track != p.Track {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 的真实曲号为 %d，与提交的 %d 冲突", t.SourcePath, t.Track, p.Track))
				}
				disc := t.Observations["disc_number"]
				if disc.Value != "" && disc.Source != "model_inferred" && disc.Source != "default_fallback" && t.Disc != p.Disc {
					return nil, operations.Invalid("invalid_grouping", fmt.Sprintf("文件 %q 的真实碟号为 %d，与提交的 %d 冲突", t.SourcePath, t.Disc, p.Disc))
				}
				result = append(result, groupPlacement{t.SourcePath, t.Path, key, p.Disc, p.Track, t.Size, t.Modified, t.Root, t.CueStart, t.CueEnd})
			}
		}
	}
	var missing []string
	for id, tracks := range files {
		if !seen[id] {
			missing = append(missing, tracks[0].Path)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, operations.Invalid("invalid_grouping", "遗漏文件："+strings.Join(missing, "、"))
	}
	if len(result) == 0 {
		return nil, operations.Invalid("invalid_grouping", "归组中没有文件")
	}
	slices.SortFunc(result, func(a, b groupPlacement) int { return strings.Compare(a.Path, b.Path) })
	return result, nil
}

const groupKind = "local_model_grouping"

type groupingState struct {
	Root       string           `json:"root_id"`
	Key        string           `json:"directory_key"`
	Digest     string           `json:"source_digest"`
	Owner      string           `json:"operation_id"`
	Dirs       []string         `json:"directories"`
	Rows       []groupPlacement `json:"assignments"`
	OtherPaths []string         `json:"other_paths"`
}

func groupFingerprint(key string, rows []groupPlacement) string {
	positions := make([]any, 0, len(rows))
	for _, r := range rows {
		positions = append(positions, []any{r.Path, r.Album, r.Disc, r.Track})
	}
	return operations.Fingerprint([]any{key, positions})
}
func storeGroup(ctx context.Context, tx *sql.Tx, g groupingState) error {
	raw, err := json.Marshal(g.Rows)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO local_model_groupings(root_id,directory_key,source_digest,operation_id,directories,assignments,other_paths) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(root_id,directory_key) DO UPDATE SET source_digest=excluded.source_digest,operation_id=excluded.operation_id,directories=excluded.directories,assignments=excluded.assignments,other_paths=excluded.other_paths`, g.Root, g.Key, g.Digest, g.Owner, g.Dirs, raw, g.OtherPaths)
	return err
}
func requestGroupScan(ctx context.Context, tx *sql.Tx, g groupingState, immediate bool) error {
	var paths []string
	for _, p := range g.Rows {
		paths = append(paths, p.File)
	}
	_, err := tx.ExecContext(ctx, `UPDATE releases SET curation_confirmed=false,curation_changed_at=CASE WHEN $3 THEN now()-interval '60 seconds' ELSE now() END WHERE id IN(SELECT release_id FROM curation_scan_files WHERE root_id=$1 AND path=ANY($2::text[]))`, g.Root, paths, immediate)
	return err
}
func (w *Writer) writeLocalGrouping(ctx context.Context, s localSubmission, a localAlbum, plan localPlan, batch, conversation string) error {
	source := Source{Kind: "model", Provider: plan.Provider, Basis: "依据本地文件推断归组与曲序"}
	hash := groupFingerprint(a.DirectoryKey, plan.Grouping)
	spec, err := w.sourceSpec(ctx, groupKind, s.item.TargetID, hash, source, batch, DerivedKey(s.item.ID, "grouping"), "", "", conversation)
	if err != nil {
		return err
	}
	spec.Origin.Mode = "local_cleanup"
	_, err = operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		var out operations.Outcome
		lock, err := governance.LockAutoRelease(ctx, tx, s.item.TargetID)
		if err != nil {
			return out, err
		}
		if err = checkWrite(ctx, tx, spec, "", s.item.TargetID, 0, 0); err != nil {
			return out, err
		}
		if _, err = s.read(ctx, tx); err != nil {
			return out, err
		}
		root := plan.Grouping[0].Root
		blocked, err := suppressionExists(ctx, tx, "library_root", root, groupKind, hash)
		if err != nil {
			return out, err
		}
		if blocked {
			return out, operations.Conflict("nothing_to_apply", "已撤销此归组")
		}
		out, err = saveLocalGrouping(ctx, tx, lock, id, a, plan.Grouping, false)

		if err == nil {
			err = finishModelItem(ctx, tx, s.item, conversation, "local_cleanup")
		}
		return out, err
	})
	var fail *operations.Failure
	if errors.As(err, &fail) && fail.Code == "nothing_to_apply" {
		return w.FinishModelItem(ctx, s.item, conversation, "local_cleanup")
	}
	return err
}

func saveLocalGrouping(ctx context.Context, tx *sql.Tx, lock *governance.LockedAutoRelease, id string, a localAlbum, rows []groupPlacement, immediate bool) (operations.Outcome, error) {
	root := rows[0].Root
	var out operations.Outcome
	var before groupingState
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT to_jsonb(g) FROM local_model_groupings g WHERE root_id=$1 AND directory_key=$2 FOR UPDATE`, root, a.DirectoryKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &before); err != nil {
			return out, err
		}
	}
	other := []string{}
	for _, release := range a.Releases {
		if release.Protected {
			for _, t := range release.Tracks {
				other = append(other, t.SourcePath, t.Path)
			}
		}
	}
	after := groupingState{root, a.DirectoryKey, a.fileDigest(), id, a.Directories, rows, other}
	if err = storeGroup(ctx, tx, after); err != nil {
		return out, err
	}
	if err = requestGroupScan(ctx, tx, after, immediate); err != nil {
		return out, err
	}
	out.TargetType, out.TargetID, out.Before, out.After, out.Response = "release", lock.ID(), before, after, map[string]string{"operation_id": id}
	out.ExpectedRevision, out.ResultRevision, err = lock.Bump(ctx)
	return out, err
}

type LocalGroupingOperations struct{ Writer *Writer }

func (LocalGroupingOperations) Type() string { return groupKind }
func (k LocalGroupingOperations) Revert(ctx context.Context, tx *sql.Tx, id string, r operations.Record) (operations.Outcome, error) {
	var out operations.Outcome
	var before, after groupingState
	if err := json.Unmarshal(r.Before, &before); err != nil {
		return out, err
	}
	if err := json.Unmarshal(r.After, &after); err != nil {
		return out, err
	}
	lock, err := governance.LockAutoReleaseForRevert(ctx, tx, r.TargetID)
	if err != nil {
		return out, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM local_model_groupings WHERE operation_id=$1`, r.ID)
	if err != nil {
		return out, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, operations.Conflict("revert_conflict", "归组的写入者已变化")
	}
	if before.Owner != "" {
		if err = storeGroup(ctx, tx, before); err != nil {
			return out, err
		}
	}
	var origin operations.Origin
	if len(r.Origin) > 0 {
		if err := json.Unmarshal(r.Origin, &origin); err != nil {
			return out, err
		}
	}
	immediate := origin.Source == "chat" || origin.Source == "mcp"
	if immediate {
		out.AfterCommit = k.Writer.groupingConfirmation(ctx)
	}
	if err = requestGroupScan(ctx, tx, after, immediate); err != nil {
		return out, err
	}
	if err = insertSuppression(ctx, tx, "library_root", after.Root, groupKind, groupFingerprint(after.Key, after.Rows), after.Rows, id, operations.ActorID(ctx)); err != nil {
		return out, err
	}
	out.TargetType, out.TargetID, out.Before, out.After = "release", r.TargetID, after, before
	out.ExpectedRevision, out.ResultRevision, err = lock.Bump(ctx)
	out.ResponseRevision = lock.EditRevision()
	return out, err
}
func (LocalGroupingOperations) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	ids := make([]string, 0, len(records))
	var owners []string
	for _, r := range records {
		ids = append(ids, r.TargetID)
		owners = append(owners, r.ID)
	}
	infos, err := governance.LoadOperationReleases(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT operation_id::text FROM local_model_groupings WHERE operation_id=ANY($1::uuid[])`, owners)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		active[id] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]operations.Description{}
	for _, r := range records {
		var before, after groupingState
		if err = json.Unmarshal(r.Before, &before); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(r.After, &after); err != nil {
			return nil, err
		}
		desc := operations.Description{Changes: map[string]any{"kind": groupKind, "previous": before.Rows, "assignments": after.Rows}, Revertible: active[r.ID]}
		if info, ok := infos[r.TargetID]; ok {
			desc.Release = &info
		}
		out[r.ID] = desc
	}
	return out, nil
}
func (LocalGroupingOperations) PreviewRevert(ctx context.Context, q operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	var before, after groupingState
	if err := json.Unmarshal(r.Before, &before); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(r.After, &after); err != nil {
		return nil, err
	}
	return []operations.PreviewChange{{Field: "grouping", Before: after.Rows, After: before.Rows}}, nil
}
