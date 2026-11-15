package ingest

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"path"
	"testing"
)

// 根内无专辑的散曲必须逐文件保留封面，不能共用一个空目录、空专辑键。
func TestPostgreSQLArtworkPriorityStagingRootSingles(t *testing.T) {
	assertArtworkPriorityStaging(t, []artworkPriorityStagingCase{
		{artworkPriorityObservation(t, "01.flac", "", 1, false), true},
		{artworkPriorityObservation(t, "02.flac", "", 2, true), true},
		{artworkPriorityObservation(t, "03.flac", "", 3, false), true},
	}, 3)
}

// 同一目录的两张专辑各保留一份；已有字节和已有延迟标记都能挡住同键的后来者。
func TestPostgreSQLArtworkPriorityStagingDirectoryAlbums(t *testing.T) {
	assertArtworkPriorityStaging(t, []artworkPriorityStagingCase{
		{artworkPriorityObservation(t, "Mixed/01.flac", "专辑甲", 1, false), true},
		{artworkPriorityObservation(t, "Mixed/02.flac", "专辑乙", 2, true), true},
		{artworkPriorityObservation(t, "Mixed/03.flac", "专辑甲", 3, true), false},
		{artworkPriorityObservation(t, "Mixed/04.flac", "专辑乙", 4, false), false},
		// 同名专辑放到另一个目录时仍保留自己的封面。
		{artworkPriorityObservation(t, "Other/01.flac", "专辑甲", 5, false), true},
	}, 2)
}

// 严格多碟虽然共用整理范围，同名专辑仍须每个碟目录保留一份封面。
func TestPostgreSQLArtworkPriorityStagingStrictDiscDirectories(t *testing.T) {
	assertArtworkPriorityStaging(t, []artworkPriorityStagingCase{
		{artworkPriorityObservation(t, "Album/CD1/01.flac", "多碟专辑", 1, true), true},
		{artworkPriorityObservation(t, "Album/CD1/02.flac", "多碟专辑", 2, false), false},
		{artworkPriorityObservation(t, "Album/CD2/01.flac", "多碟专辑", 3, false), true},
		{artworkPriorityObservation(t, "Album/CD2/02.flac", "多碟专辑", 4, true), false},
	}, 1)
}

// 同目录同专辑的不同格式各留一份内嵌图，同格式后续曲目的图片仍去重。
func TestPostgreSQLArtworkPriority暂存格式版本各留内嵌封面(t *testing.T) {
	for _, tc := range []struct {
		name     string
		codec    bool
		deferred bool
	}{
		{"编码字段归一且保留字节", true, false},
		{"编码字段归一且保留延迟来源", true, true},
		{"无编码字段按扩展名区分", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flac := artworkPriorityObservation(t, "Album/01.flac", "同名专辑", 4, tc.deferred)
			mp3 := artworkPriorityObservation(t, "Album/01.mp3", "同名专辑", 8, tc.deferred)
			flacDuplicate := artworkPriorityObservation(t, "Album/02.FLAC", "同名专辑", 5, !tc.deferred)
			mp3Duplicate := artworkPriorityObservation(t, "Album/02.MP3", "同名专辑", 9, !tc.deferred)
			mp3.SourceKind, mp3Duplicate.SourceKind = "mp3_id3v2", "mp3_id3v2"
			if tc.codec {
				flac.Codec, mp3.Codec = "FLAC", "MP3"
				flacDuplicate.Codec, mp3Duplicate.Codec = "flac", "mp3"
			}
			assertArtworkPriorityStaging(t, []artworkPriorityStagingCase{
				{flac, true}, {mp3, true}, {flacDuplicate, false}, {mp3Duplicate, false},
			}, 1)
		})
	}
}

type artworkPriorityStagingCase struct {
	observation SourceObservation
	keepArtwork bool
}

func artworkPriorityObservation(t *testing.T, relativePath, album string, width int, deferred bool) SourceObservation {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 1))); err != nil {
		t.Fatalf("生成合成封面：%v", err)
	}
	observation := SourceObservation{
		RelativePath: relativePath, Directory: path.Dir(relativePath), Album: album,
		Title: relativePath, Artist: "测试艺人", SourceKind: "flac_vorbis_comment",
		Artwork: data.Bytes(), ArtworkMIME: "image/png", ArtworkRelativePath: relativePath,
		ArtworkDeferred: deferred,
	}
	if deferred {
		observation.Artwork = nil
	}
	return observation
}

// 走实际暂存与读取入口，逐来源检查字节、延迟标记和来源路径，而非只比较去重键。
func assertArtworkPriorityStaging(t *testing.T, cases []artworkPriorityStagingCase, wantScopes int) {
	t.Helper()
	db := feedbackDB(t)
	scanID, rootID := feedbackRun(t, db), feedbackRoot(t, db)
	ctx := context.Background()
	for _, item := range cases {
		if err := StageObservation(ctx, db, scanID, rootID, item.observation); err != nil {
			t.Fatalf("暂存 %s：%v", item.observation.RelativePath, err)
		}
	}
	scopes, err := StagedScopes(ctx, db, scanID, rootID, "")
	if err != nil {
		t.Fatalf("读取暂存范围：%v", err)
	}
	if len(scopes) != wantScopes {
		t.Fatalf("整理范围数量 = %d，期望 %d：%v", len(scopes), wantScopes, scopes)
	}
	got := make(map[string]SourceObservation)
	for _, scope := range scopes {
		observations, err := LoadStagedScope(ctx, db, scanID, rootID, scope)
		if err != nil {
			t.Fatalf("读取暂存观察：%v", err)
		}
		for _, observation := range observations {
			got[observation.RelativePath] = observation
		}
	}
	if len(got) != len(cases) {
		t.Fatalf("暂存来源数量 = %d，期望 %d", len(got), len(cases))
	}
	for _, item := range cases {
		want := item.observation
		actual, ok := got[want.RelativePath]
		if !ok {
			t.Errorf("缺少暂存来源 %s", want.RelativePath)
			continue
		}
		if actual.Title != want.Title || actual.Album != want.Album {
			t.Errorf("%s 的曲目或专辑标签被去重改写", want.RelativePath)
		}
		if !item.keepArtwork {
			if len(actual.Artwork) != 0 || actual.ArtworkDeferred || actual.ArtworkMIME != "" || actual.ArtworkRelativePath != "" {
				t.Errorf("%s 的重复封面字段未全部清除", want.RelativePath)
			}
			continue
		}
		if !bytes.Equal(actual.Artwork, want.Artwork) || actual.ArtworkDeferred != want.ArtworkDeferred || actual.ArtworkMIME != want.ArtworkMIME || actual.ArtworkRelativePath != want.ArtworkRelativePath {
			t.Errorf("%s 的独立封面未原样保留", want.RelativePath)
		}
	}
}
