package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func personalTestIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("abcdef00-0000-4000-8000-%012x", i+1)
	}
	return ids
}

func TestPersonalReadsInputBounds(t *testing.T) {
	store := NewStore(nil) // 空输入和超限都不能触及数据库。
	for _, input := range [][]string{nil, {}, {"", "invalid", "1234"}} {
		facts, err := store.LoadTrackFacts(context.Background(), input)
		if err != nil || facts == nil || len(facts) != 0 {
			t.Fatalf("empty facts: %v, %v", facts, err)
		}
		groups, err := store.LoadReleaseGroupSummaries(context.Background(), input)
		if err != nil || groups == nil || len(groups) != 0 {
			t.Fatalf("empty groups: %v, %v", groups, err)
		}
	}
	if got, err := store.LoadTrackFacts(context.Background(), personalTestIDs(10001)); err == nil || got != nil {
		t.Fatalf("accepted 10001 distinct tracks: %v", err)
	}
	if got, err := store.LoadReleaseGroupSummaries(context.Background(), personalTestIDs(1001)); err == nil || got != nil {
		t.Fatalf("accepted 1001 distinct groups: %v", err)
	}
}

type personalFailingQueryer struct {
	Queryer
	ctx   context.Context
	ids   []string
	err   error
	calls int
}

func (q *personalFailingQueryer) QueryContext(ctx context.Context, _ string, args ...any) (*sql.Rows, error) {
	q.ctx = ctx
	q.calls++
	q.ids = args[0].([]string)
	return nil, q.err
}

func TestPersonalReadsNormalizeAndPreserveQueryErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int
		load  func(*Store, context.Context, []string) error
	}{
		{"facts", 10000, func(s *Store, ctx context.Context, ids []string) error {
			got, err := s.LoadTrackFacts(ctx, ids)
			if got != nil {
				t.Error("query failure returned partial facts")
			}
			return err
		}},
		{"groups", 1000, func(s *Store, ctx context.Context, ids []string) error {
			got, err := s.LoadReleaseGroupSummaries(ctx, ids)
			if got != nil {
				t.Error("query failure returned partial groups")
			}
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := personalTestIDs(test.limit)
			input := append([]string{strings.ToUpper(want[0]), "bad", want[0]}, want...)
			q := &personalFailingQueryer{err: context.Canceled}
			err := test.load(NewStore(q), ctx, input)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
				t.Fatalf("query cause or classification lost: %v", err)
			}
			if q.ctx != ctx || q.calls != 1 || !reflect.DeepEqual(q.ids, want) {
				t.Fatalf("context/batch not preserved: calls=%d IDs=%d", q.calls, len(q.ids))
			}
			if input[0] != strings.ToUpper(want[0]) {
				t.Fatal("caller input mutated")
			}
		})
	}
}

func TestPersonalReadsSafeJSON(t *testing.T) {
	fact := TrackFact{TrackID: "track", ReleaseID: "release", GroupID: "group"}
	encoded, err := json.Marshal(fact)
	if err != nil || string(encoded) != `{"track_id":"track","release_id":"release","group_id":"group","present":false,"duration_seconds":null}` {
		t.Fatalf("safe facts shape: %s, %v", encoded, err)
	}
	group := PersonalReleaseGroupSummary{GroupID: "group"}
	encoded, err = json.Marshal(group)
	if err != nil || string(encoded) != `{"group_id":"group","available":false,"release_count":0,"representative":null}` {
		t.Fatalf("unavailable group shape: %s, %v", encoded, err)
	}
}
