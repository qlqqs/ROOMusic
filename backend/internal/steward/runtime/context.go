package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// MessageContext 是用户随消息提供的界面状态，不参与资源授权。
type MessageContext struct {
	Page     *PageContext     `json:"page,omitempty"`
	Playback *PlaybackContext `json:"playback,omitempty"`
}

// PageContext 描述当前页面，标题尚未读取时可以省略。
type PageContext struct {
	Path  string `json:"path,omitempty"`
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
}

// PlaybackContext 描述当前控制分区及曲目，无当前曲目时省略曲目信息。
type PlaybackContext struct {
	ZoneID   string `json:"zone_id,omitempty"`
	ZoneName string `json:"zone_name,omitempty"`
	TrackID  string `json:"track_id,omitempty"`
	Title    string `json:"title,omitempty"`
	Artist   string `json:"artist,omitempty"`
	Status   string `json:"status,omitempty"`
}

// NormalizeContext 校验界面状态并规范化 UUID；页面与播放均为空时不提供上下文。
func NormalizeContext(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if !utf8.Valid(raw) || !json.Valid(raw) || bytes.TrimSpace(raw)[0] != '{' {
		return nil, ErrInvalidInput
	}
	var value MessageContext
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidInput
	}
	if value.Page == nil && value.Playback == nil {
		return nil, nil
	}
	valid := func(text string, limit int) bool {
		return !strings.ContainsRune(text, 0) && utf8.RuneCountInString(text) <= limit
	}
	id := func(value *string) bool {
		if *value == "" {
			return true
		}
		if !uuid.Valid(*value) {
			return false
		}
		*value = strings.ToLower(*value)
		return true
	}
	if p := value.Page; p != nil {
		switch p.Kind {
		case "album", "artist", "playlist", "track", "other":
		default:
			return nil, ErrInvalidInput
		}
		if !valid(p.Path, 512) || !valid(p.Title, 300) || !id(&p.ID) {
			return nil, ErrInvalidInput
		}
	}
	if p := value.Playback; p != nil {
		if !id(&p.ZoneID) || !id(&p.TrackID) || !valid(p.ZoneName, 300) || !valid(p.Title, 300) || !valid(p.Artist, 300) {
			return nil, ErrInvalidInput
		}
		switch p.Status {
		case "", "idle", "loading", "playing", "paused", "buffering", "error":
		default:
			return nil, ErrInvalidInput
		}
	}
	return json.Marshal(value)
}

// MessageContextStore 提供更新原用户消息上下文的窄存储接点；继续运行不使用它。
type MessageContextStore interface {
	SetMessageContext(context.Context, string, string, string, json.RawMessage) error
}

// SetMessageContext 只更新当前用户会话内的用户消息，保持正文与工具调用顺序。
func (s *Store) SetMessageContext(ctx context.Context, userID, conversationID, messageID string, raw json.RawMessage) error {
	normalized, err := NormalizeContext(raw)
	if err != nil {
		return err
	}
	return storeError("更新管家消息上下文", requireAffected(s.db.ExecContext(ctx, `UPDATE steward_messages m SET context=$4::jsonb
 FROM steward_conversations c WHERE c.id=m.conversation_id AND c.user_id=$1::uuid
 AND c.id=$2::uuid AND m.id=$3::uuid AND m.role='user'`, userID, conversationID, messageID, []byte(normalized))))
}
