package app

import (
	"encoding/json"
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
		explanations := map[string]string{"第二档": backgroundPrompt(model.LookupCandidate), "联网": backgroundPrompt(model.WebFallback)}
		for _, tool := range stewardtools.StewardTools(nil, nil) {
			if tool.Name == "resolve_release_match" {
				explanations[tool.Name] = tool.Description
			}
		}
		for which, msg := range explanations {
			if !strings.Contains(msg, "发行组") || !(strings.Contains(msg, "任一") || strings.Contains(msg, "任意") || strings.Contains(msg, "一个吻合")) {
				t.Errorf("%s 未说明可提交任一吻合版本并只落发行组：%s", which, msg)
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
		next := f.next(t, false)
		g, b := f.item(t, good.ID), f.item(t, bad.ID)
		if g.Route != "lookup" || g.Status != "waiting_route" || g.Attempts != 0 || b.Attempts != 1 || strings.Contains(string(next.Messages[len(next.Messages)-1].Content), good.ID) {
			t.Fatalf("有效项应升级且不计次，不合格项单独重试；有效项 %s/%s attempts=%d，不合格项 %s/%s attempts=%d", g.Status, g.Route, g.Attempts, b.Status, b.Route, b.Attempts)
		}
		f.pause(t)
		next.complete(t)
	})
	t.Run("三次无效只升第二档", func(t *testing.T) {
		f := newSchedulerFixtureWithSources(t, &testLookupGate{}, nil)
		p := modelRouteFixture(t, f, f.pending(t, f.a), "")
		f.configure(t, &f.admin)
		f.enable(t)
		for range 3 {
			f.next(t, false).selectCandidate(t, p.ID, uuid.New())
		}
		schedulerWait(t, "第三次非法决定释放领取", func() bool { v := f.item(t, p.ID); return v.Status == "shelved" || v.Status == "waiting_route" })
		v := f.item(t, p.ID)
		if v.Status != "waiting_route" || v.Route != "lookup" || v.ShelvedReason != nil {
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
	const canonical = "雾港刻钟三重奏"
	publishSearch(t, f.application)
	tx, err := f.application.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.ProjectPatrolInputs(t.Context(), tx, []string{target}))
	requireCurationOK(t, tx.Commit())
	mustExec(t, f.application, `UPDATE curation_patrol_names SET needs_patrol=false,confidence='high',patrolled_at=now(),model='合成巡检模型',prompt_version=1,original_name=CASE WHEN kind='artist' THEN $1 ELSE name END`, canonical)
	_, known, err := f.writer.CanonicalPatrolNames(t.Context(), target, "")
	requireCurationOK(t, err)
	if known != canonical {
		t.Fatalf("后端应有巡检规范名 %s，实际 %q", canonical, known)
	}
	p := modelRouteFixture(t, f, f.pending(t, target), "lookup")
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	f.enable(t)
	first := f.next(t, false)
	var local string
	requireCurationOK(t, json.Unmarshal(first.Messages[len(first.Messages)-1].Content, &local))
	for _, need := range []string{"fact", "组级", "时长"} {
		if !strings.Contains(local, need) {
			t.Errorf("首条消息缺少写入要求 %q", need)
		}
	}
	first.tool(t, "read_pending_items", map[string]any{"item_ids": []string{p.ID}})
	read := f.next(t, false)
	result := read.lastToolResult(t)
	// 只将成功的本项读回与首条消息合并。权限失败时首条消息仍可独立提供完整依据。
	if strings.Contains(result, p.ID) && strings.Contains(result, `"evidence"`) {
		local += "\n" + result
	} else {
		t.Logf("read_pending_items 未返回本项证据：%s", result)
	}
	for _, need := range []string{"authenticity", "duration_seconds", "disc", s.Local.Tracks[2].Title, canonical} {
		if !strings.Contains(local, need) {
			t.Errorf("首条消息与本项工具结果合计缺少本地依据 %q", need)
		}
	}
	r := s.External.Releases[0]
	read.tool(t, "get_music_metadata", map[string]any{"entity": "release_group", "mbid": r.Group, "include": []string{"releases"}})
	group := f.next(t, false)
	got := group.lastToolResult(t)
	for _, value := range []string{r.GroupTitle, r.ID, s.External.Releases[1].ID, `"track_count":3`} {
		if !strings.Contains(got, value) {
			t.Errorf("组详情没有 %q：%s", value, got)
		}
	}
	group.tool(t, "get_music_metadata", map[string]any{"entity": "release", "mbid": r.ID, "include": []string{"recordings", "artist-credits"}})
	detail := f.next(t, false)
	got = detail.lastToolResult(t)
	for _, v := range []string{s.Local.Tracks[2].Title, fmt.Sprint(r.Tracks[2].Length), s.External.Artists[0].ID} {
		if !strings.Contains(got, v) {
			t.Errorf("版本明细没有 %q：%s", v, got)
		}
	}
	detail.tool(t, "get_music_metadata", map[string]any{"entity": "artist", "mbid": s.External.Artists[0].ID, "include": []string{"aliases"}})
	artist := f.next(t, false)
	if got = artist.lastToolResult(t); !strings.Contains(got, s.External.Artists[0].Aliases[0]) {
		t.Errorf("艺人详情没有登记别名：%s", got)
	}
	const reason = "两版同名，首版封底刊号模糊，请核对内页"
	// 缺失对象的 404 应有独立错误码。这次失败之后仍可按正常路线升级。
	artist.tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": uuid.New()})
	absent := f.next(t, false)
	if got := absent.lastToolResult(t); !strings.Contains(got, "not_found") || strings.Contains(got, "source_unavailable") {
		t.Errorf("缺失的发行详情没有明确报告查无结果：%s", got)
	}
	// 另一个确实存在的编号只有一首歌。后档还应知道后端拒绝过它的具体原因。
	absent.tool(t, "resolve_release_match", map[string]any{"item_id": p.ID, "external_id": wrong.ID, "basis": map[string]string{"note": "比较另一张单曲的结构"}})
	rejected := f.next(t, false)
	if got := rejected.lastToolResult(t); !strings.Contains(got, "track_count") {
		t.Fatalf("未实际触发曲数反证：%s", got)
	}
	rejected.tool(t, "shelve_pending_item", map[string]string{"item_id": p.ID, "reason": reason})
	web := f.next(t, false)
	var msg string
	requireCurationOK(t, json.Unmarshal(web.Messages[len(web.Messages)-1].Content, &msg))
	if !strings.Contains(msg, reason) {
		t.Error("联网首条消息缺少上一档放弃的实际原因")
	}
	if !strings.Contains(msg, "track_count") && !strings.Contains(msg, "曲数") && !strings.Contains(msg, "曲目数") {
		t.Error("联网首条消息缺少前档曲数不符的拒绝原因")
	}
	f.pause(t)
	web.complete(t)
}
