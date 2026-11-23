package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

func TestPostgreSQLStewardReviewPolicyV4MergeEvidence(t *testing.T) {
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

		mustExec(t, f.application, `UPDATE tracks SET disc_number=2 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
		targetOp := seedReleaseMergeLink(t, f.curationFixture, f.a, external, uuid.New(), 2, 3)
		sourceOp := seedReleaseMergeLink(t, f.curationFixture, f.b, external, uuid.New(), 0, 0)
		discoverReleaseMerge(t, f.curationFixture, external)
		item := requireReleaseMergeItem(t, f.curationFixture, f.a, f.b, external, false)
		f.configure(t, &f.admin)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		review := f.next(t, true)
		var content string
		requireCurationOK(t, json.Unmarshal(review.Messages[len(review.Messages)-1].Content, &content))
		var payload struct {
			Evidence []struct {
				Facts struct {
					ReleaseMerge struct {
						Source string
						Data   struct {
							Target, Source    releaseMergeReviewMember
							RecordingsOverlap *bool `json:"recordings_overlap"`
						}
					} `json:"release_merge"`
				}
			}
		}
		requireCurationOK(t, json.Unmarshal([]byte(content), &payload))
		if len(payload.Evidence) != 1 {
			t.Fatalf("审查证据 %s", content)
		}
		merge := payload.Evidence[0].Facts.ReleaseMerge
		wantTarget := releaseMergeReviewMember{f.a, "Album A", 1, 1, external, targetOp, 1, float64(2), float64(3)}
		wantSource := releaseMergeReviewMember{f.b, "Album B", 1, 1, external, sourceOp, 1, "不可用", "不可用"}
		if merge.Source != "program_untrusted_external" || merge.Data.Target != wantTarget || merge.Data.Source != wantSource ||
			merge.Data.RecordingsOverlap == nil || *merge.Data.RecordingsOverlap {
			t.Fatalf("发行合并审查证据 %s", content)
		}

		// 审查发送后移除缓存，批准时重读的证据应仍相同。
		requireCurationOK(t, os.Remove(files[0]))
		conversation := f.item(t, item.ID).ConversationID
		review.review(t, "approved")
		f.next(t, false).complete(t)
		requireReleaseMergeResolution(t, f.curationFixture, item.ID, "model_merged")
		detail, err := runtime.NewStore(f.application.database).GetConversation(t.Context(), f.admin.id, conversation)
		requireCurationOK(t, err)
		if runtime.ReviewPolicyVersion != 4 || len(detail.Plans) != 1 || detail.Plans[0].Review == nil || detail.Plans[0].Review.PolicyVersion != 4 {
			t.Fatalf("审查策略版本 %d，方案 %+v", runtime.ReviewPolicyVersion, detail.Plans)
		}
	})

	t.Run("拒绝已保存的旧版批准", func(t *testing.T) {
		f := newSchedulerFixture(t)
		// 将保存的批准降为第 3 版，保留方案、结论、摘要和有效期。
		mustExec(t, f.application, `CREATE FUNCTION test_save_v3_merge_review() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN NEW.policy_version := 3; RETURN NEW; END $$`)
		mustExec(t, f.application, `CREATE TRIGGER test_save_v3_merge_review BEFORE INSERT ON steward_reviews
			FOR EACH ROW EXECUTE FUNCTION test_save_v3_merge_review()`)
		_, item := startReleaseMergeModel(t, f)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		f.next(t, true).review(t, "approved")
		schedulerWait(t, "旧版批准进入重新审查队列", func() bool {
			return f.item(t, item.ID).ReviewRetries == 1
		})
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_reviews
			WHERE policy_version=3 AND status='completed' AND items->0->>'decision'='approved'`)
		b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_plan_items WHERE tool_name='merge_releases' AND decision='pending'`)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='release_auto_merge'`)
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
	})

	t.Run("缺外部对应直接转人工", func(t *testing.T) {
		f := newSchedulerFixture(t)
		_, item := startReleaseMergeModel(t, f)
		mustExec(t, f.application, `DELETE FROM external_links WHERE object_type='release' AND object_id=$1`, f.b)
		f.next(t, false).tool(t, releaseMergeProposalTool, releaseMergeProposal(item.ID))
		f.next(t, false).complete(t)
		exceptions, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
		requireCurationOK(t, err)
		if total != 1 || len(exceptions) != 1 || exceptions[0].Tool != releaseMergeProposalTool ||
			exceptions[0].PendingItemID != item.ID || exceptions[0].Review.Source != "policy" ||
			exceptions[0].Review.Decision != "needs_human_confirmation" {
			t.Fatalf("缺对应时的例外 %+v", exceptions)
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_merges`)
		if got := f.item(t, item.ID); got.Status != "waiting_human" {
			t.Fatalf("缺对应时的待定项 %+v", got)
		}
	})
}
