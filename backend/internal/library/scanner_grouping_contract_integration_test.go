package library

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic"
)

func TestPostgreSQLScanGroupingDiscMediaAndRescan(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "grouping-media")
	service, logs := newIncrementalTestService(t, f)
	fixtures := []struct {
		path, album, scope, title string
		disc                      int
	}{
		{"Box/CD1 - 早期 (现场)/One.flac", "Early", "Box", "早期 (现场)", 1},
		{"Box/Disk II/Two.flac", "Later", "Box", "Medium", 2},
		{"Box/Bonus/Three.flac", "Bonus album tag", "Box", "Bonus", 3},
		{"Box/特典/Four.flac", "Bonus album tag", "Box", "特典", 4},
		{"Box/Notes/Five.flac", "Notes", "Box/Notes", "Medium", 1},
		{"Direct/One.flac", "Direct", "Direct", "Medium", 1},
		{"Direct/CD2/Two.flac", "Other title", "Direct", "Medium", 2},
		{"Solo/CD2/Only.flac", "Solo", "Solo", "Medium", 2},
	}
	for i, sample := range fixtures {
		writeAudio(t, root, sample.path, audioFile{seed: byte(i + 1), title: sample.path, album: sample.album, track: 1})
	}
	_, first := runIncrementalTestScan(t, service, logs, root)
	if first.CandidatesWritten != 4 {
		t.Fatalf("首扫应写入四张发行：%+v", first)
	}
	ids := map[string]string{}
	for _, sample := range fixtures {
		var scope, anchor, id, title string
		var disc int
		if err := f.database.QueryRow(`SELECT r.relative_directory,r.candidate_anchor,r.id::text,COALESCE(m.title,''),m.position
			FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.source_root_id=$1::uuid AND t.relative_path=$2`,
			root.ID, sample.path).Scan(&scope, &anchor, &id, &title, &disc); err != nil {
			t.Fatal(err)
		}
		kind := ingest.CandidateMultiDisc
		if sample.scope == "Box/Notes" {
			kind = ingest.CandidateOrdinary
		}
		wantAnchor := ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: kind, Scope: sample.scope})
		if scope != sample.scope || anchor != wantAnchor || title != sample.title || disc != sample.disc {
			t.Errorf("%s 目录、锚点、碟名、碟位 = %q %q %q %d；期望 %q %q %q %d",
				sample.path, scope, anchor, title, disc, sample.scope, wantAnchor, sample.title, sample.disc)
		}
		if previous := ids[scope]; previous != "" && previous != id {
			t.Errorf("%s 发行 ID = %s 和 %s，期望同一张", scope, id, previous)
		}
		ids[scope] = id
	}
	if len(ids) != 4 {
		t.Errorf("发行目录 = %v，期望恰好四处", ids)
	}
	tracks := readIncrementalTestTracks(t, f, root)
	states := readIncrementalTestCandidateStates(t, f, root)
	_, next := runIncrementalTestScan(t, service, logs, root)
	if next.CandidatesSkipped != 4 || next.CandidatesWritten != 0 ||
		!reflect.DeepEqual(tracks, readIncrementalTestTracks(t, f, root)) ||
		!reflect.DeepEqual(states, readIncrementalTestCandidateStates(t, f, root)) {
		t.Errorf("重扫改变介质、身份或签名状态：%+v", next)
	}
}

func TestPostgreSQLScanVersionTermsRetainIdentity(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "version-terms")
	service, logs := newIncrementalTestService(t, f)
	const title = "The Album (Subtitle)"
	const edition = "Numbered Edition"
	const from = "Album (Deluxe)"
	const to = "Album (Remastered 2009)"
	const name = "/One.flac"
	writeFixtureFile(t, root, from+name, parsertest.FLACWithAudio(t, [16]byte{7}, []byte{7},
		"TITLE=One", "ALBUM="+title, "ARTIST=Artist", "ALBUMARTIST=Artist", "EDITION="+edition, "TRACKNUMBER=1"))
	runIncrementalTestScan(t, service, logs, root)
	release := releaseIDsByTitle(t, f, root.ID)[title]
	track := presentTrackIDs(t, f, root.ID)[from+name]
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_effective WHERE id=$1::uuid AND title=$2 AND edition=$3`, 1, release, title, edition)
	assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1::uuid AND scan_version_terms=ARRAY['Deluxe','Subtitle']::text[]`, 1, release)
	moveFixture(t, root, from, to)
	_, next := runIncrementalTestScan(t, service, logs, root)
	if next.CandidatesWritten != 1 {
		t.Errorf("目录版本词变化未重写候选：%+v", next)
	}
	requireTrackAt(t, presentTrackIDs(t, f, root.ID), to+name, track)
	requireSameReleases(t, map[string]string{title: release}, releaseIDsByTitle(t, f, root.ID))
	assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1::uuid AND scan_version_terms=ARRAY['Remastered 2009','Subtitle']::text[]`, 1, release)
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_effective WHERE id=$1::uuid AND title=$2 AND edition=$3`, 1, release, title, edition)
}

func TestPostgreSQLScanFieldVotesConflictAndCorrection(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "field-vote-conflict")
	service, logs := newIncrementalTestService(t, f)
	for i, value := range []string{"Alpha", "Beta", "Beta", "Alpha"} {
		writeFixtureFile(t, root, fmt.Sprintf("Folder/%02d.flac", i+1), parsertest.FLACWithAudio(t, [16]byte{byte(i + 1)}, []byte{byte(i + 1)},
			"TITLE=Track", "ALBUM="+value, "LABEL="+value, "ARTIST=Artist", "ALBUMARTIST=Artist", fmt.Sprintf("TRACKNUMBER=%d", i+1)))
	}
	runIncrementalTestScan(t, service, logs, root)
	releases := visibleReleases(t, f, root.ID)
	if len(releases) != 1 || releases[0].Tracks != 4 || releases[0].Title != "未知专辑" {
		t.Fatalf("连续首数的 2:2 标题平票 = %+v，期望一张未知专辑", releases)
	}
	id := releases[0].ID
	assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1::uuid AND label IS NULL`, 1, id)
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_field_decisions WHERE release_id=$1::uuid AND field_key IN ('title','label')
		AND selected_value IS NULL AND reason='tied_candidates' AND action='uncertain_apply' AND candidates='["Alpha","Beta"]'::jsonb`, 2, id)

	store := governance.NewStore(f.database)
	assertIssues := func(wantFields ...string) {
		t.Helper()
		issues, err := store.LoadReleaseIssues(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if issues.Grouping != nil || issues.Release.AttentionCount != int64(len(wantFields)) {
			t.Errorf("待核对计数或独立归组问题 = %d / %+v，期望只有 %v", issues.Release.AttentionCount, issues.Grouping, wantFields)
		}
		var fields []string
		for _, field := range issues.Fields {
			fields = append(fields, field.Field)
			if field.Value != nil || field.ReasonCode == nil || *field.ReasonCode != "tied_candidates" ||
				!slices.Equal(field.Candidates, []string{"Alpha", "Beta"}) || field.Status != governance.IssueStatusOpen {
				t.Errorf("平票字段问题 = %+v", field)
			}
		}
		slices.Sort(fields)
		if !slices.Equal(fields, wantFields) {
			t.Errorf("问题字段 = %v，期望 %v", fields, wantFields)
		}
	}
	assertIssues("label", "title")
	actor := uuid.New()
	if _, err := f.database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'synthetic-editor','test-only','admin')`, actor); err != nil {
		t.Fatal(err)
	}
	changes, err := governance.ParseCorrectionChanges(map[string]governance.RawCorrectionChange{
		"title": {Mode: "set", Value: []byte(`"Edited title"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	correction, err := store.LoadCorrectionState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	request := governance.CorrectionRequest{ReleaseID: id, ExpectedRevision: correction.Revision, Changes: changes}
	if _, err := operations.Run(t.Context(), f.database, operations.Spec{ActorID: actor, Type: operations.TypeReleaseCorrection, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "synthetic-tie-correction"}, governance.CorrectionExecutor(request)); err != nil {
		t.Fatal(err)
	}
	requireScanStatus(t, runBorrowFullScan(t, f, service, root), "succeeded")
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_effective WHERE id=$1::uuid AND title='Edited title'`, 1, id)
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_field_decisions WHERE release_id=$1::uuid AND reason='tied_candidates'`, 2, id)
	assertIssues("label")
}

func TestPostgreSQLScanTrackNumbersAcrossReaders(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "track-numbers-readers")
	service, logs := newIncrementalTestService(t, f)
	paths := []string{"Numbers/Tag A.flac", "Numbers/Tag B.flac", "Numbers/Tag C.flac", "Numbers/Tag D.flac",
		"Numbers/Live 10 Take.flac", "Numbers/Live 2 Take.flac"}
	for i, path := range paths {
		comments := []string{"TITLE=" + path, "ARTIST=Artist", "ALBUMARTIST=Artist", "ALBUM=Numbers"}
		if i < 4 {
			comments = append(comments, fmt.Sprintf("TRACKNUMBER=%d", i%2+1))
		}
		writeFixtureFile(t, root, path, parsertest.FLACWithAudio(t, [16]byte{byte(i + 1)}, []byte{byte(i + 1)}, comments...))
	}
	initialScan, _ := runIncrementalTestScan(t, service, logs, root)
	ids := presentTrackIDs(t, f, root.ID)
	wantIDs := []string{ids[paths[0]], ids[paths[2]], ids[paths[1]], ids[paths[3]], ids[paths[5]], ids[paths[4]]}
	wantPositions := []int{1, 1, 2, 2, 0, 0}
	release := releaseIDsByTitle(t, f, root.ID)["Numbers"]
	var group string
	if err := f.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, release).Scan(&group); err != nil {
		t.Fatal(err)
	}

	for i, id := range wantIDs {
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1::uuid AND position=$2 AND sort_position=$3`, 1, id, wantPositions[i], i+1)
	}
	// 模拟旧解析缓存的默认 1 和旧规则保存的路径补号。
	if _, err := f.database.Exec(`UPDATE tracks SET position=sort_position,sort_position=NULL WHERE id=ANY($1::uuid[])`, wantIDs[4:]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred)
		SELECT id,$2::uuid,'track_number',position::text,'path_order_fallback',true FROM tracks WHERE id=ANY($1::uuid[])`,
		wantIDs[4:], initialScan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`UPDATE library_parse_cache
		SET observation=jsonb_set(jsonb_set(observation,'{TrackNumber}','1'),'{InferredFields,track_number}','true')
		WHERE root_id=$1::uuid AND relative_path=ANY($2::text[])`, root.ID, paths[4:]); err != nil {
		t.Fatal(err)
	}
	assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1::uuid AND relative_path=ANY($2::text[])
		AND observation->>'TrackNumber'='1' AND observation#>>'{InferredFields,track_number}'='true'`, 2, root.ID, paths[4:])
	if _, err := f.database.Exec(`UPDATE scan_candidate_states SET signature=repeat('0',64) WHERE root_id=$1::uuid`, root.ID); err != nil {
		t.Fatal(err)
	}
	_, rescan := runIncrementalTestScan(t, service, logs, root)
	if rescan.CandidatesWritten != 1 || rescan.ParseCacheHits != 6 || rescan.FilesParsed != 0 {
		t.Fatalf("重写读取旧解析缓存的统计 = %+v", rescan)
	}
	for i, id := range wantIDs {
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1::uuid AND position=$2 AND sort_position=$3`, 1, id, wantPositions[i], i+1)
		if i >= 4 {
			assertDatabaseCount(t, f, `SELECT count(*) FROM track_observations WHERE track_id=$1::uuid AND
				(field_name='track_number' OR source_kind='path_order_fallback')`, 0, id)
		}
	}
	store := catalog.NewStore(f.database)
	ctx := t.Context()
	media, err := store.LoadReleaseMedia(ctx, release)
	if err != nil || len(media) != 1 || len(media[0].Tracks) != len(wantIDs) {
		t.Fatalf("专辑详情碟轨 = %+v，错误 %v", media, err)
	}
	_, browse, err := store.BrowseAlbumTracks(ctx, group)
	if err != nil || len(browse) != len(wantIDs) {
		t.Fatalf("兼容浏览碟轨 = %+v，错误 %v", browse, err)
	}
	evidence, err := catalog.LoadCurationEvidence(ctx, f.database, []string{release})
	if err != nil || len(evidence[release].Tracks) != len(wantIDs) {
		t.Fatalf("整理碟轨 = %+v，错误 %v", evidence, err)
	}
	// 低层扫描夹具未启动维护进程。扫描已生成资料，发布当前派生代次供艺人队列读取。
	if _, err := f.database.Exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,
		published_generation=target_generation,published_textnorm_version=textnorm_version,
		published_search_projection_version=search_projection_version WHERE capability='artists'`); err != nil {
		t.Fatal(err)
	}
	var artistID string
	if err := f.database.QueryRow(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='artist'`).Scan(&artistID); err != nil {
		t.Fatal(err)
	}
	queue, queueErr := store.LoadArtistQueue(ctx, artistID, "album", nil)
	if queueErr != nil || len(queue.Items) != len(wantIDs) {
		t.Errorf("艺人专辑顺序队列 = %+v，错误 %v", queue, queueErr)
	}
	for i, id := range wantIDs {
		if tr := media[0].Tracks[i]; tr.ID != id || tr.Position != wantPositions[i] {
			t.Errorf("详情[%d] = %+v，期望 %s 的曲号 %d", i, tr, id, wantPositions[i])
		}
		if tr := subsonic.Song(browse[i], subsonic.PersonalState{}); tr.ID != id || tr.Track != wantPositions[i] {
			t.Errorf("Subsonic[%d] = %+v，期望 %s 的曲号 %d", i, tr, id, wantPositions[i])
		}
		if tr := evidence[release].Tracks[i]; tr.ID != id || tr.Position != wantPositions[i] ||
			wantPositions[i] == 0 && tr.PositionObservation != (catalog.CurationObservation{}) {
			t.Errorf("整理证据[%d] = %+v，期望 %s 的曲号 %d", i, tr, id, wantPositions[i])
		}
		if len(queue.Items) == len(wantIDs) && queue.Items[i].TrackID != id {
			t.Errorf("队列[%d] = %s，期望 %s", i, queue.Items[i].TrackID, id)
		}
	}

	// 缺失明细也要按内部次序取三首，并只返回真实曲号。
	for _, path := range []string{paths[0], paths[4], paths[5]} {
		if err := os.Remove(filepath.Join(root.Path, path)); err != nil {
			t.Fatal(err)
		}
	}
	runIncrementalTestScan(t, service, logs, root)
	issues, err := governance.NewStore(f.database).LoadReleaseIssues(ctx, release)
	if err != nil || issues.MissingTracks == nil || len(issues.MissingTracks.Tracks) != 3 {
		t.Fatalf("缺失曲目问题 = %+v，错误 %v", issues.MissingTracks, err)
	}
	for i, index := range []int{0, 4, 5} {
		track := issues.MissingTracks.Tracks[i]
		if track.ID != wantIDs[index] || track.Position != wantPositions[index] {
			t.Errorf("缺失明细[%d] = %+v，期望 %s 的曲号 %d", i, track, wantIDs[index], wantPositions[index])
		}
	}
}

func TestPostgreSQLScanGroupingFormatGroupsDoNotCrossTitles(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "grouping-format-groups")
	service, logs := newIncrementalTestService(t, f)
	albums := []string{"Alpha", "Gamma"}
	for i, album := range albums {
		writeAudio(t, root, "Mixed/"+album+".flac", audioFile{seed: byte(i + 2), title: album, album: album, track: 3})
		// 两个标题与原 ID3 帧的 Album 等长，保持合成帧格式。
		mp3 := bytes.Replace(formatSplitMP3(3), []byte("Album"), []byte(album), 1)
		writeFixtureFile(t, root, "Mixed/"+album+".mp3", mp3)
	}
	_, first := runIncrementalTestScan(t, service, logs, root)
	if first.CandidatesWritten != 4 {
		t.Fatalf("原始整目录和两个格式均不连续，应按名写入四张发行：%+v", first)
	}
	type placement struct{ group, release, anchor string }
	byTitle := map[string]placement{}
	for _, album := range albums {
		for _, format := range []string{"flac", "mp3"} {
			path := "Mixed/" + album + "." + format
			var got placement
			var title, kind string
			if err := f.database.QueryRow(`SELECT r.group_id::text,r.id::text,r.candidate_anchor,r.title,r.candidate_kind
				FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
				WHERE t.source_root_id=$1::uuid AND t.relative_path=$2`, root.ID, path).
				Scan(&got.group, &got.release, &got.anchor, &title, &kind); err != nil {
				t.Fatal(err)
			}
			if title != album || kind != string(ingest.CandidateSameDirSplit) {
				t.Errorf("%s 的发行标题和类型 = %q / %q，期望 %q / same_dir_split", path, title, kind, album)
			}
			if previous, exists := byTitle[album]; exists {
				if previous.group != got.group || previous.release == got.release || previous.anchor == got.anchor {
					t.Errorf("%s 两种格式归属 = %+v / %+v，期望同组、不同发行及锚", album, previous, got)
				}
			}
			byTitle[album] = got
		}
	}
	if byTitle[albums[0]].group == byTitle[albums[1]].group {
		t.Error("同目录的两个标题被跨格式合到一个版本组")
	}
	before := readIncrementalTestTracks(t, f, root)
	states := readIncrementalTestCandidateStates(t, f, root)
	_, next := runIncrementalTestScan(t, service, logs, root)
	if next.CandidatesSkipped != 4 || next.CandidatesWritten != 0 ||
		!reflect.DeepEqual(before, readIncrementalTestTracks(t, f, root)) ||
		!reflect.DeepEqual(states, readIncrementalTestCandidateStates(t, f, root)) {
		t.Errorf("重扫改变同目录版本组的身份或状态：%+v", next)
	}
}
