package curation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// StewardGrouping 的 directory 和文件路径均相对于曲库根；CUE 使用音频父文件路径。
type StewardGrouping struct {
	RootID    string              `json:"root_id"`
	Directory string              `json:"directory"`
	Albums    []StewardGroupAlbum `json:"albums"`
}
type StewardGroupAlbum struct {
	Files []StewardGroupFile `json:"files"`
}
type StewardGroupFile struct {
	Path   string          `json:"path"`
	Disc   int             `json:"disc,omitempty"`
	Track  int             `json:"track,omitempty"`
	Tracks []LocalPosition `json:"tracks,omitempty"`
}
type GroupingRelease struct {
	ID    string `json:"release_id"`
	Title string `json:"title"`
}

func (in *StewardGrouping) normalize() error {
	if !uuid.Valid(in.RootID) {
		return operations.Invalid("invalid_input", "根目录 ID 无效")
	}
	in.RootID = strings.ToLower(in.RootID)
	if in.Directory == "" {
		in.Directory = "."
	}
	if !fs.ValidPath(in.Directory) || strings.ContainsRune(in.Directory, 0) {
		return operations.Invalid("invalid_grouping", "目录必须是根内的相对路径")
	}
	return nil
}

// 只沿已连接的发行继续找同目录及兄弟目录，直至多目录发行也完全覆盖。
func (in StewardGrouping) scope(ctx context.Context, tx *sql.Tx) (localAlbum, error) {
	directory := in.Directory
	if directory == "." {
		directory = ""
	}
	rows, err := tx.QueryContext(ctx, `WITH audio AS (
 SELECT m.release_id, regexp_replace(COALESCE(t.cue_parent_relative_path,t.relative_path),'(^|/)[^/]*$','') AS dir
 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1 AND t.source_status='present'
 AND ($2='' OR starts_with(COALESCE(t.cue_parent_relative_path,t.relative_path),$2||'/')))
 SELECT DISTINCT release_id::text FROM audio WHERE dir=$2 OR ($2<>'' AND NOT EXISTS(SELECT FROM audio WHERE dir=$2))
 ORDER BY 1`, in.RootID, directory)
	if err != nil {
		return localAlbum{}, err
	}
	var seeds []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return localAlbum{}, err
		}
		seeds = append(seeds, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return localAlbum{}, err
	}
	if len(seeds) == 0 {
		return localAlbum{}, operations.NotFound("not_found", "目录中没有已扫描的音频文件")
	}
	for {
		scopes, err := loadLocalScopes(ctx, tx, seeds, nil, nil, true)
		if err != nil {
			return localAlbum{}, err
		}
		if len(scopes) != 1 {
			return localAlbum{}, operations.Invalid("invalid_grouping", "目录中的发行不属于一个可归组的连通范围")
		}
		a := scopes[0]
		for _, id := range seeds {
			if !slices.Contains(a.ReleaseIDs, id) {
				return localAlbum{}, operations.Invalid("invalid_grouping", "目录含有无法归组的发行 "+id)
			}
		}
		if slices.Equal(seeds, a.ReleaseIDs) {
			return a, nil
		}
		seeds = a.ReleaseIDs
	}
}

func (in StewardGrouping) plan(ctx context.Context, tx *sql.Tx) (localAlbum, []groupPlacement, error) {
	var running bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM scan_runs WHERE status='running')`).Scan(&running); err != nil {
		return localAlbum{}, nil, err
	}
	if running {
		return localAlbum{}, nil, operations.Conflict("scan_running", "曲库扫描进行中")
	}
	a, err := in.scope(ctx, tx)
	if err != nil {
		return a, nil, err
	}
	var stable bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT unnest($1::uuid[]) EXCEPT SELECT id FROM curation_stable_releases)`, a.ReleaseIDs).Scan(&stable); err != nil {
		return a, nil, err
	}
	if !stable {
		return a, nil, operations.Conflict("not_stable", "目录内的发行尚未完成稳定性确认")
	}
	var protected []string
	loose := map[string]bool{}
	for _, r := range a.Releases {
		if r.Protected {
			protected = append(protected, fmt.Sprintf("%s《%s》：%s", r.ID, r.displayTitle, strings.Join(r.protection, "、")))
		}
		for _, t := range r.Tracks {
			if t.Root != in.RootID {
				return a, nil, operations.Invalid("invalid_grouping", "连通范围跨越曲库根目录")
			}
		}
		if r.Kind == "loose_unknown" && len(r.Tracks) == 1 {
			loose[r.Tracks[0].Path] = true
		}
	}
	if len(protected) > 0 {
		return a, nil, operations.Conflict("protected", strings.Join(protected, "；")+"。请先用 revert_operation 撤销相应操作")
	}
	var group LocalGroup
	paths := map[string]string{}
	for _, album := range in.Albums {
		var files []LocalGroupFile
		for _, file := range album.Files {
			id := operations.Fingerprint([]string{in.RootID, file.Path})
			paths[id] = file.Path
			files = append(files, LocalGroupFile{FileID: id, LocalPosition: LocalPosition{Disc: file.Disc, Track: file.Track}, Tracks: file.Tracks})
		}
		group.Albums = append(group.Albums, struct {
			Files []LocalGroupFile `json:"files"`
		}{files})
	}
	placements, err := a.checkedGrouping(&group, paths)
	if err != nil {
		return a, nil, err
	}
	for _, album := range in.Albums {
		for _, file := range album.Files {
			if loose[file.Path] && len(album.Files) > 1 {
				return a, nil, operations.Invalid("invalid_grouping", fmt.Sprintf("散曲 %q 不能与其他文件归为一张专辑", file.Path))
			}
		}
	}
	return a, placements, nil
}

func (w *Writer) PreviewStewardGrouping(ctx context.Context, in StewardGrouping) ([]GroupingRelease, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, _, err := in.plan(ctx, tx)
	if err != nil {
		return nil, err
	}
	releases := make([]GroupingRelease, 0, len(a.Releases))
	for _, r := range a.Releases {
		releases = append(releases, GroupingRelease{r.ID, r.displayTitle})
	}
	return releases, tx.Commit()
}

func (w *Writer) SubmitStewardGrouping(ctx context.Context, in StewardGrouping, confirmedBy, key, requestID string, origin operations.Origin) (operations.Result, error) {
	if err := in.normalize(); err != nil {
		return operations.Result{}, err
	}
	key, ok := operations.NormalizeKey(key)
	if !ok || !uuid.Valid(confirmedBy) || (origin.Source != "chat" && origin.Source != "mcp") {
		return operations.Result{}, operations.Invalid("invalid_input", "确认者、来源或幂等键无效")
	}
	origin.ConfirmedBy = strings.ToLower(confirmedBy)
	spec := operations.Spec{ActorID: confirmedBy, Type: groupKind, Key: key, RequestID: requestID, Origin: &origin, Fingerprint: operations.Fingerprint([]any{groupKind, in})}
	return operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		var out operations.Outcome
		admin, err := identity.IsEnabledAdmin(ctx, tx, confirmedBy)
		if err != nil {
			return out, err
		}
		if !admin {
			return out, operations.Conflict("permission_denied", "需要启用的管理员确认")
		}
		if err := governance.LockCatalogWrites(ctx, tx); err != nil {
			return out, err
		}
		// 扫描先对 scan_runs 做 FOR UPDATE，再领取确认窗口并登记扫描。提交前阻止这个启动序列。
		if _, err := tx.ExecContext(ctx, "LOCK TABLE scan_runs IN EXCLUSIVE MODE NOWAIT"); err != nil {
			var failure interface{ SQLState() string }
			if errors.As(err, &failure) && failure.SQLState() == "55P03" {
				return out, operations.Conflict("scan_running", "曲库扫描进行中")
			}
			return out, err
		}
		a, rows, err := in.plan(ctx, tx)
		if err != nil {
			return out, err
		}
		lock, err := governance.LockAutoRelease(ctx, tx, a.ReleaseIDs[0])
		if err != nil {
			return out, err
		}
		out, err = saveLocalGrouping(ctx, tx, lock, id, a, rows, true)
		if err != nil {
			return out, err
		}
		for _, rid := range a.ReleaseIDs {
			out.Targets = append(out.Targets, operations.Target{Type: operations.TargetRelease, ID: rid})
		}
		out.Response = map[string]string{"operation_id": id, "message": "下一次扫描完成后生效，可用 list_library_directory 确认"}
		out.AfterCommit = w.groupingConfirmation(ctx)
		return out, nil
	})
}

func (w *Writer) WithGroupingConfirmation(start func(context.Context) error) *Writer {
	w.confirmGrouping = start
	return w
}

func (w *Writer) groupingConfirmation(ctx context.Context) func() {
	if w == nil || w.confirmGrouping == nil {
		return nil
	}
	return func() {
		if err := w.confirmGrouping(ctx); err != nil {
			w.logger.WarnContext(ctx, "归组已保存，启动确认扫描失败", "error", err)
		}
	}
}
