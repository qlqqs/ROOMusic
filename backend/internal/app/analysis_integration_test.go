package app

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

type analysisDBTrack struct{ id, root, release string }

func insertAnalysisTrack(t *testing.T, application *integrationTestApplication, rootID, releaseID, groupID string, relative string) analysisDBTrack {
	t.Helper()
	mediumID, trackID := uuid.New(), uuid.New()
	var mediumPosition int
	if err := application.database.QueryRow(`SELECT COALESCE(MAX(position),0)+1 FROM media WHERE release_id=$1::uuid`, releaseID).Scan(&mediumPosition); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,$3,'Medium')`, mediumID, releaseID, mediumPosition); err != nil {
		t.Fatal(err)
	}
	_, err := application.database.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,codec,source_size,source_modified_at,content_fingerprint)
		VALUES($1::uuid,$2::uuid,1,'Track','Artist',1,$3::uuid,$4,'present',NOW(),'flac_vorbis_comment','flac',1234,NOW(),$5)`, trackID, mediumID, rootID, relative, "fingerprint-"+trackID)
	if err != nil {
		t.Fatalf("insert analysis track: %v", err)
	}
	return analysisDBTrack{id: trackID, root: rootID, release: releaseID}
}

func newAnalysisDBFixture(t *testing.T) (*integrationTestApplication, string, string, string) {
	t.Helper()
	application := newIntegrationTestApplication(t)
	rootPath := application.allowedRoot + "/audio-analysis"
	if err := mkdirAllForTest(rootPath); err != nil {
		t.Fatal(err)
	}
	rootID, groupID, releaseID := uuid.New(), uuid.New(), uuid.New()
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, rootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID); err != nil {
		t.Fatal(err)
	}
	_, err := application.database.Exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,'Album','Artist',$3::uuid,'audio-analysis',$4,'ordinary_directory')`, releaseID, groupID, rootID, rootID+":release")
	if err != nil {
		t.Fatalf("insert analysis release: %v", err)
	}
	return application, rootID, groupID, releaseID
}

func mkdirAllForTest(path string) error { return os.MkdirAll(path, 0o755) }

func TestPostgreSQLAnalysisCandidatesFollowSnapshotVersionAndFailureCooldown(t *testing.T) {
	application, rootID, groupID, releaseID := newAnalysisDBFixture(t)
	track := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "one.flac")
	store := catalog.NewStore(application.database)
	ctx := context.Background()
	candidates, err := store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("new track candidates=%d err=%v", len(candidates), err)
	}
	candidate := candidates[0]
	done := catalog.TrackAnalysisResult{Status: "done", IntegratedLUFS: floatPtr(-20), TruePeakDBTP: floatPtr(-3), DurationMS: int32Ptr(1000), Waveform: []byte{1}}
	if err := store.SaveTrackAnalysis(ctx, candidate, analysis.Version, done); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("analyzed track candidates=%d err=%v", len(candidates), err)
	}
	if _, err := application.database.Exec(`UPDATE tracks SET source_size=source_size+1 WHERE id=$1::uuid`, track.id); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("snapshot changed candidates=%d err=%v", len(candidates), err)
	}
	if err := store.SaveTrackAnalysis(ctx, candidates[0], analysis.Version+1, done); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("version changed candidates=%d err=%v", len(candidates), err)
	}
	if err := store.SaveTrackAnalysis(ctx, candidates[0], analysis.Version, catalog.TrackAnalysisResult{Status: "failed", FailureCategory: "ffmpeg_exit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`UPDATE track_analyses SET analyzed_at=NOW() WHERE track_id=$1::uuid`, track.id); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("failure in cooldown candidates=%d err=%v", len(candidates), err)
	}
	if _, err := application.database.Exec(`UPDATE track_analyses SET analyzed_at=NOW()-INTERVAL '25 hours' WHERE track_id=$1::uuid`, track.id); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expired cooldown candidates=%d err=%v", len(candidates), err)
	}
	if err := store.SaveTrackAnalysis(ctx, candidates[0], analysis.Version, catalog.TrackAnalysisResult{Status: "failed", FailureCategory: "ffmpeg_exit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`UPDATE track_analyses SET attempts=3,analyzed_at=NOW()-INTERVAL '25 hours' WHERE track_id=$1::uuid`, track.id); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("exhausted failure candidates=%d err=%v", len(candidates), err)
	}
}

func TestPostgreSQLAnalysisEndpointsExposeProtectedReadiness(t *testing.T) {
	application, rootID, groupID, releaseID := newAnalysisDBFixture(t)
	track := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "one.flac")
	doneTrack := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "two.flac")
	failedTrack := insertAnalysisTrack(t, application, rootID, releaseID, groupID, "three.flac")
	store := catalog.NewStore(application.database)
	candidates, err := store.ListAnalysisCandidates(context.Background(), analysis.Version, 10)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("candidate setup = %d, %v", len(candidates), err)
	}
	byID := make(map[string]catalog.TrackAnalysisCandidate)
	for _, candidate := range candidates {
		byID[candidate.TrackID] = candidate
	}
	if err := store.SaveTrackAnalysis(context.Background(), byID[doneTrack.id], analysis.Version, catalog.TrackAnalysisResult{Status: "done", IntegratedLUFS: floatPtr(-20), TruePeakDBTP: floatPtr(-2), DurationMS: int32Ptr(1000), Waveform: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrackAnalysis(context.Background(), byID[failedTrack.id], analysis.Version, catalog.TrackAnalysisResult{Status: "failed", FailureCategory: "ffmpeg_exit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`UPDATE track_analyses SET attempts=3 WHERE track_id=$1::uuid`, failedTrack.id); err != nil {
		t.Fatal(err)
	}
	member := application.createUser(t, "user")
	response := application.request(t, http.MethodGet, "/api/v1/tracks/"+track.id+"/analysis", nil, &member, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("analysis status=%d body=%s", response.Code, response.Body.String())
	}
	if got := decodeJSONMap(t, response)["status"]; got != "pending" {
		t.Fatalf("analysis state=%v, want pending", got)
	}
	if unauth := application.request(t, http.MethodGet, "/api/v1/tracks/"+track.id+"/analysis", nil, nil, nil); unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated analysis status=%d", unauth.Code)
	}
	missing := application.request(t, http.MethodGet, "/api/v1/tracks/"+uuid.New()+"/analysis", nil, &member, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing track status=%d", missing.Code)
	}
	progress := application.request(t, http.MethodGet, "/api/v1/audio-analysis/progress", nil, &member, nil)
	if progress.Code != http.StatusOK {
		t.Fatalf("progress status=%d body=%s", progress.Code, progress.Body.String())
	}
	body := decodeJSONMap(t, progress)
	if body["state"] != "unavailable" || body["total"] != float64(3) || body["analyzed"] != float64(1) || body["failed"] != float64(1) {
		t.Fatalf("unavailable progress = %#v", body)
	}
}

func floatPtr(value float64) *float64 { return &value }
func int32Ptr(value int32) *int32     { return &value }

func TestPostgreSQLAnalysisTrackAndAlbumGainWaitForPendingTracks(t *testing.T) {
	application, rootID, groupID, releaseID := newAnalysisDBFixture(t)
	tracks := []analysisDBTrack{
		insertAnalysisTrack(t, application, rootID, releaseID, groupID, "one.flac"),
		insertAnalysisTrack(t, application, rootID, releaseID, groupID, "two.flac"),
		insertAnalysisTrack(t, application, rootID, releaseID, groupID, "three.flac"),
	}
	store := catalog.NewStore(application.database)
	ctx := context.Background()
	candidates, err := store.ListAnalysisCandidates(ctx, analysis.Version, 10)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	byID := make(map[string]catalog.TrackAnalysisCandidate)
	for _, candidate := range candidates {
		byID[candidate.TrackID] = candidate
	}
	first := catalog.TrackAnalysisResult{Status: "done", IntegratedLUFS: floatPtr(-22.4), TruePeakDBTP: floatPtr(-6.1), DurationMS: int32Ptr(1000), Waveform: []byte{3}}
	second := catalog.TrackAnalysisResult{Status: "done", IntegratedLUFS: floatPtr(-18), TruePeakDBTP: floatPtr(-1), DurationMS: int32Ptr(3000), Waveform: []byte{4}}
	for _, result := range []struct {
		id    string
		value catalog.TrackAnalysisResult
	}{{tracks[0].id, first}, {tracks[1].id, second}} {
		if err := store.SaveTrackAnalysis(ctx, byID[result.id], analysis.Version, result.value); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LoadTrackAnalysis(ctx, tracks[0].id, analysis.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Analysis == nil || got.Analysis.TrackGainDB == nil || *got.Analysis.TrackGainDB != 5.6 {
		t.Fatalf("positive track gain = %+v, want +5.6 dB capped by true peak", got.Analysis)
	}
	if got.Album != nil {
		t.Fatalf("album analysis while third track is pending = %+v, want null", got.Album)
	}
	if err := store.SaveTrackAnalysis(ctx, byID[tracks[2].id], analysis.Version, catalog.TrackAnalysisResult{Status: "failed", FailureCategory: "ffmpeg_exit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.database.Exec(`UPDATE track_analyses SET attempts=3 WHERE track_id=$1::uuid`, tracks[2].id); err != nil {
		t.Fatal(err)
	}
	got, err = store.LoadTrackAnalysis(ctx, tracks[0].id, analysis.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Album == nil {
		t.Fatal("terminal failed track blocked album analysis")
	}
	if got.Album.PeakDBTP != -1 || got.Album.GainDB != 0.5 {
		t.Fatalf("album analysis = %+v, want +0.5 dB limited by -1 dBTP peak", got.Album)
	}
	failed, err := store.LoadTrackAnalysis(ctx, tracks[2].id, analysis.Version)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.Analysis != nil {
		t.Fatalf("terminal failure response = %+v", failed)
	}
}
