package ingest

import (
	"fmt"
	"reflect"
	"testing"
)

func TestAssignWorksTaggedGroups(t *testing.T) {
	track := func(work, movement string, number int) SourceObservation {
		return SourceObservation{Work: work, MovementName: movement, MovementNumber: number}
	}
	for _, tc := range []struct {
		name    string
		tracks  []SourceObservation
		indexes []int
	}{
		{"三个递增乐章", []SourceObservation{track("W", "I", 1), track("W", "II", 2), track("W", "III", 3)}, []int{1, 1, 1}},
		{"同一作品两个录音", []SourceObservation{track("W", "I", 1), track("W", "II", 2), track("W", "I", 1), track("W", "II", 2)}, []int{1, 1, 2, 2}},
		{"不相邻标签作品保留但不分组", []SourceObservation{track("W", "I", 1), track("X", "", 0), track("W", "II", 2)}, []int{0, 0, 0}},
		{"Promenade白名单", []SourceObservation{track("W", "Promenade", 0), track("W", "Gnomus", 0), track("W", "Promenade", 0)}, []int{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := OrganizedCandidate{Genres: []string{"Pop"}, Mediums: map[int][]OrganizedTrack{}}
			want := map[string]WorkAssignment{}
			for i, observation := range tc.tracks {
				observation.RelativePath = fmt.Sprintf("%02d.flac", i+1)
				candidate.Mediums[1] = append(candidate.Mediums[1], OrganizedTrack{Observation: observation, Disc: 1, Position: i + 1})
				want[observation.RelativePath] = WorkAssignment{Title: observation.Work, Index: tc.indexes[i], MovementTitle: observation.MovementName, MovementNumber: observation.MovementNumber, Source: "tag"}
			}
			if got := AssignWorks(candidate); !reflect.DeepEqual(got, want) {
				t.Fatalf("标签作品 = %#v，期望 %#v", got, want)
			}
		})
	}
}

func TestAssignWorksNonClassicalDoesNotInfer(t *testing.T) {
	candidate := OrganizedCandidate{Genres: []string{"Pop"}, Mediums: map[int][]OrganizedTrack{1: {
		{Position: 1, Observation: SourceObservation{RelativePath: "01.flac", Title: "Live: Intro"}},
		{Position: 2, Observation: SourceObservation{RelativePath: "02.flac", Title: "Live: Song A"}},
	}}}
	if got := AssignWorks(candidate); len(got) != 0 {
		t.Fatalf("非古典专辑不应从标题拆作品：%+v", got)
	}
}
