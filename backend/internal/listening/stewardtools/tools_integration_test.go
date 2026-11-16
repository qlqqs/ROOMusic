package stewardtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLStewardC5ListeningTools(t *testing.T) {
	f := newC5Fixture(t)
	ctx := context.Background()
	for i, user := range []string{f.a, f.b} {
		if _, err := f.service.Record(ctx, user, uuid.New(), f.tracks[i], 120); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"list_play_history", "list_recent_releases"} {
		t.Run(name, func(t *testing.T) {
			raw := f.invoke(t, f.a, name, map[string]any{}, "")
			if !strings.Contains(string(raw), f.tracks[0]) || strings.Contains(string(raw), f.tracks[1]) {
				t.Fatalf("历史隔离失败：%s", raw)
			}
			f.invoke(t, f.a, name, map[string]any{"user_id": f.b}, "validation_failed")
			empty := f.invoke(t, uuid.New(), name, map[string]any{}, "")
			if !strings.Contains(string(empty), `"items":[]`) {
				t.Fatalf("无历史应返回空列表：%s", empty)
			}
		})
	}
	for _, tc := range []struct {
		name, track string
		deleted     int
	}{{"other_user", f.tracks[1], 0}, {"missing", uuid.New(), 0}, {"success", f.tracks[0], 1}} {
		t.Run("delete_play_history_item/"+tc.name, func(t *testing.T) {
			raw := f.invoke(t, f.a, "delete_play_history_item", map[string]any{"track_id": tc.track}, "")
			var body struct {
				Deleted int `json:"deleted"`
			}
			if err := json.Unmarshal(raw, &body); err != nil || body.Deleted != tc.deleted {
				t.Fatalf("deleted=%s err=%v", raw, err)
			}
			other := f.invoke(t, f.b, "list_play_history", map[string]any{}, "")
			if !strings.Contains(string(other), f.tracks[1]) {
				t.Fatal("删除影响另一用户")
			}
		})
	}
}
