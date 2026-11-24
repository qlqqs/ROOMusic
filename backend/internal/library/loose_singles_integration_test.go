package library

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type looseSingleFixture struct {
	relative, isrc string
	picture        []byte
}

func writeLooseSingles(t *testing.T, root registeredRoot) []looseSingleFixture {
	t.Helper()
	singles := []struct{ name, title, artist, isrc string }{
		{"bravo", "晚风", "艺人乙", "USABC2300002"},
		{"alpha", "晨光", "艺人甲", "USABC2300001"},
		{"charlie", "夜雨", "艺人丙", "USABC2300003"},
	}
	var fixtures []looseSingleFixture
	for i, single := range singles {
		relative := "Singles/" + single.name + ".flac"
		seed := byte(i + 1)
		picture := jpegArtworkOfWidth(seed)
		block := parsertest.FLACPicture(3, "image/jpeg", uint32(seed), 1, picture)
		audio := parsertest.FLACWithAudio(t, [16]byte{seed}, []byte{seed},
			"TITLE="+single.title, "ARTIST="+single.artist, "ISRC="+single.isrc,
			"METADATA_BLOCK_PICTURE="+base64.StdEncoding.EncodeToString(block))
		writeFixtureFile(t, root, relative, audio)
		fixtures = append(fixtures, looseSingleFixture{relative, single.isrc, picture})
	}
	return fixtures
}

func requireLooseSingles(t *testing.T, fixture *libraryFixture, root registeredRoot, count int) {
	t.Helper()
	releases := visibleReleases(t, fixture, root.ID)
	if len(releases) != count {
		t.Fatalf("发行 = %+v，期望 %d 个单曲发行", releases, count)
	}
	for _, release := range releases {
		if release.Kind != string(ingest.CandidateLooseUnknown) || release.Tracks != 1 ||
			release.Directory != "Singles" || release.Title != "未知专辑" {
			t.Errorf("散曲发行 = %+v，期望 Singles 下的单轨 loose_unknown 发行，原始标题为未知专辑", release)
		}
	}
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLLooseSinglesEmbeddedArtworkAndRescan(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "loose-singles")
	service, logs := newIncrementalTestService(t, fixture)
	singles := writeLooseSingles(t, root)
	writeFixtureFile(t, root, "Singles/cover.jpg", jpegArtworkOfWidth(9))

	runIncrementalTestScan(t, service, logs, root)
	requireLooseSingles(t, fixture, root, len(singles))
	before := readIncrementalTestTracks(t, fixture, root)
	for _, single := range singles {
		assertArtworkPriorityBinding(t, fixture, service, root, single.relative, "loose_unknown", "embedded", single.picture)
		requireScannedIdentifiers(t, fixture, before[single.relative].ID, scannedIdentifier{"isrc", single.isrc, 0})
	}

	runIncrementalTestScan(t, service, logs, root)
	requireLooseSingles(t, fixture, root, len(singles))
	for _, single := range singles {
		id := assertArtworkPriorityBinding(t, fixture, service, root, single.relative, "loose_unknown", "embedded", single.picture)
		if id != before[single.relative].ReleaseID {
			t.Errorf("%s 重扫后发行 ID = %q，期望沿用 %q", single.relative, id, before[single.relative].ReleaseID)
		}
	}
}

func TestPostgreSQLLooseSinglesGroupingUpgradeKeepsSmallestKey(t *testing.T) {
	if os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_CASE") == t.Name() {
		fixture, root := openVersionScanFixture(t)
		if strconv.Itoa(ingest.GroupingVersion) != os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_EXPECTED") {
			t.Fatalf("旧归组版本未生效，实际为 %d", ingest.GroupingVersion)
		}
		service, logs := newIncrementalTestService(t, fixture)
		runIncrementalTestScan(t, service, logs, root)
		return
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "loose-singles-upgrade")
	singles := writeLooseSingles(t, root)

	// 在隔离库里先按第 3 版归组，然后用现行规则增量重扫原文件。
	ingestDir := filepath.Join("..", "catalog", "ingest")
	versionPath := filepath.Join(ingestDir, "version.go")
	organizerPath := filepath.Join(ingestDir, "organizer.go")
	legacyOrganizer, err := os.ReadFile(organizerPath)
	if err != nil {
		t.Fatal(err)
	}
	gate := []byte("meta.kind == CandidateOrdinary && directoryLooseSingles(meta.scope, observations, filenameEvidence)")
	if bytes.Count(legacyOrganizer, gate) != 1 {
		t.Fatal("未找到可关闭的散曲归组条件")
	}
	legacyOrganizer = bytes.Replace(legacyOrganizer, gate, []byte("false"), 1)
	const oldGroupingVersion = 3
	version, legacyVersion := scanVersionReplacement(t, versionPath, regexp.MustCompile(`(const GroupingVersion = )(\d+)`), oldGroupingVersion-ingest.GroupingVersion)
	runScanWithSourceOverlay(t, fixture, root, version, map[string][]byte{
		versionPath: legacyVersion, organizerPath: legacyOrganizer,
	})

	releases := visibleReleases(t, fixture, root.ID)
	if len(releases) != 1 || releases[0].Kind != string(ingest.CandidateOrdinary) ||
		releases[0].Title != "Singles" || releases[0].Tracks != len(singles) {
		t.Fatalf("旧归组发行 = %+v，期望名为 Singles 的普通目录发行，含 %d 首", releases, len(singles))
	}
	oldID := releases[0].ID
	oldTracks := presentTrackIDs(t, fixture, root.ID)
	var oldGroup string
	var revision int64
	if err := fixture.database.QueryRow(`SELECT group_id::text, edit_revision FROM releases WHERE id=$1`, oldID).Scan(&oldGroup, &revision); err != nil {
		t.Fatal(err)
	}

	const correctedTitle = "手工修正的发行标题"
	actor := uuid.New()
	if _, err := fixture.database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'测试管理员','synthetic','admin')`, actor); err != nil {
		t.Fatal(err)
	}
	request := governance.CorrectionRequest{
		ReleaseID: oldID, ExpectedRevision: revision,
		Changes: map[string]governance.CorrectionChange{"title": {Mode: "set", Text: correctedTitle}},
	}
	correction, err := operations.Run(t.Context(), fixture.database, operations.Spec{
		ActorID: actor, Type: operations.TypeReleaseCorrection, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "loose-singles-upgrade",
	}, governance.CorrectionExecutor(request))
	if err != nil {
		t.Fatalf("修正旧发行失败：%v", err)
	}

	service, logs := newIncrementalTestService(t, fixture)
	_, summary := runIncrementalTestScan(t, service, logs, root)
	if summary.ParseCacheHits != int64(len(singles)) || summary.FilesParsed != 0 {
		t.Fatalf("升版应复用全部解析缓存：%+v", summary)
	}
	requireLooseSingles(t, fixture, root, len(singles))
	newTracks := readIncrementalTestTracks(t, fixture, root)
	groups := map[string]bool{}
	for _, single := range singles {
		track := newTracks[single.relative]
		if track.ID == "" || track.ID != oldTracks[single.relative] {
			t.Errorf("%s 曲目 ID = %q，期望沿用 %q", single.relative, track.ID, oldTracks[single.relative])
		}
		var group, title string
		if err := fixture.database.QueryRow(`SELECT group_id::text, title FROM release_effective WHERE id=$1`, track.ReleaseID).Scan(&group, &title); err != nil {
			t.Fatalf("读取 %s 所属发行：%v", single.relative, err)
		}
		if groups[group] {
			t.Errorf("%s 的单曲发行共用了专辑组 %s", single.relative, group)
		}
		groups[group] = true
		if single.relative == "Singles/alpha.flac" {
			if track.ReleaseID != oldID || group != oldGroup || title != correctedTitle {
				t.Errorf("键最小的曲目发行、组和有效标题 = %q/%q/%q，期望 %q/%q/%q", track.ReleaseID, group, title, oldID, oldGroup, correctedTitle)
			}
			assertDatabaseCount(t, fixture, `SELECT count(*) FROM release_corrections WHERE release_id=$1 AND field='title' AND operation_id=$2`, 1, track.ReleaseID, correction.OperationID)
		} else if track.ReleaseID == oldID || group == oldGroup || title != "未知专辑" {
			t.Errorf("%s 的发行、组和有效标题 = %q/%q/%q，期望新发行、新组与未知专辑", single.relative, track.ReleaseID, group, title)
		}
	}
}
