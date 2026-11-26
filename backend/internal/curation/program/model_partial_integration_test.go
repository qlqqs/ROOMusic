package program

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type modelPartialFinder struct {
	b3MatchFinder
	detailErrors map[string]error
}

func (f *modelPartialFinder) ReleaseByID(ctx context.Context, id string) (sources.Release, error) {
	release, err := f.b3MatchFinder.ReleaseByID(ctx, id)
	if detailErr := f.detailErrors[id]; detailErr != nil {
		return sources.Release{}, detailErr
	}
	return release, err
}

func TestPostgreSQL模型提交部分吻合补读异组(t *testing.T) {
	for _, scenario := range []string{"排除异组", "两组均吻合", "来源不可用"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f := newPendingRefreshFixture(t)
			_, err := f.writer.SetSettings(ctx, f.adminID, true)
			requirePendingRefreshOK(t, err)

			local, original := b3Album(5)
			original.ID, original.Complete, original.Date = uuid.New(), true, "2001-01-01"
			original.Group = sources.Group{ID: uuid.New(), Title: "合成专辑原版"}
			original.Media[0].TrackCount = len(local.Tracks)
			for i, track := range local.Tracks {
				original.Media[0].Tracks[i].Title = track.Title
			}
			other := original
			other.ID = uuid.New()
			other.Group = sources.Group{ID: uuid.New(), Title: "合成专辑另组"}
			other.Media = slices.Clone(original.Media)
			other.Media[0].Tracks = slices.Clone(original.Media[0].Tracks)
			for i := range other.Media[0].Tracks {
				other.Media[0].Tracks[i].RecordingID = uuid.New()
			}
			// 待定项中两条候选同名同艺人，曲数相同，只有摘要。
			summaries := []sources.Release{original, other}
			for i := range summaries {
				summaries[i].Complete = false
				summaries[i].Media = []sources.Medium{{Position: 1, TrackCount: len(local.Tracks)}}
			}

			var unavailable *sources.QueuePause
			finder := &modelPartialFinder{}
			switch scenario {
			case "排除异组":
				duration := int64(120000)
				other.Media[0].Tracks[0].LengthMS = &duration
			case "来源不可用":
				unavailable = sources.NewQueuePolicy(func() time.Time {
					return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
				}).Unavailable("musicbrainz")
				finder.detailErrors = map[string]error{other.ID: unavailable}
			}
			finder.releases = map[string]sources.Release{original.ID: original, other.ID: other}
			gate := curation.NewSources(f.db, curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: finder})
			matcher := NewModelMatcher(f.db, f.writer, gate)

			releaseID, _ := f.release(t, original.Title, original.Artists[0].Name)
			for _, track := range local.Tracks[2:4] {
				id := uuid.New()
				f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity)
 SELECT $2,id,$3,$4,$5,1,$6,$2::uuid::text || '.flac','present',now(),'physical',$2::uuid::text FROM media WHERE release_id=$1`,
					releaseID, id, track.Position, track.Title, original.Artists[0].Name, f.rootID)
			}
			f.exec(t, `UPDATE tracks SET duration_seconds=100 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, releaseID)
			evidence, err := f.writer.LoadCurationEvidence(ctx, []string{releaseID})
			requirePendingRefreshOK(t, err)
			localEvidence := evidence[releaseID]
			requirePendingRefreshOK(t, curation.UpsertPendingItem(ctx, f.db, curation.PendingItem{
				TargetID: releaseID, Kind: "release_match", RuleVersion: StoredRuleVersion, InputDigest: ReleaseDigest(localEvidence),
				Evidence: jsonValue(PendingEvidence(localEvidence)), Candidates: jsonValue(releaseCandidates("search", summaries)),
			}))
			item := f.claim(t, releaseID, "release_match")

			plan, err := matcher.Plan(ctx, item, original.ID)
			if !slices.Equal(finder.releaseReads, []string{original.ID, other.ID}) {
				t.Errorf("应先读提交的原版再补读异组版本，实际读取 %v", finder.releaseReads)
			}
			switch scenario {
			case "两组均吻合":
				var failure *operations.Failure
				if !errors.As(err, &failure) || failure.Class != operations.ClassConflict || failure.Code != "track_count" {
					t.Fatalf("两组均吻合时应拒绝提交并返回 track_count，得到 %v", err)
				}
				for _, want := range []string{"另有发行组同样吻合", original.Group.ID + "《" + original.Group.Title + "》", other.Group.ID + "《" + other.Group.Title + "》"} {
					if !strings.Contains(failure.Message, want) {
						t.Errorf("错误文本 %q 缺少 %q", failure.Message, want)
					}
				}
				return
			case "来源不可用":
				if err != unavailable {
					t.Fatalf("补读应原样返回来源不可用错误 %p，实际 %T %v", unavailable, err, err)
				}
				return
			}
			requirePendingRefreshOK(t, err)
			if plan.match.Level != "release_group" || plan.match.Rule != "partial_subset" || plan.match.GroupID != original.Group.ID ||
				plan.match.Release != nil || len(plan.match.Pairs) != 0 {
				t.Fatalf("应以 partial_subset 仅匹配原版发行组，得到 %+v", plan.match)
			}

			batch, err := f.writer.OpenBatch(ctx, "model")
			requirePendingRefreshOK(t, err)
			_, err = matcher.Apply(ctx, plan, ApplyOptions{
				Key: uuid.New(), Batch: batch.ID, PendingItemID: item.ID,
				Source: curation.Source{Kind: "model", Provider: "synthetic", Basis: "合成曲表核对"},
			})
			requirePendingRefreshOK(t, err)
			evidence, err = f.writer.LoadCurationEvidence(ctx, []string{releaseID})
			requirePendingRefreshOK(t, err)
			saved := evidence[releaseID]
			if saved.ReleaseGroupMatchID != original.Group.ID || saved.ExternalID != "" {
				t.Errorf("落库发行组对应 %q、版本对应 %q，预期只有组级对应 %s", saved.ReleaseGroupMatchID, saved.ExternalID, original.Group.ID)
			}
			for _, track := range saved.Tracks {
				if track.ExternalID != "" {
					t.Errorf("曲目 %q 不应写入录音对应 %q", track.Title, track.ExternalID)
				}
			}
			var autoValues int
			requirePendingRefreshOK(t, f.db.QueryRowContext(ctx, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, releaseID).Scan(&autoValues))
			if autoValues != 0 {
				t.Errorf("部分吻合不应写入发行字段，实际有 %d 项自动值", autoValues)
			}
			states, err := curation.LoadWorkStates(ctx, f.db, "release", []string{releaseID})
			requirePendingRefreshOK(t, err)
			state := states[releaseID]
			var result struct{ Level, Rule string }
			requirePendingRefreshOK(t, json.Unmarshal(state.Result, &result))
			if state.Status != "matched" || result.Level != "release_group" || result.Rule != "partial_subset" {
				t.Errorf("落库状态 %q，结论 %+v，预期以 partial_subset 匹配到发行组", state.Status, result)
			}
		})
	}
}
