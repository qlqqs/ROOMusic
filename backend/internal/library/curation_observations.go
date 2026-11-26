package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

// 曲目的路径和解析时大小已在 tracks。本轮只补充 CUE 文件和正在变化的来源。
type scanFileObservation struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Directory string `json:"directory"`
	Unread    bool   `json:"unread"`
	Moving    bool   `json:"moving"`
	Tree      bool   `json:"tree"`
	Scope     string `json:"scope"`
}

type scanFileKey struct{ root, path string }

func (f *scanFeedback) observeFile(root, path string, size int64, unread, moving bool) {
	if f == nil {
		return
	}
	if f.observedFiles == nil {
		f.observedFiles = make(map[scanFileKey]scanFileObservation)
	}
	key := scanFileKey{root, path}
	previous := f.observedFiles[key]
	previous.Root, previous.Path, previous.Directory, previous.Size = root, path, ingest.DirectoryOf(path), size
	previous.Unread = previous.Unread || unread
	previous.Moving = previous.Moving || moving
	f.observedFiles[key] = previous
}

func (f *scanFeedback) checkMember(root registeredRoot, relative string, before int64) {
	full := filepath.Join(root.Path, filepath.FromSlash(relative))
	info, err := os.Lstat(full)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(full)
		if resolveErr != nil || !isWithin(root.Path, resolved) {
			f.observeFile(root.ID, relative, -1, false, true)
			return
		}
		info, err = os.Stat(resolved)
	}
	if err != nil || !info.Mode().IsRegular() {
		f.observeFile(root.ID, relative, -1, false, true)
		return
	}
	if size := info.Size(); size != before {
		f.observeFile(root.ID, relative, size, false, true)
	}
}

// 在候选保存阶段复查先前读过的文件；共用父音频只检查一次。
func (f *scanFeedback) checkCandidate(root registeredRoot, candidate ingest.OrganizedCandidate) {
	if f == nil {
		return
	}
	sizes := map[string]int64{}
	for _, disc := range candidate.Mediums {
		for _, track := range disc {
			o := track.Observation
			path := o.RelativePath
			if o.CueParentRelativePath != "" {
				path = o.CueParentRelativePath
			}
			sizes[path] = o.SourceSize
			if sheet, ok := f.observedFiles[scanFileKey{root.ID, o.CueSheetPath}]; ok {
				sizes[sheet.Path] = sheet.Size
			}
		}
	}
	for path, before := range sizes {
		f.checkMember(root, path, before)
	}
}

func (f *scanFeedback) observeHeld(root, relative string, size int64, kind string) {
	if f == nil {
		return
	}
	tree := kind == ingest.HeldDirectory
	ext := strings.ToLower(filepath.Ext(relative))
	if !tree && ext != ".cue" && !isSupportedAudioExtension(ext) {
		return
	}
	f.observeFile(root, relative, size, true, false)
	key := scanFileKey{root, relative}
	file := f.observedFiles[key]
	file.Tree = tree
	directory := file.Directory
	if tree {
		directory = relative
	}
	file.Scope = ingest.OrganizationScope(ingest.SourceObservation{RelativePath: relative, Directory: directory})
	f.observedFiles[key] = file
}

// 未解析文件没有候选；在本根保存结束前复查它首次读取时的大小。
func (f *scanFeedback) checkHeldFiles(root registeredRoot) {
	if f == nil {
		return
	}
	for _, file := range f.observedFiles {
		if file.Root == root.ID && file.Unread && !file.Tree && file.Size >= 0 {
			f.checkMember(root, file.Path, file.Size)
		}
	}
}

func finishCurationObservation(ctx context.Context, tx *sql.Tx, scanID string, roots []string, feedback *scanFeedback, succeeded bool) error {
	if len(roots) == 0 {
		return nil
	}
	var coverage []byte
	var affected []string
	extra := []scanFileObservation{}
	if feedback != nil {
		coverage, affected = feedback.coverage, feedback.affectedReleases
		for _, file := range feedback.observedFiles {
			extra = append(extra, file)
		}
	}
	data, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	// 失败、取消的扫描只能撤销已有确认。缺失对账与确认共用扫描终态事务。
	_, err = tx.ExecContext(ctx, `WITH extras AS MATERIALIZED (
 SELECT * FROM jsonb_to_recordset($1::jsonb) AS x(root uuid,path text,size bigint,directory text,unread bool,moving bool,tree bool,scope text)
), files AS MATERIALIZED (
 SELECT f.* FROM curation_scan_files f
 WHERE f.release_id=ANY($6::uuid[]) OR EXISTS (SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=f.release_id
   AND t.source_root_id=ANY($2::uuid[]) AND t.source_status='present'
   AND t.observed_at>=(SELECT started_at FROM scan_runs WHERE id=$4::uuid))
), held AS (
 SELECT DISTINCT f.release_id,x.root AS root_id,x.path,x.size,x.moving
 FROM extras x JOIN files f ON f.root_id=x.root
 JOIN releases r ON r.id=f.release_id
 WHERE x.unread AND (
  f.path=x.path OR (x.tree AND starts_with(f.path,x.path||'/'))
  OR (NOT x.tree AND x.directory<>'' AND r.candidate_kind IS DISTINCT FROM 'loose_unknown'
    AND regexp_replace(f.path,'[^/]*$','')=x.directory||'/'))
 UNION
 SELECT DISTINCT m.release_id,x.root,x.path,x.size,x.moving
 FROM extras x JOIN releases origin ON origin.source_root_id=x.root
  AND origin.candidate_kind='strict_multidisc' AND x.scope='disc-parent:'||origin.relative_directory
 JOIN media m ON COALESCE(m.origin_release_id,m.release_id)=origin.id
 WHERE x.unread AND EXISTS (SELECT FROM files f WHERE f.release_id=m.release_id)
), observations AS (
 SELECT f.release_id,f.root_id,f.path,coalesce(x.size,f.size) AS size,
   coalesce(x.unread OR x.moving,false) AS blocked,coalesce(x.moving,false) AS moved
 FROM files f LEFT JOIN extras x ON x.root=f.root_id AND x.path=f.path
 UNION ALL SELECT release_id,root_id,path,size,true,moving FROM held
), members AS (
 SELECT release_id, root_id,path,max(size) AS size,bool_or(blocked) AS blocked,bool_or(moved) AS moved
 FROM observations GROUP BY release_id,root_id,path
), signatures AS (
 SELECT release_id,sha256(convert_to(jsonb_agg(jsonb_build_array(root_id,path,size)
 ORDER BY root_id,path COLLATE "C",size)::text,'UTF8')) AS signature,bool_or(blocked) AS blocked,bool_or(moved) AS moved
  ,bool_and(root_id=ANY($2::uuid[]) AND ($5::jsonb IS NULL OR ($5::jsonb->root_id::text) ? regexp_replace(path,'(^|/)[^/]*$',''))) AS covered
 FROM members GROUP BY release_id
),
-- 迁移时只能取得父音频大小。按同一份成员和现存音频大小还原旧摘要，
-- 仅当缺失的是 sheet 大小、其它证据完整相同时，才沿用已稳定的基线。
legacy_cue AS (
 SELECT f.release_id
 FROM files f JOIN signatures s ON s.release_id=f.release_id JOIN releases r ON r.id=f.release_id
 WHERE r.curation_confirmed AND r.curation_changed_at IS NULL AND NOT s.blocked
   AND r.curation_member_signature<>s.signature
 GROUP BY f.release_id,r.curation_member_signature
 HAVING bool_or(f.size IS NULL AND right(lower(f.path),4)='.cue')
    AND r.curation_member_signature=sha256(convert_to(jsonb_agg(jsonb_build_array(f.root_id,f.path,f.size)
        ORDER BY f.root_id,f.path COLLATE "C",f.size)::text,'UTF8'))
), updates AS (
 SELECT r.id,
  CASE WHEN $3 AND s.covered THEN s.signature ELSE r.curation_member_signature END AS signature,
  CASE WHEN s.moved OR (l.release_id IS NULL AND r.curation_member_signature IS DISTINCT FROM s.signature AND ($3 OR r.curation_confirmed))
       OR (s.blocked AND r.curation_confirmed) THEN now() ELSE r.curation_changed_at END AS changed_at,
  ((s.covered OR r.curation_confirmed) AND NOT s.blocked AND (l.release_id IS NOT NULL OR r.curation_member_signature=s.signature) AND ($3 OR r.curation_confirmed)) IS TRUE AS confirmed
 FROM signatures s JOIN releases r ON r.id=s.release_id LEFT JOIN legacy_cue l ON l.release_id=s.release_id
)
UPDATE releases r SET curation_member_signature=u.signature,curation_changed_at=u.changed_at,curation_confirmed=u.confirmed
 FROM updates u WHERE r.id=u.id AND (r.curation_member_signature,r.curation_changed_at,r.curation_confirmed)
 IS DISTINCT FROM (u.signature,u.changed_at,u.confirmed)`, string(data), roots, succeeded, scanID, coverage, affected)
	if err != nil || !succeeded || len(affected) == 0 {
		return err
	}
	// 失去最后一个成员的发行在本轮没有新签名，撤销旧确认。
	_, err = tx.ExecContext(ctx, `UPDATE releases r SET curation_confirmed=false,curation_member_signature=NULL,curation_changed_at=now()
		WHERE id=ANY($1::uuid[]) AND NOT EXISTS(SELECT FROM curation_scan_files f WHERE f.release_id=r.id)`, affected)
	return err
}

// 记下写入前的发行，才能看到局部扫描中成员转移或删除后留下的发行。
func (f *scanFeedback) rememberCoveredReleases(ctx context.Context) error {
	if f.coverage == nil {
		return nil
	}
	rows, err := f.executor.QueryContext(ctx, `SELECT DISTINCT release_id::text FROM curation_scan_files
	WHERE ($1::jsonb->root_id::text) ? regexp_replace(path,'(^|/)[^/]*$','')`, f.coverage)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		f.affectedReleases = append(f.affectedReleases, id)
	}
	return rows.Err()
}

const confirmationDue = `NOT r.curation_confirmed AND r.curation_changed_at<=now()-interval '60 seconds'
	AND r.curation_attempted_change_at IS DISTINCT FROM r.curation_changed_at
	AND EXISTS (SELECT FROM curation_scan_files f WHERE f.release_id=r.id)
	AND NOT EXISTS (SELECT FROM curation_scan_files f JOIN library_roots l ON l.id=f.root_id
	   WHERE f.release_id=r.id AND l.status<>'active')`

// StartCurationConfirmation 在扫描锁内领取仍到期的成员窗口，与启动扫描同事务。
func (s *Service) StartCurationConfirmation(ctx context.Context) error {
	var due bool
	if err := s.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM releases r WHERE `+confirmationDue+`)`).Scan(&due); err != nil || !due {
		return err
	}
	_, err := s.StartScan(ctx, StartOptions{Mode: ScanModeIncremental, Trigger: ScanTriggerScheduled, prepare: s.claimConfirmations})
	return err
}

func (s *Service) claimConfirmations(ctx context.Context, tx *sql.Tx) (scanTargets, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT r.id::text,r.curation_changed_at,f.root_id::text,l.revision,
	regexp_replace(f.path,'(^|/)[^/]*$','') FROM releases r JOIN curation_scan_files f ON f.release_id=r.id
	JOIN library_roots l ON l.id=f.root_id WHERE `+confirmationDue+` ORDER BY 1,3,5`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		changed time.Time
		targets scanTargets
		busy    bool
	}
	albums := map[string]*candidate{}
	for rows.Next() {
		var id, root, dir string
		var revision int64
		var changed time.Time
		if err := rows.Scan(&id, &changed, &root, &revision, &dir); err != nil {
			rows.Close()
			return nil, err
		}
		album := albums[id]
		if album == nil {
			album = &candidate{changed: changed, targets: scanTargets{}}
			albums[id] = album
		}
		target := album.targets[root]
		target.revision = revision
		target.directories = append(target.directories, dir)
		album.targets[root] = target
		album.busy = album.busy || s.directoryWriting(root, dir)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	targets := scanTargets{}
	for id, album := range albums {
		if album.busy {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE releases r SET curation_attempted_change_at=$2 WHERE id=$1 AND curation_changed_at=$2 AND `+confirmationDue, id, album.changed)
		if err != nil {
			return nil, err
		}
		claimed, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if claimed == 0 {
			continue
		}
		for root, target := range album.targets {
			shared := targets[root]
			shared.revision = target.revision
			shared.directories = append(shared.directories, target.directories...)
			targets[root] = shared
		}
	}
	return targets, nil
}
