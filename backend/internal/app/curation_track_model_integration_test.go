package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
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
	schedulerWait(t, "无效工具答复释放项目", func() bool { return f.item(t, item.ID).EmptyCount == 1 })
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
	f := newSchedulerFixture(t)
	track, before := externalTrack(t, f.application, f.a)
	item := modelClaim(t, f.curationFixture, modelConversation(t, f.curationFixture), f.pending(t, f.a))
	assertHistoricalExceptionLocked(t, f, item, "correct_track", schedulerTrackCorrection(f.a, track, 1, "title", setChange("模型建议曲名")))
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title=$2 AND edit_revision=1`, track, before)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_corrections WHERE track_id=$1`, track)
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
