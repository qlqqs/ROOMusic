package metadata

import "testing"

func TestFieldAuthenticity(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, source string
		inferred, corrected        bool
		want                       Authenticity
	}{
		{"明确标签中的占位词", "artist", "未知艺术家", "flac_vorbis_comment", false, false, Placeholder},
		{"缺来源的展示值", "release_title", "未知专辑", "", false, false, Placeholder},
		{"历史真名", "artist", "合成艺人", "", false, false, Real},
		{"历史推断观察", "artist", "合成艺人", "", true, false, Inferred},
		{"结构化目录来源即使没有推断位", "album_artist", "目录艺人", "folder", false, false, Inferred},
		{"模型自动值", "artist", "模型艺人", "automatic", false, false, Inferred},
		{"人工覆盖推断值", "artist", "人工艺人", "filename_fallback", true, true, Real},
		{"人工显式设空", "artist", "  ", "tag", false, true, Placeholder},
		{"默认位置", "track_number", "1", "default_fallback", true, false, Placeholder},
		{"合辑展示署名", "artist", "群星", "compilation", false, false, Placeholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FieldAuthenticity(tc.field, tc.value, tc.source, tc.inferred, tc.corrected)
			if got != tc.want {
				t.Fatalf("真实性 %q，期望 %q", got, tc.want)
			}
			wantSearch := tc.value
			if tc.want == Placeholder {
				wantSearch = ""
			}
			if search := got.SearchValue(tc.value); search != wantSearch {
				t.Errorf("查询值 %q，期望 %q", search, wantSearch)
			}
		})
	}
}
