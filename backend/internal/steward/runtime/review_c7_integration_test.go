package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/migrations"
)

func TestPostgreSQLStewardC7Migration55RoundTrip(t *testing.T) {
	db, s, user, other := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, user, "C7 保留会话", "model")
	mustStore(t, err)
	raw, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.BlockToolCall, ToolCall: &provider.ToolCall{ID: "c7", Name: "correct_release", Arguments: json.RawMessage(`{}`)}}}})
	m, err := s.AppendMessage(ctx, user, c.ID, MessageInput{Role: "assistant", Content: raw, ToolCalls: []ToolCallInput{{Name: "correct_release", Input: json.RawMessage(`{}`)}}})
	mustStore(t, err)
	plan, err := s.CreatePlan(ctx, user, c.ID, []PlanItem{{Tool: "correct_release", CallID: "c7", ToolCallID: m.ToolCalls[0].ID, Input: json.RawMessage(`{}`)}})
	mustStore(t, err)
	review := Review{ID: uuid.New(), PlanID: plan.ID, PlanDigest: plan.Digest, EvidenceDigest: "c7-evidence", PolicyVersion: 1, Model: "c7-reviewer", Status: "completed", Items: []ReviewItem{{ItemID: plan.Items[0].ID, Decision: "rejected", Risk: "low", Reasons: []string{"合成理由"}, Source: "model"}}, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Truncate(time.Microsecond)}
	mustStore(t, s.SaveReview(ctx, user, c.ID, review))
	_, err = db.Exec(`INSERT INTO steward_user_settings(user_id,review_model) VALUES($1,'c7-reviewer')`, user)
	mustStore(t, err)
	_, err = db.Exec(`UPDATE steward_plan_items SET decision='rejected_by_review' WHERE id=$1`, plan.Items[0].ID)
	mustStore(t, err)
	detail, err := s.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if detail.Plans[0].Review == nil || detail.Plans[0].Review.ID != review.ID || detail.Plans[0].Review.PlanDigest != review.PlanDigest || detail.Plans[0].Review.EvidenceDigest != review.EvidenceDigest || !detail.Plans[0].Review.ExpiresAt.Equal(review.ExpiresAt) {
		t.Fatal("审查未持久化", detail.Plans)
	}
	before, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	foreign := review
	foreign.ID = uuid.New()
	if err := s.SaveReview(ctx, other, c.ID, foreign); !errors.Is(err, ErrNotFound) {
		t.Errorf("他人保存审查 err=%v", err)
	}
	after, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	if diff := postgrestest.Diff(before, after); len(diff) != 0 {
		t.Fatal("越权写入改变数据库", diff)
	}
	down, err := migrations.Files.ReadFile("0055_steward_reviews.down.sql")
	mustStore(t, err)
	up, err := migrations.Files.ReadFile("0055_steward_reviews.sql")
	mustStore(t, err)
	down64, err := migrations.Files.ReadFile("0064_auto_curation_model.down.sql")
	mustStore(t, err)
	up64, err := migrations.Files.ReadFile("0064_auto_curation_model.sql")
	mustStore(t, err)
	_, err = db.Exec(string(down64))
	mustStore(t, err)
	_, err = db.Exec(string(down))
	mustStore(t, err)
	var decision string
	mustStore(t, db.QueryRow(`SELECT decision FROM steward_plan_items WHERE id=$1`, plan.Items[0].ID).Scan(&decision))
	if decision != "declined" {
		t.Fatal("回退未转换 rejected_by_review", decision)
	}
	var removed bool
	mustStore(t, db.QueryRow(`SELECT to_regclass('steward_reviews') IS NULL AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='steward_user_settings' AND column_name='review_model')`).Scan(&removed))
	if !removed {
		t.Fatal("回退仍保留 C7 结构")
	}
	baseline, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	_, err = db.Exec(string(up))
	mustStore(t, err)
	var model string
	mustStore(t, db.QueryRow(`SELECT review_model FROM steward_user_settings WHERE user_id=$1`, user).Scan(&model))
	if model != "" {
		t.Fatal("升级默认模型非空", model)
	}
	_, err = db.Exec(string(down))
	mustStore(t, err)
	again, err := postgrestest.Snapshot(ctx, db)
	mustStore(t, err)
	if diff := postgrestest.Diff(baseline, again); len(diff) != 0 {
		t.Fatal("往返改变既有数据", diff)
	}
	_, err = db.Exec(string(up))
	mustStore(t, err)
	_, err = db.Exec(string(up64))
	mustStore(t, err)
	detail, err = s.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if len(detail.Messages) != 1 || detail.Conversation.Title != "C7 保留会话" || detail.Plans[0].Review != nil || detail.Plans[0].Items[0].Decision != "declined" {
		t.Fatal("重新升级损坏旧数据", detail)
	}
	review.ID = uuid.New()
	mustStore(t, s.SaveReview(ctx, user, c.ID, review))
	for name, query := range map[string]string{
		"status":   `UPDATE steward_reviews SET status='approved'`,
		"policy":   `UPDATE steward_reviews SET policy_version=0`,
		"items":    `UPDATE steward_reviews SET items='{}'::jsonb`,
		"decision": `UPDATE steward_plan_items SET decision='approved'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(query); err == nil {
				t.Error("迁移缺少约束")
			}
		})
	}
	_, err = db.Exec(`DELETE FROM steward_plans WHERE id=$1`, plan.ID)
	mustStore(t, err)
	var count int
	mustStore(t, db.QueryRow(`SELECT count(*) FROM steward_reviews`).Scan(&count))
	if count != 0 {
		t.Fatal("删除方案未级联清理审查")
	}
}
