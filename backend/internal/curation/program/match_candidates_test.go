package program

import (
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Test匹配候选按编号排序并保留整页(t *testing.T) {
	externalIDs := []string{"12", "10", "04", "03", "11", "08", "06", "09", "05", "07", "02", "01"}
	wantIDs := []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}
	check := func(t *testing.T, first, reordered []Candidate) {
		t.Helper()
		gotIDs := make([]string, 0, len(first))
		for _, candidate := range first {
			gotIDs = append(gotIDs, candidate.ExternalID)
		}
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("候选 ID 为 %v，期望按 ID 排序的整页外部返回结果 %v", gotIDs, wantIDs)
		}
		if !reflect.DeepEqual(first, reordered) {
			t.Fatalf("同一批外部结果换序后生成了不同候选，换序前 %+v，换序后 %+v", first, reordered)
		}
	}

	t.Run("release", func(t *testing.T) {
		releases := make([]sources.Release, 0, len(externalIDs))
		for _, id := range externalIDs {
			releases = append(releases, sources.Release{ID: id, Title: "发行 " + id})
		}
		releases[0].Labels = []sources.Label{{CatalogNumber: "CAT-Z"}, {CatalogNumber: "CAT-A"}, {CatalogNumber: "CAT-Z"}}
		reordered := slices.Clone(releases)
		slices.Reverse(reordered)
		first := releaseCandidates("search", releases)
		check(t, first, releaseCandidates("search", reordered))
		if got := first[len(first)-1].CatalogNumbers; !slices.Equal(got, []string{"CAT-A", "CAT-Z"}) {
			t.Fatalf("发行 12 的厂牌编号为 %v，期望去重并排序", got)
		}
	})

	t.Run("recording", func(t *testing.T) {
		recordings := make([]sources.Recording, 0, len(externalIDs))
		for _, id := range externalIDs {
			recordings = append(recordings, sources.Recording{ID: id, Title: "录音 " + id})
		}
		reordered := slices.Clone(recordings)
		slices.Reverse(reordered)
		check(t, recordingCandidates("search", recordings), recordingCandidates("search", reordered))
	})
}
