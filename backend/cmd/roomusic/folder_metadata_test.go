package main

import "testing"

func TestParseFolderMetadataSupportedPatterns(t *testing.T) {
	testCases := []struct {
		name    string
		folder  string
		artist  string
		album   string
		year    int
		source  string
		media   string
		label   string
		catalog string
		edition string
		pattern string
	}{
		{
			name:   "DIC 变体",
			folder: "Example Artist - Example Album (2024) {Deluxe Edition, Example Label, CAT-1001} [FLAC]",
			artist: "Example Artist", album: "Example Album", year: 2024,
			label: "Example Label", catalog: "CAT-1001", edition: "Deluxe Edition", pattern: "DIC-Variant",
		},
		{
			name:   "WEB-DL",
			folder: "Example Artist - Example Album (2023) - WEB-DL - 24bit FLAC-Source",
			artist: "Example Artist", album: "Example Album", year: 2023,
			source: "web", media: "web", pattern: "WEB-DL",
		},
		{
			name:   "EAC",
			folder: "[EAC][2022.01.02][CAT-2002][Example Artist][Example Album][First Press]",
			artist: "Example Artist", album: "Example Album", year: 2022,
			source: "cd", media: "cd", catalog: "CAT-2002", edition: "First Press", pattern: "EAC",
		},
		{
			name:   "日式横线",
			folder: "[2021.09.27] Example Artist - Example Album [CD][CAT-3003][FLAC]",
			artist: "Example Artist", album: "Example Album", year: 2021,
			source: "cd", media: "cd", catalog: "CAT-3003", pattern: "JP-PT-Dash",
		},
		{
			name:   "日期标题",
			folder: "[2020.12.22] Example Album [WEB][FLAC 48kHz-24bit]",
			album:  "Example Album", year: 2020, source: "web", media: "web", pattern: "Date-Title-Bracket",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, matched := parseFolderMetadata(testCase.folder)
			if !matched {
				t.Fatal("目录名未匹配")
			}
			if got.AlbumArtist != testCase.artist || got.Album != testCase.album || got.Year != testCase.year || got.SourceType != testCase.source || got.MediaType != testCase.media || got.Label != testCase.label || got.Catalog != testCase.catalog || got.Edition != testCase.edition || got.pattern != testCase.pattern {
				t.Fatalf("目录名解析结果不符：%+v", got)
			}
		})
	}
}

func TestParseFolderMetadataRejectsUnmatchedAndOversizedNames(t *testing.T) {
	if _, matched := parseFolderMetadata("plain-folder"); matched {
		t.Fatal("普通目录名不应被当作结构化 metadata")
	}
	oversized := make([]byte, folderMetadataMaxBytes+1)
	for index := range oversized {
		oversized[index] = 'x'
	}
	if _, matched := parseFolderMetadata(string(oversized)); matched {
		t.Fatal("超长目录名不应进入正则解析")
	}
}

func TestFolderMetadataOnlySupplementsMissingCandidateFields(t *testing.T) {
	scope := "Example Artist - Folder Album (2024) {Folder Edition, Folder Label, CAT-1001} [FLAC]"
	candidates := organizeObservations([]sourceObservation{{
		RelativePath:   scope + "/01.flac",
		Directory:      scope,
		Album:          "Tagged Album",
		AlbumArtist:    "Tagged Artist",
		TrackNumber:    1,
		DiscNumber:     1,
		SourceKind:     "flac_vorbis_comment",
		InferredFields: map[string]bool{},
	}})
	if len(candidates) != 1 {
		t.Fatalf("候选数量 = %d，期望 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Title != "Tagged Album" || candidate.AlbumArtist != "Tagged Artist" {
		t.Fatalf("目录证据覆盖了标签：%+v", candidate)
	}
	if releaseYear(candidate) != 2024 || candidate.CatalogNumber != "CAT-1001" || candidate.Edition != "Folder Edition" || candidate.Label != "Folder Label" {
		t.Fatalf("目录证据没有补齐缺失字段：%+v", candidate)
	}
	for _, decision := range candidate.Decisions {
		if decision.Field == "year" || decision.Field == "catalog_number" || decision.Field == "edition" || decision.Field == "label" {
			if decision.Source != "folder" || decision.Confidence != "high" {
				t.Fatalf("目录字段证据来源不正确：%+v", decision)
			}
		}
	}
}

func TestFolderMetadataDoesNotChangeCandidateIdentity(t *testing.T) {
	scope := "Example Artist - Folder Album (2024) - WEB-DL - 24bit FLAC-Source"
	observations := []sourceObservation{{
		RelativePath:   scope + "/01.flac",
		Directory:      scope,
		Album:          scope,
		Artist:         "未知艺术家",
		TrackNumber:    1,
		DiscNumber:     1,
		SourceKind:     "flac_vorbis_comment",
		InferredFields: map[string]bool{"album": true, "artist": true},
	}}
	candidates := organizeObservations(observations)
	if len(candidates) != 1 {
		t.Fatalf("候选数量 = %d，期望 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Anchor.Kind != candidateOrdinary || candidate.Anchor.Scope != scope || candidate.Anchor.Partition != "" {
		t.Fatalf("目录 metadata 改变了候选身份：%+v", candidate.Anchor)
	}
	if candidate.Title != "Folder Album" || candidate.AlbumArtist != "Example Artist" || candidate.SourceType != "web" || candidate.MediaType != "web" {
		t.Fatalf("目录 metadata 没有补齐发行字段：%+v", candidate)
	}
}

func TestStrictMultidiscFolderMetadataDoesNotUseParentScope(t *testing.T) {
	parent := "Example Artist - Example Album (2024) {Example Label, CAT-1001} [FLAC]"
	candidates := organizeObservations([]sourceObservation{
		{
			RelativePath: parent + "/Disc 1/01.flac",
			Directory:    parent + "/Disc 1",
			Album:        "Tagged Album",
			AlbumArtist:  "Tagged Artist",
			TrackNumber:  1,
			DiscNumber:   1,
		},
		{
			RelativePath: parent + "/Disc 2/01.flac",
			Directory:    parent + "/Disc 2",
			Album:        "Tagged Album",
			AlbumArtist:  "Tagged Artist",
			TrackNumber:  1,
			DiscNumber:   2,
		},
	})
	if len(candidates) != 1 || candidates[0].Anchor.Kind != candidateMultiDisc {
		t.Fatalf("严格多碟候选不正确：%+v", candidates)
	}
	if candidates[0].CatalogNumber != "" || candidates[0].Label != "" {
		t.Fatalf("严格多碟父目录制造了 V0 不存在的 metadata：%+v", candidates[0])
	}
	for _, decision := range candidates[0].Decisions {
		if decision.Source == "folder" && (decision.Field == "catalog_number" || decision.Field == "label") {
			t.Fatalf("严格多碟父目录产生了高置信度决定：%+v", decision)
		}
	}
}
