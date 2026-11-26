package app

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQLCurationProgramGates(t *testing.T) {
	for _, name := range []string{"disabled", "inactive_enabler", "paused", "scan", "pause_during_lookup", "scan_during_lookup"} {
		t.Run(name, func(t *testing.T) {
			f := newProgramFixture(t)
			_, r := f.tagged(t)
			mustExec(t, f.application, `DELETE FROM track_identifiers`)
			mustExec(t, f.application, `UPDATE releases SET barcode=$2 WHERE id=$1`, f.a, r.Barcode)
			f.source.barcode[r.Barcode] = []sources.Release{{ID: r.ID}}
			if name != "disabled" {
				f.enable(t)
			}
			want := "disabled"
			switch name {
			case "inactive_enabler":
				mustExec(t, f.application, `UPDATE users SET disabled_at=now() WHERE id=$1`, f.admin.id)
			case "paused":
				if _, err := f.writer.SetPaused(t.Context(), f.admin.id, true); err != nil {
					t.Fatal(err)
				}
				want = "paused"
			case "scan":
				f.scan.Store(true)
				want = "waiting_for_scan"
			case "pause_during_lookup", "scan_during_lookup":
				want = "paused"
				if name == "scan_during_lookup" {
					want = "waiting_for_scan"
				}
				f.source.hook = func(method string) error {
					if method == "ReleasesByBarcode" {
						if name == "scan_during_lookup" {
							f.scan.Store(true)
						} else {
							_, err := f.writer.SetPaused(context.Background(), f.admin.id, true)
							return err
						}
					}
					return nil
				}
			}
			f.run(t)
			calls := map[string]int{}
			if name == "pause_during_lookup" || name == "scan_during_lookup" {
				calls["ReleasesByBarcode"] = 1
			}
			if f.worker.State() != want || !maps.Equal(calls, f.source.calls) {
				t.Fatalf("状态 %s 调用 %v，期望 %s %v", f.worker.State(), f.source.calls, want, calls)
			}
			if name == "pause_during_lookup" || name == "scan_during_lookup" {
				requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
			} else {
				f.assertCount(t, 0, `SELECT count(*) FROM curation_work_states`)
			}
			f.assertCount(t, 0, `SELECT count(*) FROM curation_batches`)
			if name == "scan" {
				f.scan.Store(false)
				f.clock.advance(t, time.Second)
				f.assertRelease(t, f.a, "matched", "barcode")
			}
		})
	}
}

func TestPostgreSQLCurationProgramSourceSwitches(t *testing.T) {
	f := newProgramFixture(t)
	track, _ := f.tagged(t)
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, track)
	f.enable(t)
	for _, id := range []string{"musicbrainz", "lrclib", "coverartarchive"} {
		f.sourceEnabled(t, id, false)
	}
	f.run(t)
	if len(f.source.calls) != 0 {
		t.Fatal(f.source.calls)
	}
	requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
	f.sourceEnabled(t, "lrclib", true)
	f.clock.advance(t, time.Minute)
	if !maps.Equal(f.source.calls, map[string]int{"LyricsFor": 1}) || f.stateFor(t, "lyrics", track).Status != "done" {
		t.Fatal(f.source.calls)
	}
	f.sourceEnabled(t, "musicbrainz", true)
	f.clock.advance(t, time.Minute)
	f.assertRelease(t, f.a, "matched", "tag_id")
	if f.source.calls["Covers"] != 0 {
		t.Fatal("关闭的封面主题执行了")
	}
	requireUnattemptedProgramState(t, f.stateFor(t, "artwork", f.a))
	f.sourceEnabled(t, "coverartarchive", true)
	f.clock.advance(t, time.Minute)
	if f.source.calls["Covers"] != 2 || f.stateFor(t, "artwork", f.a).Status != "no_match" {
		t.Fatal(f.source.calls)
	}
}

func TestPostgreSQLCurationProgramRetry(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.tagged(t)
	f.source.hook = func(method string) error {
		if method == "ReleaseByID" && f.source.callCount(method) <= 2 {
			return sources.ErrTryLater
		}
		return nil
	}
	f.run(t)
	for i, delay := range []time.Duration{5 * time.Second, 10 * time.Second} {
		requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
		queues, err := f.worker.QueueProgress(t.Context())
		requireCurationOK(t, err)
		now := f.clock.options().Now()
		q := queues[0]
		if q.ID != "release" || q.State != "paused" || q.PauseReason == nil || *q.PauseReason != "source_unavailable" ||
			q.Source == nil || *q.Source != "musicbrainz" || q.RetryAt == nil || !q.RetryAt.Equal(now.Add(delay)) {
			t.Fatalf("第 %d 次退避 %+v", i+1, q)
		}
		f.clock.advance(t, delay-time.Second)
		if f.source.calls["ReleaseByID"] != i+1 {
			t.Fatal("提前出站", f.source.calls)
		}
		f.clock.advance(t, time.Second)
	}
	s := f.assertRelease(t, f.a, "matched", "tag_id")
	if s.Attempts != 1 || s.Generation != 1 || s.RetryCount != 0 || s.NextAttemptAt != nil || f.source.calls["ReleaseByID"] != 3 {
		t.Fatalf("%+v %v", s, f.source.calls)
	}
}

func TestPostgreSQLCurationProgramRelink(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.tagged(t)
	f.run(t)
	first := f.linkOwner(t, f.a)
	s := f.assertRelease(t, f.a, "matched", "tag_id")
	if s.Generation != 1 {
		t.Fatalf("初次代次 %d，期望 1", s.Generation)
	}
	f.undo(t, first)
	f.clock.advance(t, time.Minute)
	s = f.assertRelease(t, f.a, "no_usable_candidates", "tag_id")
	if s.Generation != 2 {
		t.Fatal(s)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND subject='release' AND result->'conflicts' ? 'suppressed'`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, f.a)
	var markers struct{ Items []curation.Suppression }
	getJSON(t, f.application, &f.admin, curationBase+"/suppressions?release_id="+f.a, &markers)
	if len(markers.Items) != 3 {
		t.Fatal(markers)
	}
	for _, s := range markers.Items {
		r := f.application.request(t, "DELETE", curationBase+"/suppressions/"+s.ID, nil, &f.admin, nil)
		if r.Code != 204 && r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	f.clock.advance(t, time.Minute)
	s = f.assertRelease(t, f.a, "matched", "tag_id")
	second := f.linkOwner(t, f.a)
	if first == second || s.Generation != 3 {
		t.Fatalf("对应记录 %s -> %s，代次 %d", first, second, s.Generation)
	}
	f.assertCount(t, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, f.a)
}

func TestPostgreSQLCurationProgramRescanAndChanges(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	track, _ := f.tagged(t)
	f.run(t)
	before := f.assertRelease(t, f.a, "matched", "tag_id")
	owner := f.linkOwner(t, f.a)
	orig := maps.Clone(f.source.calls)
	// 模拟扫描完成的持久化边界，先重报相同标签。
	programFinishedScan(t, f)
	f.clock.advance(t, time.Minute)
	if !maps.Equal(orig, f.source.calls) {
		t.Fatalf("相同输入重查 %v -> %v", orig, f.source.calls)
	}
	mustExec(t, f.application, `UPDATE curation_work_states SET match_rule_version=$2 WHERE target_id=$1 AND subject='release'`, f.a, program.MatchRuleVersion-1)
	f.clock.advance(t, 6*time.Hour)
	after := f.assertRelease(t, f.a, "matched", "tag_id")
	if after.MatchRuleVersion != program.MatchRuleVersion || after.Generation != before.Generation ||
		after.Attempts != before.Attempts || f.linkOwner(t, f.a) != owner || !maps.Equal(orig, f.source.calls) {
		t.Fatalf("外部规则升级应沿用已匹配对应，升级前 %+v，升级后 %+v，调用 %v -> %v", before, after, orig, f.source.calls)
	}
	f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题")})
	f.clock.advance(t, time.Minute)
	s := f.assertRelease(t, f.a, "matched", "tag_id")
	if s.Attempts != before.Attempts+1 || f.source.calls["ReleaseByID"] != orig["ReleaseByID"]+1 {
		t.Fatal(s, f.source.calls)
	}
	programTag(t, f.application, track, "mb_recording", uuid.New())
	programFinishedScan(t, f)
	f.clock.advance(t, time.Minute)
	f.assertRelease(t, f.a, "uncertain", "tag_id")
}

func TestPostgreSQLCurationProgramRestart(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.tagged(t)
	// 在对应操作已提交、字段操作尚未开始时终止进程。
	f.gate.WithScanCheck(func(ctx context.Context) (bool, error) {
		var exists bool
		err := f.application.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM external_links WHERE object_type='release' AND object_id=$1)`, f.a).Scan(&exists)
		if exists {
			f.stop()
			return false, context.Canceled
		}
		return false, err
	})
	f.start(t)
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("未到达中断点")
	}
	owner := f.linkOwner(t, f.a)
	batch := programString(t, f.application, `SELECT batch_id FROM operations WHERE id=$1`, owner)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_batches WHERE id=$1 AND closed_at IS NULL`, batch)
	requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
	f.gate.WithScanCheck(f.scanCheck)
	f.run(t)
	s := f.assertRelease(t, f.a, "matched", "tag_id")
	if s.Generation != 1 || f.linkOwner(t, f.a) != owner {
		t.Fatal(s)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE operation_type='external_link' AND target_id=$1`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM curation_batches WHERE closed_at IS NULL`)
	f.assertCount(t, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'`, f.a)
}

func programFinishedScan(t *testing.T, f *programFixture) {
	t.Helper()
	mustExec(t, f.application, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, uuid.New())
}

func TestPostgreSQLCurationProgramRestartArtworkReplay(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.tagged(t)
	png := externalFixturePNG(t, 4, 4)
	stored, path := externalArtworkFile(t, f.application, png)
	f.source.cover = sources.CoverFile{Image: sources.CoverImage{ID: "16", Front: true}, Data: png}
	interruptProgramArtwork(t, f)
	requireExternalFile(t, path, png)
	oldOperation := programString(t, f.application, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1`, f.a)
	linkOwner := f.linkOwner(t, f.a)
	fieldCount := countRows(t, f.application, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
	removeExternalFixture(t, path)

	f.run(t)
	s := f.stateFor(t, "artwork", f.a)
	if s.Status != "done" || s.Generation != 2 || f.source.calls["FrontCover"] != 2 {
		t.Fatalf("重启后状态 %+v，下载次数 %d", s, f.source.calls["FrontCover"])
	}
	newOperation := programString(t, f.application, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1 AND storage_key=$2`, f.a, stored.Key)
	if newOperation == oldOperation {
		t.Fatalf("封面仍归属原操作 %s", oldOperation)
	}
	f.assertCount(t, 2, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_external_artwork'`, f.a)
	var list struct {
		Items []struct {
			ID         string  `json:"id"`
			Type       string  `json:"type"`
			Revertible bool    `json:"revertible"`
			RevertedBy *string `json:"reverted_by"`
		}
	}
	getJSON(t, f.application, &f.admin, "/api/v1/operations?release_id="+f.a, &list)
	revertible := make(map[string]bool)
	for _, item := range list.Items {
		if item.Type == operations.TypeReleaseExternalArtwork {
			if item.RevertedBy != nil {
				t.Fatalf("封面操作 %s 已被撤销", item.ID)
			}
			revertible[item.ID] = item.Revertible
		}
	}
	if !maps.Equal(revertible, map[string]bool{oldOperation: false, newOperation: true}) {
		t.Fatalf("封面操作的可撤销状态错误：%v，旧操作 %s，新操作 %s", revertible, oldOperation, newOperation)
	}
	if f.linkOwner(t, f.a) != linkOwner {
		t.Fatalf("发行对应的原操作 %s 被替换", linkOwner)
	}
	f.assertCount(t, 3, `SELECT count(*) FROM external_links WHERE operation_id=$1`, linkOwner)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='external_link'`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_auto_correction'`, f.a)
	f.assertCount(t, fieldCount, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM curation_batches WHERE closed_at IS NULL`)
	requireExternalArtworkHTTP(t, f.application, &f.admin, stored, png)
}

func interruptProgramArtwork(t *testing.T, f *programFixture) {
	t.Helper()
	// 封面已写入，结论尚未保存，此时中断工作者。
	f.gate.WithScanCheck(func(ctx context.Context) (bool, error) {
		var exists bool
		err := f.application.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM release_external_artworks WHERE release_id=$1)`, f.a).Scan(&exists)
		if exists {
			f.stop()
			return false, context.Canceled
		}
		return false, err
	})
	f.start(t)
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("未到达封面中断点")
	}
	requireUnattemptedProgramState(t, f.stateFor(t, "artwork", f.a))
	f.gate.WithScanCheck(f.scanCheck)
}

func TestPostgreSQLCurationProgramRestartArtworkReplayRetry(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.tagged(t)
	png := externalFixturePNG(t, 4, 4)
	stored, path := externalArtworkFile(t, f.application, png)
	f.source.cover = sources.CoverFile{Image: sources.CoverImage{ID: "16", Front: true}, Data: png}
	interruptProgramArtwork(t, f)
	removeExternalFixture(t, path)
	oldOperation := programString(t, f.application, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1`, f.a)

	// 替换行的事务提交时同步删除刚写入的文件，确保工作者在复查前看到文件缺失。
	mustExec(t, f.application, `CREATE FUNCTION notice_artwork_replacement() RETURNS trigger LANGUAGE plpgsql
		AS $$ BEGIN RAISE NOTICE 'fixture replaced artwork'; RETURN NULL; END $$`)
	mustExec(t, f.application, `CREATE CONSTRAINT TRIGGER notice_artwork_replacement AFTER UPDATE ON release_external_artworks
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (OLD.operation_id IS DISTINCT FROM NEW.operation_id)
		EXECUTE FUNCTION notice_artwork_replacement()`)
	schema := programString(t, f.application, `SELECT current_schema()`)
	dsn, err := postgrestest.ScopedURL(postgrestest.BaseURL(t), schema)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var removals int
	var removeErr error
	cfg.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
		if notice.Message == "fixture replaced artwork" {
			removals++
			removeErr = os.Remove(path)
		}
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	f.writer = curation.NewWriter(db, f.application.dataDirectory, nil)
	f.run(t)
	if removals != 1 || removeErr != nil {
		t.Fatalf("应同步删除一次新写入的封面，删除次数 %d，错误 %v", removals, removeErr)
	}
	requireExternalFile(t, path, nil)
	replacement := programString(t, f.application, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1 AND storage_key=$2`, f.a, stored.Key)
	if replacement == oldOperation {
		t.Fatalf("封面仍归属原操作 %s", oldOperation)
	}
	f.assertCount(t, 2, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_external_artwork'`, f.a)

	const delay = 10 * time.Minute
	s := f.stateFor(t, "artwork", f.a)
	if s.Status != "try_later" || s.Generation != 2 || s.Attempts != 0 || s.RetryCount != 1 ||
		s.NextAttemptAt == nil || !s.NextAttemptAt.Equal(f.clock.options().Now().Add(delay)) || f.source.calls["FrontCover"] != 2 {
		t.Fatalf("二次写入后缺图的状态 %+v，累计下载次数 %d", s, f.source.calls["FrontCover"])
	}
	mustExec(t, f.application, `DROP TRIGGER notice_artwork_replacement ON release_external_artworks`)
	f.clock.advance(t, delay-time.Second)
	if f.source.calls["FrontCover"] != 2 {
		t.Fatalf("退避到期前已下载，累计下载次数 %d", f.source.calls["FrontCover"])
	}
	f.clock.advance(t, time.Second)
	s = f.stateFor(t, "artwork", f.a)
	if s.Status != "done" || s.Generation != 3 || s.Attempts != 1 || s.RetryCount != 0 ||
		s.NextAttemptAt != nil || f.source.calls["FrontCover"] != 3 {
		t.Fatalf("到期重试后的状态 %+v，累计下载次数 %d", s, f.source.calls["FrontCover"])
	}
	f.assertCount(t, 3, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_external_artwork'`, f.a)
	if owner := programString(t, f.application, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1 AND storage_key=$2`, f.a, stored.Key); owner == oldOperation || owner == replacement {
		t.Fatalf("到期重试后封面仍归属先前操作 %s", owner)
	}
	requireExternalArtworkHTTP(t, f.application, &f.admin, stored, png)
}

func TestPostgreSQLCurationProgramRealRescan(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	const album = "合成重扫"
	path := filepath.Join(f.application.allowedRoot, album, "01.flac")
	writeSyntheticTrack(t, path, 42, "TITLE=第一首", "ARTIST=艺人", "ALBUM="+album, "TRACKNUMBER=1")
	runScan(t, f.application, &f.admin)
	id := programString(t, f.application, `SELECT id FROM releases WHERE relative_directory=$1`, album)
	f.run(t)
	requireUnattemptedProgramState(t, f.stateFor(t, "release", id))
	if len(f.source.calls) != 0 {
		t.Fatalf("首次扫描后仍需确认，出站记录 %v", f.source.calls)
	}
	confirmProgramRelease(t, f.application, &f.admin, id)
	f.clock.advance(t, time.Minute)
	first := f.assertRelease(t, id, "unlisted", "search")
	before := maps.Clone(f.source.calls)
	if before["SearchReleases"] < 2 {
		t.Fatal(before)
	}
	runScan(t, f.application, &f.admin)
	f.clock.advance(t, time.Minute)
	again := f.assertRelease(t, id, "unlisted", "search")
	if again.Attempts != first.Attempts || !maps.Equal(before, f.source.calls) {
		t.Fatalf("重扫相同文件后 %v -> %v", before, f.source.calls)
	}
	writeSyntheticTrack(t, filepath.Join(f.application.allowedRoot, album, "02.flac"), 43, "TITLE=第二首", "ARTIST=艺人", "ALBUM="+album, "TRACKNUMBER=2")
	runScan(t, f.application, &f.admin)
	f.clock.advance(t, time.Minute)
	// 成员集合变化时扫描可能生成新的发行，核对当前可见发行的待定证据。
	scanned := claimRescanEnvironment{application: f.application}
	visible, _ := scanned.releaseIDs(t, album)
	if !maps.Equal(before, f.source.calls) {
		t.Fatalf("新成员尚未确认，出站记录 %v → %v", before, f.source.calls)
	}
	confirmProgramRelease(t, f.application, &f.admin, visible)
	f.clock.advance(t, time.Minute)
	changed := f.assertRelease(t, visible, "unlisted", "search")
	f.assertCount(t, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, visible)
	if changed.InputDigest == first.InputDigest || f.source.calls["SearchReleases"] != before["SearchReleases"]+2 {
		t.Fatalf("增加曲目后状态 %+v，调用 %v", changed, f.source.calls)
	}
}

func requireUnattemptedProgramState(t *testing.T, s curation.WorkState) {
	t.Helper()
	if s.Status != "pending" || s.Attempts != 0 || s.Generation != 0 || s.RetryCount != 0 || s.NextAttemptAt != nil {
		t.Fatalf("尚未处理的工作状态 %+v", s)
	}
}

func confirmProgramRelease(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, id string) {
	t.Helper()
	mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, id)
	runScan(t, app, admin)
}
