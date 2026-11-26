package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQL整理名字核对按新依据重排一次(t *testing.T) {
	const before, after = "合成正试专辑", "合成正式专辑"
	for _, tc := range []struct {
		name, scanned string
	}{
		{"重新核对并直接纠正错字", before},
		{"扫描变化后按当前依据核对", "合成正识专辑"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			app := f.application
			release, tracks := b3ReleaseTracks(t, app, f.rootID, before, "合成正式艺人", 2)
			mustExec(t, app, `UPDATE tracks SET duration_seconds=CASE position WHEN 1 THEN 12 ELSE 16 END
				WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
			item := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
				ReleaseID: release, Field: "title", Before: before, After: after, Structure: true, ExternalID: f.remote.ID, ExternalValue: after,
			})

			// 先入队，再建立发行和逐轨对应。第二例还模拟扫描原文在模型处理前发生变化。
			f.enable(t)
			link := curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(release, "",
				curationCLink("release", release, f.remote.ID),
				curationCLink("track", tracks[0], f.remote.Recordings[0]),
				curationCLink("track", tracks[1], f.remote.Recordings[1]),
			))
			if tc.scanned != before {
				mustExec(t, app, `UPDATE releases SET title=$2 WHERE id=$1`, release, tc.scanned)
			}
			f.configure(t, &f.admin)
			decision, err := json.Marshal(map[string]any{"items": []map[string]string{{"item_id": item, "verdict": "typo", "reason": "对应的外部专辑原名含一字差异"}}})
			requireCurationOK(t, err)
			send := func(q schedulerRequest) {
				q.reply <- q.frame(t, map[string]string{"content": string(decision)}, "stop")
			}
			first := f.next(t, false)
			old := f.item(t, item)
			send(first)
			retry := f.next(t, false)
			current := f.item(t, item)
			if old.ConversationID == "" || current.ConversationID == "" || current.ConversationID == old.ConversationID ||
				current.Status != "in_progress" || current.IdentityDigest == old.IdentityDigest || current.InputDigest == old.InputDigest {
				t.Fatalf("同一核对项应由新会话按新依据领取：旧=%+v，新=%+v", old, current)
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id
				WHERE m.conversation_id=$1 AND c.tool_name='submit_verification' AND c.output->>'status'='requeued'`, old.ConversationID)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, release)
			var prompt string
			requireCurationOK(t, json.Unmarshal(retry.Messages[len(retry.Messages)-1].Content, &prompt))
			if !strings.Contains(prompt, link.OperationID) || !strings.Contains(prompt, f.remote.ID) || !strings.Contains(prompt, after) {
				t.Fatalf("重排后的模型消息缺少当前发行的外部标题和对应依据：%s", prompt)
			}
			send(retry)
			modelWaitResolution(t, f, item, "model_verified")

			var state governance.CorrectionState
			getJSON(t, app, &f.admin, "/api/v1/releases/"+release+"/corrections", &state)
			i := slices.IndexFunc(state.Fields, func(v governance.CorrectionField) bool { return v.Field == "title" })
			if i < 0 || state.Fields[i].ScanValue != tc.scanned || state.Fields[i].EffectiveValue != after || state.Fields[i].Auto == nil {
				t.Fatalf("修正接口应保留扫描标题并显示纠正值：%+v", state.Fields)
			}
			op := state.Fields[i].Auto.OperationID
			b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_reviews`)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM operations WHERE id=$1 AND origin->>'provider_kind'='model'`, op)
			f.undo(t, op)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND title=$2`, release, tc.scanned)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, release)
		})
	}
}
