package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

func TestPostgreSQLStewardC10SummaryStoreBoundariesAndOwnership(t *testing.T) {
	db, s, user, other := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, user, "摘要存储", "model")
	mustStore(t, err)
	otherConversation, err := s.CreateConversation(ctx, other, "他人的会话", "model")
	mustStore(t, err)
	var messages []Message
	for _, role := range []string{"user", "assistant", "tool", "assistant", "user"} {
		raw, _ := json.Marshal(provider.Message{Role: provider.Role(role), Content: []provider.ContentBlock{{Type: provider.BlockText, Text: "合成历史"}}})
		m, err := s.AppendMessage(ctx, user, c.ID, MessageInput{Role: role, Content: raw})
		mustStore(t, err)
		messages = append(messages, m)
	}
	for _, test := range []struct{ name, user, conversation, through string }{{"other_user", other, c.ID, messages[3].ID}, {"other_conversation", user, otherConversation.ID, messages[3].ID}, {"split_tool", user, c.ID, messages[1].ID}, {"missing", user, c.ID, uuid.New()}} {
		t.Run(test.name, func(t *testing.T) {
			if err := s.SaveSummary(ctx, test.user, test.conversation, "越权或错误分界", test.through); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
	mustStore(t, s.SaveSummary(ctx, user, c.ID, "合成私有摘要", messages[3].ID))
	detail, err := s.GetConversation(ctx, user, c.ID)
	mustStore(t, err)
	if detail.Summary.Text != "合成私有摘要" || detail.Summary.ThroughMessageID == nil || *detail.Summary.ThroughMessageID != messages[3].ID || len(detail.Messages) != 5 {
		t.Fatal(detail)
	}
	var updated bool
	mustStore(t, db.QueryRow(`SELECT summary_updated_at IS NOT NULL FROM steward_conversations WHERE id=$1`, c.ID).Scan(&updated))
	if !updated {
		t.Fatal("摘要更新时间未保存")
	}
	raw, err := json.Marshal(detail)
	mustStore(t, err)
	var decoded map[string]any
	mustStore(t, json.Unmarshal(raw, &decoded))
	if _, exists := decoded["Summary"]; exists {
		t.Fatal("摘要通过结构序列化泄漏")
	}
}
func TestPostgreSQLStewardC10UnreadTimeBoundary(t *testing.T) {
	db, s, user, other := runtimeDatabase(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, user, "未读边界", "model")
	mustStore(t, err)
	for _, tc := range []struct {
		name, read, finished string
		want                 bool
	}{{"never_finished", "NULL", "NULL", false}, {"never_read", "NULL", "'2026-01-01Z'", true}, {"equal", "'2026-01-01Z'", "'2026-01-01Z'", false}, {"newer", "'2026-01-01Z'", "'2026-01-02Z'", true}, {"older", "'2026-01-02Z'", "'2026-01-01Z'", false}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`UPDATE steward_conversations SET last_read_at=`+tc.read+`,last_run_finished_at=`+tc.finished+` WHERE id=$1`, c.ID)
			mustStore(t, err)
			detail, err := s.GetConversation(ctx, user, c.ID)
			mustStore(t, err)
			if detail.Conversation.Unread != tc.want {
				t.Fatal(detail.Conversation)
			}
		})
	}
	if err := s.MarkConversationRead(ctx, other, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
