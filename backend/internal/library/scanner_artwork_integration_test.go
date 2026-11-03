package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

// pngArtworkOfWidth 与 jpegArtworkOfWidth 生成只在宽度上不同的最小图片，测试用宽度区分绑定的是哪一张封面。
func pngArtworkOfWidth(width byte) []byte {
	data := minimalPNGArtwork()
	data[19] = width
	return data
}

func jpegArtworkOfWidth(width byte) []byte {
	return []byte{0xff, 0xd8, 0xff, 0xc0, 0, 17, 8, 0, 1, 0, width, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0, 0xff, 0xd9}
}

func writeScanFixtureFile(t *testing.T, root registeredRoot, relativePath string, data []byte) {
	t.Helper()
	path := filepath.Join(root.Path, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", relativePath, err)
	}
}

func scanFixtureRoot(t *testing.T, fixture *libraryFixture, root registeredRoot) {
	t.Helper()
	application := scannerTestApplication(fixture)
	if err := application.scanRoot(context.Background(), fixture.database, insertRunningScan(t, fixture), root); err != nil {
		t.Fatalf("scan fixture root: %v", err)
	}
}

// folderArtworks 返回该根下所有 Release 的封面（来源类型、MIME、宽度），按宽度排序，用于断言绑定了哪几张封面。
func folderArtworks(t *testing.T, fixture *libraryFixture, root registeredRoot) []string {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT artworks.source_type||':'||artworks.mime_type||':'||artworks.width FROM release_artworks artworks JOIN releases ON releases.id=artworks.release_id WHERE releases.source_root_id=$1::uuid ORDER BY artworks.width`, root.ID)
	if err != nil {
		t.Fatalf("query artworks: %v", err)
	}
	defer rows.Close()
	var artworks []string
	for rows.Next() {
		var artwork string
		if err := rows.Scan(&artwork); err != nil {
			t.Fatalf("scan artwork: %v", err)
		}
		artworks = append(artworks, artwork)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate artworks: %v", err)
	}
	return artworks
}

func assertArtworks(t *testing.T, fixture *libraryFixture, root registeredRoot, want ...string) {
	t.Helper()
	got := folderArtworks(t, fixture, root)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("artworks = %v, want %v", got, want)
	}
}

func taggedTrack(t *testing.T, seed byte, disc int, album string) []byte {
	t.Helper()
	comments := []string{"TITLE=Track", "ARTIST=Artist", "ALBUM=" + album, "TRACKNUMBER=1"}
	if disc > 0 {
		comments = append(comments, "DISCNUMBER="+string(rune('0'+disc)))
	}
	return parsertest.FLACWithAudio(t, [16]byte{seed}, []byte{seed}, comments...)
}

func TestPostgreSQLCueAlbumGetsFolderArtworkAndRescanRebindsIt(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "cue-folder-artwork")
	writeScanFixtureFile(t, root, "Album/image.flac", parsertest.FLACStream())
	writeScanFixtureFile(t, root, "Album/album.cue", []byte(strings.Join([]string{
		`TITLE "Cue Release"`,
		`PERFORMER "Cue Artist"`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "First"`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Second"`,
		`    INDEX 01 01:00:00`,
	}, "\n")))

	scanFixtureRoot(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual'`, 2, root.ID)
	assertArtworks(t, fixture, root)

	// 文件名大小写不同也要找到；下一次扫描直接绑定，不需要迁移。
	writeScanFixtureFile(t, root, "Album/Cover.PNG", pngArtworkOfWidth(3))
	scanFixtureRoot(t, fixture, root)
	assertArtworks(t, fixture, root, "folder:image/png:3")
}

func TestPostgreSQLStrictMultiDiscUsesParentFolderArtwork(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "multi-disc-parent-artwork")
	writeScanFixtureFile(t, root, "Album/CD1/01.flac", taggedTrack(t, 1, 1, "Release"))
	writeScanFixtureFile(t, root, "Album/CD2/01.flac", taggedTrack(t, 2, 2, "Release"))
	writeScanFixtureFile(t, root, "Album/Cover.JPG", jpegArtworkOfWidth(4))

	scanFixtureRoot(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertArtworks(t, fixture, root, "folder:image/jpeg:4")
}

func TestPostgreSQLStrictMultiDiscParentArtworkOutranksFirstDisc(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "multi-disc-parent-and-disc-artwork")
	writeScanFixtureFile(t, root, "Album/CD1/01.flac", taggedTrack(t, 1, 1, "Release"))
	writeScanFixtureFile(t, root, "Album/CD2/01.flac", taggedTrack(t, 2, 2, "Release"))
	writeScanFixtureFile(t, root, "Album/cover.png", pngArtworkOfWidth(3))
	writeScanFixtureFile(t, root, "Album/CD1/cover.png", pngArtworkOfWidth(5))

	scanFixtureRoot(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertArtworks(t, fixture, root, "folder:image/png:3")
}

func TestPostgreSQLStrictMultiDiscFallsBackToDiscFolderArtwork(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "multi-disc-disc-artwork")
	writeScanFixtureFile(t, root, "Album/CD1/01.flac", taggedTrack(t, 1, 1, "Release"))
	writeScanFixtureFile(t, root, "Album/CD2/01.flac", taggedTrack(t, 2, 2, "Release"))
	writeScanFixtureFile(t, root, "Album/CD2/Folder.png", pngArtworkOfWidth(6))

	scanFixtureRoot(t, fixture, root)
	assertArtworks(t, fixture, root, "folder:image/png:6")
}

func TestPostgreSQLBoxLeafDoesNotUseBoxParentArtwork(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "box-leaf-artwork")
	// 碟号不连续，所以每张碟是独立的 Box 叶子；Box 目录里的封面不属于任何一张碟。
	writeScanFixtureFile(t, root, "Box/Disc 1/01.flac", taggedTrack(t, 1, 1, "Set"))
	writeScanFixtureFile(t, root, "Box/Disc 3/01.flac", taggedTrack(t, 3, 3, "Set"))
	writeScanFixtureFile(t, root, "Box/cover.png", pngArtworkOfWidth(7))
	writeScanFixtureFile(t, root, "Box/Disc 1/cover.png", pngArtworkOfWidth(8))

	scanFixtureRoot(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
	assertArtworks(t, fixture, root, "folder:image/png:8")
}

func TestPostgreSQLEmbeddedArtworkOutranksFolderArtwork(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "embedded-over-folder")
	application := scannerTestApplication(fixture)
	writeScanFixtureFile(t, root, "Album/cover.png", pngArtworkOfWidth(3))
	observation := ingest.SourceObservation{RelativePath: "Album/01.flac", Directory: "Album", Title: "Track", Album: "Release", Artist: "Artist", TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment", Artwork: pngArtworkOfWidth(9), ArtworkMIME: "image/png"}
	if err := persistTestObservations(context.Background(), application, fixture, insertRunningScan(t, fixture), root, observation); err != nil {
		t.Fatalf("persist embedded artwork: %v", err)
	}
	assertArtworks(t, fixture, root, "embedded:image/png:9")
}
