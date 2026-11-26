package ingest

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestOrganizeFourContinuityModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		files         []string
		discs, tracks []int
		want          []discTrack
	}{
		{"标签", []string{"1-07 Alpha", "2-08 Beta"}, []int{0, 0}, []int{1, 2}, []discTrack{{1, 1}, {1, 2}}},
		{"文件名碟轨", []string{"1-01 Alpha", "2-01 Beta"}, []int{1, 1}, []int{7, 8}, []discTrack{{1, 1}, {2, 1}}},
		{"混合", []string{"Alpha", "02 Beta"}, []int{1, 1}, []int{1, 0}, []discTrack{{1, 1}, {1, 2}}},
		{"只用文件名曲号", []string{"1-01 Alpha", "2-02 Beta"}, []int{1, 1}, []int{7, 8}, []discTrack{{1, 1}, {1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observations []SourceObservation
			for i, file := range tc.files {
				observations = append(observations, SourceObservation{
					RelativePath: "Mixed/" + file + ".flac", Album: file, Artist: "Artist",
					DiscNumber: tc.discs[i], TrackNumber: tc.tracks[i],
				})
			}
			filenames := prepareFilenameEvidence(observations)
			if got := continuousEvidence(observations, filenames, 2); !slices.Equal(got, tc.want) {
				t.Fatalf("连续性证据 = %v，期望 %v，文件名证据 %v", got, tc.want, filenames)
			}
			got := OrganizeObservations(observations)
			if len(got) != 1 || got[0].Anchor.Kind != CandidateOrdinary || len(got[0].Mediums[1]) != 2 {
				t.Fatalf("连续的异名曲应归一张，且保留标签的第一碟：%+v", got)
			}
			for _, track := range got[0].Mediums[1] {
				for i, o := range observations {
					if track.Observation.RelativePath == o.RelativePath && tc.tracks[i] > 0 && track.Position != tc.tracks[i] {
						t.Errorf("%s 曲号 = %d，期望保留标签 %d", o.RelativePath, track.Position, tc.tracks[i])
					}
				}
			}
			slices.Reverse(observations)
			if reversed := OrganizeObservations(observations); !reflect.DeepEqual(got, reversed) {
				t.Errorf("乱序改变归组或锚点：%+v → %+v", got, reversed)
			}
		})
	}
}

func TestOrganizeMixedDirectoryFallback(t *testing.T) {
	t.Run("连续 01 到 03 合并甲甲乙", func(t *testing.T) {
		c := organizeSingleCandidate(t,
			SourceObservation{RelativePath: "Mixed/01.flac", Album: "甲"},
			SourceObservation{RelativePath: "Mixed/02.flac", Album: "甲"},
			SourceObservation{RelativePath: "Mixed/03.flac", Album: "乙"},
		)
		if c.Title != "甲" || c.Anchor.Kind != CandidateOrdinary || len(c.Mediums[1]) != 3 {
			t.Errorf("连续文件名的候选 = %+v，期望甲含三首", c)
		}
	})
	for _, tc := range []struct {
		name   string
		tracks []SourceObservation
		want   map[string]CandidateKind
	}{
		{
			"缺号和重号均不吸收少数",
			[]SourceObservation{
				{RelativePath: "Mixed/Alpha.flac", Album: "A", TrackNumber: 1},
				{RelativePath: "Mixed/Beta.flac", Album: "A", TrackNumber: 1},
				{RelativePath: "Mixed/Gamma.flac", Album: "B"},
			},
			map[string]CandidateKind{"Alpha.flac,Beta.flac": CandidateSameDirSplit, "Gamma.flac": CandidateSameDirSplit},
		},
		{
			"两种格式均连续",
			[]SourceObservation{
				{RelativePath: "Mixed/A.flac", Album: "A", TrackNumber: 1},
				{RelativePath: "Mixed/B.flac", Album: "B", TrackNumber: 2},
				{RelativePath: "Mixed/C.mp3", Album: "A", TrackNumber: 1},
				{RelativePath: "Mixed/D.mp3", Album: "B", TrackNumber: 2},
			},
			map[string]CandidateKind{"A.flac,B.flac": CandidateFormatSplit, "C.mp3,D.mp3": CandidateFormatSplit},
		},
		{
			"只有一种格式连续且失败桶全员有名",
			[]SourceObservation{
				{RelativePath: "Mixed/A.flac", Album: "A", TrackNumber: 1},
				{RelativePath: "Mixed/B.flac", Album: "B", TrackNumber: 2},
				{RelativePath: "Mixed/C.mp3", Album: "A", Artist: "One", TrackNumber: 1},
				{RelativePath: "Mixed/D.mp3", Album: "A", Artist: "Two", TrackNumber: 1},
				{RelativePath: "Mixed/E.mp3", Album: "B", TrackNumber: 4},
			},
			map[string]CandidateKind{"A.flac,B.flac": CandidateFormatSplit, "C.mp3,D.mp3": CandidateSameDirSplit, "E.mp3": CandidateSameDirSplit},
		},
		{
			"两种格式均失败且一个桶有缺名",
			[]SourceObservation{
				{RelativePath: "Mixed/A.flac", Album: "A", Artist: "One", TrackNumber: 3},
				{RelativePath: "Mixed/B.flac", Album: "A", Artist: "Two", TrackNumber: 3},
				{RelativePath: "Mixed/C.flac", Album: "Mixed", Artist: "One", InferredFields: map[string]bool{"album": true}},
				{RelativePath: "Mixed/D.flac", Artist: "Two"},
				{RelativePath: "Mixed/E.flac", Artist: "One"},
				{RelativePath: "Mixed/F.flac"},
				{RelativePath: "Mixed/G.flac"},
				{RelativePath: "Mixed/H.mp3", Album: "A", TrackNumber: 4},
			},
			map[string]CandidateKind{
				"A.flac": CandidateSameDirSplit, "B.flac": CandidateSameDirSplit,
				"C.flac,E.flac": CandidateSameDirSplit, "D.flac": CandidateSameDirSplit,
				"F.flac": CandidateSameDirSplit, "G.flac": CandidateSameDirSplit, "H.mp3": CandidateSameDirSplit,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := OrganizeObservations(tc.tracks)
			got := make(map[string]CandidateKind)
			for _, candidate := range candidates {
				var paths []string
				for _, medium := range candidate.Mediums {
					for _, track := range medium {
						paths = append(paths, track.Observation.RelativePath[len("Mixed/"):])
					}
				}
				slices.Sort(paths)
				got[strings.Join(paths, ",")] = candidate.Anchor.Kind
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("归组成员 = %v，期望 %v", got, tc.want)
			}
			slices.Reverse(tc.tracks)
			if reversed := OrganizeObservations(tc.tracks); !reflect.DeepEqual(candidates, reversed) {
				t.Errorf("乱序改变候选：%+v → %+v", candidates, reversed)
			}
		})
	}
}

func TestOrganizeFormatVersionGroupsKeepTitlesSeparate(t *testing.T) {
	var observations []SourceObservation
	for _, dir := range []string{"Mixed", "Other"} {
		for _, album := range []string{"Alpha", "Beta"} {
			for _, format := range []string{"flac", "mp3"} {
				observations = append(observations, SourceObservation{
					RelativePath: fmt.Sprintf("%s/%s.%s", dir, album, format),
					Album:        album, TrackNumber: 3, Identifiers: []metadata.Identifier{{Kind: metadata.IdentifierMBRelease, Value: "1dee6e39-6367-4143-b3ca-07018db66574"}},
				})
			}
		}
	}
	candidates := OrganizeObservations(observations)
	if len(candidates) != len(observations) {
		t.Fatalf("候选数 = %d，期望每目录每种格式的两个标题都独立，共 %d 张", len(candidates), len(observations))
	}
	groups := map[string]map[string]bool{}
	for _, c := range candidates {
		key := c.Anchor.Scope + "/" + c.Title
		if c.VersionGroup == "" || c.Anchor.Partition == "" || len(c.Mediums[1]) != 1 {
			t.Errorf("%s 缺格式版本组或独立锚点：%+v", key, c)
		}
		if groups[key] == nil {
			groups[key] = map[string]bool{}
		}
		groups[key][c.VersionGroup] = true
	}
	seen := map[string]string{}
	for key, versions := range groups {
		if len(versions) != 1 {
			t.Errorf("%s 的格式版本归组 = %v，期望同组", key, versions)
		}
		for version := range versions {
			if previous, found := seen[version]; found {
				t.Errorf("%s 和 %s 共用版本组 %s", key, previous, version)
			}
			seen[version] = key
		}
	}
	if len(groups) != 4 {
		t.Errorf("带同一 MBID 的两目录归组 = %v，期望每目录两组", groups)
	}
}

func TestOrganizeFieldVotesRetainTiedDecisions(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		values                       []string
		wantTitle, wantLabel, reason string
		wantValues                   []string
	}{
		{"一致", []string{"Alpha", "Alpha", "Alpha", "Alpha"}, "Alpha", "Alpha", "", []string{"Alpha"}},
		{"唯一最高票", []string{"Alpha", "Beta", "Gamma", "Alpha"}, "Alpha", "Alpha", "inconsistent_candidates", []string{"Alpha", "Beta", "Gamma"}},
		{"平票", []string{"Alpha", "Beta", "Beta", "Alpha"}, unknownCandidateTitle, "", "tied_candidates", []string{"Alpha", "Beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observations []SourceObservation
			for i, value := range tc.values {
				observations = append(observations, SourceObservation{
					RelativePath: fmt.Sprintf("Folder/%02d.flac", i+1), Album: value, Label: value, Artist: "Artist", TrackNumber: i + 1,
				})
			}
			c := organizeSingleCandidate(t, observations...)
			if c.Title != tc.wantTitle || c.Label != tc.wantLabel {
				t.Errorf("发行标题和厂牌 = %q / %q，期望 %q / %q", c.Title, c.Label, tc.wantTitle, tc.wantLabel)
			}
			for _, field := range []string{"title", "label"} {
				at := slices.IndexFunc(c.Decisions, func(d FieldDecision) bool { return d.Field == field })
				if at < 0 {
					t.Errorf("缺少 %s 的字段决定", field)
					continue
				}
				d := c.Decisions[at]
				if d.Reason != tc.reason || !slices.Equal(d.Candidates, tc.wantValues) ||
					tc.reason == "tied_candidates" && (d.Value != "" || d.Action != "uncertain_apply" || d.Source != "tag") {
					t.Errorf("%s 的字段决定 = %+v", field, d)
				}
			}
			if len(c.Attention) != 0 {
				t.Errorf("字段冲突重复写入归组问题：%v", c.Attention)
			}
		})
	}
}

func TestOrganizeDiscParentsPreserveNamedAndBonusMedia(t *testing.T) {
	observations := []SourceObservation{
		{RelativePath: "Box/CD1 - 早期 (现场)/One.flac", Album: "First", TrackNumber: 1},
		{RelativePath: "Box/Disk II/Two.flac", Album: "Second", TrackNumber: 1},
		{RelativePath: "Box/Bonus/Three.flac", Album: "Extra One", TrackNumber: 1},
		{RelativePath: "Box/特典/Four.flac", Album: "Extra Two", TrackNumber: 1},
		{RelativePath: "Box/Notes/Five.flac", Album: "Notes", TrackNumber: 1},
		{RelativePath: "Other/One.flac", Album: "Other", TrackNumber: 1},
		{RelativePath: "Other/CD2/Two.flac", Album: "Other", TrackNumber: 1},
		{RelativePath: "Solo/CD2/Only.flac", Album: "Solo", TrackNumber: 1},
		{RelativePath: "Root A.flac"},
		{RelativePath: "Root B.flac"},
	}
	got := OrganizeObservations(observations)
	slices.Reverse(observations)
	if reversed := OrganizeObservations(observations); !reflect.DeepEqual(got, reversed) {
		t.Fatal("输入乱序导致候选或碟序变化")
	}
	expected := map[string]struct {
		positions []int
		titles    map[int]string
	}{
		"Box":       {[]int{1, 2, 3, 4}, map[int]string{1: "早期 (现场)", 3: "Bonus", 4: "特典"}},
		"Box/Notes": {[]int{1}, nil},
		"Other":     {[]int{1, 2}, nil},
		"Solo":      {[]int{2}, nil},
	}
	rootCandidates := 0
	for _, c := range got {
		if c.Anchor.Scope == "" {
			rootCandidates++
			if c.Anchor.Kind != CandidateLooseUnknown || len(c.Mediums[1]) != 1 {
				t.Errorf("根散曲被归并：%+v", c)
			}
			continue
		}
		want, exists := expected[c.Anchor.Scope]
		positions := slices.Sorted(maps.Keys(c.Mediums))
		if !exists || !slices.Equal(positions, want.positions) || !maps.Equal(c.MediumTitles, want.titles) {
			t.Errorf("目录 %q 的碟位与标题 = %v / %v，期望 %+v", c.Anchor.Scope, positions, c.MediumTitles, want)
		}
		delete(expected, c.Anchor.Scope)
	}
	if rootCandidates != 2 || len(expected) != 0 {
		t.Errorf("根散曲 = %d 张，未匹配目录 = %v", rootCandidates, expected)
	}
}

func TestOrganizeTrackNumbersSeparateFromSort(t *testing.T) {
	const dir = "Numbers"
	var observations []SourceObservation
	for i, position := range []int{1, 2, 1, 2} {
		observations = append(observations, SourceObservation{
			RelativePath: fmt.Sprintf("%s/Tag %c.flac", dir, 'A'+i), Album: dir, TrackNumber: position,
		})
	}
	for _, path := range []string{"Live 10 Take.flac", "Live 2 Take.flac"} {
		observations = append(observations, SourceObservation{
			RelativePath: dir + "/" + path, Album: dir, TrackNumber: 1,
			InferredFields: map[string]bool{"track_number": true},
			FieldSources:   map[string]string{"track_number": "default_fallback"},
		})
	}
	c := organizeSingleCandidate(t, observations...)
	wantNames := []string{"Tag A", "Tag C", "Tag B", "Tag D", "Live 2 Take", "Live 10 Take"}
	wantNumbers := []int{1, 1, 2, 2, 0, 0}
	if len(c.Mediums) != 1 || len(c.Mediums[1]) != len(wantNames) {
		t.Fatalf("碟轨分布 = %+v", c.Mediums)
	}
	for i, track := range c.Mediums[1] {
		if track.Position != wantNumbers[i] || track.SortPosition != i+1 || track.Observation.RelativePath != dir+"/"+wantNames[i]+".flac" {
			t.Errorf("第 %d 个内部次序 = %+v，期望 %s，曲号 %d", i+1, track, wantNames[i], wantNumbers[i])
		}
		for _, field := range track.Observation.fieldObservations() {
			if field.SourceKind == "path_order_fallback" || field.Name == "track_number" && track.Position == 0 {
				t.Errorf("缺失曲号写入虚构观察：%+v", field)
			}
		}
	}

	single := organizeSingleCandidate(t, SourceObservation{
		RelativePath: "Single/1-03 Music.flac", Album: "Single", TrackNumber: 1,
		InferredFields: map[string]bool{"disc_number": true, "track_number": true},
	})
	if tracks := single.Mediums[1]; len(tracks) != 1 || tracks[0].Position != 3 || tracks[0].SortPosition != 1 {
		t.Errorf("单文件明确碟轨 1-03 的结果 = %+v", single.Mediums)
	}

	vinyl := organizeSingleCandidate(t, []SourceObservation{
		{RelativePath: "Vinyl/A.flac", Album: "Vinyl", DiscNumber: 1, VinylSide: 1, TrackNumber: 1},
		{RelativePath: "Vinyl/B.flac", Album: "Vinyl", DiscNumber: 1, VinylSide: 2, TrackNumber: 1},
		{RelativePath: "Vinyl/C.flac", Album: "Vinyl", DiscNumber: 1, VinylSide: 2, TrackNumber: 2},
	}...)
	var numbers []int
	for _, track := range vinyl.Mediums[1] {
		numbers = append(numbers, track.Position)
	}
	if !slices.Equal(numbers, []int{1, 2, 3}) {
		t.Errorf("黑胶 A1/B1/B2 换算为 %v，期望 1/2/3", numbers)
	}
}
