package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

// 两个版本共享标题、曲序和时长，各自有独立版本和录音编号。
func liveGapsEditions(tracks int) metricSample {
	artist := metricArtist{ID: uuid.New(), Name: "雾港刻钟组", Aliases: []string{"Mist Dial Folio"}}
	s := cMatchAlbum("回廊钟声", artist.Name, tracks, artist.ID)
	s.External.Artists = []metricArtist{artist}
	first := s.External.Releases[0]
	other := cMatchAlbum(first.Title, artist.Name, tracks, artist.ID).External.Releases[0]
	other.Group = first.Group
	first.Catalog, other.Catalog = "HG-71", "HG-93"
	s.External.Releases = []metricRelease{first, other}
	return s
}

func liveGapsSource(t *testing.T, s metricSample) http.Handler {
	t.Helper()
	mock, err := upstreammock.New(upstreammock.Options{DataDir: writeMetricCatalog(t, []metricSample{s})})
	requireCurationOK(t, err)
	return http.StripPrefix("/mb", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/musicbrainz/ws/2" + r.URL.Path
		mock.ServeHTTP(w, r)
	}))
}

func liveGapsLocal(t *testing.T, f *schedulerFixture, s metricSample, name string) (string, []string) {
	t.Helper()
	target, tracks := b3ReleaseTracks(t, f.application, f.rootID, name, s.Local.Artist, len(s.Local.Tracks))
	for i, track := range tracks {
		mustExec(t, f.application, `UPDATE tracks SET title=$2,duration_seconds=$3 WHERE id=$1`, track, s.Local.Tracks[i].Title, s.Local.Tracks[i].Seconds)
	}
	return target, tracks
}

func TestPostgreSQLLiveGapsArtistFromTaggedRelease(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "青釉折尺三重奏"}
	s := cMatchAlbum("岚桥纸谱", "", 3, artist.ID)
	version := s.External.Releases[0].ID
	s.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": version}
	s.External.Artists = []metricArtist{artist}
	s.Expect = metricExpect{Release: version, Artists: map[string]string{artist.Name: artist.ID}}
	t.Setenv("ROOMUSIC_CURATION_METRICS_MODEL", "oracle")
	app, entries, endpoint := runMetricSamples(t, []metricSample{s})
	entry := entries[0]
	if entry.drainIssue != "" {
		t.Fatal(entry.drainIssue)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz'
		AND object_type='release' AND object_id=$1 AND method='tag_id' AND external_id=$2`, entry.release, version)
	// 从补写后的有效专辑署名取得本地艺人，避免把别的同名艺人的对应算在本发行上。
	localID := programString(t, app, `SELECT c.artist_id FROM artist_effective_credits c
		JOIN artist_names n ON n.id=c.name_id WHERE c.release_id=$1 AND c.role='album_artist' AND n.spelling=$2`, entry.release, artist.Name)
	links := metricStoredLinks(t, app)[metricLinkSubject{"artist", localID}]
	var external []string
	for _, link := range links {
		if link.sourceID == "musicbrainz" {
			external = append(external, link.value)
		}
	}
	if len(external) != 1 || external[0] != artist.ID {
		t.Errorf("发行已按标签对应、专辑艺人已补写为 %s，本地艺人的 MusicBrainz 对应为 %v，期望 %s", artist.Name, external, artist.ID)
	}
	if n := endpoint.usage()[s.ID]["web"].Calls; n != 0 {
		t.Errorf("发行已提供艺人署名，联网档调用应为 0，实际 %d", n)
	}
	if n := entry.requests["searxng/search"]; n != 0 {
		t.Errorf("网页搜索请求应为 0，实际 %d", n)
	}
}

func TestPostgreSQLLiveGapsGroupTitle(t *testing.T) {
	const original = "回廊衷声"
	for _, tc := range []struct {
		name                        string
		editionKeepsOriginal, track bool
	}{
		{"组标题核实错字", false, false}, {"组内一个版本保留原题", true, false}, {"曲目标题没有组级佐证", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := liveGapsEditions(3)
			if tc.editionKeepsOriginal {
				s.External.Releases[1].Title = original
			}
			f := newSchedulerFixtureWithSources(t, nil, liveGapsSource(t, s))
			target, tracks := liveGapsLocal(t, f, s, original)
			v := curation.VerificationCandidate{ReleaseID: target, Field: "title", Before: original, After: s.External.Releases[0].GroupTitle}
			if tc.track {
				v.TrackID, v.Before, v.After = tracks[0], "回廊衷声第一节", s.Local.Tracks[0].Title
				mustExec(t, f.application, `UPDATE tracks SET title=$2 WHERE id=$1`, v.TrackID, v.Before)
			}
			f.enable(t)
			link := curationCLinkRequest(target, "", curationCLink("release_group_match", target, s.External.Releases[0].Group))
			link.Method = "search"
			curationCApplyLinks(t, f.curationFixture, link)
			id := queueModelVerification(t, f, "verify_name", v)
			f.configure(t, &f.admin)
			f.next(t, false).tool(t, "submit_verification", map[string]string{"item_id": id, "verdict": "typo", "reason": "核对目录中同组版本的标题"})
			if tc.editionKeepsOriginal || tc.track {
				modelWaitResolution(t, f, id, "suggestion_recorded")
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, target)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=ANY($1::uuid[])`, tracks)
			} else {
				review := f.next(t, true)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, target)
				if !strings.Contains(string(review.Messages[len(review.Messages)-1].Content), s.External.Releases[0].Group) {
					t.Error("审查材料缺少生效的发行组编号")
				}
				review.review(t, "approved")
				modelWaitResolution(t, f, id, "model_verified")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_auto_values a JOIN releases r ON r.id=a.release_id WHERE r.id=$1 AND r.title=$2 AND a.field='title' AND a.text_value=$3 AND a.writer_level=3`, target, original, v.After)
			}
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE (object_type='release' AND object_id=$1) OR (object_type='track' AND object_id=ANY($2::uuid[]))`, target, tracks)
		})
	}
}

func TestPostgreSQLLiveGapsReviewedEditions(t *testing.T) {
	t.Run("提交说明", func(t *testing.T) {
		prompt := backgroundPrompt(model.SelectCandidate)
		if !strings.Contains(prompt, "selected_id") || !strings.Contains(prompt, "groups") {
			t.Fatal(prompt)
		}
		for _, tdef := range stewardtools.StewardTools(nil, nil) {
			if tdef.Name == "resolve_release_match" {
				t.Fatal("后台仍开放旧版匹配工具")
			}
		}

	})
	t.Run("三首曲目经审查也只能建立组级对应", func(t *testing.T) {
		s := liveGapsEditions(3)
		gate := &testLookupGate{}
		gate.ready.Store(true)
		f := newSchedulerFixtureWithSources(t, gate, liveGapsSource(t, s))
		target, tracks := liveGapsLocal(t, f, s, s.Local.Album)
		p := modelRouteFixture(t, f, f.pending(t, target), "lookup")
		mustExec(t, f.application, `UPDATE curation_pending_items SET candidates='[]' WHERE id=$1`, p.ID)
		f.configure(t, &f.admin)
		f.enable(t)
		first := s.External.Releases[0]
		f.next(t, false).tool(t, "resolve_release_match", map[string]any{"item_id": p.ID, "external_id": first.ID, "basis": map[string]string{"note": "两版均有相同的三首歌与时长"}})
		f.next(t, true).review(t, "approved")
		modelWaitResolution(t, f, p.ID, "model_matched")
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1 AND external_id=$2`, target, first.Group)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE (object_type='release' AND object_id=$1) OR (object_type='track' AND object_id=ANY($2::uuid[]))`, target, tracks)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field IN ('barcode','label','catalog_number','year')`, target)
	})
}

func TestPostgreSQLLiveGapsSelection(t *testing.T) {
	t.Run("围栏内混合回复逐项生效", func(t *testing.T) {
		f := newSchedulerFixtureWithSources(t, &testLookupGate{}, nil)
		good := modelRouteFixture(t, f, f.pending(t, f.a), "")
		bad := modelRouteFixture(t, f, f.pending(t, f.b), "")
		f.configure(t, &f.admin)
		f.enable(t)
		q := f.next(t, false)
		prompt := string(q.Messages[len(q.Messages)-1].Content)
		if !strings.Contains(prompt, good.ID) || !strings.Contains(prompt, bad.ID) {
			t.Fatalf("首批未同时领取两项：%s", prompt)
		}
		payload := fmt.Sprintf("{\"items\":[{\"item_id\":%q,\"decision\":\"upgrade\"},{\"item_id\":%q,\"decision\":\"select\",\"external_id\":%q}]}", good.ID, bad.ID, uuid.New())
		q.reply <- q.frame(t, map[string]string{"content": "```json\n" + payload + "\n```"}, "stop")
		schedulerWait(t, "缺答项排定重试", func() bool { return f.item(t, bad.ID).NextAttemptAt != nil })
		f.clock.now.Store(f.item(t, bad.ID).NextAttemptAt.UnixNano())
		next := f.next(t, false)
		g, b := f.item(t, good.ID), f.item(t, bad.ID)
		if g.Resolution == nil || *g.Resolution != "undetermined" || g.Attempts != 1 || b.Attempts != 0 || b.EmptyCount != 1 || strings.Contains(string(next.Messages[len(next.Messages)-1].Content), good.ID) {
			t.Fatalf("定不了的项应结案并计次，不合格项单独重试；有效项 %s/%s attempts=%d，不合格项 %s/%s attempts=%d", g.Status, g.Route, g.Attempts, b.Status, b.Route, b.Attempts)
		}
		f.pause(t)
		next.complete(t)
	})
	t.Run("三次无效后搁置", func(t *testing.T) {
		f := newSchedulerFixtureWithSources(t, &testLookupGate{}, nil)
		p := modelRouteFixture(t, f, f.pending(t, f.a), "")
		f.configure(t, &f.admin)
		f.enable(t)
		for i := range 3 {
			f.next(t, false).selectCandidate(t, p.ID, uuid.New())
			if i < 2 {
				schedulerWait(t, "无效决定延后", func() bool { return f.item(t, p.ID).NextAttemptAt != nil })
				f.clock.now.Store(f.item(t, p.ID).NextAttemptAt.UnixNano())
			}
		}
		schedulerWait(t, "第三次非法决定释放领取", func() bool { v := f.item(t, p.ID); return v.Status == "shelved" || v.Status == "waiting_route" })
		v := f.item(t, p.ID)
		if v.Status != "shelved" || v.ShelvedReason == nil || *v.ShelvedReason != "模型没给出结果" {
			t.Fatalf("第三次非法决定后得到 %s/%s，尝试 %d", v.Status, v.Route, v.Attempts)
		}
	})
}

func TestPostgreSQLLiveGapsModelEvidence(t *testing.T) {
	s := liveGapsEditions(3)
	wrong := cMatchAlbum("旧桥单曲", s.Local.Artist, 1, s.External.Artists[0].ID).External.Releases[0]
	s.External.Releases = append(s.External.Releases, wrong)
	gate := &testLookupGate{}
	gate.ready.Store(true)
	f := newSchedulerFixtureWithSources(t, gate, liveGapsSource(t, s))
	target, _ := liveGapsLocal(t, f, s, s.Local.Album)
	item := f.pending(t, target)
	f.configure(t, &f.admin)
	f.enable(t)
	request := f.next(t, false)
	prompt := curationRulePrompt(t, request)
	for _, need := range []string{"authenticity", "duration_seconds", "disc", s.Local.Tracks[2].Title} {
		if !strings.Contains(prompt, need) {
			t.Errorf("首条消息缺少本地依据 %q", need)
		}
	}
	if len(request.Tools) != 0 {
		t.Fatal("识别请求带有工具")
	}
	request.undetermined(t, item.ID, "没有可核实的候选")
	modelWaitResolution(t, f, item.ID, "undetermined")
}
