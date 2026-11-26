package program

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQLProgramRuleUpgradeUsesExistingID(t *testing.T) {
	for _, tc := range []struct {
		level   string
		request bool
	}{
		{"release", true}, {"recording", true}, {"release_group", false}, {"artist", false},
	} {
		t.Run(tc.level, func(t *testing.T) {
			kind := "release_match"
			if tc.level == "recording" || tc.level == "artist" {
				kind = "recording_match"
			}
			f := newShelvedPendingFixture(t, kind)
			ctx := t.Context()
			old := matchedRecheckVersions
			matchedRecheckVersions = map[string]int16{"release": MatchRuleVersion, "recording": MatchRuleVersion}
			t.Cleanup(func() { matchedRecheckVersions = old })
			var trackID string
			requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
				WHERE m.release_id=$1 AND t.source_status='present' ORDER BY t.position LIMIT 1`, f.releaseID).Scan(&trackID))
			f.exec(t, `UPDATE tracks SET duration_seconds=100 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.releaseID)
			transitions := map[string]struct{ kind, objectID, externalID string }{
				"release":       {"release", f.releaseID, f.remote.ID},
				"recording":     {"track", trackID, f.remote.Media[0].Tracks[0].RecordingID},
				"release_group": {"release_group_match", f.releaseID, f.remote.Group.ID},
			}
			link := transitions[tc.level]
			if tc.level == "release" || tc.level == "recording" {
				f.exec(t, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
					VALUES(gen_random_uuid(),'musicbrainz',$1,$2,$3,'search','certain')`, link.kind, link.objectID, link.externalID)
			}
			f.remote.Complete = true
			f.finder.releases[f.remote.ID] = f.remote
			ms := int64(100000)
			recID := f.remote.Media[0].Tracks[0].RecordingID
			f.finder.recordings = map[string]sources.Recording{recID: {ID: recID, Title: "曲1", Artists: f.remote.Artists, LengthMS: &ms}}
			rows, err := catalog.LoadCurationEvidence(ctx, f.db, []string{f.releaseID})
			requirePendingRefreshOK(t, err)
			e := rows[f.releaseID]
			if tc.level == "release_group" {
				e.ReleaseGroupMatchID = link.externalID
			}
			if tc.level == "artist" {
				e.ArtistMBID = f.remote.Artists[0].ArtistID
			}
			state := curation.WorkState{Subject: "release", TargetID: f.releaseID, Status: "matched",
				RuleVersion: MatchRuleVersion - 1, MatchRuleVersion: MatchRuleVersion - 1, Generation: 7}
			digests := releaseInputs(e)
			digests.record(&state)
			requirePendingRefreshOK(t, curation.SaveWorkState(ctx, f.db, state))
			deferred, matched, err := f.worker.prepareRelease(ctx, e, digests, &state)
			requirePendingRefreshOK(t, err)
			if !deferred || matched != nil || (state.RequeryMatchVersion == MatchRuleVersion) != tc.request || len(f.finder.releaseReads)+len(f.finder.recordingReads) != 0 {
				t.Fatalf("本地升级登记错误：deferred=%t 结果=%+v 状态=%+v", deferred, matched, state)
			}
			if !tc.request {
				return
			}
			got, err := f.worker.searchRelease(ctx, &unit{releaseState: state}, e, false)
			requirePendingRefreshOK(t, err)
			if got.Status != "matched" || got.Level != tc.level || f.finder.searches+f.finder.recordingSearches != 0 {
				t.Fatalf("应只复核已有%s：%+v，搜索 %d/%d", tc.level, got, f.finder.searches, f.finder.recordingSearches)
			}
			if tc.level == "release" && (!slices.Equal(f.finder.releaseReads, []string{link.externalID}) || len(f.finder.recordingReads) != 0) ||
				tc.level == "recording" && (!slices.Equal(f.finder.recordingReads, []string{link.externalID}) || len(f.finder.releaseReads) != 0) {
				t.Fatalf("仅应读取 %s，发行 %v，录音 %v", link.externalID, f.finder.releaseReads, f.finder.recordingReads)
			}
		})
	}
}

func TestPostgreSQLProgramFileDigestChanges(t *testing.T) {
	f := newPendingRefreshFixture(t)
	ctx := t.Context()
	release, first := f.release(t, "合成唱片", "合成艺人")
	f.exec(t, `UPDATE tracks SET source_size=240, source_modified_at='2026-01-02T00:00:00Z'
		WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
	read := func() catalog.CurationEvidence {
		t.Helper()
		rows, err := catalog.LoadCurationEvidence(ctx, f.db, []string{release})
		requirePendingRefreshOK(t, err)
		e, ok := rows[release]
		if !ok || e.FileDigest == "" {
			t.Fatalf("合成发行没有文件基线：%+v", rows)
		}
		return e
	}
	before := read()
	f.exec(t, `UPDATE releases SET title='重新解析的同一文件' WHERE id=$1`, release)
	f.exec(t, `UPDATE tracks SET title='重新解析的曲名' WHERE id=$1`, first)
	projected := read()
	if projected.FileDigest != before.FileDigest || ReleaseDigest(projected) == ReleaseDigest(before) {
		t.Fatalf("仅解析值改动时，文件摘要 %q → %q，输入摘要 %q → %q",
			before.FileDigest, projected.FileDigest, ReleaseDigest(before), ReleaseDigest(projected))
	}
	f.exec(t, `UPDATE tracks SET source_modified_at=source_modified_at+interval '1 microsecond' WHERE id=$1`, first)
	modified := read()
	if modified.FileDigest == before.FileDigest {
		t.Fatal("同样大小的音频写入后应改变文件摘要")
	}
	f.exec(t, `UPDATE tracks SET source_status='missing' WHERE id=$1`, first)
	deleted := read()
	if deleted.FileDigest == modified.FileDigest {
		t.Fatal("删除成员未改变文件摘要")
	}
	f.exec(t, `UPDATE tracks SET source_status='present' WHERE id=$1`, first)
	if again := read(); again.FileDigest != modified.FileDigest {
		t.Fatalf("恢复同一成员应复现原摘要，实际 %q，期望 %q", again.FileDigest, modified.FileDigest)
	}
	f.exec(t, `UPDATE tracks SET cue_parent_relative_path='synthetic.flac', cue_sheet_path='synthetic.cue', cue_index_frames=0, cue_end_frames=7500 WHERE id=$1`, first)
	f.exec(t, `UPDATE releases SET curation_member_signature=decode(repeat('01',32),'hex') WHERE id=$1`, release)
	cue := read()
	f.exec(t, `UPDATE tracks SET cue_index_frames=75 WHERE id=$1`, first)
	if sameSize := read(); sameSize.FileDigest != cue.FileDigest {
		t.Fatalf("等长 CUE 文本修改局限：解析索引变了时文件摘要应保持 %q，实际 %q", cue.FileDigest, sameSize.FileDigest)
	}
	f.exec(t, `UPDATE releases SET curation_member_signature=decode(repeat('02',32),'hex') WHERE id=$1`, release)
	if resized := read(); resized.FileDigest == cue.FileDigest {
		t.Fatal("CUE 大小变化后成员签名已更新，文件摘要应改变")
	}
}

func TestProgramRuleUpgradeStatePolicy(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"matched", false}, {"unmatched", true}, {"conflict", true}, {"query_error", true},
		{"try_later", false}, {"done", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			if got := recheckOnUpgrade(tc.status); got != tc.want {
				t.Fatalf("规则升级是否重查 %t，期望 %t", got, tc.want)
			}
		})
	}
}

func TestProgramCreditSplitPlans(t *testing.T) {
	artists := []sources.Credit{{Name: "Jay-Z", ArtistID: uuid.New(), JoinPhrase: " & "}, {Name: "Kanye West", ArtistID: uuid.New()}}
	for _, tc := range []struct {
		name, text string
		corrected  bool
		want       []string
	}{
		{"exact", "Jay-Z & Kanye West", false, []string{"release_artist", "album_artist", "track_artist"}},
		{"different-join", "Jay-Z feat. Kanye West", false, nil},
		{"human-correction", "Jay-Z & Kanye West", true, []string{"release_artist", "album_artist"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := b3Album(1)
			e.Artist, e.AlbumArtist, e.Tracks[0].Artist = tc.text, tc.text, tc.text
			e.Tracks[0].ArtistCorrected = tc.corrected
			r.Artists, r.Media[0].Tracks[0].Artists = artists, artists
			credit := []catalog.CurationArtistCredit{{Position: 1, Name: tc.text, ArtistID: uuid.New()}}
			local := catalog.CurationArtistCredits{ReleaseArtists: credit, AlbumArtists: credit,
				Tracks: map[string][]catalog.CurationArtistCredit{e.Tracks[0].ID: credit}}
			m := MatchResult{Level: "release", Release: &r, Pairs: []TrackPair{{e.Tracks[0].ID, r.Media[0].Tracks[0].RecordingID}}}
			var roles []string
			splits, requests := planCreditSplits(local, e, m)
			var wantRequests localCreditRequests
			if tc.name == "different-join" {
				wantRequests = localCreditRequests{{"", "release_artist", tc.text}, {"", "album_artist", tc.text}, {e.Tracks[0].ID, "track_artist", tc.text}}
			}
			if !reflect.DeepEqual(requests, wantRequests) {
				t.Fatalf("本地署名请求 %+v，期望 %+v", requests, wantRequests)
			}
			for _, split := range splits {
				roles = append(roles, split.Role)
				if split.LocalText != tc.text || !reflect.DeepEqual(split.Credits, artists) {
					t.Fatalf("拆分应保留署名原文及外部位置：%+v", split)
				}
				switch split.Role {
				case "track_artist":
					if split.TrackID != e.Tracks[0].ID || split.BasisType != "track" || split.BasisID != m.Pairs[0].RecordingID {
						t.Fatalf("曲目拆分需要录音依据：%+v", split)
					}
				default:
					if split.TrackID != "" || split.BasisType != "release" || split.BasisID != r.ID {
						t.Fatalf("专辑拆分需要版级依据：%+v", split)
					}
				}
			}
			if !slices.Equal(roles, tc.want) {
				t.Fatalf("拆分角色 %v，期望 %v", roles, tc.want)
			}
		})
	}
}

func TestProgramUnmatchedRecheckSchedule(t *testing.T) {
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, day := range []int{89, 90, 179, 180} {
		due := start.Add(90 * 24 * time.Hour)
		if day >= 179 {
			due = due.Add(90 * 24 * time.Hour)
		}
		state := curation.WorkState{Status: "unmatched", FileDigest: "same", NextAttemptAt: &due}
		now := start.Add(time.Duration(day) * 24 * time.Hour)
		want := day%90 == 0
		if got := needsWork("release", state, "same", now, true); got != want {
			t.Errorf("第 %d 天是否重查 %t，期望 %t", day, got, want)
		}
		state.NextAttemptAt = nil
		if needsWork("release", state, "same", now, true) {
			t.Errorf("无到期安排的撤销项在第 %d 天进入重查", day)
		}
		state.ManualRequestedAt = &now
		if !needsWork("release", state, "same", now, true) {
			t.Errorf("第 %d 天的手动申请未入队", day)
		}
	}
}
