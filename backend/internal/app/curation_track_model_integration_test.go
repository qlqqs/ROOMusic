package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func schedulerTrackCorrection(release, track string, revision int, field string, change any) map[string]any {
	input := schedulerCorrection(release, revision, field, change)
	input["track_id"] = track
	return input
}

func TestPostgreSQL整理第一档禁用曲目字段直写(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	track, _ := externalTrack(t, f.application, f.a)
	f.enable(t)
	request := f.next(t, false)
	if len(request.Tools) != 0 {
		t.Fatalf("第一档附带工具 %+v", request.Tools)
	}
	request.tool(t, "correct_track", schedulerTrackCorrection(f.a, track, 1, "title", setChange("模型曲名")))
	schedulerWait(t, "无效工具答复释放项目", func() bool { return f.item(t, item.ID).Attempts > 0 })
	f.pause(t)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type IN ('track_correction','track_auto_correction')`)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQLStewardCorrectTrackManual(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	track, _ := externalTrack(t, f.application, f.a)
	chat := f.startChat(t)
	f.next(t, false).tool(t, "correct_track", schedulerTrackCorrection(f.b, track, 1, "artist", setChange("发行不符的艺人")))
	q := f.next(t, false)
	if result := q.lastToolResult(t); !strings.Contains(result, "not_found") {
		t.Fatalf("曲目所属发行不符，工具返回 %s", result)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_corrections WHERE track_id=$1`, track)
	q.tool(t, "correct_track", schedulerTrackCorrection(f.a, track, 1, "artist", setChange("对话艺人")))
	f.next(t, true).review(t, "approved")
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
	b3RequireCount(t, f.application, 1, `
		SELECT count(*) FROM track_corrections c JOIN track_effective t ON t.id=c.track_id
		WHERE c.track_id=$1 AND c.field='artist' AND c.text_value='对话艺人' AND t.artist=c.text_value AND t.edit_revision=2`, track)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE operation_type='track_correction' AND target_id=$1 AND actor_id=$2 AND batch_id IS NULL`, f.a, f.admin.id)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, track)
}

func TestPostgreSQLCurationExceptionTrackAcceptDecline(t *testing.T) {
	for _, action := range []string{"accept", "stale", "decline"} {
		t.Run(action, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			reviewer := f.application.createUser(t, "admin")
			track, scannedTitle := externalTrack(t, f.application, f.a)
			f.enable(t)
			f.pause(t)
			conversation := modelConversation(t, f.curationFixture)
			item := modelClaim(t, f.curationFixture, conversation, f.pending(t, f.a))
			input, err := json.Marshal(schedulerTrackCorrection(f.a, track, 1, "title", setChange("模型建议曲名")))
			requireCurationOK(t, err)
			preview, err := json.Marshal(map[string]any{"changes": []map[string]string{{"field": "title", "before": scannedTitle, "after": "模型建议曲名"}}})
			requireCurationOK(t, err)
			id, err := f.writer.CreateException(t.Context(), curation.ModelException{
				PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &conversation, Tool: "correct_track", Provider: "test-main",
				Input: input, Preview: preview, Review: curation.ExceptionReview{ID: uuid.New(), Model: "test-review", Decision: "needs_human_confirmation", Source: "model", Reasons: []string{"合成审查理由"}},
			})
			requireCurationOK(t, err)
			ex, err := f.writer.Exception(t.Context(), id)
			requireCurationOK(t, err)
			if ex.PendingItemID != item.ID || ex.Track == nil || ex.Track.ID != track || ex.Track.Title != scannedTitle {
				t.Fatalf("曲目例外 %+v，曲目 %+v", ex, ex.Track)
			}

			switch action {
			case "accept":
				accepted := readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "accept"), "accepted", reviewer.id)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_corrections WHERE track_id=$1 AND field='title' AND text_value='模型建议曲名'`, track)
				b3RequireCount(t, f.application, 1, `
					SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2 AND batch_id IS NULL
					  AND operation_type='track_correction' AND origin->>'review_id'=$3 AND origin->>'review_decision'='human'`,
					accepted.OperationIDs[0], reviewer.id, ex.Review.ID)
			case "stale":
				// 只改有效前值，曲目的人工版本保持为 1。
				f.update(t, "PUT", curationBase+"/settings", map[string]bool{"paused": false})
				applyTrackAuto(t, f.curationFixture, trackAutoRequest(track, f.a, "fixture", "", map[string]any{"title": "变更后的自动前值"}))
				f.pause(t)
				requireAPIError(t, f.decide(t, &reviewer, ex.ID, "accept"), 409, "exception_stale")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='open' AND decided_by IS NULL`, ex.ID)
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title='变更后的自动前值' AND edit_revision=1`, track)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_corrections WHERE track_id=$1`, track)
				return
			case "decline":
				readExceptionDecision(t, f.decide(t, &reviewer, ex.ID, "decline"), "declined", reviewer.id)
				b3RequireCount(t, f.application, 1, `
					SELECT count(*) FROM curation_suppressions
					WHERE target_type='track' AND target_id=$1 AND subject='title' AND created_by=$2
					  AND value='{"mode":"set","value":"模型建议曲名"}'::jsonb`, track, reviewer.id)
				modelWaitResolution(t, f, item.ID, "suggestion_recorded")
				f.update(t, "PUT", curationBase+"/settings", map[string]bool{"paused": false})
				repeated := trackAutoRequest(track, f.a, "musicbrainz", "", map[string]any{"title": "模型建议曲名"})
				repeated.OnlyEmpty = true
				applied, err := f.writer.ApplyTrack(t.Context(), repeated)
				curationCRequireProblem(t, err, 409, "nothing_to_apply")
				if applied.Skipped["title"] != "suppressed" {
					t.Fatalf("程序重复提出的结果 %+v", applied)
				}
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_corrections WHERE track_id=$1`, track)
			}
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, track)
		})
	}
}

func TestPostgreSQLStewardReviewTrackEvidence(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	track, scannedTitle := externalTrack(t, f.application, f.a)
	scannedArtist := programString(t, f.application, `SELECT artist FROM tracks WHERE id=$1`, track)
	f.enable(t)
	recording := uuid.New()
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "", curationCLink("track", track, recording)))
	applyTrackAuto(t, f.curationFixture, trackAutoRequest(track, f.a, "fixture", "", map[string]any{"artist": "现有自动艺人"}))
	manual := correctTrackForCuration(t, f.curationFixture, track, 1, map[string]any{"title": setChange("现有人工曲名")})

	chat := f.startChat(t)
	f.next(t, false).tool(t, "correct_track", schedulerTrackCorrection(f.a, track, int(manual.Revision), "title", setChange("建议曲名")))
	review := f.next(t, true)
	var content string
	requireCurationOK(t, json.Unmarshal(review.Messages[len(review.Messages)-1].Content, &content))
	var payload struct {
		Evidence []struct {
			Facts struct {
				Targets []struct {
					Type, ID string
					Revision int64
					Data     struct {
						ReleaseID string `json:"release_id"`
						Source    string
						Fields    []governance.CorrectionField
					}
				}
				External struct {
					Source string
					Data   []struct {
						ReleaseID string `json:"release_id"`
						Links     []struct {
							ObjectType string `json:"object_type"`
							TrackID    string `json:"track_id"`
							ExternalID string `json:"external_id"`
						}
					}
				}
			}
		}
	}
	requireCurationOK(t, json.Unmarshal([]byte(content), &payload))
	if len(payload.Evidence) != 1 {
		t.Fatalf("审查证据 %s", content)
	}
	facts := payload.Evidence[0].Facts
	if len(facts.Targets) != 2 || facts.Targets[0].Type != "release" || facts.Targets[0].ID != f.a {
		t.Fatalf("审查目标 %+v", facts.Targets)
	}
	target := facts.Targets[1]
	if target.Type != "track" || target.ID != track || target.Revision != manual.Revision ||
		target.Data.ReleaseID != f.a || target.Data.Source != "catalog_effective_untrusted" {
		t.Fatalf("曲目审查目标 %+v", target)
	}
	fields := map[string]governance.CorrectionField{}
	for _, field := range target.Data.Fields {
		fields[field.Field] = field
	}
	if len(target.Data.Fields) != 2 || fields["title"].ScanValue != scannedTitle || fields["title"].EffectiveValue != "现有人工曲名" ||
		fields["artist"].ScanValue != scannedArtist || fields["artist"].EffectiveValue != "现有自动艺人" {
		t.Fatalf("曲目审查字段 %+v", target.Data.Fields)
	}
	external := facts.External
	if external.Source != "program_untrusted_external" || len(external.Data) != 1 || external.Data[0].ReleaseID != f.a || len(external.Data[0].Links) != 1 {
		t.Fatalf("外部审查证据 %+v", external)
	}
	link := external.Data[0].Links[0]
	if link.ObjectType != "track" || link.TrackID != track || link.ExternalID != recording {
		t.Fatalf("曲目外部对应 %+v", link)
	}

	review.review(t, "approved")
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
}
