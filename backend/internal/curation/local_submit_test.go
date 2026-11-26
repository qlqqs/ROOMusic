package curation

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestLocalAssistChecks(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	t.Run("文字", func(t *testing.T) {
		track := localTrack{ID: "track", Path: "Ｍｏｏｎ.Lit_Sky-Walk/雨声.flac", Fields: []string{"title"}}
		release := localRelease{ID: "album", Fields: []string{"title"}, Tracks: []localTrack{track}}
		for _, tc := range []struct {
			name, value string
			want        bool
		}{
			{"等价", "Moon Lit Sky Walk", true},
			{"跨片段", "Sky Walk 雨声", false},
			{"知识补字", "Moon Lit Sky Waltz", false},
			{"空标点", "._-", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := release.checkedTargets(LocalInput{Texts: []LocalText{{"album", "title", tc.value}}})
				if (len(got) == 1) != tc.want {
					t.Fatalf("提交 %q，期望通过=%v，实际 %+v", tc.value, tc.want, got)
				}
				if tc.want && got[0].Fields["title"].Value != tc.value {
					t.Fatalf("写入文字应保留提交值，实际 %+v", got)
				}
			})
		}
		t.Run("有效与无效同答", func(t *testing.T) {
			got := release.checkedTargets(LocalInput{Texts: []LocalText{
				{"album", "title", "外部专辑"}, {"track", "title", "雨声"}, {"track", "artist", "月亮乐队"},
			}})
			if len(got) != 1 || got[0].Track != "track" || len(got[0].Fields) != 1 || got[0].Fields["title"].Value != "雨声" {
				t.Fatalf("只应计划写入已核准的曲名，实际 %+v", got)
			}
		})
	})
	t.Run("编码", func(t *testing.T) {
		release := localRelease{
			ID: "album", Title: "乱码专辑", Artist: "乱码艺人", Fields: []string{"title", "album_artist"},
			Tracks: []localTrack{{ID: "track", Fields: []string{"title", "artist"}, LocalFile: catalog.LocalFile{
				Observations: map[string]catalog.LocalObservation{
					"album": {Value: "乱码专辑", Source: "garbled"}, "album_artist": {Value: "乱码艺人", Source: "garbled"},
					"title": {Value: "乱码曲名", Source: "garbled"}, "artist": {Value: "乱码曲目艺人", Source: "garbled"},
				},
				Readings: map[string]map[string]string{
					"GBK":  {"album": "江畔", "album_artist": "林舟 & 周雁", "title": "碎月", "artist": "林舟 & 周雁"},
					"Big5": {"album": "別字", "album_artist": "別名", "title": "別曲", "artist": "別人"},
				},
			}}},
		}
		for _, tc := range []struct {
			name           string
			choices, picks []string
			want           bool
		}{
			{"整张多字段", []string{"GBK", "Big5"}, []string{"GBK"}, true},
			{"空交集", []string{}, []string{"GBK"}, false},
			{"多选", []string{"GBK", "Big5"}, []string{"GBK", "Big5"}, false},
			{"候选外", []string{"GBK", "Big5"}, []string{"Shift-JIS"}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := release
				r.Encodings = tc.choices
				in := LocalInput{}
				for _, encoding := range tc.picks {
					in.Encodings = append(in.Encodings, LocalEncoding{"album", encoding})
				}
				got := map[string]any{}
				for _, target := range r.checkedTargets(in) {
					for field, value := range target.Fields {
						got[target.Track+"/"+field] = value.Value
					}
				}
				want := map[string]any{}
				if tc.want {
					want = map[string]any{"/title": "江畔", "/album_artist": "林舟 & 周雁", "track/title": "碎月", "track/artist": "林舟 & 周雁"}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("解码结果 %v，期望 %v", got, want)
				}
			})
		}
	})
	t.Run("署名", func(t *testing.T) {
		const raw = "林舟  & 周雁, 海鸥"
		credit := localCredit{Target: "album", Role: "album_artist", Ref: "track/ALBUMARTIST/0", Raw: raw}
		release := localRelease{ID: "album", Artist: raw, Credits: []localCredit{
			credit, {Target: "album", Role: credit.Role, Ref: "track/ALBUMARTIST/1", Raw: raw},
		}}
		valid := LocalSplit{"album", credit.Role, credit.Ref, []string{"林舟", "周雁", "海鸥"}, []string{"  & ", ", "}}
		repeated := valid
		repeated.Ref = release.Credits[1].Ref
		for _, tc := range []struct {
			name  string
			input []LocalSplit
			want  int
		}{
			{"逐字拼回", []LocalSplit{valid, repeated}, 2},
			{"替换连接词", []LocalSplit{{"album", credit.Role, credit.Ref, valid.Parts, []string{" feat. ", ", "}}}, 0},
			{"少空格", []LocalSplit{{"album", credit.Role, credit.Ref, valid.Parts, []string{" & ", ", "}}}, 0},
			{"顺序颠倒", []LocalSplit{{"album", credit.Role, credit.Ref, []string{"周雁", "林舟", "海鸥"}, valid.Joins}}, 0},
			{"重复提交", []LocalSplit{valid, valid}, 0},
			{"重复原文的分段冲突", []LocalSplit{valid, {"album", credit.Role, repeated.Ref, []string{"林舟  & 周雁", "海鸥"}, []string{", "}}}, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				targets := release.checkedTargets(LocalInput{Splits: tc.input})
				count := 0
				for _, target := range targets {
					count += len(target.Splits)
					if target.Fields["album_artist"].Value != raw {
						t.Fatalf("显示署名改变：%+v", target)
					}
				}
				if count != tc.want {
					t.Fatalf("通过 %d 个署名，期望 %d，实际 %+v", count, tc.want, targets)
				}
			})
		}
	})
	t.Run("归组", func(t *testing.T) {
		a := localAlbum{Group: true, Releases: []localRelease{{ID: "album", Tracks: []localTrack{
			{ID: "a", FileID: "first", Path: "left/01.flac", LocalFile: catalog.LocalFile{SourcePath: "left/01.flac"}},
			{ID: "b", FileID: "second", Path: "right/01.flac", LocalFile: catalog.LocalFile{SourcePath: "right/01.flac"}},
		}}}}
		f := func(id string, disc, track int) LocalGroupFile {
			return LocalGroupFile{FileID: id, LocalPosition: LocalPosition{Disc: disc, Track: track}}
		}
		first := f("first", 1, 1)
		for _, tc := range []struct {
			name  string
			files []LocalGroupFile
			want  int
		}{
			{"跨碟同号", []LocalGroupFile{first, f("second", 2, 1)}, 2},
			{"缺文件", []LocalGroupFile{first}, 0},
			{"越界文件", []LocalGroupFile{first, f("unknown", 2, 1)}, 0},
			{"重复文件", []LocalGroupFile{first, f("first", 1, 2), f("second", 2, 1)}, 0},
			{"同碟重号", []LocalGroupFile{first, f("second", 1, 1)}, 0},
			{"缺位次", []LocalGroupFile{first, f("second", 0, 1)}, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				in := &LocalGroup{}
				in.Albums = append(in.Albums, struct {
					Files []LocalGroupFile `json:"files"`
				}{Files: tc.files})
				if got := a.checkGrouping(in); len(got) != tc.want {
					t.Fatalf("通过 %d 条位次，期望 %d，实际 %+v", len(got), tc.want, got)
				}
			})
		}
	})
}
