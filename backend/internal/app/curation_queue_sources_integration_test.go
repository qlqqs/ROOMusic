package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQL整理队列限流时歌词和艺人资料继续(t *testing.T) {
	const remote = "30000000-0000-4000-8000-000000000001"
	clock := newProgramClock()
	app, mock, _ := newCurationMockApplication(t, "", clock)
	admin := app.createUser(t, "admin")
	root := b3Root(t, app)
	release, _ := insertGroupedRelease(t, app, root, groupedRelease{
		title: "潮线以北", artist: "纸屿", tracks: []string{"present", "present"},
	})
	track := programString(t, app, `SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE m.release_id=$1 AND t.position=1`, release)
	second := programString(t, app, `SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE m.release_id=$1 AND t.position=2`, release)
	mustExec(t, app, `UPDATE tracks SET title=CASE position WHEN 1 THEN '潮汐信箱' ELSE '细雨码头' END
		WHERE id IN ($1,$2)`, track, second)
	programTag(t, app, track, "mb_release", remote)
	publishSearch(t, app)

	setScenario := func(body string) {
		t.Helper()
		r := httptest.NewRecorder()
		mock.ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/_mock/scenarios/musicbrainz", strings.NewReader(body)))
		if r.Code != http.StatusOK {
			t.Fatalf("设置受控限流情景 HTTP %d %s", r.Code, r.Body)
		}
	}
	mbReads := func() int {
		t.Helper()
		count := 0
		for _, req := range cMatchRequests(t, mock) {
			if req.Service == "musicbrainz" && req.Path == "/musicbrainz/ws/2/release/"+remote {
				count++
			}
		}
		return count
	}
	progress := func() curation.Progress {
		t.Helper()
		var p curation.Progress
		getJSON(t, app, &admin, curationBase+"/progress", &p)
		return p
	}
	requireBackoff := func(delay time.Duration, calls int) {
		t.Helper()
		p := progress()
		q := p.Queues[0]
		if q.ID != "release" || q.State != "paused" || q.Remaining < 1 || q.Ready != 0 ||
			q.PauseReason == nil || *q.PauseReason != "rate_limited" || q.Source == nil || *q.Source != "musicbrainz" ||
			q.RetryAt == nil || !q.RetryAt.Equal(clock.options().Now().Add(delay)) || q.ETASeconds != nil ||
			q.SuccessfulRequestsPerSecond != nil || q.CompletedItemsPerSecond != nil ||
			p.Releases.Processed != 0 || p.Releases.TryLater != 0 || mbReads() != calls {
			t.Fatalf("期望 MusicBrainz 第 %d 次失败后等 %v，当前 %v 次、识别 %+v、进度 %+v", calls, delay, mbReads(), q, p.Releases)
		}
		requireUnattemptedProgramState(t, b3WorkState(t, app, "release", release))
	}

	setScenario(`{"scenario":"rate_limited","retry_after":2}`)
	b3Enable(t, app, admin)
	clock.advance(t, time.Second)
	requireBackoff(2*time.Second, 1)

	// 在 MusicBrainz 冷却已生效后才加入歌词缺口与可独立补资料的 Q 号。
	mustExec(t, app, `UPDATE tracks SET duration_seconds=180 WHERE id=$1`, track)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=192 WHERE id=$1`, second)
	artist := programString(t, app, `SELECT id FROM artists WHERE initial_name='纸屿'`)
	mustExec(t, app, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'wikidata','artist',$1,'Q900005','artist_credit','certain')`, artist)
	mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, uuid.New())
	clock.advance(t, time.Second)
	requireBackoff(time.Second, 1)
	if s := b3WorkState(t, app, "lyrics", track); s.Status != "done" || s.Attempts != 1 {
		t.Fatalf("限流中歌词应已补齐，状态 %+v", s)
	}
	media := readArtistMedia(t, app, &admin, artist)
	if media.Bio == nil || media.Bio.Source != "wikipedia" || media.Image == nil || media.Image.Source != "commons" {
		t.Fatalf("限流中应已补齐独立的艺人资料：%+v", media)
	}
	p := progress()
	for _, index := range []int{2, 3} {
		q := p.Queues[index]
		if q.State != "idle" || q.Remaining != 0 || q.Ready != 0 || q.ETASeconds == nil || *q.ETASeconds != 0 ||
			q.PauseReason != nil || q.Source != nil || q.RetryAt != nil ||
			q.CompletedItemsPerSecond == nil || *q.CompletedItemsPerSecond <= 0 ||
			q.SuccessfulRequestsPerSecond == nil || *q.SuccessfulRequestsPerSecond <= 0 {
			t.Fatalf("已经继续执行并排空的队列 %+v", q)
		}
	}
	if q := p.Queues[4]; q.ID != "fingerprint" || q.SuccessfulRequestsPerSecond != nil {
		t.Fatalf("声纹队列不计网络请求：%+v", q)
	}

	clock.advance(t, time.Second)
	requireBackoff(4*time.Second, 2)
	setScenario(`{"scenario":"ok"}`)
	clock.advance(t, 3*time.Second)
	requireBackoff(time.Second, 2)
	clock.advance(t, time.Second)
	s := b3WorkState(t, app, "release", release)
	if s.Status != "matched" || s.Attempts != 1 || s.Generation != 1 || s.RetryCount != 0 ||
		mbReads() != 3 || s.NextAttemptAt != nil {
		t.Fatalf("MusicBrainz 恢复后状态 %+v，共 %d 次发行请求", s, mbReads())
	}
	q := progress().Queues[0]
	if q.State != "idle" || q.Remaining != 0 || q.PauseReason != nil || q.RetryAt != nil ||
		q.SuccessfulRequestsPerSecond == nil || *q.SuccessfulRequestsPerSecond <= 0 {
		t.Fatalf("恢复后的识别队列 %+v", q)
	}
}
