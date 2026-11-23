package app

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func trackAutoRequest(track, release, provider, batch string, values map[string]any) curation.TrackRequest {
	r := autoRequest(release, provider, batch, values)
	return curation.TrackRequest{TrackID: track, ReleaseID: release, Source: r.Source, Key: r.Key, BatchID: batch, Fields: r.Fields}
}

func applyTrackAuto(t *testing.T, f *curationFixture, r curation.TrackRequest) string {
	t.Helper()
	applied, err := f.writer.ApplyTrack(t.Context(), r)
	if err != nil || !uuid.Valid(applied.OperationID) || len(applied.Written) != len(r.Fields) || len(applied.Skipped) != 0 {
		t.Fatalf("曲目自动修正 %+v，错误 %v", applied, err)
	}
	return applied.OperationID
}

func requireTrackAutoOwner(t *testing.T, f *curationFixture, track, field, value, owner string) {
	t.Helper()
	if owner == "" {
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1 AND field=$2`, track, field)
		return
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_auto_values WHERE track_id=$1 AND field=$2 AND text_value=$3 AND operation_id=$4`, track, field, value, owner)
}

func correctTrackForCuration(t *testing.T, f *curationFixture, track string, revision int, changes map[string]any) governance.TrackCorrectionResult {
	t.Helper()
	r := f.application.request(t, "PATCH", "/api/v1/tracks/"+track+"/corrections", correctionBody(revision, changes), &f.admin, idempotencyHeader(uuid.New()))
	if r.Code != 200 {
		t.Fatalf("曲目人工修正 HTTP %d %s", r.Code, r.Body)
	}
	var result governance.TrackCorrectionResult
	requireCurationOK(t, json.Unmarshal(r.Body.Bytes(), &result))
	return result
}

func TestPostgreSQLTrackAutoApply(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	request := func(values map[string]any) curation.TrackRequest {
		return trackAutoRequest(track, f.a, "fixture", "", values)
	}
	manual := correctTrackForCuration(t, f, track, 1, map[string]any{"title": setChange("人工标题")})
	onlyEmpty := request(map[string]any{"title": "外部标题", "artist": "外部艺人"})
	onlyEmpty.OnlyEmpty = true
	result, err := f.writer.ApplyTrack(t.Context(), onlyEmpty)
	requireCurationOK(t, err)
	if !slices.Equal(result.Written, []string{"artist"}) || !maps.Equal(result.Skipped, map[string]string{"title": "occupied"}) {
		t.Fatalf("只补空缺的结果 %+v", result)
	}
	requireTrackAutoOwner(t, f, track, "title", "", "")
	requireTrackAutoOwner(t, f, track, "artist", "外部艺人", result.OperationID)

	expectSkipped := func(r curation.TrackRequest, want map[string]string) {
		t.Helper()
		applied, err := f.writer.ApplyTrack(t.Context(), r)
		curationCRequireProblem(t, err, 409, "nothing_to_apply")
		if applied.OperationID != "" || len(applied.Written) != 0 || !maps.Equal(applied.Skipped, want) {
			t.Fatalf("跳过结果=%+v，期望原因 %v", applied, want)
		}
	}
	onlyEmpty.Key = uuid.New()
	expectSkipped(onlyEmpty, map[string]string{"title": "occupied", "artist": "occupied"})
	model := request(map[string]any{"title": "模型标题", "artist": "模型艺人"})
	model.ExpectedRevision, model.Source.Kind = manual.Revision, "model"
	_, err = f.writer.ApplyTrack(t.Context(), model)
	curationCRequireProblem(t, err, 409, "manual_override")
	requireTrackAutoOwner(t, f, track, "artist", "外部艺人", result.OperationID)

	stale := request(map[string]any{"artist": "其他发行艺人"})
	stale.ReleaseID = f.b
	_, err = f.writer.ApplyTrack(t.Context(), stale)
	curationCRequireProblem(t, err, 409, "item_stale")

	shadow := applyTrackAuto(t, f, request(map[string]any{"title": "外部标题"}))
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title='人工标题' AND artist='外部艺人'`, track)
	expectSkipped(request(map[string]any{"title": "外部标题"}), map[string]string{"title": "unchanged"})
	f.undo(t, shadow)
	expectSkipped(request(map[string]any{"title": "外部标题", "artist": "外部艺人"}), map[string]string{"title": "suppressed", "artist": "unchanged"})
}

func TestPostgreSQLTrackAutoRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, scannedTitle := externalTrack(t, f.application, f.a)
	first := applyTrackAuto(t, f, trackAutoRequest(track, f.a, "first", "", map[string]any{"title": "旧标题"}))
	newValues := trackAutoRequest(track, f.a, "second", "", map[string]any{"title": "新标题", "artist": "新艺人"})
	second := applyTrackAuto(t, f, newValues)

	requireAPIError(t, f.revert(t, &f.admin, first, uuid.New()), 409, "revert_conflict")
	requireTrackAutoOwner(t, f, track, "title", "新标题", second)
	requireTrackAutoOwner(t, f, track, "artist", "新艺人", second)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_suppressions WHERE target_id=$1`, track)

	reverted := f.undo(t, second)
	requireTrackAutoOwner(t, f, track, "title", "旧标题", first)
	requireTrackAutoOwner(t, f, track, "artist", "", "")
	b3RequireCount(t, f.application, 2, `
		SELECT count(*) FROM curation_suppressions
		WHERE target_type='track' AND target_id=$1 AND operation_id=$2 AND created_by=$3
		  AND ((subject='title' AND value='{"mode":"set","value":"新标题"}'::jsonb)
		    OR (subject='artist' AND value='{"mode":"set","value":"新艺人"}'::jsonb))`, track, reverted["operation_id"], f.admin.id)
	newValues.Key = uuid.New()
	got, err := f.writer.ApplyTrack(t.Context(), newValues)
	curationCRequireProblem(t, err, 409, "nothing_to_apply")
	if !maps.Equal(got.Skipped, map[string]string{"title": "suppressed", "artist": "suppressed"}) {
		t.Fatalf("撤销后重新提出的结果 %+v", got)
	}

	f.undo(t, first)
	requireTrackAutoOwner(t, f, track, "title", "", "")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title=$2`, track, scannedTitle)
}

func TestPostgreSQLTrackAutoBatchRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	otherTrack, _ := externalTrack(t, f.application, f.b)
	batch := f.batch(t, "program")
	first := applyTrackAuto(t, f, trackAutoRequest(track, f.a, "musicbrainz", batch, map[string]any{"title": "批次初值"}))
	second := applyTrackAuto(t, f, trackAutoRequest(track, f.a, "musicbrainz", batch, map[string]any{"title": "批次后值"}))
	otherBatch := f.batch(t, "program")
	control := applyTrackAuto(t, f, trackAutoRequest(otherTrack, f.b, "musicbrainz", otherBatch, map[string]any{"title": "其他批次"}))
	requireCurationOK(t, f.writer.CloseBatch(t.Context(), batch))

	got := f.scope(t, curation.Scope{Kind: "batch", ID: batch}, "", uuid.New(), uuid.New())
	if got.Reverted != 2 || len(got.Skipped) != 0 || got.NextCursor != nil {
		t.Fatalf("按批撤销 %+v", got)
	}
	requireTrackAutoOwner(t, f, track, "title", "", "")
	requireTrackAutoOwner(t, f, otherTrack, "title", "其他批次", control)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM operations WHERE reverts_operation_id IN ($1,$2)`, first, second)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_suppressions WHERE target_type='track' AND target_id=$1 AND subject='title'`, track)
}

func TestPostgreSQLTrackAutoProviderRevert(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	otherTrack, _ := externalTrack(t, f.application, f.b)
	control := applyTrackAuto(t, f, trackAutoRequest(track, f.a, "other", "", map[string]any{"title": "另一来源标题"}))
	first := applyTrackAuto(t, f, trackAutoRequest(track, f.a, "musicbrainz", "", map[string]any{"title": "外部标题"}))
	second := applyTrackAuto(t, f, trackAutoRequest(otherTrack, f.b, "musicbrainz", "", map[string]any{"artist": "外部艺人"}))
	got := f.scope(t, curation.Scope{Kind: "provider", Provider: "musicbrainz"}, "", uuid.New(), uuid.New())
	if got.Reverted != 2 || len(got.Skipped) != 0 || got.NextCursor != nil {
		t.Fatalf("按来源撤销 %+v", got)
	}
	requireTrackAutoOwner(t, f, track, "title", "另一来源标题", control)
	requireTrackAutoOwner(t, f, otherTrack, "artist", "", "")
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM operations WHERE reverts_operation_id IN ($1,$2)`, first, second)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_suppressions WHERE target_type='track' AND target_id IN ($1,$2)`, track, otherTrack)
}

func TestPostgreSQLTrackAutoIndependentRevisions(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	check := func(trackEdit, releaseEdit, releaseAuto int) {
		t.Helper()
		b3RequireCount(t, f.application, 1, `
			SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
			WHERE t.id=$1 AND r.id=$2 AND t.edit_revision=$3 AND r.edit_revision=$4 AND r.auto_revision=$5`,
			track, f.a, trackEdit, releaseEdit, releaseAuto)
	}
	check(1, 1, 1)
	manual := correctTrackForCuration(t, f, track, 1, map[string]any{"title": setChange("人工曲名")})
	check(2, 1, 1)
	auto := trackAutoRequest(track, f.a, "fixture", "", map[string]any{"artist": "外部艺人"})
	auto.ExpectedRevision = 2
	f.correct(t, f.a, 1, map[string]any{"title": setChange("人工发行标题")})
	f.correct(t, f.a, 2, map[string]any{"edition": setChange("人工版本")})
	check(2, 3, 1)
	operation := applyTrackAuto(t, f, auto)
	check(2, 3, 2)
	if reverted := f.undo(t, operation); reverted["revision"] != float64(2) {
		t.Fatalf("自动撤销应返回曲目人工版本，得到 %v", reverted)
	}
	check(2, 3, 3)
	f.undo(t, manual.OperationID)
	check(3, 3, 3)
}

func TestPostgreSQLTrackAutoConcurrent(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tx, err := f.application.database.BeginTx(ctx, nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	const gate = 0x43415450
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,hashtext(current_schema()))`, gate)
	requireCurationOK(t, err)

	type outcome struct {
		result curation.Applied
		err    error
	}
	queue := func(value string, waiters int) <-chan outcome {
		t.Helper()
		r := trackAutoRequest(track, f.a, "fixture", "", map[string]any{"title": value})
		r.OnlyEmpty = true
		done := make(chan outcome, 1)
		go func() {
			result, err := f.writer.ApplyTrack(ctx, r)
			done <- outcome{result, err}
		}()
		schedulerWait(t, "自动写入进入目录锁等待队列", func() bool {
			select {
			case result := <-done:
				t.Fatalf("写入越过目录锁 %+v", result)
			default:
			}
			return countRows(t, f.application, `
				SELECT count(*) FROM pg_locks WHERE locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid=$1 AND objid=hashtext(current_schema())::oid AND objsubid=2 AND NOT granted`, gate) == waiters
		})
		return done
	}
	first := queue("先排队的值", 1)
	second := queue("后排队的值", 2)
	requireCurationOK(t, tx.Rollback())
	winner, loser := <-first, <-second
	requireCurationOK(t, winner.err)
	curationCRequireProblem(t, loser.err, 409, "nothing_to_apply")
	if !uuid.Valid(winner.result.OperationID) || !slices.Equal(winner.result.Written, []string{"title"}) || loser.result.Skipped["title"] != "occupied" {
		t.Fatalf("并发写入结果 %+v / %+v", winner.result, loser.result)
	}
	requireTrackAutoOwner(t, f, track, "title", "先排队的值", winner.result.OperationID)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='track_auto_correction'`, f.a)
}
