package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPostgreSQLCatalogReadProjectionAndEvidencePermissions(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")

	rootID := createIdentifier()
	if _, err := application.database.connection.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, application.allowedRoot); err != nil {
		t.Fatalf("insert catalog root fixture: %v", err)
	}
	scanID := createIdentifier()
	if _, err := application.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1::uuid,'incomplete',NOW(),NOW())`, scanID); err != nil {
		t.Fatalf("insert scan fixture: %v", err)
	}

	releaseID := createCatalogReleaseFixture(t, application, rootID, "CUE 专辑", "专辑艺术家", "current", true)
	var mediumID string
	if err := application.database.connection.QueryRow(`SELECT id::text FROM media WHERE release_id=$1::uuid`, releaseID).Scan(&mediumID); err != nil {
		t.Fatalf("read release medium: %v", err)
	}
	trackID := createIdentifier()
	if _, err := application.database.connection.Exec(`INSERT INTO tracks(
		id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,
		source_kind,source_identity,duration_seconds,codec,bit_depth,sample_rate,channels,bitrate,
		cue_sheet_path,cue_parent_relative_path,cue_referenced_file,cue_index_frames,cue_end_frames,cue_isrc
	) VALUES(
		$1::uuid,$2::uuid,1,'第一轨','曲目艺术家',1,$3::uuid,'CUE Album/image.flac#track-1','present',NOW(),
		'cue_virtual',$4,240.0,'flac',24,96000,2,2400,
		'CUE Album/album.cue','CUE Album/image.flac','image.flac',0,18000,'USAAA2600001'
	)`, trackID, mediumID, rootID, rootID+":cue:v1:album"); err != nil {
		t.Fatalf("insert present CUE track: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES
		($1::uuid,'composer','作曲者',1),
		($1::uuid,'producer','制作人',2)`, trackID); err != nil {
		t.Fatalf("insert track credits: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO tracks(
		id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity
	) VALUES($1::uuid,$2::uuid,2,'缺失轨','曲目艺术家',1,$3::uuid,'CUE Album/missing.flac','missing',NOW()-INTERVAL '1 day','flac_vorbis_comment',$4)`, createIdentifier(), mediumID, rootID, rootID+":physical:v1:missing"); err != nil {
		t.Fatalf("insert missing track: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO release_field_decisions(
		release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason,scan_run_id
	) VALUES($1::uuid,'title',to_jsonb('CUE 专辑'::text),'tag','medium','uncertain_apply','majority_v1',to_jsonb(ARRAY['CUE 专辑','CUE 专辑（重制版）']::text[]),'inconsistent_candidates',$2::uuid)`, releaseID, scanID); err != nil {
		t.Fatalf("insert field evidence: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO release_grouping_evidence(
		release_id,candidate_kind,rule_id,source_refs,reason,scan_run_id
	) VALUES($1::uuid,'same_dir_split','organizer_v2',$2::jsonb,'same_directory_conflict',$3::uuid)`, releaseID, `["CUE Album/image.flac#track-1","/srv/private.flac"]`, scanID); err != nil {
		t.Fatalf("insert grouping evidence: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO release_credits(release_id,role,name,position) VALUES($1::uuid,'album_artist','专辑艺术家',1)`, releaseID); err != nil {
		t.Fatalf("insert release credit: %v", err)
	}

	legacyReleaseID := createCatalogReleaseFixture(t, application, rootID, "旧版专辑", "旧版艺术家", "legacy", true)
	hiddenReleaseID := createCatalogReleaseFixture(t, application, rootID, "空壳专辑", "艺术家", "hidden", false)

	allResponse := application.request(t, http.MethodGet, "/api/v1/releases", nil, &ordinary, nil)
	if allResponse.Code != http.StatusOK {
		t.Fatalf("list releases: HTTP %d: %s", allResponse.Code, allResponse.Body.String())
	}
	var allList struct {
		Items      []releaseSummaryDTO   `json:"items"`
		Pagination struct{ Total int64 } `json:"pagination"`
	}
	if err := json.Unmarshal(allResponse.Body.Bytes(), &allList); err != nil {
		t.Fatalf("decode release list: %v", err)
	}
	if allList.Pagination.Total != 2 || len(allList.Items) != 2 {
		t.Fatalf("present-only list mismatch: total=%d items=%+v", allList.Pagination.Total, allList.Items)
	}
	if strings.Contains(allResponse.Body.String(), hiddenReleaseID) {
		t.Fatal("missing-only release was visible in the ordinary list")
	}

	attentionResponse := application.request(t, http.MethodGet, "/api/v1/releases?attention=required", nil, &ordinary, nil)
	if attentionResponse.Code != http.StatusOK {
		t.Fatalf("list attention releases: HTTP %d: %s", attentionResponse.Code, attentionResponse.Body.String())
	}
	var attentionList struct {
		Items []releaseSummaryDTO `json:"items"`
	}
	if err := json.Unmarshal(attentionResponse.Body.Bytes(), &attentionList); err != nil {
		t.Fatalf("decode attention list: %v", err)
	}
	if len(attentionList.Items) != 1 || attentionList.Items[0].ID != releaseID || attentionList.Items[0].AttentionCount != 3 {
		t.Fatalf("unexpected attention projection: %+v", attentionList.Items)
	}
	if attentionList.Items[0].MediumCount != 1 || attentionList.Items[0].TrackCount != 1 || attentionList.Items[0].SourceType != nil || attentionList.Items[0].MediaType == nil || *attentionList.Items[0].MediaType != "CD" {
		t.Fatalf("unexpected release facts: %+v", attentionList.Items[0])
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/releases?attention=guessed", nil, &ordinary, nil), http.StatusBadRequest, "invalid_attention")

	detailResponse := application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &ordinary, nil)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("release detail: HTTP %d: %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail releaseDetailDTO
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode release detail: %v", err)
	}
	if len(detail.Media) != 1 || len(detail.Media[0].Tracks) != 1 {
		t.Fatalf("detail did not contain only present tracks: %+v", detail.Media)
	}
	track := detail.Media[0].Tracks[0]
	if track.Source != "image.flac#track-1" || track.BitDepth == nil || *track.BitDepth != 24 || track.Bitrate == nil || *track.Bitrate != 2400 || track.Cue == nil || track.Cue.IndexFrames == nil || *track.Cue.IndexFrames != 0 {
		t.Fatalf("audio/CUE facts were not projected: %+v", track)
	}
	if len(track.Credits) != 2 || track.Credits[0].Role != "composer" || track.Credits[0].Name != "作曲者" || track.Credits[1].Role != "producer" {
		t.Fatalf("track credits were not projected in order: %+v", track.Credits)
	}
	if detail.Edition == nil || *detail.Edition != "初回限定版" || detail.Label == nil || *detail.Label != "示例唱片公司" || detail.Barcode == nil || *detail.Barcode != "0123456789012" {
		t.Fatalf("release metadata was not projected: edition=%v label=%v barcode=%v", detail.Edition, detail.Label, detail.Barcode)
	}
	if len(detail.Credits) != 1 || detail.Credits[0].Name != "专辑艺术家" || len(detail.Evidence) != 1 {
		t.Fatalf("credit/evidence summary mismatch: credits=%+v evidence=%+v", detail.Credits, detail.Evidence)
	}
	if strings.Contains(detailResponse.Body.String(), "CUE Album/") || strings.Contains(detailResponse.Body.String(), "/srv/") || strings.Contains(detailResponse.Body.String(), "CUE 专辑（重制版）") {
		t.Fatal("ordinary detail disclosed source paths or administrator-only candidates")
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/releases/"+hiddenReleaseID, nil, &ordinary, nil), http.StatusNotFound, "not_found")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/releases/not-a-uuid", nil, &ordinary, nil), http.StatusBadRequest, "invalid_id")

	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID+"/evidence", nil, &ordinary, nil), http.StatusForbidden, "forbidden")
	evidenceResponse := application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID+"/evidence", nil, &admin, nil)
	if evidenceResponse.Code != http.StatusOK {
		t.Fatalf("administrator evidence: HTTP %d: %s", evidenceResponse.Code, evidenceResponse.Body.String())
	}
	var evidence releaseEvidenceResponseDTO
	if err := json.Unmarshal(evidenceResponse.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("decode evidence response: %v", err)
	}
	if len(evidence.Fields) != 1 || len(evidence.Fields[0].Candidates) != 2 || evidence.Grouping == nil || len(evidence.Grouping.SourceRefs) != 1 || !evidence.Truncated {
		t.Fatalf("unexpected bounded evidence: %+v", evidence)
	}
	if strings.Contains(evidenceResponse.Body.String(), "/srv/private.flac") {
		t.Fatal("administrator evidence disclosed an unsafe absolute source reference")
	}
	legacyEvidenceResponse := application.request(t, http.MethodGet, "/api/v1/releases/"+legacyReleaseID+"/evidence", nil, &admin, nil)
	if legacyEvidenceResponse.Code != http.StatusOK {
		t.Fatalf("legacy evidence response: HTTP %d: %s", legacyEvidenceResponse.Code, legacyEvidenceResponse.Body.String())
	}
	var legacyEvidence releaseEvidenceResponseDTO
	if err := json.Unmarshal(legacyEvidenceResponse.Body.Bytes(), &legacyEvidence); err != nil || len(legacyEvidence.Fields) != 0 || legacyEvidence.Grouping != nil {
		t.Fatalf("legacy evidence did not return a recoverable empty state: evidence=%+v err=%v", legacyEvidence, err)
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/releases/not-a-uuid/evidence", nil, &admin, nil), http.StatusBadRequest, "invalid_id")

	if _, err := application.database.connection.Exec(`INSERT INTO scan_diagnostics(scan_run_id,root_id,relative_path,kind,message) VALUES
		($1::uuid,$2::uuid,'CUE Album/broken.m4a','parse_failure','音频文件解析失败'),
		($1::uuid,$2::uuid,'/srv/private.flac','permission_denied','文件不可读取')`, scanID, rootID); err != nil {
		t.Fatalf("insert scan diagnostics: %v", err)
	}
	statusResponse := application.request(t, http.MethodGet, "/api/v1/scans/"+scanID, nil, &ordinary, nil)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("scan status: HTTP %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	var scan scanDTO
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &scan); err != nil || scan.Diagnostics.Total != 2 || len(scan.Diagnostics.Counts) != 2 {
		t.Fatalf("scan diagnostic aggregate mismatch: scan=%+v err=%v", scan, err)
	}
	diagnosticsResponse := application.request(t, http.MethodGet, "/api/v1/scans/"+scanID+"/diagnostics", nil, &admin, nil)
	if diagnosticsResponse.Code != http.StatusOK {
		t.Fatalf("scan diagnostics: HTTP %d: %s", diagnosticsResponse.Code, diagnosticsResponse.Body.String())
	}
	if strings.Contains(diagnosticsResponse.Body.String(), "/srv/private.flac") {
		t.Fatal("scan diagnostics disclosed an unsafe absolute path")
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/scans/not-a-uuid/diagnostics", nil, &admin, nil), http.StatusBadRequest, "invalid_id")
}

func createCatalogReleaseFixture(t *testing.T, application *integrationTestApplication, rootID, title, artist, anchorSuffix string, present bool) string {
	t.Helper()
	groupID, releaseID, mediumID := createIdentifier(), createIdentifier(), createIdentifier()
	if _, err := application.database.connection.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID); err != nil {
		t.Fatalf("insert release group fixture: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO releases(
		id,group_id,title,artist,album_artist,year,source_type,media_type,genre,catalog_number,edition,label,barcode,source_root_id,relative_directory,candidate_anchor,candidate_kind
	) VALUES($1::uuid,$2::uuid,$3,$4,$4,2026,NULL,'CD','Jazz','CAT-001','初回限定版','示例唱片公司','0123456789012',$5::uuid,$6,$7,'ordinary_directory')`, releaseID, groupID, title, artist, rootID, anchorSuffix, rootID+":v2:"+anchorSuffix); err != nil {
		t.Fatalf("insert release fixture: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, mediumID, releaseID); err != nil {
		t.Fatalf("insert medium fixture: %v", err)
	}
	if present {
		if title == "CUE 专辑" {
			return releaseID
		}
		if _, err := application.database.connection.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity) VALUES($1::uuid,$2::uuid,1,'旧版轨',$3,1,$4::uuid,$5,'present',NOW(),'physical',$6)`, createIdentifier(), mediumID, artist, rootID, anchorSuffix+"/track.flac", rootID+":physical:v1:"+anchorSuffix); err != nil {
			t.Fatalf("insert present legacy track: %v", err)
		}
	} else if _, err := application.database.connection.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity) VALUES($1::uuid,$2::uuid,1,'旧轨',$3,1,$4::uuid,$5,'missing',NOW(),'physical',$6)`, createIdentifier(), mediumID, artist, rootID, anchorSuffix+"/track.flac", rootID+":physical:v1:"+anchorSuffix); err != nil {
		t.Fatalf("insert missing-only track: %v", err)
	}
	return releaseID
}
