package library

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

type releaseRow struct {
	ID, Title, Directory, Kind string
	Tracks                     int // present 曲目数
}

// visibleReleases 返回 root 中至少有一条 present 曲目的 Release。
func visibleReleases(t *testing.T, fixture *libraryFixture, rootID string) []releaseRow {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT r.id::text,r.title,COALESCE(r.relative_directory,''),COALESCE(r.candidate_kind,''),
			COUNT(t.id) FILTER (WHERE t.source_status='present')
		FROM releases r
		LEFT JOIN media m ON m.release_id=r.id
		LEFT JOIN tracks t ON t.medium_id=m.id
		WHERE r.source_root_id=$1::uuid
		GROUP BY r.id
		HAVING COUNT(t.id) FILTER (WHERE t.source_status='present')>0
		ORDER BY r.title,r.relative_directory,r.id`, rootID)
	if err != nil {
		t.Fatalf("read releases: %v", err)
	}
	defer rows.Close()
	var releases []releaseRow
	for rows.Next() {
		var row releaseRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Directory, &row.Kind, &row.Tracks); err != nil {
			t.Fatalf("scan release: %v", err)
		}
		releases = append(releases, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate releases: %v", err)
	}
	return releases
}

// releaseIDsByTitle 返回可见 Release 的 标题 → ID；标题必须唯一。
func releaseIDsByTitle(t *testing.T, fixture *libraryFixture, rootID string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, release := range visibleReleases(t, fixture, rootID) {
		if _, duplicate := ids[release.Title]; duplicate {
			t.Fatalf("Release 标题 %q 重复", release.Title)
		}
		ids[release.Title] = release.ID
	}
	return ids
}

func releaseOfTrack(t *testing.T, fixture *libraryFixture, trackID string) string {
	t.Helper()
	var releaseID string
	if err := fixture.database.QueryRow(`SELECT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=$1::uuid`, trackID).Scan(&releaseID); err != nil {
		t.Fatalf("read release of track: %v", err)
	}
	return releaseID
}

func requireSameReleases(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("Release 集合变化：%v → %v", before, after)
	}
	for title, id := range before {
		if after[title] != id {
			t.Fatalf("Release %q 的 ID 变了：%q → %q", title, id, after[title])
		}
	}
}

func TestPostgreSQLAddedEarlierFileKeepsReleaseIdentity(t *testing.T) {
	t.Run("loose album", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "added-earlier-loose")
		service := scannerTestApplication(fixture)
		writeAudio(t, root, "02 Two.flac", audioFile{seed: 2, title: "Two", album: "Loose Album", track: 2})
		writeAudio(t, root, "03 Three.flac", audioFile{seed: 3, title: "Three", album: "Loose Album", track: 3})
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := visibleReleases(t, fixture, root.ID)
		if len(before) != 1 || before[0].Kind != string(ingest.CandidateLooseAlbum) || before[0].Tracks != 2 {
			t.Fatalf("首次扫描的 Release = %+v，期望一个散落专辑", before)
		}

		// 路径最靠前的文件改变了候选的定位锚点，但专辑仍是同一个 Release。
		writeAudio(t, root, "01 One.flac", audioFile{seed: 1, title: "One", album: "Loose Album", track: 1})
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		after := visibleReleases(t, fixture, root.ID)
		if len(after) != 1 || after[0].ID != before[0].ID || after[0].Tracks != 3 {
			t.Fatalf("加入更靠前的文件后 Release = %+v，期望沿用 %q 并含 3 首", after, before[0].ID)
		}
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	})

	t.Run("same directory split", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "added-earlier-split")
		service := scannerTestApplication(fixture)
		for index, album := range []string{"X", "Y", "Z"} {
			lower := string(rune('x' + index))
			writeAudio(t, root, "Mixed/"+lower+"2.flac", audioFile{seed: byte(10 + index*2), title: album + " two", album: album, track: 2})
			writeAudio(t, root, "Mixed/"+lower+"3.flac", audioFile{seed: byte(11 + index*2), title: album + " three", album: album, track: 3})
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := releaseIDsByTitle(t, fixture, root.ID)
		if len(before) != 3 {
			t.Fatalf("同目录三张专辑应拆成三个 Release：%v", before)
		}
		for _, release := range visibleReleases(t, fixture, root.ID) {
			if release.Kind != string(ingest.CandidateSameDirSplit) {
				t.Fatalf("Release %+v 不是同目录拆分候选", release)
			}
		}

		writeAudio(t, root, "Mixed/x1.flac", audioFile{seed: 30, title: "X one", album: "X", track: 1})
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		requireSameReleases(t, before, releaseIDsByTitle(t, fixture, root.ID))
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 3, root.ID)
	})
}

func TestPostgreSQLRemovedSmallestFileKeepsReleaseIdentity(t *testing.T) {
	t.Run("loose album", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "removed-smallest-loose")
		service := scannerTestApplication(fixture)
		for index, name := range []string{"01 One", "02 Two", "03 Three"} {
			writeAudio(t, root, name+".flac", audioFile{seed: byte(index + 1), title: name, album: "Loose Album", track: index + 1})
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := releaseIDsByTitle(t, fixture, root.ID)

		moveFixture(t, root, "01 One.flac", "../outside.flac") // 移出 root：该文件从 root 中消失
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		requireSameReleases(t, before, releaseIDsByTitle(t, fixture, root.ID))
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
		if releases := visibleReleases(t, fixture, root.ID); releases[0].Tracks != 2 {
			t.Fatalf("删除最靠前文件后的 present 曲目 = %d，期望 2", releases[0].Tracks)
		}
	})

	t.Run("same directory split", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "removed-smallest-split")
		service := scannerTestApplication(fixture)
		for index, album := range []string{"X", "Y", "Z"} {
			lower := string(rune('x' + index))
			for number := 1; number <= 3; number++ {
				if lower != "x" && number == 1 {
					continue // 只有 X 有第一首，使 X 的最小路径是 x1
				}
				writeAudio(t, root, "Mixed/"+lower+string(rune('0'+number))+".flac", audioFile{seed: byte(10*index + number), title: album + string(rune('0'+number)), album: album, track: number})
			}
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := releaseIDsByTitle(t, fixture, root.ID)
		if len(before) != 3 {
			t.Fatalf("同目录三张专辑应拆成三个 Release：%v", before)
		}

		moveFixture(t, root, "Mixed/x1.flac", "../outside.flac")
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		requireSameReleases(t, before, releaseIDsByTitle(t, fixture, root.ID))
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 3, root.ID)
	})
}

func TestPostgreSQLDirectoryRenameKeepsReleaseIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "release-directory-rename")
	service := scannerTestApplication(fixture)
	for number, name := range []string{"01.flac", "02.flac", "03.flac"} {
		writeAudio(t, root, "Old Name/"+name, audioFile{seed: byte(number + 1), title: name, album: "Album", track: number + 1})
	}
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := visibleReleases(t, fixture, root.ID)
	if len(before) != 1 {
		t.Fatalf("首次扫描的 Release = %+v", before)
	}
	var groupID string
	if err := fixture.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, before[0].ID).Scan(&groupID); err != nil {
		t.Fatalf("read release group: %v", err)
	}

	moveFixture(t, root, "Old Name", "New Name")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := visibleReleases(t, fixture, root.ID)
	if len(after) != 1 || after[0].ID != before[0].ID || after[0].Directory != "New Name" || after[0].Tracks != 3 {
		t.Fatalf("目录改名后 Release = %+v，期望沿用 %q 并记录新位置", after, before[0].ID)
	}
	var anchor, currentGroup string
	if err := fixture.database.QueryRow(`SELECT candidate_anchor,group_id::text FROM releases WHERE id=$1::uuid`, before[0].ID).Scan(&anchor, &currentGroup); err != nil {
		t.Fatalf("read release location: %v", err)
	}
	if want := ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: ingest.CandidateOrdinary, Scope: "New Name"}); anchor != want {
		t.Fatalf("锚点 = %q，期望更新为最近一次的位置 %q", anchor, want)
	}
	if currentGroup != groupID {
		t.Fatalf("ReleaseGroup 变了：%q → %q", groupID, currentGroup)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_groups`, 1)
}

func TestPostgreSQLMovedFileJoinsTargetRelease(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "moved-joins-target")
	service := scannerTestApplication(fixture)
	for number := 1; number <= 3; number++ {
		writeAudio(t, root, "A/a"+string(rune('0'+number))+".flac", audioFile{seed: byte(number), title: "A" + string(rune('0'+number)), album: "A", track: number})
	}
	for number := 1; number <= 2; number++ {
		writeAudio(t, root, "B/b"+string(rune('0'+number))+".flac", audioFile{seed: byte(10 + number), title: "B" + string(rune('0'+number)), album: "B", track: number})
	}
	// 一首属于 B 的曲目放错在 A 目录里：A 目录里 A 专辑占多数，它作为杂音留在 A 的 Release 中。
	writeAudio(t, root, "A/b3.flac", audioFile{seed: 20, title: "B3", album: "B", track: 3})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := releaseIDsByTitle(t, fixture, root.ID)
	strayID := presentTrackIDs(t, fixture, root.ID)["A/b3.flac"]
	if len(before) != 2 || releaseOfTrack(t, fixture, strayID) != before["A"] {
		t.Fatalf("放错位置的曲目应留在 A 的 Release 中：%v", before)
	}

	moveFixture(t, root, "A/b3.flac", "B/b3.flac")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	requireSameReleases(t, before, releaseIDsByTitle(t, fixture, root.ID))
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "B/b3.flac", strayID)
	if got := releaseOfTrack(t, fixture, strayID); got != before["B"] {
		t.Fatalf("移动后的曲目属于 Release %q，期望目标专辑 %q", got, before["B"])
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLSplitReleaseKeepsIdentityForLargerPart(t *testing.T) {
	setup := func(t *testing.T, tracks int) (*libraryFixture, registeredRoot, *Service, string) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "split-release")
		service := scannerTestApplication(fixture)
		for number := 1; number <= tracks; number++ {
			writeAudio(t, root, "Album/0"+string(rune('0'+number))+".flac", audioFile{seed: byte(number), title: "T" + string(rune('0'+number)), album: "Album", track: number})
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		releases := visibleReleases(t, fixture, root.ID)
		if len(releases) != 1 || releases[0].Tracks != tracks {
			t.Fatalf("首次扫描的 Release = %+v", releases)
		}
		return fixture, root, service, releases[0].ID
	}
	requireSplit := func(t *testing.T, fixture *libraryFixture, root registeredRoot, originalID, originalDirectory string, originalTracks, otherTracks int) {
		t.Helper()
		releases := visibleReleases(t, fixture, root.ID)
		if len(releases) != 2 {
			t.Fatalf("拆分后的 Release = %+v，期望两个", releases)
		}
		for _, release := range releases {
			if release.ID == originalID {
				if release.Directory != originalDirectory || release.Tracks != originalTracks {
					t.Fatalf("沿用原 ID 的一方 = %+v，期望在 %q 并含 %d 首", release, originalDirectory, originalTracks)
				}
			} else if release.Tracks != otherTracks {
				t.Fatalf("另一方 = %+v，期望含 %d 首", release, otherTracks)
			}
		}
	}

	t.Run("the larger part keeps the identity", func(t *testing.T) {
		fixture, root, service, originalID := setup(t, 5)
		// 3 首移到新目录，2 首留在原处：重叠更多的一方沿用，即使它不在原位置。
		for _, name := range []string{"03", "04", "05"} {
			moveFixture(t, root, "Album/"+name+".flac", "Split/"+name+".flac")
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		requireSplit(t, fixture, root, originalID, "Split", 3, 2)
		requireNoMissingTracks(t, fixture, root.ID)
	})

	t.Run("a tie stays in place", func(t *testing.T) {
		fixture, root, service, originalID := setup(t, 4)
		for _, name := range []string{"03", "04"} {
			moveFixture(t, root, "Album/"+name+".flac", "Split/"+name+".flac")
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		requireSplit(t, fixture, root, originalID, "Album", 2, 2)
		requireNoMissingTracks(t, fixture, root.ID)
	})
}

func TestPostgreSQLMinorityOverlapCreatesNewRelease(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "minority-overlap")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := visibleReleases(t, fixture, root.ID)
	oldTracks := presentTrackIDs(t, fixture, root.ID)

	// 新增的文件比原有的还多：原有曲目只占 2/5，不过半，候选不沿用原 Release，避免新专辑继承旧发行的 ID。
	for number := 3; number <= 5; number++ {
		writeAudio(t, root, "Album/0"+string(rune('0'+number))+".flac", audioFile{seed: byte(number), title: "T" + string(rune('0'+number)), album: "Album", track: number})
	}
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := visibleReleases(t, fixture, root.ID)
	if len(after) != 1 || after[0].ID == before[0].ID || after[0].Tracks != 5 {
		t.Fatalf("Release = %+v，期望一个含 5 首的新 Release（旧 %q）", after, before[0].ID)
	}
	// 曲目身份不受影响：两条原有曲目仍是原来的 ID，只是归到了新的 Release。
	ids := presentTrackIDs(t, fixture, root.ID)
	for _, path := range []string{"Album/01.flac", "Album/02.flac"} {
		requireTrackAt(t, ids, path, oldTracks[path])
		if got := releaseOfTrack(t, fixture, oldTracks[path]); got != after[0].ID {
			t.Fatalf("曲目 %q 属于 Release %q，期望新 Release %q", path, got, after[0].ID)
		}
	}
	// 新旧 Release 的锚点相同：锚点只是位置信息，不再要求唯一。
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid AND candidate_anchor=$2`, 2, root.ID,
		ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: ingest.CandidateOrdinary, Scope: "Album"}))
}

func TestPostgreSQLExactlyHalfOverlapIsNotAMajority(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "half-overlap")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Single/01.flac", audioFile{seed: 1, title: "Only", album: "Single", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := visibleReleases(t, fixture, root.ID)

	// 1 首加 1 首：原有曲目恰好占一半，按严格过半的规则不沿用。
	writeAudio(t, root, "Single/02.flac", audioFile{seed: 2, title: "Added", album: "Single", track: 2})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := visibleReleases(t, fixture, root.ID)
	if len(after) != 1 || after[0].ID == before[0].ID || after[0].Tracks != 2 {
		t.Fatalf("Release = %+v，期望新 Release（旧 %q）", after, before[0].ID)
	}
}
