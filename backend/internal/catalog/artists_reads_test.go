package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestArtistReadSnapshotRequiresBeginner(t *testing.T) {
	called := false
	_, err := artistSnapshot(context.Background(), NewStore(struct{ Queryer }{}), func(*Store) (bool, error) { called = true; return true, nil })
	if called || !errors.Is(err, ErrQuery) {
		t.Fatalf("snapshot boundary: called=%v err=%v", called, err)
	}
}

func TestArtistQueueRandomizesCompleteCandidateSet(t *testing.T) {
	query := artistQueueSQL("random")
	if strings.Count(query, "LIMIT") != 1 || !strings.Contains(query, "ORDER BY md5($2::text||':'||t.id::text),t.id LIMIT 10000") || !strings.Contains(query, "\n UNION SELECT t.id") {
		t.Fatalf("random queue must deduplicate full participation before ordering and limiting: %s", query)
	}
	if strings.Contains(query, "enable_") || strings.Contains(query, "ANALYZE") {
		t.Fatal("query changed planner settings")
	}
}

func TestArtistDTOExplicitNullAndEmpty(t *testing.T) {
	data, err := json.Marshal(ArtistDetail{Artist: ArtistSummary{Roles: []string{}}, Names: []ArtistName{}, RoleCounts: []ArtistRoleCount{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"roles":[]`, `"names":[]`, `"role_counts":[]`, `"redirect_artist_id":null`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("missing %s: %s", field, data)
		}
	}
	for _, initial := range []string{"A", "Z", "kana", "#"} {
		if !validArtistInitial(initial) {
			t.Fatal(initial)
		}
	}
	for _, initial := range []string{"a", "AA", "", "假名", "é"} {
		if validArtistInitial(initial) {
			t.Fatal(initial)
		}
	}
}
