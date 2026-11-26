package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
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
	Source string
	Data   struct {
		Target, Source    releaseMergeReviewMember
		RecordingsOverlap *bool                    `json:"recordings_overlap"`
		Layout            []releaseMergeReviewDisc `json:"layout"`
		LayoutTrackTotal  int                      `json:"layout_track_total"`
		TracksOmitted     bool                     `json:"tracks_omitted"`
	}
}

func readReleaseMergeReview(t *testing.T, review schedulerRequest) releaseMergeReviewFacts {
	t.Helper()
	var content string
	requireCurationOK(t, json.Unmarshal(review.Messages[len(review.Messages)-1].Content, &content))
	var payload struct {
		Evidence []struct {
			Facts struct {
				ReleaseMerge releaseMergeReviewFacts `json:"release_merge"`
			}
		}
	}
	requireCurationOK(t, json.Unmarshal([]byte(content), &payload))
	if len(payload.Evidence) != 1 {
		t.Fatalf("审查证据 %s", content)
	}
	return payload.Evidence[0].Facts.ReleaseMerge
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
		discoverReleaseMerge(t, f.curationFixture, external)
		item := requireReleaseMergeItem(t, f.curationFixture, f.a, source, external, false)
		f.configure(t, &f.admin)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		review := f.next(t, true)
		merge := readReleaseMergeReview(t, review)
		wantTarget := releaseMergeReviewMember{f.a, "Album A", 1, 1, external, targetOp, 1, float64(2), float64(3)}
		wantSource := releaseMergeReviewMember{source, "两首曲目的来源发行", 1, 2, external, sourceOp, 1, "不可用", "不可用"}
		if merge.Source != "program_untrusted_external" || merge.Data.Target != wantTarget || merge.Data.Source != wantSource ||
			merge.Data.RecordingsOverlap == nil || *merge.Data.RecordingsOverlap {
			t.Fatalf("发行合并审查证据 %+v", merge)
		}

		// 审查发送后移除缓存，批准时重读的证据应仍相同。
		requireCurationOK(t, os.Remove(files[0]))
		conversation := f.item(t, item.ID).ConversationID
		review.review(t, "approved")
		modelWaitResolution(t, f, item.ID, "model_merged")
		requireReleaseMergeReviewLayout(t, f.curationFixture, merge, source, 2)
		detail, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversation)
		requireCurationOK(t, err)
		if len(detail.Plans) != 1 || detail.Plans[0].Review == nil || detail.Plans[0].Review.PolicyVersion != runtime.ReviewPolicyVersion {
			t.Fatalf("审查策略版本 %d，方案 %+v", runtime.ReviewPolicyVersion, detail.Plans)
		}
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
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(second.ID))
		review := f.next(t, true)
		merge := readReleaseMergeReview(t, review)
		if len(merge.Data.Layout) != 3 {
			t.Fatalf("后续并入的审查布局 %+v", merge.Data.Layout)
		}
		if disc := merge.Data.Layout[1]; disc.From != "target" || disc.FromReleaseID != f.b || disc.OriginPosition != 1 {
			t.Errorf("先前并入的碟 %+v，期望来自目标、原发行 %s、原碟号 1", disc, f.b)
		}
		review.review(t, "approved")
		modelWaitResolution(t, f, second.ID, "model_merged")
		requireReleaseMergeReviewLayout(t, f.curationFixture, merge, source, 3)
	})

	t.Run("拒绝已保存的旧版批准", func(t *testing.T) {
		f := newSchedulerFixture(t)
		// 将保存的批准降为第 4 版，保留方案、结论、摘要和有效期。
		mustExec(t, f.application, `CREATE FUNCTION test_save_v4_merge_review() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN NEW.policy_version := 4; RETURN NEW; END $$`)
		mustExec(t, f.application, `CREATE TRIGGER test_save_v4_merge_review BEFORE INSERT ON steward_reviews
			FOR EACH ROW EXECUTE FUNCTION test_save_v4_merge_review()`)
		_, item := startReleaseMergeModel(t, f)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		f.next(t, true).review(t, "approved")
		schedulerWait(t, "旧版批准进入重新审查队列", func() bool {
			return f.item(t, item.ID).ReviewRetries == 1
		})
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_reviews
			WHERE policy_version=4 AND status='completed' AND items->0->>'decision'='approved'`)
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_plan_items WHERE tool_name='merge_releases' AND decision='pending'`)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='release_auto_merge'`)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
	})

	t.Run("缺外部对应即结项留建议", func(t *testing.T) {
		f := newSchedulerFixture(t)
		_, item := startReleaseMergeModel(t, f)
		mustExec(t, f.application, `DELETE FROM external_links WHERE object_type='release' AND object_id=$1`, f.b)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		modelWaitResolution(t, f, item.ID, "suggestion_recorded")
		exceptions, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
		requireCurationOK(t, err)
		if total != 1 || len(exceptions) != 1 || exceptions[0].Tool != releaseMergeProposalTool ||
			exceptions[0].PendingItemID != item.ID || exceptions[0].Review.Source != "policy" ||
			exceptions[0].Review.Decision != "needs_human_confirmation" {
			t.Fatalf("缺对应时的例外 %+v", exceptions)
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
		if f.progress(t).Items.WaitingHuman != 0 {
			t.Fatal("缺失外部对应的发行仍计入人工待办")
		}
	})
}
