package library

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

const directoryTraceSchema = "ROOMUSIC_TEST_CONFIRMATION_TRACE_SCHEMA"

type directoryReadCounts struct {
	directories map[string]int
	files       map[string]int
}

func confirmationScan(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	directoryOK(t, s.StartCurationConfirmation(ctx))
	directoryOK(t, s.WaitForScans(ctx))
}

// 只跟踪子测试进程的目录枚举和读数据系统调用，-s 0 省略读到的数据。
func confirmationReadCounts(t *testing.T, f *libraryFixture, root registeredRoot, strace string) directoryReadCounts {
	t.Helper()
	var schema string
	directoryOK(t, f.database.QueryRowContext(t.Context(), "SELECT current_schema()").Scan(&schema))
	binary, err := os.Executable()
	directoryOK(t, err)
	tracePath := filepath.Join(t.TempDir(), "confirmation.trace")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, strace, "-qq", "-f", "-y", "-s", "0", "-e", "trace=getdents64,read,pread64,readv,preadv,preadv2",
		"-o", tracePath, binary, "-test.run", "^TestPostgreSQLDirectoryConfirmationReads$")
	child.Env = append(os.Environ(), directoryTraceSchema+"="+schema)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("跟踪确认扫描失败：%v\n%s", err, output)
	}
	trace, err := os.Open(tracePath)
	directoryOK(t, err)
	defer trace.Close()
	counts := directoryReadCounts{directories: map[string]int{}, files: map[string]int{}}
	lines := bufio.NewScanner(trace)
	for lines.Scan() {
		line := lines.Text()
		before, descriptor, ok := strings.Cut(line, "<")
		if !ok {
			continue
		}
		encodedPath, _, ok := strings.Cut(descriptor, ">")
		if !ok {
			continue
		}
		path, err := strconv.Unquote(`"` + encodedPath + `"`)
		if err != nil {
			continue
		}
		if path == root.Path {
			path = ""
		} else {
			path, ok = strings.CutPrefix(path, root.Path+"/")
			if !ok {
				continue
			}
		}
		if strings.Contains(before, "getdents64(") {
			counts.directories[path]++
		} else {
			counts.files[path]++
		}
	}
	directoryOK(t, lines.Err())
	return counts
}

func TestPostgreSQLDirectoryConfirmationReads(t *testing.T) {
	if schema := os.Getenv(directoryTraceSchema); schema != "" {
		url, err := postgrestest.ScopedURL(postgrestest.BaseURL(t), schema)
		directoryOK(t, err)
		database, err := postgres.Open(t.Context(), url)
		directoryOK(t, err)
		defer database.Close()
		confirmationScan(t, scannerTestApplication(&libraryFixture{database: database, allowedRoot: t.TempDir()}))
		return
	}

	t.Run("分时到期的兄弟目录实际读取", func(t *testing.T) {
		f := newLibraryFixture(t)
		strace, err := exec.LookPath("strace")
		if err != nil {
			t.Skip("实际枚举和读取计数需要 Linux strace")
		}
		s := scannerTestApplication(f)
		root := insertScannerRoot(t, f, "confirmation")
		for i, album := range []string{"First", "Second"} {
			writeDirectoryTrack(t, root, album+"/01.flac", album, byte(61+i))
		}
		boundaryCompletedScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1`, 2, root.ID)
		directoryExec(t, f, `UPDATE releases SET curation_changed_at=now()+interval '1 hour' WHERE source_root_id=$1`, root.ID)

		for i, pair := range [][2]string{{"First", "Second"}, {"Second", "First"}} {
			album, sibling := pair[0], pair[1]
			// 只改 mtime，要求到期曲目实际读文件，同时兄弟曲目的热缓存完整保留。
			setBorrowFullFileTime(t, root, album+"/01.flac", time.Date(2025, 1, i+1, 0, 0, 0, 0, time.UTC))
			directoryExec(t, f, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE relative_directory=$1`, album)
			counts := confirmationReadCounts(t, f, root, strace)
			if counts.directories[album] < 2 || counts.files[album+"/01.flac"] == 0 {
				t.Errorf("%s 要实际枚举并读取：目录 %v，文件 %v", album, counts.directories, counts.files)
			}
			for dir, count := range counts.directories {
				if dir != album && count != 0 {
					t.Errorf("确认 %s 时枚举了 %q 共 %d 次", album, dir, count)
				}
			}
			for path, count := range counts.files {
				if !strings.HasPrefix(path, album+"/") && count != 0 {
					t.Errorf("确认 %s 时读取了 %q 共 %d 次", album, path, count)
				}
			}
			assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE relative_directory=$1 AND curation_confirmed`, 1, album)
			assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE relative_directory=$1 AND curation_confirmed`, i, sibling)
			assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library AND status='succeeded'`, i+1)
			confirmationScan(t, s)
			assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library`, i+1)
			assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1`, 2, root.ID)
		}
	})

	t.Run("归并发行的所有成员目录", func(t *testing.T) {
		f := newMergeScanFixture(t, 2, 2)
		f.merge()
		// 部分扫描不能把合并后的四首成员当成两首来确认。
		directoryExec(t, f.libraryFixture, `UPDATE releases SET curation_confirmed=false,curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, f.target)
		root := f.root
		root.revision = 1
		id := startDirectoryScan(t, f.service, scopedDirectoryOptions(root, []string{"A/CD1"}, nil))
		waitDirectoryScan(t, f.service, id, "succeeded")
		assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM releases WHERE id=$1 AND NOT curation_confirmed`, 1, f.target)
		directoryExec(t, f.libraryFixture, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, f.target)
		confirmationScan(t, f.service)
		report, err := f.service.GetScanReport(t.Context(), latestDirectoryScan(t, f.libraryFixture), 1, 20)
		directoryOK(t, err)
		if report.Files.Total == nil || *report.Files.Total != 4 {
			t.Errorf("归并确认应枚举两目录四首成员，实际 %+v", report.Files)
		}
		f.checkMerged(4, 2)
		// 首次完整观察建立归并后的基线，第二次确认签名稳定。
		assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM releases WHERE id=$1 AND curation_member_signature IS NOT NULL`, 1, f.target)
		directoryExec(t, f.libraryFixture, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, f.target)
		confirmationScan(t, f.service)
		assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM releases WHERE id=$1 AND curation_confirmed`, 1, f.target)
	})

	t.Run("多碟和跨目录 CUE 全部覆盖", func(t *testing.T) {
		f := newLibraryFixture(t)
		s := scannerTestApplication(f)
		root := insertScannerRoot(t, f, "compound-confirmation")
		writeArtworkPriorityFLAC(t, root, "Box/CD1/01.flac", "Box", 1, 70, nil)
		writeArtworkPriorityFLAC(t, root, "Box/CD2/01.flac", "Box", 2, 71, nil)
		writeDirectoryCUE(t, root)
		writeDirectoryTrack(t, root, "Other/01.flac", "Other", 72)
		boundaryCompletedScan(t, s)
		for _, album := range []string{"Box", "Cue Album"} {
			directoryExec(t, f, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE source_root_id=$1 AND title=$2`, root.ID, album)
			confirmationScan(t, s)
			id := latestDirectoryScan(t, f)
			report, err := s.GetScanReport(t.Context(), id, 1, 20)
			directoryOK(t, err)
			if report.Files.Total == nil || *report.Files.Total != 2 || report.Files.Processed != 2 {
				t.Errorf("%s 确认应读取两项且不扫兄弟目录：%+v", album, report.Files)
			}
			assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE source_root_id=$1 AND title=$2 AND curation_confirmed`, 1, root.ID, album)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE source_root_id=$1 AND title='Other' AND curation_attempted_change_at IS NOT NULL`, 0, root.ID)
	})

	t.Run("忙碌不领取、读取失败和成员变动", func(t *testing.T) {
		f := newLibraryFixture(t)
		clock := newDirectoryTestClock()
		s := scannerTestApplication(f).WithDirectoryWatchClock(clock)
		root := insertScannerRoot(t, f, "failed-confirmation")
		root.revision = 1
		const album = "Album"
		// 两首已入库曲目修复后仍占三首中的严格多数，可沿用发行 ID 检查新窗口。
		writeAudio(t, root, "Album/01.flac", audioFile{seed: 73, title: "First", album: album, track: 1})
		writeAudio(t, root, "Album/02.flac", audioFile{seed: 74, title: "Second", album: album, track: 2})
		writeDirectoryTrack(t, root, "Other/01.flac", "Other", 75)
		writeFixtureFile(t, root, "Album/03.flac", []byte("fLaC"))
		boundaryCompletedScan(t, s)
		release := releaseIDsByTitle(t, f, root.ID)[album]
		makeDue := func() {
			t.Helper()
			directoryExec(t, f, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id=$1`, release)
		}
		makeDue()
		gate := blockDirectoryProjection(t, f)
		blocker := scannerTestApplication(f)
		busy := startDirectoryScan(t, blocker, scopedDirectoryOptions(root, []string{"Other"}, nil))
		gate.wait(t)
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1 AND curation_attempted_change_at IS NULL`, 1, release)
		gate.release(t)
		waitDirectoryScan(t, blocker, busy, "succeeded")

		// 目录还在安静期内时也不能领取。
		watched := &watchedRoot{root: root, mode: "realtime"}
		s.watch.roots[root.ID] = watched
		s.watch.record(watched, watchEvent{dirs: []string{album}})
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1 AND curation_attempted_change_at IS NULL`, 1, release)
		clock.advance(watchQuiet)

		// 仍存在的损坏成员不能确认。同一失败窗口只尝试一次。
		confirmationScan(t, s)
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library`, 2)
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1 AND NOT curation_confirmed AND curation_attempted_change_at=curation_changed_at`, 1, release)

		// 修复文件并增加曲目，确认扫描本身观察到的新成员要开启新窗口。
		writeAudio(t, root, "Album/03.flac", audioFile{seed: 76, title: "Third", album: album, track: 3})
		makeDue()
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1 AND NOT curation_confirmed
			AND curation_changed_at>curation_attempted_change_at`, 1, release)
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library`, 3)
		makeDue()
		confirmationScan(t, s)
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library AND status='succeeded'`, 4)
		assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1 AND curation_confirmed
			AND curation_attempted_change_at=curation_changed_at`, 1, release)
		assertDatabaseCount(t, f, `SELECT count(*) FROM curation_scan_files WHERE release_id=$1`, 3, release)
	})
}
