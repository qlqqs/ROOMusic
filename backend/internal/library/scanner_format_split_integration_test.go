package library

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanFormatSplitVersionGroup(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "首扫"
		if legacy {
			name = "已有旧混合发行"
		}
		t.Run(name, func(t *testing.T) {
			f := newLibraryFixture(t)
			root := insertScannerRoot(t, f, "format-split")
			service := scannerTestApplication(f)
			paths := map[string]int{}
			for position := 1; position <= 2; position++ {
				flacPath := fmt.Sprintf("Album/%02d.flac", position)
				mp3Path := fmt.Sprintf("Album/%02d.mp3", position)
				writeAudio(t, root, flacPath, audioFile{seed: byte(position), title: fmt.Sprintf("Track %d", position), album: "Album", track: position})
				writeFixtureFile(t, root, mp3Path, formatSplitMP3(position))
				paths[flacPath], paths[mp3Path] = position, position
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := f.database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}

			oldRelease, oldGroup := "", ""
			var oldTracks map[string]string
			if legacy {
				oldRelease, oldGroup = uuid.New(), uuid.New()
				mediumID := uuid.New()
				exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, oldGroup)
				anchor := ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: ingest.CandidateOrdinary, Scope: "Album"})
				exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind,edit_revision)
					VALUES($1::uuid,$2::uuid,'Album','Artist',$3::uuid,'Album',$4,'ordinary_directory',9)`, oldRelease, oldGroup, root.ID, anchor)
				exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, mediumID, oldRelease)
				// 合法的旧扫描结果：同一介质混有两种格式，轨位各自为 1..2，来源身份仍绑定原路径。
				// 不先运行新 organizer 再改回，避免夹具实际上已经完成格式切分。
				for path, position := range paths {
					codec, sourceKind := "flac", "flac_vorbis_comment"
					if path == "Album/01.mp3" || path == "Album/02.mp3" {
						codec, sourceKind = "mp3", "mp3_id3v2"
					}
					exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_identity,codec,source_kind,source_status,observed_at)
						VALUES($1::uuid,$2::uuid,$3,$4,'Artist',1,$5::uuid,$6,$7,$8,$9,'present',NOW()-INTERVAL '1 hour')`,
						uuid.New(), mediumID, position, fmt.Sprintf("Track %d", position), root.ID, path,
						ingest.SourceIdentityForObservation(root.ID, ingest.SourceObservation{RelativePath: path}), codec, sourceKind)
				}
				exec(`UPDATE release_groups SET default_release_id=$1::uuid,edit_revision=7 WHERE id=$2::uuid`, oldRelease, oldGroup)
				oldTracks = presentTrackIDs(t, f, root.ID)
				before := visibleReleases(t, f, root.ID)
				if len(oldTracks) != 4 || len(before) != 1 || before[0].ID != oldRelease || before[0].Kind != "ordinary_directory" || before[0].Tracks != 4 {
					t.Fatalf("旧混合夹具不成立: 发行=%+v，曲目=%v", before, oldTracks)
				}
				assertDatabaseCount(t, f, `SELECT COUNT(DISTINCT position) FROM tracks WHERE medium_id=$1::uuid`, 2, mediumID)
				assertDatabaseCount(t, f, `SELECT COUNT(DISTINCT codec) FROM tracks WHERE medium_id=$1::uuid`, 2, mediumID)
			}

			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			first := readFormatSplitPlacements(t, f, root.ID, paths)
			flac, mp3 := first["Album/01.flac"], first["Album/01.mp3"]
			if flac.ReleaseID == mp3.ReleaseID || flac.GroupID != mp3.GroupID {
				t.Fatalf("两种格式未成为同组的两个版本: flac=%+v mp3=%+v", flac, mp3)
			}
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM release_groups`, 1)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM media m JOIN releases r ON r.id=m.release_id WHERE r.source_root_id=$1::uuid`, 2, root.ID)
			if legacy {
				// 两个候选与旧发行重叠相同，旧锚点又不是格式锚点，按候选键升序由 flac 沿用。
				if flac.ReleaseID != oldRelease || flac.GroupID != oldGroup || mp3.ReleaseID == oldRelease {
					t.Fatalf("旧发行未沿用或新版本未加入旧组: 旧=%s/%s，flac=%+v mp3=%+v", oldRelease, oldGroup, flac, mp3)
				}
				if got := presentTrackIDs(t, f, root.ID); !reflect.DeepEqual(got, oldTracks) {
					t.Fatalf("升级改变了曲目身份: %v → %v", oldTracks, got)
				}
			} else {
				exec(`UPDATE release_groups SET default_release_id=$1::uuid,edit_revision=7 WHERE id=$2::uuid`, flac.ReleaseID, flac.GroupID)
				exec(`UPDATE releases SET edit_revision=9 WHERE id=$1::uuid`, flac.ReleaseID)
			}
			assertManualState := func() {
				t.Helper()
				assertDatabaseCount(t, f, `SELECT COUNT(*) FROM release_groups WHERE id=$1::uuid AND default_release_id=$2::uuid AND edit_revision=7`, 1, flac.GroupID, flac.ReleaseID)
				assertDatabaseCount(t, f, `SELECT edit_revision FROM releases WHERE id=$1::uuid`, 9, flac.ReleaseID)
			}
			assertManualState()
			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			if again := readFormatSplitPlacements(t, f, root.ID, paths); !reflect.DeepEqual(first, again) {
				t.Fatalf("重扫改变了曲目、发行、组或轨位: %v → %v", first, again)
			}
			assertManualState()

			// 模拟管理员把已有 mp3 版本分到另一组；扫描只能给新发行选组，不能搬走已有版本。
			manualGroup := uuid.New()
			exec(`INSERT INTO release_groups(id,default_release_id,edit_revision) VALUES($1::uuid,$2::uuid,13)`, manualGroup, mp3.ReleaseID)
			exec(`UPDATE releases SET group_id=$1::uuid,edit_revision=11 WHERE id=$2::uuid`, manualGroup, mp3.ReleaseID)
			for path, placement := range first {
				if placement.ReleaseID == mp3.ReleaseID {
					placement.GroupID = manualGroup
					first[path] = placement
				}
			}
			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			if again := readFormatSplitPlacements(t, f, root.ID, paths); !reflect.DeepEqual(first, again) {
				t.Fatalf("扫描覆盖了人工归组或更换身份: %v → %v", first, again)
			}
			assertManualState()
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM release_groups WHERE id=$1::uuid AND default_release_id=$2::uuid AND edit_revision=13`, 1, manualGroup, mp3.ReleaseID)
			assertDatabaseCount(t, f, `SELECT edit_revision FROM releases WHERE id=$1::uuid`, 11, mp3.ReleaseID)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 4, root.ID)
			requireNoMissingTracks(t, f, root.ID)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM scan_observations WHERE root_id=$1::uuid`, 0, root.ID)
		})
	}
}

type formatSplitPlacement struct {
	TrackID, ReleaseID, GroupID string
	Position                    int
}

func readFormatSplitPlacements(t *testing.T, f *libraryFixture, rootID string, paths map[string]int) map[string]formatSplitPlacement {
	t.Helper()
	rows, err := f.database.Query(`SELECT t.relative_path,t.id::text,r.id::text,r.group_id::text,t.position,m.position,t.disc_number,t.codec,r.candidate_kind,r.candidate_anchor
		FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
		WHERE t.source_root_id=$1::uuid AND t.source_status='present' ORDER BY t.relative_path`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	placements := map[string]formatSplitPlacement{}
	releasesByFormat := map[string]string{}
	for rows.Next() {
		var path, codec, kind, anchor string
		var mediumPosition, disc int
		var placement formatSplitPlacement
		if err := rows.Scan(&path, &placement.TrackID, &placement.ReleaseID, &placement.GroupID, &placement.Position, &mediumPosition, &disc, &codec, &kind, &anchor); err != nil {
			t.Fatal(err)
		}
		wantPosition, exists := paths[path]
		wantCodec := "flac"
		if path == "Album/01.mp3" || path == "Album/02.mp3" {
			wantCodec = "mp3"
		}
		wantAnchor := ingest.CandidateIdentity(rootID, ingest.CandidateAnchor{Kind: ingest.CandidateFormatSplit, Scope: "Album", Partition: "format:" + wantCodec})
		if !exists || placement.Position != wantPosition || mediumPosition != 1 || disc != 1 || codec != wantCodec || kind != "format_split" || anchor != wantAnchor {
			t.Fatalf("格式版本或轨位不符: 路径=%s，位置=%+v，介质=%d，碟=%d，格式=%s，种类=%s，锚点=%s", path, placement, mediumPosition, disc, codec, kind, anchor)
		}
		if previous := releasesByFormat[codec]; previous != "" && previous != placement.ReleaseID {
			t.Fatalf("同一种格式被拆为多个发行: %s", codec)
		}
		releasesByFormat[codec] = placement.ReleaseID
		placements[path] = placement
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(placements) != len(paths) || len(releasesByFormat) != 2 {
		t.Fatalf("格式切分后的成员不完整: %v", placements)
	}
	return placements
}

// formatSplitMP3 与现有歌词夹具一样，使用 ID3v2.3 标签和合成 MPEG 帧字节，不包含真实音乐。
func formatSplitMP3(position int) []byte {
	var body []byte
	for _, tag := range []struct{ id, value string }{
		{"TIT2", fmt.Sprintf("Track %d", position)}, {"TPE1", "Artist"},
		{"TALB", "Album"}, {"TRCK", fmt.Sprint(position)},
	} {
		payload := append([]byte{3}, tag.value...)
		body = append(body, tag.id...)
		body = append(body, byte(len(payload)>>24), byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)), 0, 0)
		body = append(body, payload...)
	}
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(len(body)>>21) & 0x7f, byte(len(body)>>14) & 0x7f, byte(len(body)>>7) & 0x7f, byte(len(body)) & 0x7f}
	content := append(header, body...)
	return append(content, bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x00, byte(position)}, 64)...)
}
