package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const artistWebEdition = "30000000-0000-4000-8000-000000000001"
const artistWebSecondEdition = "30000000-0000-4000-8000-000000000098"
const artistWebGroup = "20000000-0000-4000-8000-000000000001"
const artistWebOtherGroup = "20000000-0000-4000-8000-000000000098"

func TestPostgreSQLArtistWebSearchAlbumStructure(t *testing.T) {
	for _, tc := range []struct {
		name, secondGroup, linkType, linkID string
		localArtist                         string
		trackCount, firstDuration           int
	}{
		{name: "unique", trackCount: 2, firstDuration: 180, linkType: "release", linkID: artistWebEdition},
		{name: "same_group", secondGroup: artistWebGroup, trackCount: 2, firstDuration: 180, linkType: "release_group_match", linkID: artistWebGroup},
		{name: "cross_group", secondGroup: artistWebOtherGroup, trackCount: 2, firstDuration: 180},
		{name: "track_count_mismatch", trackCount: 1, firstDuration: 180},
		{name: "duration_mismatch", trackCount: 2, firstDuration: 30},
		{name: "different_artist_name", localArtist: "另一种本地艺名", trackCount: 2, firstDuration: 180, linkType: "release", linkID: artistWebEdition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newArtistWebSearchFixture(t, "identity", false, true, artistWebEditions(tc.secondGroup, tc.localArtist))
			f.configureMock(t)
			app := f.application
			localArtist := webSearchArtistName
			if tc.localArtist != "" {
				localArtist = tc.localArtist
			}
			release, group := insertGroupedRelease(t, app, f.rootID, groupedRelease{
				title: "潮线以北", artist: localArtist, tracks: slices.Repeat([]string{"present"}, tc.trackCount),
			})
			mustExec(t, app, `UPDATE tracks t SET duration_seconds=CASE t.position WHEN 1 THEN $2 ELSE 192 END,
 title=CASE t.position WHEN 1 THEN '潮汐信箱' ELSE '细雨码头' END FROM media m WHERE m.id=t.medium_id AND m.release_id=$1`, release, tc.firstDuration)
			publishSearch(t, app)
			artist := f.artistID(t, localArtist)
			item := f.startArtist(t, artist)
			if tc.trackCount != 2 || tc.firstDuration != 180 {
				f.waitArtistResolution(t, item.ID, "suggestion_recorded")
				candidate := f.status(t, artist, "waiting_human", 1).Candidates[0]
				if candidate.ExternalID != artistMediaMBID {
					t.Errorf("结构不符时的候选 %+v", candidate)
				}
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_id IN ($1,$2,$3)`, artist, release, group)
			} else {
				deadline := time.Now().Add(12 * time.Second)
				for f.item(t, item.ID).Status != "resolved" && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
				}
				got := f.item(t, item.ID)
				resolution := ""
				if got.Resolution != nil {
					resolution = *got.Resolution
				}
				if resolution != "model_linked" {
					t.Fatalf("结构吻合应写入对应，实际状态 %s、结论 %q，工具结果 %s", got.Status, resolution, f.modelToolResults(t))
				}
				f.requireModelArtistLink(t, artist, "musicbrainz", artistMediaMBID, false)
				if tc.linkType != "" {
					b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND object_type=$2 AND external_id=$3`, release, tc.linkType, tc.linkID)
				}
				if tc.linkType != "release" {
					b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_id=$1 AND object_type='release'`, release)
					b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field IN ('label','catalog_number','barcode','country','edition_date')`, release)
				}
				if tc.name == "cross_group" {
					b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_id IN ($1,$2)`, release, group)
					b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_release_match_results WHERE release_id=$1 AND status='conflict' AND 'different_release_groups'=ANY(conflicts)`, release)
				}
				if tc.name == "unique" {
					artistOp := programString(t, app, `SELECT operation_id FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
					releaseOp := programString(t, app, `SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1`, release)
					b3RequireCount(t, app, 1, `SELECT count(*) FROM operations a JOIN operations r ON r.batch_id=a.batch_id
 JOIN curation_batches b ON b.id=a.batch_id WHERE a.id=$1 AND r.id=$2 AND b.tier='model'
 AND a.origin->>'provider'=r.origin->>'provider' AND a.origin->>'conversation_id'=r.origin->>'conversation_id'
 AND a.xmin=r.xmin`, artistOp, releaseOp)
					f.undo(t, releaseOp)
					f.undo(t, artistOp)
					b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_id IN ($1,$2,$3)
 OR object_id IN (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$2)`, artist, release, group)
				}
			}
			b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_plans WHERE conversation_id=$1`, f.item(t, item.ID).ConversationID)
		})
	}
}

// 把现有合成发行复制成结构一致的第二版，只替换发行和组的编号。
func artistWebEditions(group, alternateArtist string) func(http.Handler) http.Handler {
	return func(mock http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const path = "/musicbrainz/ws/2/release"
			if r.URL.Path == "/searxng/search" && alternateArtist != "" {
				// 合成名录也能由另一种署名搜到。
				probe := r.Clone(r.Context())
				query := probe.URL.Query()
				query.Set("q", webSearchArtistName)
				probe.URL.RawQuery = query.Encode()
				mock.ServeHTTP(w, probe)
				return
			}
			if r.URL.Path == "/searxng/artists/coast" && alternateArtist != "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte("<html><body><p>另一种本地艺名是纸屿二重奏的本地署名，作品包括潮线以北。</p></body></html>"))
				return
			}
			if group == "" {
				mock.ServeHTTP(w, r)
				return
			}
			switch {
			case r.URL.Path == path+"/"+artistWebSecondEdition:
				probe := r.Clone(r.Context())
				probe.URL.Path = path + "/" + artistWebEdition
				response := httptest.NewRecorder()
				mock.ServeHTTP(response, probe)
				raw := bytes.ReplaceAll(response.Body.Bytes(), []byte(artistWebEdition), []byte(artistWebSecondEdition))
				raw = bytes.ReplaceAll(raw, []byte(artistWebGroup), []byte(group))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.Code)
				w.Write(raw)
			case r.URL.Path == path && strings.Contains(r.URL.Query().Get("query"), artistMediaMBID):
				response := httptest.NewRecorder()
				mock.ServeHTTP(response, r)
				var page struct {
					Count    int               `json:"count"`
					Offset   int               `json:"offset"`
					Releases []json.RawMessage `json:"releases"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Releases) != 1 {
					http.Error(w, "合成发行检索应只返回原版", http.StatusInternalServerError)
					return
				}
				second := bytes.ReplaceAll(page.Releases[0], []byte(artistWebEdition), []byte(artistWebSecondEdition))
				second = bytes.ReplaceAll(second, []byte(artistWebGroup), []byte(group))
				page.Count++
				page.Releases = append(page.Releases, second)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(page)
			default:
				mock.ServeHTTP(w, r)
			}
		})
	}
}

func TestPostgreSQLArtistWebSearchStaleCandidate(t *testing.T) {
	for _, change := range []string{"local_evidence", "upstream_candidate"} {
		t.Run(change, func(t *testing.T) {
			upstreamChanged := make(chan struct{})
			f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/musicbrainz/ws/2/artist/"+artistMediaMBID {
					return false
				}
				name := webSearchArtistName
				select {
				case <-upstreamChanged:
					name = "合成名录已修订"
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"id": artistMediaMBID, "name": name, "type": "Group"})
				return true
			})
			track := f.addRelease(t, webSearchArtistName)
			publishSearch(t, f.application)
			artist := f.artistID(t, webSearchArtistName)
			item := f.startArtist(t, artist)
			f.next(t, false).tool(t, "web_search", map[string]string{"q": webSearchArtistName})
			f.next(t, false).tool(t, "resolve_artist_identity", webSearchIdentityProposal(item.ID, "musicbrainz", artistMediaMBID, f.endpoint+"/searxng/artists/coast"))
			f.waitArtistResolution(t, item.ID, "suggestion_recorded")
			id := f.status(t, artist, "waiting_human", 1).Candidates[0].ExceptionID
			if change == "local_evidence" {
				mustExec(t, f.application, `UPDATE tracks SET title='本地证据已更新' WHERE id=$1`, track)
			} else {
				close(upstreamChanged)
				// 删除本测试的来源响应缓存，让确认请求重新读模拟上游。
				requireCurationOK(t, os.RemoveAll(filepath.Join(f.application.dataDirectory, "sources", "cache", "musicbrainz")))
			}
			requireAPIError(t, f.decide(t, &f.admin, id, "accept"), 409, "exception_stale")
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_plans WHERE conversation_id=$1`, f.item(t, item.ID).ConversationID)
		})
	}
}

func TestPostgreSQLArtistWebSearchCandidateDecisions(t *testing.T) {
	for _, decision := range []string{"accept", "decline"} {
		t.Run(decision, func(t *testing.T) {
			f := newControlledArtistWebSearch(t, nil)
			artist := f.addArtist(t, webSearchArtistName)
			item := f.startArtist(t, artist)
			f.next(t, false).tool(t, "web_search", map[string]string{"q": webSearchArtistName})
			f.next(t, false).tool(t, "resolve_artist_identity", webSearchIdentityProposal(item.ID, "musicbrainz", artistMediaMBID, f.endpoint+"/searxng/artists/coast"))
			f.waitArtistResolution(t, item.ID, "suggestion_recorded")
			candidate := f.status(t, artist, "waiting_human", 1).Candidates[0]
			if decision == "accept" {
				readExceptionDecision(t, f.decide(t, &f.admin, candidate.ExceptionID, decision), "accepted", f.admin.id)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1 AND source='musicbrainz' AND external_id=$2`, artist, artistMediaMBID)
			} else {
				readExceptionDecision(t, f.decide(t, &f.admin, candidate.ExceptionID, decision), "declined", f.admin.id)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_suppressions WHERE target_type='artist' AND target_id=$1 AND subject='external_link'`, artist)
			}
			f.status(t, artist, "none", 0)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_plans WHERE conversation_id=$1`, f.item(t, item.ID).ConversationID)
			if candidate := f.item(t, item.ID); candidate.ArtistWebGeneration == nil {
				t.Error("候选夹具必须来自手动代次")
			}
		})
	}
}
