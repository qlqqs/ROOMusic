package ingest

import (
	"path"
	"strconv"
	"strings"
	"testing"
)

// filenameObservation 复现无标签 parser 输出经 scanner 映射后的来源形状。
func filenameObservation(relativePath string) SourceObservation {
	directory := DirectoryOf(relativePath)
	format := strings.TrimPrefix(strings.ToLower(path.Ext(relativePath)), ".")
	return SourceObservation{
		RelativePath: relativePath, Directory: directory,
		Title: strings.TrimSuffix(path.Base(relativePath), path.Ext(relativePath)),
		Album: path.Base(directory), Artist: "未知艺术家", TrackNumber: 1, DiscNumber: 1,
		SourceKind: format, Codec: format,
		InferredFields: map[string]bool{"title": true, "artist": true, "album": true, "track_number": true, "disc_number": true},
		FieldSources: map[string]string{
			"title": "filename_fallback", "artist": "display_fallback", "album": "folder_fallback",
			"track_number": "default_fallback", "disc_number": "default_fallback",
		},
	}
}

func TestFilenameNumbersFiveLevelRules(t *testing.T) {
	cases := []struct {
		stem, title string
		disc, track int
	}{
		{"07", "", 0, 7},
		{"2", "", 0, 2},
		{"1-03 Song", "Song", 1, 3},
		{"02 03 Song", "Song", 2, 3},
		{"2.03 Song", "Song", 2, 3},
		{"1_02_track", "track", 1, 2},
		{"12. Zoltraak", "Zoltraak", 0, 12},
		{"01 - A - B", "A - B", 0, 1},
		{"103 Song", "Song", 1, 3},
		{"  0103 Song", "Song", 1, 3},
		{"Song 2", "Song 2", 0, 2},
		{"Interlude 05", "Interlude 05", 0, 5},
		{"Track 07 - Title", "Title", 0, 7},
		{"track05", "track05", 0, 5},
		{"Bonus Track_08 - Title", "Bonus Track_08 - Title", 0, 8},
		{"1999 - 标题", "1999 - 标题", 0, 0},
		{"2046", "2046", 0, 0},
		{"Blink-182", "Blink-182", 0, 0},
		{"00", "00", 0, 0},
		{"100", "100", 0, 0},
		{"0100", "0100", 0, 0},
		{"Song 00", "Song 00", 0, 0},
		{"Track 00 - Title", "Track 00 - Title", 0, 0},
		{"100 Track 07 - Title", "100 Track 07 - Title", 0, 7},
		{"Song", "Song", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.stem, func(t *testing.T) {
			got := inferFilenameNumbers(tc.stem)
			if got.Disc != tc.disc || got.Track != tc.track {
				t.Errorf("碟/轨 = %d/%d，期望 %d/%d", got.Disc, got.Track, tc.disc, tc.track)
			}
			stem := strings.TrimSpace(tc.stem)
			if title := stem[got.prefixEnd:]; title != tc.title {
				t.Errorf("去编号前缀后 = %q，期望 %q", title, tc.title)
			}
		})
	}
}

func TestFilenameDirectoryAcceptanceAndDowngrade(t *testing.T) {
	cases := []struct {
		name, target string
		others       []string
		disc, track  int
		title        string
	}{
		{"碟轨写法过半", "1-03 Song", []string{"1-04 Other", "Finale"}, 1, 3, "Song"},
		{"三位写法过半", "103 Song", []string{"104 Other", "Finale"}, 1, 3, "Song"},
		{"四位写法过半", "0103 Song", []string{"0104 Other"}, 1, 3, "Song"},
		{"单文件不足两首", "1-03 Song", nil, 0, 1, "03 Song"},
		{"恰好一半不采信", "1-03 Song", []string{"1-04 Other", "Intro", "Finale"}, 0, 1, "03 Song"},
		{"歌名数字降级", "01 99 Luftballons", []string{"02 xx"}, 0, 1, "99 Luftballons"},
		{"三四位不是同一种写法", "103 Song", []string{"0104 Other"}, 0, 0, "103 Song"},
		{"拒绝三位后继续末尾规则", "103 Song 7", []string{"Other"}, 0, 7, "103 Song 7"},
		{"拒绝四位后继续关键词规则", "0103 Track 07 Title", []string{"Other"}, 0, 7, "0103 Track 07 Title"},
		{"年份多数仍不拆", "1999 - Title", []string{"2046", "2024 - Other"}, 0, 0, "1999 - Title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := []SourceObservation{filenameObservation("album/" + tc.target + ".flac")}
			for _, name := range tc.others {
				items = append(items, filenameObservation("album/"+name+".flac"))
			}
			numbers := prepareFilenameEvidence(items)[items[0].RelativePath]
			if numbers.Disc != tc.disc || numbers.Track != tc.track || items[0].Title != tc.title {
				t.Fatalf("目录采信 = %d/%d/%q，期望 %d/%d/%q", numbers.Disc, numbers.Track, items[0].Title, tc.disc, tc.track, tc.title)
			}
		})
	}
}

func TestFilenameEvidenceUsesPhysicalDirectoryAndSkipsCue(t *testing.T) {
	items := []SourceObservation{
		filenameObservation("a/103 Song.flac"), filenameObservation("b/104 Other.flac"),
		filenameObservation("album/1-03 Song.flac"), filenameObservation("album/1-04 Other.flac"),
	}
	for n := 1; n <= 2; n++ {
		cue := filenameObservation(items[2].RelativePath)
		cue.CueIndexFrames = (n - 1) * 75
		cue.RelativePath = "album/disc.cue#track-" + strconv.Itoa(n) + "@album/1-03 Song.flac#index-" + strconv.Itoa(cue.CueIndexFrames)
		cue.SourceKind = "cue_virtual"
		cue.CueSheetPath, cue.CueParentRelativePath = "album/disc.cue", items[2].RelativePath
		cue.CueIndexPresent = true
		cue.Title, cue.TrackNumber = "CUE 标题", n
		for _, field := range []string{"title", "track_number"} {
			delete(cue.InferredFields, field)
			cue.FieldSources[field] = "cue_track"
		}
		items = append(items, cue)
	}
	cueTitle := items[4].Title
	numbers := prepareFilenameEvidence(items)
	if numbers[items[0].RelativePath].Track != 0 || numbers[items[1].RelativePath].Track != 0 {
		t.Fatal("不同物理目录的三位写法被合计采信")
	}
	if items[2].DiscNumber != 1 || items[2].TrackNumber != 3 || items[3].TrackNumber != 4 {
		t.Fatalf("CUE 虚拟轨污染了物理文件多数：%#v", items)
	}
	if items[4].Title != cueTitle || items[4].TrackNumber != 1 || items[4].FieldSources["track_number"] != "cue_track" {
		t.Fatalf("文件名预处理改写了虚拟轨：%#v", items[4])
	}
}

func TestFilenameTitleAndArtistCleaningTable(t *testing.T) {
	cases := []struct {
		name, title, artist string
		track, disc         int
		companion           string
		tagged              bool
	}{
		{"12. Zoltraak.flac", "Zoltraak", "未知艺术家", 12, 0, "", false},
		{"1.01 - 酸欠少女.flac", "酸欠少女", "未知艺术家", 1, 1, "1.02 - 其他.flac", false},
		{"01 - 周杰伦 - 晴天.flac", "晴天", "周杰伦", 1, 0, "", false},
		{"成龙,金喜善 - 无尽的爱.flac", "无尽的爱", "成龙,金喜善", 0, 0, "", false},
		{"Track 07 - Title.mp3", "Title", "未知艺术家", 7, 0, "", false},
		{"07.flac", "07", "未知艺术家", 7, 0, "", false},
		{"1999 - Title.flac", "1999 - Title", "未知艺术家", 0, 0, "", false},
		{"2046.flac", "2046", "未知艺术家", 0, 0, "", false},
		{"A - B - C.flac", "A - B - C", "未知艺术家", 0, 0, "", false},
		{"01 99 Luftballons.flac", "99 Luftballons", "未知艺术家", 1, 0, "02 xx.flac", false},
		{"01 - 文件艺人 - 文件标题.flac", "标签标题", "标签艺人", 9, 4, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := []SourceObservation{filenameObservation("album/" + tc.name)}
			if tc.companion != "" {
				items = append(items, filenameObservation("album/"+tc.companion))
			}
			if tc.tagged {
				items[0].Title, items[0].Artist = tc.title, tc.artist
				items[0].TrackNumber, items[0].DiscNumber = tc.track, tc.disc
				for _, field := range []string{"title", "artist", "track_number", "disc_number"} {
					delete(items[0].InferredFields, field)
					delete(items[0].FieldSources, field)
				}
			}
			prepareFilenameEvidence(items)
			got := items[0]
			if got.Title != tc.title || got.Artist != tc.artist {
				t.Errorf("标题/艺人 = %q/%q，期望 %q/%q", got.Title, got.Artist, tc.title, tc.artist)
			}
			if tc.tagged {
				if got.TrackNumber != tc.track || got.DiscNumber != tc.disc {
					t.Errorf("标签编号被改写：%#v", got)
				}
				for _, field := range []string{"title", "artist", "track_number", "disc_number"} {
					if got.InferredFields[field] || got.FieldSources[field] != "" {
						t.Errorf("标签 %s 来源被改写：%#v", field, got)
					}
				}
				return
			}
			sources := map[string]string{"title": "filename_fallback", "artist": "display_fallback", "track_number": "default_fallback", "disc_number": "default_fallback"}
			if tc.artist != "未知艺术家" {
				sources["artist"] = "filename_fallback"
			}
			if tc.track > 0 {
				sources["track_number"] = "filename_fallback"
			}
			if tc.disc > 0 {
				sources["disc_number"] = "filename_fallback"
			}
			if got.TrackNumber != max(tc.track, 1) || got.DiscNumber != max(tc.disc, 1) {
				t.Errorf("预处理编号 = %d/%d，期望 %d/%d", got.DiscNumber, got.TrackNumber, max(tc.disc, 1), max(tc.track, 1))
			}
			for field, source := range sources {
				if !got.InferredFields[field] || got.FieldSources[field] != source {
					t.Errorf("%s 推断来源 = %t/%q，期望 true/%q", field, got.InferredFields[field], got.FieldSources[field], source)
				}
			}
		})
	}
}

func TestFilenameTitleCleaningDoesNotReplaceTaggedArtist(t *testing.T) {
	o := filenameObservation("album/01 - 文件艺人 - 文件标题.flac")
	o.Artist = "标签艺人"
	delete(o.InferredFields, "artist")
	delete(o.FieldSources, "artist")
	items := []SourceObservation{o}
	prepareFilenameEvidence(items)
	if items[0].Title != "文件艺人 - 文件标题" || items[0].Artist != "标签艺人" || items[0].InferredFields["artist"] || items[0].FieldSources["artist"] != "" {
		t.Fatalf("艺人标签不应被文件名覆盖：%#v", items[0])
	}
}
