package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/testsupport"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func feedbackDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "feedback_"))
	if err != nil {
		t.Fatal(err)
	}
	if err := testsupport.SetArtistProjectionVersionCurrent(ctx, db); err != nil {
		t.Fatalf("set current artist projection version: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func feedbackExec(t *testing.T, db Execer, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func feedbackRun(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := uuid.New()
	feedbackExec(t, db, `INSERT INTO scan_runs(id,status,started_at,feedback_version,report_state) VALUES($1,'running',clock_timestamp(),1,'pending')`, id)
	return id
}
func feedbackRoot(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := uuid.New()
	feedbackExec(t, db, `INSERT INTO library_roots(id,path) VALUES($1,$2)`, id, "/synthetic/"+id)
	return id
}
func feedbackCandidate(t *testing.T, db *sql.DB, scanID, rootID, releaseID string, trackIDs []string, observations ...SourceObservation) OrganizedCandidate {
	t.Helper()
	for i, observation := range observations {
		if err := StageObservation(context.Background(), db, scanID, rootID, observation); err != nil {
			t.Fatal(err)
		}
		var trackID any
		if i < len(trackIDs) && trackIDs[i] != "" {
			trackID = trackIDs[i]
		}
		feedbackExec(t, db, `UPDATE scan_observations SET planned=true,planned_release_id=$1::uuid,planned_track_id=$2::uuid WHERE scan_run_id=$3::uuid AND root_id=$4::uuid AND relative_path=$5`, nullableString(releaseID), trackID, scanID, rootID, NormalizedPath(observation.RelativePath))
	}
	return organizeSingleCandidate(t, observations...)
}
func feedbackPersist(t *testing.T, db *sql.DB, f *ScanFeedback, root string, c OrganizedCandidate) string {
	t.Helper()
	id, err := PersistCandidateWithFeedback(context.Background(), db, f.scanID, root, c, f)
	if err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatalf("feedback broken: %v", f.Err())
	}
	return id
}
func feedbackSummary(t *testing.T, db *sql.DB, f *ScanFeedback) ScanChangeSummary {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := SummarizeScanChanges(context.Background(), tx, f.scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return result
}
func feedbackTracks(t *testing.T, db *sql.DB, releaseID string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 ORDER BY t.position`, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}
func observation(path, title string, position int) SourceObservation {
	return SourceObservation{RelativePath: path, Directory: "album", Album: "Album", Artist: "Artist", Title: title, TrackNumber: position, DiscNumber: 1, SourceKind: "physical"}
}

func TestPostgreSQLScanFeedbackCandidateChanges(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a, b := observation("album/a.flac", "A", 1), observation("album/b.flac", "B", 2)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, a, b))
	ids := feedbackTracks(t, db, release)
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Added: 1}, Tracks: ChangeCounts{Added: 2}}) {
		t.Fatalf("initial: %+v", got)
	}
	var historicalTitle string
	if err := db.QueryRow(`SELECT title_at_creation FROM scan_added_releases WHERE scan_run_id=$1`, f.scanID).Scan(&historicalTitle); err != nil || historicalTitle != "Album" {
		t.Fatalf("history: %q %v", historicalTitle, err)
	}
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a, b))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{}) {
		t.Fatalf("unchanged: %+v", got)
	}
	// 历史 NULL 与当前规范化的空文本/零音频参数均表示缺值，不虚报更新。
	feedbackExec(t, db, `UPDATE tracks SET duration_seconds=NULL,codec=NULL,sample_rate=NULL,channels=NULL,bitrate=NULL WHERE id=ANY($1::uuid[])`, ids)
	feedbackExec(t, db, `UPDATE releases SET album_artist=NULL WHERE id=$1`, release)
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a, b))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{}) {
		t.Fatalf("normalized absence: %+v", got)
	}
	// 指纹/大小/时间回填与 Medium UUID 更换不构成资料更新。
	newMedium := uuid.New()
	feedbackExec(t, db, `UPDATE media SET position=99 WHERE release_id=$1`, release)
	feedbackExec(t, db, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, newMedium, release)
	feedbackExec(t, db, `UPDATE tracks SET medium_id=$1 WHERE id=ANY($2::uuid[])`, newMedium, ids)
	feedbackExec(t, db, `DELETE FROM media WHERE release_id=$1 AND position=99`, release)
	feedbackExec(t, db, `UPDATE tracks SET content_fingerprint='old',source_size=42,source_modified_at=NOW() WHERE id=ANY($1::uuid[])`, ids)
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a, b))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{}) {
		t.Fatalf("bookkeeping: %+v", got)
	}
	a.Title = "Updated"
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a, b))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Updated: 1}}) {
		t.Fatalf("metadata: %+v", got)
	}
	// 同路径内容替换由规划分配新 UUID，腾位的旧曲目必须记入消失。
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, []string{"", ids[1]}, a, b))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Added: 1, Missing: 1}}) {
		t.Fatalf("replace: %+v", got)
	}
}

func TestPostgreSQLScanFeedbackMoveRestoreAndMissing(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, a))
	ids := feedbackTracks(t, db, release)
	f = NewScanFeedback(feedbackRun(t, db))
	newRelease := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", ids, a))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Added: 1, Missing: 1}, Tracks: ChangeCounts{Updated: 1}}) {
		t.Fatalf("move: %+v", got)
	}
	f = NewScanFeedback(feedbackRun(t, db))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Missing: 1}, Tracks: ChangeCounts{Missing: 1}}) {
		t.Fatalf("missing: %+v", got)
	}
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, newRelease, ids, a))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Updated: 1}}) {
		t.Fatalf("restore: %+v", got)
	}
}

func TestPostgreSQLScanFeedbackMissingBatchesAtomic(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	group, release, medium := uuid.New(), uuid.New(), uuid.New()
	feedbackExec(t, db, `INSERT INTO release_groups(id) VALUES($1)`, group)
	feedbackExec(t, db, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'Album','Artist')`, release, group)
	feedbackExec(t, db, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	for i := 0; i < 600; i++ {
		feedbackExec(t, db, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at) VALUES($1,$2,$3,'A','Artist',$4,$5,NOW()-INTERVAL '1 hour')`, uuid.New(), medium, i+1, root, fmt.Sprintf("%d.flac", i))
	}
	f := NewScanFeedback(feedbackRun(t, db))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tracks WHERE source_status='present'`).Scan(&count); err != nil || count != 600 {
		t.Fatalf("rollback: %d %v", count, err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Missing: 1}, Tracks: ChangeCounts{Missing: 600}}) {
		t.Fatalf("batch: %+v", got)
	}
	f = NewScanFeedback(feedbackRun(t, db))
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{}) {
		t.Fatalf("already missing: %+v", got)
	}

	// 第二批记账故障只回滚报告，不回滚已经完成的核心 missing 更新。
	feedbackExec(t, db, `UPDATE tracks SET source_status='present',observed_at=NOW()-INTERVAL '1 hour'`)
	feedbackExec(t, db, `CREATE FUNCTION reject_later_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  IF (SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=NEW.scan_run_id)>300 THEN RAISE EXCEPTION 'later feedback batch fault'; END IF;
  RETURN NEW; END $$`)
	feedbackExec(t, db, `CREATE TRIGGER reject_later_batch BEFORE INSERT ON scan_catalog_changes FOR EACH ROW EXECUTE FUNCTION reject_later_batch()`)
	f = NewScanFeedback(feedbackRun(t, db))
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if !f.Broken() {
		t.Fatal("later feedback batch did not break report")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tracks WHERE source_status='missing'`).Scan(&count); err != nil || count != 600 {
		t.Fatalf("core missing lost: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=$1`, f.scanID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial feedback survived: %d %v", count, err)
	}
}

func TestPostgreSQLScanFeedbackWriteFailurePreservesCandidate(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	feedbackExec(t, db, `CREATE FUNCTION reject_feedback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'feedback fault'; END $$`)
	feedbackExec(t, db, `CREATE TRIGGER feedback_fault BEFORE INSERT ON scan_catalog_changes FOR EACH ROW EXECUTE FUNCTION reject_feedback()`)
	f := NewScanFeedback(feedbackRun(t, db))
	c := feedbackCandidate(t, db, f.scanID, root, "", nil, observation("album/a.flac", "A", 1))
	release, err := PersistCandidateWithFeedback(context.Background(), db, f.scanID, root, c, f)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Broken() {
		t.Fatal("feedback must break")
	}
	if len(feedbackTracks(t, db, release)) != 1 {
		t.Fatal("core write lost")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=$1`, f.scanID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("feedback rollback: %d %v", count, err)
	}
}

func TestPostgreSQLScanFeedbackGovernanceLockTimeoutReleasesLocks(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, observation("album/a.flac", "A", 1)))
	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	feedbackExec(t, blocker, `SELECT id FROM release_groups WHERE id=(SELECT group_id FROM releases WHERE id=$1) FOR UPDATE`, release)
	f = NewScanFeedback(feedbackRun(t, db))
	scan, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.CaptureArtworkBefore(ctx, scan, release); err != nil {
		t.Fatal(err)
	}
	if !f.Broken() {
		t.Fatal("lock timeout must disable report")
	}
	// 报告尝试已回滚，尽管扫描事务仍活着，另一连接能立即锁住发行。
	probe, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	feedbackExec(t, probe, `SELECT id FROM releases WHERE id=$1 FOR UPDATE NOWAIT`, release)
	var timeout string
	if err := scan.QueryRow(`SHOW lock_timeout`).Scan(&timeout); err != nil || timeout != "0" {
		t.Fatalf("leaked timeout: %q %v", timeout, err)
	}
	feedbackExec(t, scan, `SELECT 1`)
}

func TestPostgreSQLScanFeedbackGovernanceReadsGroupAfterReleaseLock(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, observation("album/a.flac", "A", 1)))
	nextGroup := uuid.New()
	feedbackExec(t, db, `INSERT INTO release_groups(id) VALUES($1)`, nextGroup)
	governance, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer governance.Rollback()
	feedbackExec(t, governance, `SELECT id FROM releases WHERE id=$1 FOR UPDATE`, release)
	feedbackExec(t, governance, `UPDATE releases SET group_id=$1 WHERE id=$2`, nextGroup, release)
	f = NewScanFeedback(feedbackRun(t, db))
	scan, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	var pid int
	if err := scan.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- f.CaptureArtworkBefore(ctx, scan, release) }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("capture ended before lock barrier: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := governance.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	var observedGroup string
	if err := scan.QueryRow(`SELECT group_id::text FROM scan_feedback_before WHERE kind='release' AND id=$1`, release).Scan(&observedGroup); err != nil {
		t.Fatal(err)
	}
	if observedGroup != nextGroup {
		t.Fatalf("stale group after waiting: %s want %s", observedGroup, nextGroup)
	}
	feedbackExec(t, scan, `UPDATE releases SET title='Changed by scan' WHERE id=$1`, release)
	if err := f.CaptureArtworkAfter(ctx, scan, release); err != nil {
		t.Fatal(err)
	}
	if err := scan.Commit(); err != nil {
		t.Fatal(err)
	}
	var recordedGroup string
	if err := db.QueryRow(`SELECT entity_id::text FROM scan_catalog_changes WHERE scan_run_id=$1 AND entity_kind='album'`, f.scanID).Scan(&recordedGroup); err != nil {
		t.Fatal(err)
	}
	if recordedGroup != nextGroup {
		t.Fatalf("mixed governance snapshot: %s want %s", recordedGroup, nextGroup)
	}
}

func TestPostgreSQLScanFeedbackScopeDenominator(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	scan := feedbackRun(t, db)
	for _, path := range []string{"one/a.flac", "one/b.flac", "two/a.flac", "three/a.flac"} {
		item := observation(path, "A", 1)
		if err := StageObservation(context.Background(), db, scan, root, item); err != nil {
			t.Fatal(err)
		}
	}
	feedbackExec(t, db, `UPDATE scan_observations SET organization_scope=split_part(relative_path,'/',1),planned=relative_path NOT LIKE 'three/%',withheld=relative_path LIKE 'two/%' WHERE scan_run_id=$1`, scan)
	count, err := CountPlannedScopes(context.Background(), db, scan, root)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	scopes, err := PlannedStagedScopes(context.Background(), db, scan, root, "")
	if err != nil || len(scopes) != 1 || scopes[0] != "one" {
		t.Fatalf("scopes=%v err=%v", scopes, err)
	}
}

func TestPostgreSQLScanFeedbackProjectionFields(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, a))
	ids := feedbackTracks(t, db, release)
	changes := []struct {
		name  string
		apply func(*SourceObservation)
	}{
		{"lyrics", func(a *SourceObservation) { a.Lyrics = "Words"; a.LyricsSource = "vorbis_lyrics" }},
		{"audio", func(a *SourceObservation) { a.DurationSeconds = 42; a.SampleRate = 48000; a.Channels = 2 }},
		{"source location", func(a *SourceObservation) { a.RelativePath = "album/renamed.flac" }},
		{"cue interval", func(a *SourceObservation) {
			a.CueIndexPresent = true
			a.CueIndexFrames = 75
			a.CueEndPresent = true
			a.CueEndFrames = 150
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			change.apply(&a)
			f = NewScanFeedback(feedbackRun(t, db))
			feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a))
			if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Updated: 1}}) {
				t.Fatalf("projection: %+v", got)
			}
		})
	}
}

func TestPostgreSQLScanFeedbackNewThenMissingPriority(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, a))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, nil, a))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Added: 1}, Tracks: ChangeCounts{Added: 2}}) {
		t.Fatalf("new priority: %+v", got)
	}
}

func TestPostgreSQLScanFeedbackMissingVersionDoesNotHideWholeAlbum(t *testing.T) {
	db := feedbackDB(t)
	root1, root2 := feedbackRoot(t, db), feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	f := NewScanFeedback(feedbackRun(t, db))
	r1 := feedbackPersist(t, db, f, root1, feedbackCandidate(t, db, f.scanID, root1, "", nil, a))
	r2 := feedbackPersist(t, db, f, root2, feedbackCandidate(t, db, f.scanID, root2, "", nil, a))
	feedbackExec(t, db, `UPDATE releases SET group_id=(SELECT group_id FROM releases WHERE id=$1) WHERE id=$2`, r1, r2)
	f = NewScanFeedback(feedbackRun(t, db))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root1}, f); err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Missing: 1}}) {
		t.Fatalf("visible version: %+v", got)
	}
}

func TestPostgreSQLScanFeedbackGovernanceDoesNotRewriteEarlierVisibility(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	initial := NewScanFeedback(feedbackRun(t, db))
	visible := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, a))
	var group string
	if err := db.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1`, visible).Scan(&group); err != nil {
		t.Fatal(err)
	}
	hidden := uuid.New()
	feedbackExec(t, db, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'Invisible version','Artist')`, hidden, group)
	f := NewScanFeedback(feedbackRun(t, db))
	changeHidden := func(title string) {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := f.CaptureArtworkBefore(context.Background(), tx, hidden); err != nil {
			t.Fatal(err)
		}
		feedbackExec(t, tx, `UPDATE releases SET title=$1 WHERE id=$2`, title, hidden)
		if err := f.CaptureArtworkAfter(context.Background(), tx, hidden); err != nil {
			t.Fatal(err)
		}
		if f.Broken() {
			t.Fatal(f.Err())
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	changeHidden("Scan update one")
	next := uuid.New()
	feedbackExec(t, db, `INSERT INTO release_groups(id) VALUES($1)`, next)
	feedbackExec(t, db, `UPDATE releases SET group_id=$1 WHERE id=$2`, next, visible)
	changeHidden("Scan update two")
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}}) {
		t.Fatalf("governance contaminated history: %+v", got)
	}
}

func TestPostgreSQLScanFeedbackStagingCleanupIsExplicit(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	terminal, running := feedbackRun(t, db), feedbackRun(t, db)
	for _, scan := range []string{terminal, running} {
		if err := StageObservation(context.Background(), db, scan, root, observation("album/a.flac", "A", 1)); err != nil {
			t.Fatal(err)
		}
	}
	feedbackExec(t, db, `UPDATE scan_runs SET status='incomplete' WHERE id=$1`, terminal)
	if err := ClearScansStaging(context.Background(), db, []string{terminal}); err != nil {
		t.Fatal(err)
	}
	var remaining string
	if err := db.QueryRow(`SELECT scan_run_id::text FROM scan_observations`).Scan(&remaining); err != nil || remaining != running {
		t.Fatalf("remaining=%q err=%v", remaining, err)
	}
}

func TestPostgreSQLScanFeedbackSchemaBoundsAndLegacy(t *testing.T) {
	db := feedbackDB(t)
	legacy := uuid.New()
	feedbackExec(t, db, `INSERT INTO scan_runs(id,status,started_at) VALUES($1,'succeeded',NOW())`, legacy)
	var version sql.NullInt64
	if err := db.QueryRow(`SELECT feedback_version FROM scan_runs WHERE id=$1`, legacy).Scan(&version); err != nil || version.Valid {
		t.Fatalf("legacy was backfilled: %v %v", version, err)
	}
	id := feedbackRun(t, db)
	for _, query := range []string{
		`UPDATE scan_runs SET files_counted=-1 WHERE id=$1`,
		`UPDATE scan_runs SET files_processed=9007199254740992 WHERE id=$1`,
		`UPDATE scan_runs SET phase='unknown' WHERE id=$1`,
		`UPDATE scan_runs SET progress_bp=10000 WHERE id=$1`,
		`UPDATE scan_runs SET progress_bp=10001 WHERE id=$1`,
		`UPDATE scan_runs SET roots_total=1,current_root_index=2 WHERE id=$1`,
		`UPDATE scan_runs SET report_state='ready_maybe' WHERE id=$1`,
	} {
		if _, err := db.Exec(query, id); err == nil {
			t.Fatalf("invalid feedback accepted: %s", query)
		}
	}
	feedbackExec(t, db, `UPDATE scan_runs SET status='succeeded',progress_bp=10000 WHERE id=$1`, id)
}

func TestPostgreSQLScanFeedbackCanonicalCredits(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	a.Credits = []metadata.Credit{{Role: "composer", Name: "Composer"}, {Role: "conductor", Name: "Conductor"}}
	f := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, a))
	ids := feedbackTracks(t, db, release)
	// position 是规范化后的派生序号，不属于扫描更新的独立事实。
	feedbackExec(t, db, `UPDATE track_credits SET position=position+10 WHERE track_id=$1`, ids[0])
	a.Credits = []metadata.Credit{{Role: "Conductor", Name: " Conductor "}, {Role: "composer", Name: "Composer"}, {Role: "composer", Name: "Composer"}}
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{}) {
		t.Fatalf("canonical credits: %+v", got)
	}
	a.Credits[0].Name = "Other conductor"
	f = NewScanFeedback(feedbackRun(t, db))
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, ids, a))
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Updated: 1}, Tracks: ChangeCounts{Updated: 1}}) {
		t.Fatalf("changed credits: %+v", got)
	}
}

func waitFeedbackLock(t *testing.T, db *sql.DB, ctx context.Context, pid int, queryPrefix string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) AND query LIKE $2 FROM pg_stat_activity WHERE pid=$1`, pid, queryPrefix+"%").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestPostgreSQLScanFeedbackGovernanceChangedLockSetFallsBack(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	a := observation("album/a.flac", "A", 1)
	initial := NewScanFeedback(feedbackRun(t, db))
	oldRelease := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, a))
	track := feedbackTracks(t, db, oldRelease)[0]
	b := observation("album/b.flac", "B", 1)
	otherRelease := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, b))
	var otherMedium string
	if err := db.QueryRow(`SELECT id::text FROM media WHERE release_id=$1`, otherRelease).Scan(&otherMedium); err != nil {
		t.Fatal(err)
	}
	f := NewScanFeedback(feedbackRun(t, db))
	candidate := feedbackCandidate(t, db, f.scanID, root, "", []string{track}, a)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	releaseBlocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseBlocker.Rollback()
	feedbackExec(t, releaseBlocker, `SELECT id FROM releases WHERE id=$1 FOR UPDATE`, oldRelease)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		release string
		err     error
	}
	finished := make(chan outcome, 1)
	go func() {
		id, err := PersistCandidateWithFeedback(ctx, conn, f.scanID, root, candidate, f)
		finished <- outcome{id, err}
	}()
	// 正式入口已经收集旧发行集合，并在第一次发行行锁处等待。
	waitFeedbackLock(t, db, ctx, pid, "SELECT COUNT(*) FROM (SELECT r.id FROM releases r")
	// 此连接模拟集合在收集后改变；不依赖生产 hook，也不在扫描持组锁后追加发行锁。
	feedbackExec(t, db, `UPDATE tracks SET medium_id=$1 WHERE id=$2`, otherMedium, track)
	trackBlocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer trackBlocker.Rollback()
	feedbackExec(t, trackBlocker, `SELECT id FROM tracks WHERE id=$1 FOR UPDATE`, track)
	if err := releaseBlocker.Commit(); err != nil {
		t.Fatal(err)
	}
	// 集合复检失败后，核心流程继续新建发行；在核心曲目写入处停住，以检查先前锁已释放。
	waitFeedbackLock(t, db, ctx, pid, "UPDATE tracks SET medium_id=")
	probe, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	feedbackExec(t, probe, `SELECT id FROM releases WHERE id=$1 FOR UPDATE NOWAIT`, oldRelease)
	if err := probe.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := trackBlocker.Commit(); err != nil {
		t.Fatal(err)
	}
	result := <-finished
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !f.Broken() || !errors.Is(f.Err(), errFeedbackLockSetChanged) {
		t.Fatalf("wrong downgrade: broken=%v err=%v", f.Broken(), f.Err())
	}
	if got := feedbackTracks(t, db, result.release); len(got) != 1 || got[0] != track {
		t.Fatalf("core placement lost: %v", got)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=$1`, f.scanID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial feedback survived: %d %v", count, err)
	}
}
