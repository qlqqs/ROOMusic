package library

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestPostgreSQLParseCacheFormatVersionUpgrade(t *testing.T) {
	testParseCacheVersionUpgrade(t, true)
}

func TestPostgreSQLParseCacheGroupingVersionUpgrade(t *testing.T) {
	testParseCacheVersionUpgrade(t, false)
}

// 版本属于不同包，归组版本还是常量；用编译覆盖在第二个进程真实升版本，
// 两个进程共用本用例的隔离 schema，既不修改实现文件，也不绕过包边界。
func testParseCacheVersionUpgrade(t *testing.T, format bool) {
	t.Helper()
	if os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_CASE") == t.Name() {
		runVersionUpgradedScan(t, format)
		return
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "version-upgrade")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 61, title: "One", album: "Album", track: 1})
	writeLyricsMP3(t, root, "Other/02.mp3", "Two", "")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	if len(statesBefore) != 2 {
		t.Fatalf("版本升级夹具应有两个候选：%+v", statesBefore)
	}
	releases := releaseIDsByTitle(t, fixture, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM library_parse_cache WHERE root_id=$1`, 2, root.ID)

	var schema string
	if err := fixture.database.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	scopedURL, err := postgrestest.ScopedURL(postgrestest.BaseURL(t), schema)
	if err != nil {
		t.Fatal(err)
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(packageDir, "..", "catalog", "ingest", "version.go")
	pattern := regexp.MustCompile(`(const GroupingVersion = )(\d+)`)
	if format {
		source = filepath.Join(packageDir, "parser", "version.go")
		pattern = regexp.MustCompile(`("\.flac"\s*:\s*)(\d+)`)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	match := pattern.FindSubmatchIndex(contents)
	if match == nil {
		t.Fatal("找不到要升级的版本声明")
	}
	version, err := strconv.Atoi(string(contents[match[4]:match[5]]))
	if err != nil {
		t.Fatal(err)
	}
	replacement := append([]byte{}, contents[:match[4]]...)
	replacement = strconv.AppendInt(replacement, int64(version+1), 10)
	replacement = append(replacement, contents[match[5]:]...)
	temporary := t.TempDir()
	replacementPath := filepath.Join(temporary, "version.go")
	if err := os.WriteFile(replacementPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{source: replacementPath}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-overlay="+overlayPath, "./internal/library", "-run", "^"+t.Name()+"$", "-count=1", "-v")
	command.Dir = filepath.Clean(filepath.Join(packageDir, "..", ".."))
	command.Env = append(os.Environ(),
		"ROOMUSIC_TEST_SCAN_VERSION_CASE="+t.Name(),
		"ROOMUSIC_TEST_SCAN_VERSION_URL="+scopedURL,
		"ROOMUSIC_TEST_SCAN_VERSION_ROOT="+root.ID,
		"ROOMUSIC_TEST_SCAN_VERSION_DIRECTORY="+fixture.allowedRoot,
		"ROOMUSIC_TEST_SCAN_VERSION_EXPECTED="+strconv.Itoa(version+1))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("升版本后的扫描失败：%v\n%s", err, output)
	}
	t.Logf("真实升版本子进程：\n%s", output)
	statesAfter := readIncrementalTestCandidateStates(t, fixture, root)
	if format {
		var flacRelease string
		if err := fixture.database.QueryRow(`SELECT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=$1::uuid`, before["Album/01.flac"]).Scan(&flacRelease); err != nil {
			t.Fatal(err)
		}
		assertIncrementalTestCandidateRewrites(t, statesBefore, statesAfter, flacRelease)
	} else {
		if len(statesAfter) != len(statesBefore) {
			t.Fatalf("归组升版丢失候选状态：%+v", statesAfter)
		}
		for key, old := range statesBefore {
			current, ok := statesAfter[key]
			if !ok || current.ReleaseID != old.ReleaseID || current.Signature == old.Signature || !current.UpdatedAt.After(old.UpdatedAt) {
				t.Fatalf("归组升版未重写全部候选：%s 前=%+v 后=%+v", key, old, current)
			}
		}
	}
	for path, id := range before {
		requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), path, id)
	}
	requireSameReleases(t, releases, releaseIDsByTitle(t, fixture, root.ID))
	requireNoMissingTracks(t, fixture, root.ID)
}

func runVersionUpgradedScan(t *testing.T, format bool) {
	t.Helper()
	database, err := postgres.Open(context.Background(), os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	fixture := &libraryFixture{database: database, allowedRoot: os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_DIRECTORY")}
	root := registeredRoot{ID: os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_ROOT")}
	if err := database.QueryRow(`SELECT path FROM library_roots WHERE id=$1`, root.ID).Scan(&root.Path); err != nil {
		t.Fatal(err)
	}
	wantVersion := os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_EXPECTED")
	wantParsed, wantHits := 0, 2
	wantWritten, wantSkipped := int64(2), int64(0)
	if format {
		want := "c" + strconv.Itoa(parser.CommonVersion) + ".f" + wantVersion
		if parser.CacheVersion(".flac") != want {
			t.Fatalf("FLAC 版本未真实升级：%s，期望 %s", parser.CacheVersion(".flac"), want)
		}
		wantParsed, wantHits = 1, 1
		wantWritten, wantSkipped = 1, 1
	} else if strconv.Itoa(ingest.GroupingVersion) != wantVersion {
		t.Fatalf("归组版本未真实升级：%d，期望 %s", ingest.GroupingVersion, wantVersion)
	}
	var logs bytes.Buffer
	service := scannerTestApplication(fixture)
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var summary struct {
			Event   string `json:"event"`
			Parsed  int    `json:"files_parsed"`
			Hits    int    `json:"parse_cache_hits"`
			Written int64  `json:"candidates_written"`
			Skipped int64  `json:"candidates_skipped"`
		}
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Event != "library.scan.incremental_summary" {
			continue
		}
		found = true
		if summary.Parsed != wantParsed || summary.Hits != wantHits || summary.Written != wantWritten || summary.Skipped != wantSkipped {
			t.Fatalf("升版本后统计=%+v，期望 parsed/hits=%d/%d written/skipped=%d/%d", summary, wantParsed, wantHits, wantWritten, wantSkipped)
		}
		t.Logf("升版本后 files_parsed=%d parse_cache_hits=%d", summary.Parsed, summary.Hits)
	}
	if !found {
		t.Fatal("缺少增量扫描统计日志")
	}
	for extension, count := range map[string]int{".flac": 1, ".mp3": 1} {
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM library_parse_cache WHERE root_id=$1 AND relative_path LIKE $2 AND parser_version=$3`, count, root.ID, "%"+extension, parser.CacheVersion(extension))
	}
}
