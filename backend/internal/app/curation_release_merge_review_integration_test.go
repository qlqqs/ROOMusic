package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type releaseMergeReviewMember struct {
	ReleaseID          string `json:"release_id"`
	Title              string `json:"title"`
	DiscCount          int    `json:"disc_count"`
	TrackCount         int    `json:"track_count"`
	ExternalID         string `json:"external_id"`
	OperationID        string `json:"operation_id"`
	RecordingCount     int    `json:"recording_count"`
	ExternalDiscCount  any    `json:"external_disc_count"`
	ExternalTrackCount any    `json:"external_track_count"`
}

type releaseMergeReviewTrack struct {
	Position   int    `json:"position"`
	Title      string `json:"title"`
	ExternalID string `json:"external_id"`
}

type releaseMergeReviewDisc struct {
	Position       int                       `json:"position"`
	From           string                    `json:"from"`
	FromReleaseID  string                    `json:"from_release_id"`
	OriginPosition int                       `json:"origin_position"`
	Title          string                    `json:"title"`
	TrackCount     int                       `json:"track_count"`
	Tracks         []releaseMergeReviewTrack `json:"tracks"`
}

type releaseMergeReviewFacts struct {
	Data struct {
		Target, Source    releaseMergeReviewMember
		RecordingsOverlap *bool                    `json:"recordings_overlap"`
		Layout            []releaseMergeReviewDisc `json:"layout"`
		LayoutTrackTotal  int                      `json:"layout_track_total"`
		TracksOmitted     bool                     `json:"tracks_omitted"`
	}
}

func readReleaseMergeFacts(t *testing.T, f *schedulerFixture, source string) releaseMergeReviewFacts {
	t.Helper()
	facts, err := curation.ReadReleaseMergeEvidence(t.Context(), f.application.database, f.a, source, f.writer.ArtworkExists, true)
	requireCurationOK(t, err)
	raw, err := json.Marshal(facts)
	requireCurationOK(t, err)
	var got releaseMergeReviewFacts
	requireCurationOK(t, json.Unmarshal(raw, &got.Data))
	return got
}

func readStoredReleaseMergeLayout(t *testing.T, f *curationFixture, release, source string) []releaseMergeReviewDisc {
	t.Helper()
	rows, err := f.application.database.QueryContext(t.Context(), `
		SELECT m.position, CASE WHEN m.origin_release_id::text=$2 THEN 'source' ELSE 'target' END,
		       COALESCE(m.origin_release_id,m.release_id)::text, COALESCE(m.origin_position,m.position), m.title,
		       count(t.id), COALESCE(jsonb_agg(jsonb_build_object('position',t.position,'title',t.title,
		           'external_id',COALESCE(l.external_id,'')) ORDER BY t.position,t.id)
		           FILTER (WHERE t.id IS NOT NULL), '[]'::jsonb)
		FROM media m
		LEFT JOIN track_effective t ON t.medium_id=m.id AND t.source_status='present'
		LEFT JOIN external_links l ON l.object_id=t.id AND l.object_type='track' AND l.source='musicbrainz'
		WHERE m.release_id=$1 GROUP BY m.id ORDER BY m.position`, release, source)
	requireCurationOK(t, err)
	defer rows.Close()
	var layout []releaseMergeReviewDisc
	for rows.Next() {
		var disc releaseMergeReviewDisc
		var tracks []byte
		requireCurationOK(t, rows.Scan(&disc.Position, &disc.From, &disc.FromReleaseID, &disc.OriginPosition,
			&disc.Title, &disc.TrackCount, &tracks))
		requireCurationOK(t, json.Unmarshal(tracks, &disc.Tracks))
		layout = append(layout, disc)
	}
	requireCurationOK(t, rows.Err())
	return layout
}

func requireReleaseMergeReviewLayout(t *testing.T, f *curationFixture, merge releaseMergeReviewFacts, source string, discCount int) {
	t.Helper()
	stored := readStoredReleaseMergeLayout(t, f, f.a, source)
	if len(merge.Data.Layout) != discCount || len(stored) != discCount || merge.Data.TracksOmitted {
		t.Fatalf("审查碟数 %d，执行后碟数 %d，期望 %d，省略曲目 %t",
			len(merge.Data.Layout), len(stored), discCount, merge.Data.TracksOmitted)
	}
	total := 0
	for i, disc := range stored {
		if got := merge.Data.Layout[i]; !reflect.DeepEqual(got, disc) {
			t.Errorf("第 %d 张碟审查布局 %+v，执行后 %+v", i+1, got, disc)
		}
		total += disc.TrackCount
	}
	if merge.Data.LayoutTrackTotal != total {
		t.Errorf("审查曲目总数 %d，执行后 %d", merge.Data.LayoutTrackTotal, total)
	}
}

func TestPostgreSQL整理审查发行并入证据(t *testing.T) {
	t.Run("证据取自对应操作后态", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.enable(t)
		external := f.remote.ID
		cacheClient := musicbrainz.New(musicbrainz.Options{
			BaseURL: f.endpoint + "/mb", DataDirectory: f.application.dataDirectory,
			Limiter:   sources.NewLimiter(0, nil, nil),
			NewClient: func(egress.Options) (*http.Client, error) { return http.DefaultClient, nil },
		})
		cached, err := cacheClient.ReleaseByID(t.Context(), external)
		requireCurationOK(t, err)
		if len(cached.Media) != 1 || len(cached.Media[0].Tracks) != 2 {
			t.Fatalf("缓存夹具应为一碟两首，实际 %+v", cached.Media)
		}
		files, err := filepath.Glob(filepath.Join(f.application.dataDirectory, "sources", "cache", "musicbrainz", "*", "*.json"))
		requireCurationOK(t, err)
		if len(files) != 1 {
			t.Fatalf("外部缓存样本 %v", files)
		}

		source, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
			title: "两首曲目的来源发行", artist: "Artist B", tracks: []string{"present", "present"},
		})
		// 标签碟号用于选择并入方向，来源碟的本地位置仍为 1，执行后应为 2。
		mustExec(t, f.application, `UPDATE tracks SET disc_number=2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, source)
		track, _ := externalTrack(t, f.application, source)
		correctTrackForCuration(t, f.curationFixture, track, 1, map[string]any{"title": setChange("修正后的曲名")})
		targetOp := seedReleaseMergeLink(t, f.curationFixture, f.a, external, uuid.New(), 2, 3)
		sourceOp := seedReleaseMergeLink(t, f.curationFixture, source, external, uuid.New(), 0, 0)
		f.configure(t, &f.admin)
		discoverReleaseMerge(t, f.curationFixture, external)
		item := requireReleaseMergeItem(t, f.curationFixture, f.a, source, external, false)
		request := f.next(t, false)
		requireCurationRuleItems(t, request, item.ID)
		merge := readReleaseMergeFacts(t, f, source)
		wantTarget := releaseMergeReviewMember{f.a, "Album A", 1, 1, external, targetOp, 1, float64(2), float64(3)}
		wantSource := releaseMergeReviewMember{source, "两首曲目的来源发行", 1, 2, external, sourceOp, 1, "不可用", "不可用"}
		if merge.Data.Target != wantTarget || merge.Data.Source != wantSource ||
			merge.Data.RecordingsOverlap == nil || *merge.Data.RecordingsOverlap {
			t.Fatalf("发行合并审查证据 %+v", merge)
		}

		// 模型收到待办后移除缓存，执行合并仍应使用对应操作的后态。
		requireCurationOK(t, os.Remove(files[0]))
		request.answerItems(t, map[string]string{"item_id": item.ID, "verdict": "merge", "reason": "同版的不同录音"})
		modelWaitResolution(t, f, item.ID, "model_merged")
		requireReleaseMergeReviewLayout(t, f.curationFixture, merge, source, 2)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
	})

	t.Run("目标保留先前并入碟的来源", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.enable(t)
		external, first := seedReleaseMergePair(t, f.curationFixture)
		manualReleaseMerge(t, f.curationFixture, f.a, f.b)
		requireReleaseMergeResolution(t, f.curationFixture, first.ID, "merged")

		source, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
			title: "后续并入的发行", artist: "Artist A", tracks: []string{"present"},
		})
		mustExec(t, f.application, `UPDATE tracks SET disc_number=3 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, source)
		seedReleaseMergeLink(t, f.curationFixture, source, external, uuid.New(), 0, 0)
		discoverReleaseMerge(t, f.curationFixture, external)
		second := requireReleaseMergeItem(t, f.curationFixture, f.a, source, external, false)
		f.configure(t, &f.admin)
		request := f.next(t, false)
		requireCurationRuleItems(t, request, second.ID)
		merge := readReleaseMergeFacts(t, f, source)
		if len(merge.Data.Layout) != 3 {
			t.Fatalf("后续并入的审查布局 %+v", merge.Data.Layout)
		}
		if disc := merge.Data.Layout[1]; disc.From != "target" || disc.FromReleaseID != f.b || disc.OriginPosition != 1 {
			t.Errorf("先前并入的碟 %+v，期望来自目标、原发行 %s、原碟号 1", disc, f.b)
		}
		request.answerItems(t, map[string]string{"item_id": second.ID, "verdict": "merge", "reason": "同版的不同录音"})
		modelWaitResolution(t, f, second.ID, "model_merged")
		requireReleaseMergeReviewLayout(t, f.curationFixture, merge, source, 3)
	})

	t.Run("在途决定不能覆盖管理员的变更", func(t *testing.T) {
		f := newSchedulerFixture(t)
		_, item := startReleaseMergeModel(t, f)
		q := f.next(t, false)
		f.correct(t, f.a, 1, map[string]any{"title": setChange("管理员刚修改的名字")})
		q.answerItems(t, map[string]string{"item_id": item.ID, "verdict": "merge", "reason": "旧结论"})
		schedulerWait(t, "重新核对发行并入失败", func() bool {
			return countRows(t, f.application, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='merge_releases' AND status='failed'`) > 0
		})
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
	})

	t.Run("缺外部对应拒绝写入", func(t *testing.T) {
		f := newSchedulerFixture(t)
		_, item := startReleaseMergeModel(t, f)
		mustExec(t, f.application, `DELETE FROM external_links WHERE object_type='release' AND object_id=$1`, f.b)
		f.next(t, false).answerItems(t, map[string]string{"item_id": item.ID, "verdict": "merge", "reason": "核对合并"})
		schedulerWait(t, "缺对应时拒绝并入", func() bool {
			return countRows(t, f.application, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='merge_releases' AND status='failed'`) > 0
		})
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
		if f.progress(t).Items.WaitingHuman != 0 {
			t.Fatal("缺失外部对应的发行仍计入人工待办")
		}
	})
}
