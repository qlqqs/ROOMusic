package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPostgreSQLCandidatePersistenceIsIdempotentAndReplacesCurrentEvidence(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "candidate-idempotency")
	application := scannerTestApplication(fixture)
	firstScan := insertRunningScan(t, fixture)
	artwork := minimalPNGArtwork()
	observations := []sourceObservation{
		{RelativePath: "Album/01.flac", Directory: "Album", Title: "First", Album: "Release", AlbumArtist: "Album Artist", Artist: "Track Artist / Guest Artist", Year: 2024, TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment", DurationSeconds: 60, Codec: "flac", BitDepth: 24, SampleRate: 96000, Channels: 2, Bitrate: 1800, Edition: "Deluxe Edition", Label: "Example Label", Barcode: "012345678901", Credits: []creditObservation{{Role: "composer", Name: "Composer One"}, {Role: "composer", Name: "Composer Two"}}, Artwork: artwork, ArtworkMIME: "image/png"},
		{RelativePath: "Album/02.flac", Directory: "Album", Title: "Second", Album: "Release", AlbumArtist: "Album Artist", Artist: "Track Artist", Year: 1999, TrackNumber: 2, DiscNumber: 1, SourceKind: "flac_vorbis_comment", InferredFields: map[string]bool{"year": true}},
	}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, firstScan, root, organizeObservations(observations)); err != nil {
		t.Fatalf("persist first candidate: %v", err)
	}

	var releaseID, groupID string
	if err := fixture.database.connection.QueryRow(`SELECT id::text,group_id::text FROM releases WHERE source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &groupID); err != nil {
		t.Fatalf("read release identity: %v", err)
	}
	trackIDs := readTrackIDs(t, fixture, root.ID)
	if len(trackIDs) != 2 {
		t.Fatalf("track count = %d, want 2", len(trackIDs))
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_grouping_evidence WHERE release_id=$1::uuid`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_credits WHERE release_id=$1::uuid AND role='album_artist' AND name='Album Artist'`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits credits JOIN tracks ON tracks.id=credits.track_id WHERE tracks.source_root_id=$1::uuid AND credits.role='composer'`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1::uuid`, 1, releaseID)
	var edition, label, barcode string
	if err := fixture.database.connection.QueryRow(`SELECT edition,label,barcode FROM releases WHERE id=$1::uuid`, releaseID).Scan(&edition, &label, &barcode); err != nil {
		t.Fatalf("read release metadata: %v", err)
	}
	if edition != "Deluxe Edition" || label != "Example Label" || barcode != "012345678901" {
		t.Fatalf("release metadata = %q/%q/%q", edition, label, barcode)
	}
	var trackArtist string
	if err := fixture.database.connection.QueryRow(`SELECT artist FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Album/01.flac'`, root.ID).Scan(&trackArtist); err != nil {
		t.Fatalf("read canonical track artist: %v", err)
	}
	if trackArtist != "Guest Artist / Track Artist" {
		t.Fatalf("canonical track artist = %q", trackArtist)
	}
	var releaseYear int
	if err := fixture.database.connection.QueryRow(`SELECT year FROM releases WHERE id=$1::uuid`, releaseID).Scan(&releaseYear); err != nil {
		t.Fatalf("read selected release year: %v", err)
	}
	if releaseYear != 2024 {
		t.Fatalf("release year = %d, want authoritative year 2024", releaseYear)
	}
	var artworkKey string
	if err := fixture.database.connection.QueryRow(`SELECT storage_key FROM release_artworks WHERE release_id=$1::uuid`, releaseID).Scan(&artworkKey); err != nil {
		t.Fatalf("read artwork key: %v", err)
	}

	secondScan := insertRunningScan(t, fixture)
	observations[0].Title = "First (rescanned)"
	observations[0].BitDepth = 16
	observations[0].Credits = []creditObservation{{Role: "composer", Name: "Composer Three"}}
	observations = []sourceObservation{observations[1], observations[0]}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, secondScan, root, organizeObservations(observations)); err != nil {
		t.Fatalf("persist repeated candidate: %v", err)
	}

	var repeatedReleaseID, repeatedGroupID string
	if err := fixture.database.connection.QueryRow(`SELECT id::text,group_id::text FROM releases WHERE source_root_id=$1::uuid`, root.ID).Scan(&repeatedReleaseID, &repeatedGroupID); err != nil {
		t.Fatalf("read repeated identity: %v", err)
	}
	if repeatedReleaseID != releaseID || repeatedGroupID != groupID {
		t.Fatalf("release identity changed: %s/%s -> %s/%s", releaseID, groupID, repeatedReleaseID, repeatedGroupID)
	}
	if repeatedTracks := readTrackIDs(t, fixture, root.ID); fmt.Sprint(repeatedTracks) != fmt.Sprint(trackIDs) {
		t.Fatalf("track identities changed: %v -> %v", trackIDs, repeatedTracks)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM media WHERE release_id=$1::uuid`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_grouping_evidence WHERE release_id=$1::uuid`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_observations observations JOIN tracks ON tracks.id=observations.track_id WHERE tracks.source_root_id=$1::uuid AND observations.scan_run_id<>$2::uuid`, 0, root.ID, secondScan)
	var title string
	var bitDepth int
	if err := fixture.database.connection.QueryRow(`SELECT title,bit_depth FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Album/01.flac'`, root.ID).Scan(&title, &bitDepth); err != nil {
		t.Fatalf("read updated track: %v", err)
	}
	if title != "First (rescanned)" || bitDepth != 16 {
		t.Fatalf("track facts were not replaced: title=%q bit_depth=%d", title, bitDepth)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits credits JOIN tracks ON tracks.id=credits.track_id WHERE tracks.source_root_id=$1::uuid AND credits.role='composer' AND credits.name='Composer Three'`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits credits JOIN tracks ON tracks.id=credits.track_id WHERE tracks.source_root_id=$1::uuid AND credits.name IN ('Composer One','Composer Two')`, 0, root.ID)

	thirdScan := insertRunningScan(t, fixture)
	observations[1].Artwork = nil
	observations[1].ArtworkMIME = ""
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, thirdScan, root, organizeObservations(observations)); err != nil {
		t.Fatalf("persist candidate without artwork: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1::uuid`, 0, releaseID)
	if _, err := os.Stat(filepath.Join(application.config.DataDirectory, "artwork", artworkKey)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced artwork was not removed: %v", err)
	}
}

func TestPostgreSQLCandidateTransactionRollsBackOnEvidenceFailure(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "candidate-rollback")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	if _, err := fixture.database.connection.Exec(`CREATE FUNCTION reject_grouping_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test rejection'; END $$; CREATE TRIGGER reject_grouping_evidence BEFORE INSERT ON release_grouping_evidence FOR EACH ROW EXECUTE FUNCTION reject_grouping_evidence()`); err != nil {
		t.Fatalf("install rejection trigger: %v", err)
	}
	observations := []sourceObservation{{RelativePath: "Album/01.flac", Directory: "Album", Title: "Track", Album: "Release", TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment"}}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, scanID, root, organizeObservations(observations)); err == nil {
		t.Fatal("candidate persistence unexpectedly succeeded")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 0, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 0, root.ID)
}

func TestPostgreSQLIncompleteRootPersistsValidCandidatesAndCleansStaging(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "partial-root")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	albumDirectory := filepath.Join(root.Path, "Album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "good.flac"), createFLACFixture(t, []string{"TITLE=Good", "ARTIST=Artist", "ALBUM=Release", "TRACKNUMBER=1"}), 0o644); err != nil {
		t.Fatalf("write valid fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "bad.mp3"), []byte("not-an-mp3"), 0o644); err != nil {
		t.Fatalf("write invalid fixture: %v", err)
	}
	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err == nil {
		t.Fatal("partial root was reported complete")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations WHERE scan_run_id=$1::uuid`, 0, scanID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid AND kind='parse_failure'`, 1, scanID)
}

func TestPostgreSQLScanRootReportsTerminalStagingCleanupFailure(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "staging-cleanup-failure")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	albumDirectory := filepath.Join(root.Path, "Album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "track.flac"), createFLACFixture(t, []string{"TITLE=Track", "ARTIST=Artist", "ALBUM=Release", "TRACKNUMBER=1"}), 0o644); err != nil {
		t.Fatalf("write audio fixture: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`
		CREATE FUNCTION reject_staging_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test staging cleanup failure';
		END
		$$;
		CREATE TRIGGER reject_staging_cleanup BEFORE DELETE ON scan_observations
		FOR EACH ROW EXECUTE FUNCTION reject_staging_cleanup()`); err != nil {
		t.Fatalf("install staging cleanup rejection: %v", err)
	}

	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err == nil {
		t.Fatal("terminal staging cleanup failure was reported as a complete root")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations WHERE scan_run_id=$1::uuid AND root_id=$2::uuid`, 1, scanID, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid AND kind='staging_write_failure'`, 1, scanID)
}

func TestPostgreSQLScanRootRejectsNonRegularAudioSource(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "non-regular-audio")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	pipePath := filepath.Join(root.Path, "blocked.flac")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("create audio-named FIFO: %v", err)
	}

	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err == nil {
		t.Fatal("non-regular audio source was reported as a complete root")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 0, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid AND kind='non_regular_file'`, 1, scanID)
}

func TestPostgreSQLScanPersistsExplicitRipLogCDSemantics(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "rip-log-evidence")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	albumDirectory := filepath.Join(root.Path, "Album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "01.flac"), createFLACFixture(t, []string{"TITLE=Track", "ARTIST=Artist", "ALBUM=Release", "TRACKNUMBER=1"}), 0o644); err != nil {
		t.Fatalf("write audio fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "rip.log"), []byte("Exact Audio Copy V1.6\nEAC extraction logfile"), 0o644); err != nil {
		t.Fatalf("write rip-log fixture: %v", err)
	}
	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err != nil {
		t.Fatalf("scan root with rip-log evidence: %v", err)
	}

	var releaseID, sourceType, mediaType string
	if err := fixture.database.connection.QueryRow(`SELECT id::text,source_type,media_type FROM releases WHERE source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &sourceType, &mediaType); err != nil {
		t.Fatalf("read release source/media: %v", err)
	}
	if sourceType != "CD" || mediaType != "CD" {
		t.Fatalf("release source/media = %q/%q, want CD/CD", sourceType, mediaType)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_field_decisions WHERE release_id=$1::uuid AND field_key IN ('source_type','media_type') AND selected_source='rip_log' AND confidence='high' AND action='auto_apply'`, 2, releaseID)
	var sourceRefs string
	if err := fixture.database.connection.QueryRow(`SELECT source_refs::text FROM release_grouping_evidence WHERE release_id=$1::uuid`, releaseID).Scan(&sourceRefs); err != nil {
		t.Fatalf("read rip-log grouping evidence: %v", err)
	}
	if !strings.Contains(sourceRefs, "Album/rip.log") {
		t.Fatalf("rip-log source reference was not retained: %s", sourceRefs)
	}
}

func TestPostgreSQLScanRootPersistsCueSheetMetadata(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "cue-sheet-metadata")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	albumDirectory := filepath.Join(root.Path, "Album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDirectory, "image.flac"), repairFLACFixture(), 0o644); err != nil {
		t.Fatalf("write CUE parent fixture: %v", err)
	}
	cue := strings.Join([]string{
		`TITLE "Cue Release"`,
		`PERFORMER "Cue Album Artist"`,
		`REM DATE 2024`,
		`REM GENRE "Ambient"`,
		`CATALOG 0123456789012`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "First"`,
		`    PERFORMER "First Artist"`,
		`    ISRC CN-A01-24-00001`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Second"`,
		`    PERFORMER "Second Artist"`,
		`    ISRC CN-A01-24-00002`,
		`    INDEX 01 01:00:00`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(albumDirectory, "album.cue"), []byte(cue), 0o644); err != nil {
		t.Fatalf("write CUE fixture: %v", err)
	}

	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err != nil {
		t.Fatalf("scan CUE root: %v", err)
	}
	var releaseID, title, albumArtist, genre, catalogNumber string
	var year int
	if err := fixture.database.connection.QueryRow(`SELECT id::text,title,album_artist,year,genre,catalog_number FROM releases WHERE source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &title, &albumArtist, &year, &genre, &catalogNumber); err != nil {
		t.Fatalf("read CUE release metadata: %v", err)
	}
	if title != "Cue Release" || albumArtist != "Cue Album Artist" || year != 2024 || genre != "Ambient" || catalogNumber != "0123456789012" {
		t.Fatalf("unexpected CUE release metadata: title=%q album_artist=%q year=%d genre=%q catalog=%q", title, albumArtist, year, genre, catalogNumber)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_credits WHERE release_id=$1::uuid AND role='album_artist' AND name='Cue Album Artist'`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual'`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(DISTINCT position) FROM tracks WHERE source_root_id=$1::uuid AND position IN (1,2)`, 2, root.ID)
	for _, field := range []struct {
		name   string
		source string
		count  int
	}{
		{name: "album", source: "cue_sheet", count: 2},
		{name: "album_artist", source: "cue_sheet", count: 2},
		{name: "year", source: "cue_sheet", count: 2},
		{name: "genre", source: "cue_sheet", count: 2},
		{name: "catalog_number", source: "cue_sheet", count: 2},
		{name: "isrc", source: "cue_track", count: 2},
	} {
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_observations observations JOIN tracks ON tracks.id=observations.track_id WHERE tracks.source_root_id=$1::uuid AND observations.field_name=$2 AND observations.source_kind=$3 AND NOT observations.inferred`, field.count, root.ID, field.name, field.source)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations WHERE scan_run_id=$1::uuid`, 0, scanID)
}

func TestPostgreSQLScanRootAlignsCrossDirectoryCueParent(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "cross-directory-cue")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	mediaDirectory := filepath.Join(root.Path, "media")
	sheetDirectory := filepath.Join(root.Path, "sheets")
	for _, directory := range []string{mediaDirectory, sheetDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create CUE fixture directory: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(mediaDirectory, "image.flac"), createFLACFixture(t, []string{"TITLE=Image", "ARTIST=Artist", "ALBUM=Release"}), 0o644); err != nil {
		t.Fatalf("write CUE parent fixture: %v", err)
	}
	content := strings.Join([]string{
		`TITLE "Release"`,
		`PERFORMER "Artist"`,
		`FILE "../media/image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "First"`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Second"`,
		`    INDEX 01 00:01:00`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(sheetDirectory, "album.cue"), []byte(content), 0o644); err != nil {
		t.Fatalf("write cross-directory CUE fixture: %v", err)
	}

	if err := application.scanRoot(context.Background(), fixture.database.connection, scanID, root); err != nil {
		t.Fatalf("scan cross-directory CUE fixture: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND cue_parent_relative_path='media/image.flac'`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND relative_path='media/image.flac'`, 0, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations WHERE scan_run_id=$1::uuid`, 0, scanID)
}

func TestPostgreSQLCueStagingAlignsParentAndPersistsVirtualIdentity(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "cue-staging")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	physical := sourceObservation{RelativePath: "image.flac", Title: "Image", Artist: "Artist", TrackNumber: 1, DiscNumber: 1, SourceKind: "physical"}
	virtual := sourceObservation{RelativePath: "album.cue#track-1@image.flac#index-0", Title: "Cue Track", Album: "Cue Album", AlbumArtist: "Cue Artist", Artist: "Cue Artist", TrackNumber: 1, DiscNumber: 1, SourceKind: "cue_virtual", CueSheetPath: "album.cue", CueParentRelativePath: "image.flac", CueReferencedFile: "image.flac", CueIndexFrames: 0, CueIndexPresent: true, CueEndFrames: 750, CueEndPresent: true, CueISRC: "TEST00000001", DurationSeconds: 10, Codec: "flac", BitDepth: 16, SampleRate: 44100, Channels: 2}
	secondVirtual := virtual
	secondVirtual.RelativePath = "album.cue#track-2@image.flac#index-750"
	secondVirtual.Title = "Cue Track 2"
	secondVirtual.TrackNumber = 2
	secondVirtual.CueIndexFrames = 750
	secondVirtual.CueEndFrames = 1500
	secondVirtual.CueISRC = "TEST00000002"
	for _, observation := range []sourceObservation{physical, virtual, secondVirtual} {
		if err := stageObservation(context.Background(), fixture.database.connection, scanID, root.ID, observation); err != nil {
			t.Fatalf("stage observation: %v", err)
		}
	}
	if err := alignCueStagingScopes(context.Background(), fixture.database.connection, scanID, root.ID); err != nil {
		t.Fatalf("align CUE scope: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(DISTINCT organization_scope) FROM scan_observations WHERE scan_run_id=$1::uuid`, 1, scanID)
	if err := application.persistStagedCandidates(context.Background(), fixture.database.connection, scanID, root); err != nil {
		t.Fatalf("persist CUE candidate: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
	var kind, identity, sheet, parent, isrc string
	var indexFrames, endFrames, bitDepth int
	if err := fixture.database.connection.QueryRow(`SELECT source_kind,source_identity,cue_sheet_path,cue_parent_relative_path,cue_index_frames,cue_end_frames,cue_isrc,bit_depth FROM tracks WHERE source_root_id=$1::uuid AND position=1`, root.ID).Scan(&kind, &identity, &sheet, &parent, &indexFrames, &endFrames, &isrc, &bitDepth); err != nil {
		t.Fatalf("read CUE track: %v", err)
	}
	if kind != "cue_virtual" || identity != sourceIdentityForObservation(root.ID, virtual) || sheet != "album.cue" || parent != "image.flac" || indexFrames != 0 || endFrames != 750 || isrc != "TEST00000001" || bitDepth != 16 {
		t.Fatalf("unexpected CUE facts: kind=%q identity=%q sheet=%q parent=%q index=%d end=%d isrc=%q bitDepth=%d", kind, identity, sheet, parent, indexFrames, endFrames, isrc, bitDepth)
	}
	var refs string
	if err := fixture.database.connection.QueryRow(`SELECT source_refs::text FROM release_grouping_evidence evidence JOIN releases ON releases.id=evidence.release_id WHERE releases.source_root_id=$1::uuid`, root.ID).Scan(&refs); err != nil {
		t.Fatalf("read grouping refs: %v", err)
	}
	if !strings.Contains(refs, "image.flac") || !strings.Contains(refs, "album.cue") {
		t.Fatalf("CUE/parent evidence was not retained: %s", refs)
	}
}

func TestPostgreSQLFirstRepairScanReusesLegacyCueTrackIdentity(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "legacy-cue-identity")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	groupID, releaseID, mediumID, trackID := createIdentifier(), createIdentifier(), createIdentifier(), createIdentifier()
	anchor := candidateIdentity(root.ID, candidateAnchor{Kind: candidateOrdinary, Scope: "Album"})
	if _, err := fixture.database.connection.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID); err != nil {
		t.Fatalf("insert legacy CUE release group: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1::uuid,$2::uuid,'Release','Artist',$3::uuid,'Album',$4,'ordinary_directory')`, releaseID, groupID, root.ID, anchor); err != nil {
		t.Fatalf("insert legacy CUE release: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, mediumID, releaseID); err != nil {
		t.Fatalf("insert legacy CUE medium: %v", err)
	}
	legacyIdentity := root.ID + ":Album/album.cue#track-1@image.flac"
	if _, err := fixture.database.connection.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity) VALUES($1::uuid,$2::uuid,1,'Old title','Artist',1,$3::uuid,'Album/album.cue#track-1@image.flac','present',NOW(),'cue_virtual',$4)`, trackID, mediumID, root.ID, legacyIdentity); err != nil {
		t.Fatalf("insert legacy CUE track: %v", err)
	}

	observation := sourceObservation{
		RelativePath:          "Album/album.cue#track-1@Album/image.flac#index-0",
		Directory:             "Album",
		Title:                 "New title",
		Album:                 "Release",
		Artist:                "Artist",
		TrackNumber:           1,
		DiscNumber:            1,
		SourceKind:            "cue_virtual",
		CueSheetPath:          "Album/album.cue",
		CueParentRelativePath: "Album/image.flac",
		CueReferencedFile:     "Album/image.flac",
		CueIndexPresent:       true,
	}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, scanID, root, organizeObservations([]sourceObservation{observation})); err != nil {
		t.Fatalf("persist repaired CUE candidate: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 1, root.ID)
	var reusedID, sourceIdentity, title string
	if err := fixture.database.connection.QueryRow(`SELECT id::text,source_identity,title FROM tracks WHERE source_root_id=$1::uuid`, root.ID).Scan(&reusedID, &sourceIdentity, &title); err != nil {
		t.Fatalf("read repaired CUE track: %v", err)
	}
	if reusedID != trackID || sourceIdentity != sourceIdentityForObservation(root.ID, observation) || title != "New title" {
		t.Fatalf("legacy CUE track was not reused: id=%q identity=%q title=%q", reusedID, sourceIdentity, title)
	}
}

func TestPostgreSQLArtworkLinkFailureRemovesNewManagedFile(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "artwork-link-failure")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	if _, err := fixture.database.connection.Exec(`CREATE FUNCTION reject_release_artwork() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test rejection'; END $$; CREATE TRIGGER reject_release_artwork BEFORE INSERT ON release_artworks FOR EACH ROW EXECUTE FUNCTION reject_release_artwork()`); err != nil {
		t.Fatalf("install artwork rejection trigger: %v", err)
	}
	observation := sourceObservation{RelativePath: "Album/01.flac", Directory: "Album", Title: "Track", Album: "Release", Artist: "Artist", TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment", Artwork: minimalPNGArtwork(), ArtworkMIME: "image/png"}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, scanID, root, organizeObservations([]sourceObservation{observation})); err == nil {
		t.Fatal("artwork link failure unexpectedly succeeded")
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks`, 0)
	entries, err := os.ReadDir(filepath.Join(application.config.DataDirectory, "artwork"))
	if err != nil {
		t.Fatalf("read managed artwork directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("artwork link failure left %d managed files", len(entries))
	}
}

func TestPostgreSQLManagedArtworkIsRevalidatedAndCleanupErrorsPropagate(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "artwork-revalidation")
	application := scannerTestApplication(fixture)
	scanID := insertRunningScan(t, fixture)
	artwork := minimalPNGArtwork()
	observation := sourceObservation{RelativePath: "Album/01.flac", Directory: "Album", Title: "Track", Album: "Release", Artist: "Artist", TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment", Artwork: artwork, ArtworkMIME: "image/png"}
	if err := application.persistOrganizedCandidates(context.Background(), fixture.database.connection, scanID, root, organizeObservations([]sourceObservation{observation})); err != nil {
		t.Fatalf("persist initial artwork: %v", err)
	}
	var releaseID, originalKey string
	if err := fixture.database.connection.QueryRow(`SELECT releases.id::text,release_artworks.storage_key FROM releases JOIN release_artworks ON release_artworks.release_id=releases.id WHERE releases.source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &originalKey); err != nil {
		t.Fatalf("read initial artwork link: %v", err)
	}
	originalPath := filepath.Join(application.config.DataDirectory, "artwork", originalKey)
	if err := os.WriteFile(originalPath, []byte("corrupt"), 0o640); err != nil {
		t.Fatalf("corrupt managed artwork: %v", err)
	}
	if err := application.saveArtworkForRelease(context.Background(), fixture.database.connection, releaseID, root, observation); err != nil {
		t.Fatalf("repair managed artwork: %v", err)
	}
	repaired, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(repaired, artwork) {
		t.Fatalf("managed artwork was not repaired: bytes=%x err=%v", repaired, err)
	}

	if err := os.Remove(originalPath); err != nil {
		t.Fatalf("remove original artwork: %v", err)
	}
	if err := os.Mkdir(originalPath, 0o750); err != nil {
		t.Fatalf("replace original artwork with directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(originalPath, "blocker"), []byte("x"), 0o640); err != nil {
		t.Fatalf("make artwork cleanup fail: %v", err)
	}
	replacement := append(append([]byte(nil), artwork...), 0)
	observation.Artwork = replacement
	if err := application.saveArtworkForRelease(context.Background(), fixture.database.connection, releaseID, root, observation); err == nil || !strings.Contains(err.Error(), "remove replaced artwork") {
		t.Fatalf("old artwork cleanup error was discarded: %v", err)
	}
}

func TestPostgreSQLOrganizationMigrationConsolidatesLegacyCurrentRows(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "legacy-consolidation")
	scanID := insertRunningScan(t, fixture)
	groupID, releaseID := createIdentifier(), createIdentifier()
	firstMedium, secondMedium := createIdentifier(), createIdentifier()
	for _, statement := range []string{"DROP INDEX media_release_position_uq", "DROP INDEX release_grouping_evidence_current_uq"} {
		if _, err := fixture.database.connection.Exec(statement); err != nil {
			t.Fatalf("drop current index: %v", err)
		}
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID); err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}
	legacyAnchor := root.ID + ":v1:loose_album::Legacy|Artist"
	if _, err := fixture.database.connection.Exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1::uuid,$2::uuid,'Legacy','Artist',$3::uuid,'',$4,'loose_album')`, releaseID, groupID, root.ID, legacyAnchor); err != nil {
		t.Fatalf("insert legacy release: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$3::uuid,1),($2::uuid,$3::uuid,1)`, firstMedium, secondMedium, releaseID); err != nil {
		t.Fatalf("insert duplicate media: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at) VALUES
		($1::uuid,$2::uuid,1,'One','Artist',1,$3::uuid,'Legacy/one.flac','present',NOW()),
		($4::uuid,$5::uuid,2,'Two','Artist',1,$3::uuid,'Legacy/two.flac','present',NOW())`, createIdentifier(), firstMedium, root.ID, createIdentifier(), secondMedium); err != nil {
		t.Fatalf("insert legacy tracks: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason,scan_run_id,observed_at) VALUES
		($1::uuid,'legacy','old','[]','old',$2::uuid,NOW()-INTERVAL '1 minute'),
		($1::uuid,'legacy','new','[]','new',$2::uuid,NOW())`, releaseID, scanID); err != nil {
		t.Fatalf("insert duplicate evidence: %v", err)
	}
	migration := findMigrationByVersion(t, 11)
	if _, err := fixture.database.connection.Exec(string(migration.SQL)); err != nil {
		t.Fatalf("reapply organization migration to legacy rows: %v", err)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM media WHERE release_id=$1::uuid AND position=1`, 1, releaseID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(DISTINCT medium_id) FROM tracks WHERE source_root_id=$1::uuid`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_grouping_evidence WHERE release_id=$1::uuid`, 1, releaseID)
	var migratedAnchor string
	if err := fixture.database.connection.QueryRow(`SELECT candidate_anchor FROM releases WHERE id=$1::uuid`, releaseID).Scan(&migratedAnchor); err != nil {
		t.Fatalf("read migrated anchor: %v", err)
	}
	expectedAnchor := candidateIdentity(root.ID, candidateAnchor{Kind: candidateLooseAlbum, Partition: "source:Legacy/one.flac"})
	if migratedAnchor != expectedAnchor {
		t.Fatalf("legacy anchor = %q, want %q", migratedAnchor, expectedAnchor)
	}
	var ruleID string
	if err := fixture.database.connection.QueryRow(`SELECT rule_id FROM release_grouping_evidence WHERE release_id=$1::uuid`, releaseID).Scan(&ruleID); err != nil || ruleID != "new" {
		t.Fatalf("latest grouping evidence was not retained: rule=%q err=%v", ruleID, err)
	}
}

func TestPostgreSQLOrganizationMigrationLeavesConflictingLegacyAnchors(t *testing.T) {
	fixture := newIntegrationTestApplication(t)
	root := insertScannerRoot(t, fixture, "legacy-anchor-conflict")
	firstGroup, secondGroup := createIdentifier(), createIdentifier()
	firstRelease, secondRelease := createIdentifier(), createIdentifier()
	if _, err := fixture.database.connection.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid),($2::uuid)`, firstGroup, secondGroup); err != nil {
		t.Fatalf("insert legacy groups: %v", err)
	}
	firstAnchor := root.ID + ":v1:ordinary_directory:Album:First|Artist"
	secondAnchor := root.ID + ":v1:ordinary_directory:Album:Second|Artist"
	if _, err := fixture.database.connection.Exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES
		($1::uuid,$2::uuid,'First','Artist',$3::uuid,'Album',$4,'ordinary_directory'),
		($5::uuid,$6::uuid,'Second','Artist',$3::uuid,'Album',$7,'ordinary_directory')`, firstRelease, firstGroup, root.ID, firstAnchor, secondRelease, secondGroup, secondAnchor); err != nil {
		t.Fatalf("insert conflicting legacy releases: %v", err)
	}

	migration := findMigrationByVersion(t, 11)
	if _, err := fixture.database.connection.Exec(string(migration.SQL)); err != nil {
		t.Fatalf("reapply organization migration with conflicting legacy anchors: %v", err)
	}

	for releaseID, wantAnchor := range map[string]string{firstRelease: firstAnchor, secondRelease: secondAnchor} {
		var gotAnchor string
		if err := fixture.database.connection.QueryRow(`SELECT candidate_anchor FROM releases WHERE id=$1::uuid`, releaseID).Scan(&gotAnchor); err != nil {
			t.Fatalf("read retained legacy anchor: %v", err)
		}
		if gotAnchor != wantAnchor {
			t.Fatalf("conflicting legacy anchor = %q, want %q", gotAnchor, wantAnchor)
		}
	}
}

func scannerTestApplication(fixture *integrationTestApplication) *roomusicApplication {
	return &roomusicApplication{config: serverConfig{DataDirectory: filepath.Join(fixture.allowedRoot, "data")}, database: fixture.database, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func insertScannerRoot(t *testing.T, fixture *integrationTestApplication, name string) registeredRoot {
	t.Helper()
	root := registeredRoot{ID: createIdentifier(), Path: filepath.Join(fixture.allowedRoot, name)}
	if err := os.MkdirAll(root.Path, 0o755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if _, err := fixture.database.connection.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, root.ID, root.Path); err != nil {
		t.Fatalf("insert root: %v", err)
	}
	return root
}

func insertRunningScan(t *testing.T, fixture *integrationTestApplication) string {
	t.Helper()
	id := createIdentifier()
	if _, err := fixture.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, id); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	return id
}

func readTrackIDs(t *testing.T, fixture *integrationTestApplication, rootID string) []string {
	t.Helper()
	rows, err := fixture.database.connection.Query(`SELECT id::text FROM tracks WHERE source_root_id=$1::uuid ORDER BY relative_path`, rootID)
	if err != nil {
		t.Fatalf("query track ids: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan track id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate track ids: %v", err)
	}
	return ids
}

func assertDatabaseCount(t *testing.T, fixture *integrationTestApplication, query string, expected int, args ...any) {
	t.Helper()
	var count int
	if err := fixture.database.connection.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != expected {
		t.Fatalf("count = %d, want %d for %s", count, expected, query)
	}
}

func minimalPNGArtwork() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1}
}
