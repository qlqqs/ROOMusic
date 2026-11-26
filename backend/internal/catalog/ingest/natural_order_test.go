package ingest

import (
	"slices"
	"testing"
)

func TestNaturalOrder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want []string
	}{
		{"数字段", []string{"10.flac", "2.flac", "1.flac"}, []string{"1.flac", "2.flac", "10.flac"}},
		{"相同数值前导零少者先", []string{"002.flac", "02.flac", "2.flac", "000.flac", "0.flac"}, []string{"0.flac", "000.flac", "2.flac", "02.flac", "002.flac"}},
		{"非数字按字节并继续比较后段", []string{"b2.flac", "a10b2.flac", "a2b10.flac", "a2b2.flac", "A.flac"}, []string{"A.flac", "a2b2.flac", "a2b10.flac", "a10b2.flac", "b2.flac"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slices.SortFunc(tc.in, func(a, b string) int {
				if naturalLess(a, b) {
					return -1
				}
				if naturalLess(b, a) {
					return 1
				}
				return 0
			})
			if !slices.Equal(tc.in, tc.want) {
				t.Fatalf("自然序 = %v，期望 %v", tc.in, tc.want)
			}
			for _, value := range tc.want {
				if naturalLess(value, value) {
					t.Errorf("相同字符串 %q 不应小于自身", value)
				}
			}
		})
	}
}

func TestNaturalFallbackTrackPositions(t *testing.T) {
	tracks := []OrganizedTrack{
		{Observation: filenameObservation("album/part10song.flac")},
		{Observation: filenameObservation("album/part2song.flac")},
		{Observation: filenameObservation("album/part1song.flac")},
	}
	assignFallbackTrackPositions(tracks)
	for index, want := range []int{1, 2, 3} {
		o := tracks[index].Observation
		if tracks[index].SortPosition != want || o.TrackNumber != 0 || tracks[index].Position != 0 || o.RelativePath != []string{"album/part1song.flac", "album/part2song.flac", "album/part10song.flac"}[index] {
			t.Errorf("%q 曲位 = %#v，期望内部次序 %d 且曲号未知", o.RelativePath, tracks[index], want)
		}
	}
}
