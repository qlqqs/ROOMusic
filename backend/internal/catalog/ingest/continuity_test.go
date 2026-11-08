package ingest

import "testing"

func TestContinuityAlbum(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pairs []discTrack
		want  bool
	}{
		{"连续且输入乱序", []discTrack{{1, 3}, {1, 1}, {1, 2}}, true},
		{"每碟独立连续", []discTrack{{2, 1}, {1, 2}, {1, 1}}, true},
		{"缺轨", []discTrack{{1, 1}, {1, 3}}, false},
		{"重轨", []discTrack{{1, 1}, {1, 1}}, false},
		{"未从一开始", []discTrack{{1, 2}, {1, 3}}, false},
		{"没有第一碟", []discTrack{{2, 1}, {2, 2}}, false},
		{"允许缺第二碟", []discTrack{{1, 1}, {3, 1}, {3, 2}}, true},
		{"只有一首", []discTrack{{1, 1}}, false},
		{"缺少轨号", []discTrack{{1, 1}, {1, 0}}, false},
		{"无碟号按第一碟", []discTrack{{0, 1}, {0, 2}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := continuousAlbum(tc.pairs); got != tc.want {
				t.Fatalf("continuousAlbum(%v) = %t，期望 %t", tc.pairs, got, tc.want)
			}
		})
	}
}

func TestContinuityUsesMixedOrFilenameOnlyEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		tags  []int
		want  bool
	}{
		{"标签和文件名混合", []string{"album/one.flac", "album/02.flac"}, []int{1, 0}, true},
		{"标签不连续但文件名连续", []string{"album/01.flac", "album/02.flac"}, []int{7, 9}, true},
		{"文件名不能重复使用默认轨号", []string{"album/one.flac", "album/two.flac"}, []int{0, 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observations []SourceObservation
			for i, relativePath := range tc.paths {
				o := filenameObservation(relativePath)
				if tc.tags[i] > 0 {
					o.TrackNumber = tc.tags[i]
					delete(o.InferredFields, "track_number")
					delete(o.FieldSources, "track_number")
				}
				observations = append(observations, o)
			}
			filenames := prepareFilenameEvidence(observations)
			if got := continuousObservations(observations, filenames); got != tc.want {
				t.Fatalf("混合/纯文件名连续性 = %t，期望 %t", got, tc.want)
			}
		})
	}
}
