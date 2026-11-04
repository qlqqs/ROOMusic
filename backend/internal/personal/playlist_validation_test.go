package personal

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func requirePlaylistSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != code {
		t.Fatalf("SQLSTATE: got %v, want %s", err, code)
	}
}

func TestPlaylistCommandNormalization(t *testing.T) {
	playlist, item, anchor, token, track := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cases := []Command{
		{Kind: "create", Name: "  乐单  ", TrackIDs: []string{track, track}},
		{Kind: "rename", PlaylistID: playlist, ExpectedRevision: 2, Name: "  乐单  "},
		{Kind: "delete", PlaylistID: playlist, ExpectedRevision: 2},
		{Kind: "add_items", PlaylistID: playlist, ExpectedRevision: 2, TrackIDs: []string{track, track}},
		{Kind: "remove_item", PlaylistID: playlist, ItemID: item, ExpectedRevision: 2},
		{Kind: "restore_item", PlaylistID: playlist, Token: token},
		{Kind: "move_item", PlaylistID: playlist, ItemID: item, ExpectedRevision: 2, Action: "before", BeforeItemID: &anchor},
	}
	for _, c := range cases {
		t.Run(c.Kind, func(t *testing.T) {
			original, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			lower, err := normalizeCommand(c)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := json.Marshal(c)
			if err != nil || string(unchanged) != string(original) {
				t.Fatal("normalization mutated its input")
			}
			c.PlaylistID, c.ItemID, c.Token = strings.ToUpper(c.PlaylistID), strings.ToUpper(c.ItemID), strings.ToUpper(c.Token)
			c.TrackIDs = append([]string(nil), c.TrackIDs...)
			for i := range c.TrackIDs {
				c.TrackIDs[i] = strings.ToUpper(c.TrackIDs[i])
			}
			if c.BeforeItemID != nil {
				upper := strings.ToUpper(*c.BeforeItemID)
				c.BeforeItemID = &upper
			}
			upper, err := normalizeCommand(c)
			if err != nil || !reflect.DeepEqual(lower, upper) {
				t.Fatalf("normalization: %#v / %#v (%v)", lower, upper, err)
			}
			if len(upper.TrackIDs) > 0 {
				if len(upper.TrackIDs) != 2 {
					t.Fatal("normalization discarded duplicates")
				}
				upper.TrackIDs[0] = "changed"
				if c.TrackIDs[0] == "changed" {
					t.Fatal("normalization aliases caller track array")
				}
			}
			if upper.BeforeItemID != nil {
				*upper.BeforeItemID = "changed"
				if *c.BeforeItemID == "changed" {
					t.Fatal("normalization aliases caller anchor")
				}
			}
			if c.Kind == "create" || c.Kind == "rename" {
				if lower.Name != "乐单" {
					t.Fatal("name not normalized")
				}
			}
		})
	}
	nilIDs, err := normalizeCommand(Command{Kind: "create", Name: "空歌单"})
	if err != nil {
		t.Fatal(err)
	}
	emptyIDs, err := normalizeCommand(Command{Kind: "create", Name: "空歌单", TrackIDs: []string{}})
	if err != nil || !reflect.DeepEqual(nilIDs, emptyIDs) {
		t.Fatal("omitted and empty tracks differ")
	}
}

func TestPlaylistCommandValidation(t *testing.T) {
	id := uuid.New()
	overlong := make([]string, TrackLimit+1)
	for i := range overlong {
		overlong[i] = id
	}
	invalidAnchor := "invalid"
	cases := []struct {
		name string
		c    Command
	}{
		{"unknown_kind", Command{Kind: "unknown"}},
		{"empty_name", Command{Kind: "create", Name: "\u3000 "}},
		{"invalid_utf8", Command{Kind: "create", Name: string([]byte{0xff})}},
		{"control_name", Command{Kind: "create", Name: "曲\u007f目"}},
		{"too_many_codepoints", Command{Kind: "create", Name: strings.Repeat("𝄞", 101)}},
		{"raw_track_limit", Command{Kind: "create", Name: "过长", TrackIDs: overlong}},
		{"invalid_track", Command{Kind: "create", Name: "曲目", TrackIDs: []string{"invalid"}}},
		{"invalid_playlist", Command{Kind: "delete", PlaylistID: "invalid", ExpectedRevision: 1}},
		{"zero_revision", Command{Kind: "delete", PlaylistID: id}},
		{"negative_revision", Command{Kind: "delete", PlaylistID: id, ExpectedRevision: -1}},
		{"unsafe_revision", Command{Kind: "delete", PlaylistID: id, ExpectedRevision: MaxRevision + 1}},
		{"empty_add", Command{Kind: "add_items", PlaylistID: id, ExpectedRevision: 1}},
		{"invalid_item", Command{Kind: "remove_item", PlaylistID: id, ItemID: "invalid", ExpectedRevision: 1}},
		{"invalid_token", Command{Kind: "restore_item", PlaylistID: id, Token: "invalid"}},
		{"unknown_action", Command{Kind: "move_item", PlaylistID: id, ItemID: id, ExpectedRevision: 1, Action: "sideways"}},
		{"menu_anchor", Command{Kind: "move_item", PlaylistID: id, ItemID: id, ExpectedRevision: 1, Action: "up", BeforeItemID: &id}},
		{"invalid_anchor", Command{Kind: "move_item", PlaylistID: id, ItemID: id, ExpectedRevision: 1, Action: "before", BeforeItemID: &invalidAnchor}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeCommand(tc.c); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("accepted %#v: %v", tc.c, err)
			}
		})
	}
	for _, revision := range []int64{1, MaxRevision} {
		if _, err := normalizeCommand(Command{Kind: "delete", PlaylistID: id, ExpectedRevision: revision}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaylistOrderBoundaryIntervalsAndLockNamespaces(t *testing.T) {
	for _, tc := range []struct {
		left, right *playlistItem
		ok          bool
	}{
		{nil, nil, true},
		{nil, &playlistItem{OrderKey: math.MinInt64 + orderStep}, true},
		{nil, &playlistItem{OrderKey: math.MinInt64 + orderStep - 1}, false},
		{&playlistItem{OrderKey: math.MaxInt64 - orderStep}, nil, true},
		{&playlistItem{OrderKey: math.MaxInt64 - orderStep + 1}, nil, false},
		{&playlistItem{OrderKey: 1}, &playlistItem{OrderKey: 1}, false},
		{&playlistItem{OrderKey: 2}, &playlistItem{OrderKey: 1}, false},
	} {
		k, ok := intervalKey(tc.left, tc.right)
		if ok != tc.ok || ok && ((tc.left != nil && k <= tc.left.OrderKey) || (tc.right != nil && k >= tc.right.OrderKey)) {
			t.Fatalf("interval %v/%v: %d %t", tc.left, tc.right, k, ok)
		}
	}
	seen := map[int32]bool{}
	for _, namespace := range []int32{personalKeyLock, personalQuotaLock, 0x4f504552, 0x524f4f54, 0x43415450} {
		if seen[namespace] {
			t.Fatalf("lock namespace collision: %x", namespace)
		}
		seen[namespace] = true
	}
}
