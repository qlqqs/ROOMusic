package library

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestPostgreSQLScanAttachmentSharingAndCueScope(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "attachment-sharing")
	service, logs := newIncrementalTestService(t, f)
	first := jpegArtworkOfWidth(4)
	second := jpegArtworkOfWidth(8)
	writeArtworkPriorityFLAC(t, root, "Mixed/A.flac", "Alpha", 1, 1, first)
	writeArtworkPriorityFLAC(t, root, "Mixed/B.flac", "Beta", 1, 2, second)
	writeFixtureFile(t, root, "Mixed/cover.jpg", jpegArtworkOfWidth(12))
	writeFixtureFile(t, root, "Mixed/sCaNs/page.png", pngArtworkOfWidth(5))
	writeFixtureFile(t, root, "Mixed/Extras/notes.PDF", []byte("%PDF-1.7"))
	writeFixtureFile(t, root, "Mixed/sCaNs/deep/ignored.png", pngArtworkOfWidth(7))

	writeFixtureFile(t, root, "Audio/image.flac", parsertest.FLACWithComments(t, []string{"TITLE=Image", "ALBUM=Cue Album", "ARTIST=Cue Artist"}))
	writeFixtureFile(t, root, "Audio/not-for-sheet.pdf", []byte("%PDF-1.7"))
	writeFixtureFile(t, root, "Sheets/booklet.pdf", []byte("%PDF-1.7"))
	writeFixtureFile(t, root, "Sheets/album.cue", []byte("TITLE \"Cue Album\"\nPERFORMER \"Cue Artist\"\nFILE \"../Audio/image.flac\" FLAC\n TRACK 01 AUDIO\n  INDEX 01 00:00:00\n TRACK 02 AUDIO\n  INDEX 01 00:01:00\n"))

	_, firstScan := runIncrementalTestScan(t, service, logs, root)
	if firstScan.CandidatesWritten != 3 {
		t.Fatalf("应写入两张同目录发行和一张 CUE 发行：%+v", firstScan)
	}
	alpha := assertArtworkPriorityBinding(t, f, service, root, "Mixed/A.flac", "same_dir_split", "embedded", first)
	beta := assertArtworkPriorityBinding(t, f, service, root, "Mixed/B.flac", "same_dir_split", "embedded", second)
	if alpha == beta {
		t.Fatal("同目录异名重复曲号被合并")
	}
	for _, id := range []string{alpha, beta} {
		assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid`, 3, id)
		assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid AND
			(relative_path='Mixed/cover.jpg' AND kind='image' AND image_format='jpeg' AND width=12 AND height=1
			 OR relative_path='Mixed/sCaNs/page.png' AND kind='image' AND image_format='png' AND width=5 AND height=1
			 OR relative_path='Mixed/Extras/notes.PDF' AND kind='document' AND image_format IS NULL AND width IS NULL AND height IS NULL)`, 3, id)
	}
	cueID := releaseIDsByTitle(t, f, root.ID)["Cue Album"]
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid`, 1, cueID)
	assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid AND relative_path='Sheets/booklet.pdf'`, 1, cueID)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid
		AND t.source_kind='cue_virtual' AND t.cue_parent_relative_path='Audio/image.flac' AND t.position=t.sort_position AND t.position IN (1,2)`, 2, cueID)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Audio/image.flac'`, 0, root.ID)

	ids := presentTrackIDs(t, f, root.ID)
	states := readIncrementalTestCandidateStates(t, f, root)
	_, rescan := runIncrementalTestScan(t, service, logs, root)
	if rescan.CandidatesSkipped != 3 || rescan.CandidatesWritten != 0 ||
		!reflect.DeepEqual(ids, presentTrackIDs(t, f, root.ID)) || !reflect.DeepEqual(states, readIncrementalTestCandidateStates(t, f, root)) {
		t.Fatalf("同范围重扫改变身份、锚点或写入状态：%+v", rescan)
	}
}

func TestPostgreSQLScanAttachmentChangesAndRetry(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "attachment-retry")
	service, logs := newIncrementalTestService(t, f)
	const audio = "Album/One.flac"
	const pdf = "Album/notes.pdf"
	const picture = "Album/Scans/page.png"
	writeAudio(t, root, audio, audioFile{seed: 1, title: "Original", album: "Album", track: 1})
	runIncrementalTestScan(t, service, logs, root)
	release := releaseIDsByTitle(t, f, root.ID)["Album"]
	states := readIncrementalTestCandidateStates(t, f, root)
	assertWritten := func() {
		t.Helper()
		_, summary := runIncrementalTestScan(t, service, logs, root)
		if summary.CandidatesWritten != 1 || summary.FilesParsed != 0 || summary.ParseCacheHits != 1 {
			t.Fatalf("仅更改附属文件应命中解析缓存且重写候选：%+v", summary)
		}
		current := readIncrementalTestCandidateStates(t, f, root)
		assertIncrementalTestCandidateRewrites(t, states, current, release)
		states = current
	}
	assertAttachments := func(count, width int, hasPDF bool) {
		t.Helper()
		assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid`, count, release)
		if width > 0 {
			assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid AND relative_path=$2 AND width=$3`, 1, release, picture, width)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM release_attachments WHERE release_id=$1::uuid AND relative_path=$2 AND kind='document'`, boolInt(hasPDF), release, pdf)
	}
	remove := func(path string) {
		t.Helper()
		if err := os.Remove(filepath.Join(root.Path, path)); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, root, pdf, []byte("%PDF original"))
	assertWritten()
	assertAttachments(1, 0, true)
	writeFixtureFile(t, root, pdf, []byte("%PDF edited contents"))
	assertWritten()
	writeFixtureFile(t, root, picture, pngArtworkOfWidth(3))
	assertWritten()
	assertAttachments(2, 3, true)
	writeFixtureFile(t, root, picture, pngArtworkOfWidth(5))
	assertWritten()
	assertAttachments(2, 5, true)
	remove(pdf)
	assertWritten()
	assertAttachments(1, 5, false)
	remove(picture)
	assertWritten()
	assertAttachments(0, 0, false)
	writeFixtureFile(t, root, picture, pngArtworkOfWidth(5))
	assertWritten()
	_, unchanged := runIncrementalTestScan(t, service, logs, root)
	if unchanged.CandidatesSkipped != 1 || unchanged.CandidatesWritten != 0 {
		t.Fatalf("二次未变应跳过：%+v", unchanged)
	}

	writeFixtureFile(t, root, picture, []byte("损坏的图片"))
	failedRead := runTestScan(t, f, service, root)
	requireDiagnosticCount(t, f, failedRead.ID, "attachment_unreadable", 1)
	assertAttachments(1, 5, false)
	if got := readIncrementalTestCandidateStates(t, f, root); !reflect.DeepEqual(got, states) {
		t.Errorf("读取失败覆盖成功签名：%+v → %+v", states, got)
	}
	writeFixtureFile(t, root, picture, pngArtworkOfWidth(8))
	assertWritten()
	assertAttachments(1, 8, false)

	exec := func(query string) {
		t.Helper()
		if _, err := f.database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE FUNCTION reject_synthetic_attachment() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.width=9 THEN RAISE EXCEPTION 'synthetic attachment write failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_synthetic_attachment BEFORE INSERT ON release_attachments
		FOR EACH ROW EXECUTE FUNCTION reject_synthetic_attachment()`)
	writeFixtureFile(t, root, picture, pngArtworkOfWidth(9))
	writeAudio(t, root, audio, audioFile{seed: 1, title: "Edited", album: "Revised Album", track: 1})
	failedWrite := runTestScan(t, f, service, root)
	if failedWrite.Status == "succeeded" {
		t.Error("附件写入触发器返回异常后，扫描仍报告成功")
	}
	assertAttachments(1, 8, false)
	assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1::uuid AND title='Album'`, 1, release)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1::uuid AND title='Original' AND source_status='present'`, 1, root.ID)
	if got := readIncrementalTestCandidateStates(t, f, root); !reflect.DeepEqual(got, states) {
		t.Errorf("候选回滚后成功签名变化：%+v → %+v", states, got)
	}
	exec(`DROP TRIGGER reject_synthetic_attachment ON release_attachments`)
	_, retried := runIncrementalTestScan(t, service, logs, root)
	if retried.CandidatesWritten != 1 {
		t.Errorf("解除失败后未重试候选：%+v", retried)
	}
	assertAttachments(1, 9, false)
	assertDatabaseCount(t, f, `SELECT count(*) FROM releases WHERE id=$1::uuid AND title='Revised Album'`, 1, release)
	track := presentTrackIDs(t, f, root.ID)[audio]
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1::uuid AND title='Edited'`, 1, track)
}
