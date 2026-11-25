package runtime

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
)

// BackgroundClaims 在独立审查续作时读取原始模型输入绑定的摘要。
func (s *Store) BackgroundClaims(ctx context.Context, userID, conversationID string) (map[string]string, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(m.content->'background_claims','{}'::json)
 FROM steward_messages m JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE c.id=$1 AND c.user_id=$2 AND c.kind='curation' AND m.role='user' ORDER BY m.sequence DESC LIMIT 1`, conversationID, userID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var digests map[string]string
	err = json.Unmarshal(raw, &digests)
	return digests, err
}

// BackgroundPage 只核实指定项本轮实际取得的网页地址。已读正文可供审查重放。
func (s *Store) BackgroundPage(ctx context.Context, userID, conversationID, itemID, url string) (bool, string, error) {
	detail, err := s.GetConversation(ctx, userID, conversationID)
	if err != nil {
		return false, "", err
	}
	var seen bool
	var text string
	for i := len(detail.Messages) - 1; i >= 0; i-- {
		msg := detail.Messages[i]
		var content messageContent
		if err := json.Unmarshal(msg.Content, &content); err != nil {
			return false, "", err
		}
		if content.Role == provider.RoleUser {
			return seen && content.BackgroundItemID == itemID, text, nil
		}
		for _, citation := range content.Citations {
			seen = seen || citation.URL == url
		}
		for _, search := range content.Searches {
			for _, result := range search.Results {
				seen = seen || result.URL == url
			}
		}
		for _, call := range msg.ToolCalls {
			if call.Status != "succeeded" || call.Truncated {
				continue
			}
			switch call.Name {
			case "web_search":
				var out struct{ Results []struct{ URL string } }
				if err := json.Unmarshal(call.Output, &out); err != nil {
					return false, "", err
				}
				for _, result := range out.Results {
					seen = seen || result.URL == url
				}
			case "fetch_page":
				var out struct{ URL, Text string }
				if err := json.Unmarshal(call.Output, &out); err != nil {
					return false, "", err
				}
				if out.URL == url && text == "" {
					text = out.Text
				}
			}
		}
	}
	return false, "", nil
}
