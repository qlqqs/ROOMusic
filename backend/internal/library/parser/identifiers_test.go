package parser

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

const (
	recordingMBID = "a1234567-0000-3000-8000-000000000001"
	releaseMBID   = "a1234567-0000-3000-8000-000000000002"
	artistMBID    = "a1234567-0000-3000-8000-000000000003"
	testDiscID    = "AbCdEf0123456789._-abcdefgh-"
)

type identifierTag struct{ key, value string }

func TestParseIdentifiersByFormat(t *testing.T) {
	identifiers := []struct {
		vorbis, ape, freeform, kind, value string
	}{
		{"MUSICBRAINZ_ALBUMID", "Musicbrainz_Albumid", "MusicBrainz Album Id", "mb_release", releaseMBID},
		{"MUSICBRAINZ_RELEASEGROUPID", "Musicbrainz_Releasegroupid", "MusicBrainz Release Group Id", "mb_release_group", "a1234567-0000-3000-8000-000000000004"},
		{"MUSICBRAINZ_TRACKID", "musicbrainz_trackid", "MusicBrainz Track Id", "mb_recording", recordingMBID},
		{"MUSICBRAINZ_RELEASETRACKID", "musicbrainz_releasetrackid", "MusicBrainz Release Track Id", "mb_track", "a1234567-0000-3000-8000-000000000005"},
		{"MUSICBRAINZ_ARTISTID", "Musicbrainz_Artistid", "MusicBrainz Artist Id", "mb_artist", artistMBID},
		{"MUSICBRAINZ_ALBUMARTISTID", "Musicbrainz_Albumartistid", "MusicBrainz Album Artist Id", "mb_album_artist", "a1234567-0000-3000-8000-000000000006"},
		{"MUSICBRAINZ_WORKID", "Musicbrainz_Workid", "MusicBrainz Work Id", "mb_work", "a1234567-0000-3000-8000-000000000007"},
		{"MUSICBRAINZ_DISCID", "Musicbrainz_Discid", "MusicBrainz Disc Id", "mb_disc", testDiscID},
		{"ACOUSTID_ID", "Acoustid_Id", "Acoustid Id", "acoustid", "a1234567-0000-3000-8000-000000000008"},
		{"ISRC", "ISRC", "ISRC", "isrc", "USX1Z2500001"},
	}
	payload := bytes.Repeat([]byte{0x55}, 8192)
	var vorbis, ape, freeform []identifierTag
	var want []metadata.Identifier
	for _, row := range identifiers {
		vorbis = append(vorbis, identifierTag{row.vorbis, row.value})
		ape = append(ape, identifierTag{row.ape, row.value})
		freeform = append(freeform, identifierTag{row.freeform, row.value})
		want = append(want, metadata.Identifier{Kind: row.kind, Value: row.value})
	}
	// 用 UUID 外形的指纹值检验指纹键误映射成 AcoustID 的情况。
	vorbis = append(vorbis, identifierTag{"ACOUSTID_FINGERPRINT", releaseMBID})
	ape = append(ape, identifierTag{"Acoustid_Fingerprint", releaseMBID})
	freeform = append(freeform, identifierTag{"Acoustid Fingerprint", releaseMBID})
	comments := identifierComments(vorbis)
	apeTag := identifierAPEv2(ape)
	id3 := func(major byte) []byte {
		var txxx []identifierTag
		for _, tag := range freeform {
			if tag.key != "MusicBrainz Track Id" && tag.key != "ISRC" {
				txxx = append(txxx, tag)
			}
		}
		return identifierID3(major, txxx,
			identifierUFID(major, "http://musicbrainz.org", recordingMBID),
			identifierID3Text(major, "TSRC", "USX1Z2500001"))
	}
	tag24 := id3(4)
	for _, format := range []struct {
		name, file string
		content    []byte
	}{
		{"FLAC", "track.flac", parsertest.FLACWithComments(t, comments)},
		{"OGG", "track.ogg", lyricsVorbisStream(comments...)},
		{"Opus", "track.opus", lyricsOpusStream(comments...)},
		{"MP3/ID3v2.3", "track.mp3", id3(3)},
		{"MP3/ID3v2.4", "track.mp3", tag24},
		{"M4A", "track.m4a", identifierM4A(freeform)},
		{"APE", "track.ape", append(parsertest.APE([16]byte{}, []byte{1, 2}), apeTag...)},
		{"WavPack", "track.wv", append(parsertest.WavPackBlock(wavpackStereo44100, 4410, 4410, parsertest.WavPackMetadata(0x0a, []byte{1, 2})), apeTag...)},
		{"WAV", "track.wav", artworkWAV(testWAVChunk("id3 ", tag24))},
		{"AIFF", "track.aiff", parsertest.AIFF("", 44100, 16, 2, 4410, []byte{1, 2}, parsertest.AIFFChunk("ID3 ", tag24))},
		{"DSF", "track.dsf", dsdTestDSF(payload, tag24, uint64(92+len(payload)))},
		{"DFF", "track.dff", dsdTestDFF(payload, "DSD ", "DSD ", dsdTestDFFChunk("ID3 ", tag24))},
	} {
		t.Run(format.name, func(t *testing.T) {
			got := parseLyricsFixture(t, format.file, format.content).Identifiers
			for _, id := range want {
				t.Run(id.Kind, func(t *testing.T) {
					if !slices.Contains(got, id) {
						t.Errorf("缺少标识 %+v，读取到 %+v", id, got)
					}
				})
			}
			if len(got) != len(want) {
				t.Errorf("标识数 = %d，期望 %d，实际 %+v", len(got), len(want), got)
			}
		})
	}
}

func TestParseIdentifierAlbumKeyAliases(t *testing.T) {
	want := metadata.Identifier{Kind: "mb_release", Value: releaseMBID}
	for _, key := range []string{"MUSICBRAINZ_ALBUMID", "MusicBrainz Album Id", "Musicbrainz_Albumid"} {
		t.Run(key, func(t *testing.T) {
			content := parsertest.FLACWithComments(t, []string{key + "=" + releaseMBID})
			requireParsedIdentifiers(t, parseLyricsFixture(t, "track.flac", content), want)
		})
	}
	t.Run("TXXX:MUSICBRAINZ_ALBUMID", func(t *testing.T) {
		content := identifierID3(4, []identifierTag{{"MUSICBRAINZ_ALBUMID", releaseMBID}})
		requireParsedIdentifiers(t, parseLyricsFixture(t, "track.mp3", content), want)
	})
}

func TestParseIdentifierUFIDOwnersAndTXXX(t *testing.T) {
	for _, major := range []byte{3, 4} {
		name := "ID3v2." + string(rune('0'+major))
		t.Run(name, func(t *testing.T) {
			tags := []identifierTag{
				{"MusicBrainz Track Id", strings.ToUpper(recordingMBID)},
				{"MusicBrainz Release Track Id", releaseMBID},
			}
			for _, tc := range []struct {
				name  string
				extra [][]byte
			}{
				{"仅TXXX", nil},
				{"同时含UFID与TXXX", [][]byte{
					identifierUFID(major, "http://musicbrainz.org", recordingMBID),
					identifierUFID(major, "http://musicbrainz.org/", artistMBID),
					identifierUFID(major, "https://example.invalid", artistMBID),
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got := parseLyricsFixture(t, "track.mp3", identifierID3(major, tags, tc.extra...))
					requireParsedIdentifiers(t, got,
						metadata.Identifier{Kind: "mb_recording", Value: recordingMBID},
						metadata.Identifier{Kind: "mb_track", Value: releaseMBID})
				})
			}
		})
	}
}

func TestParseIdentifierArtistMultiValues(t *testing.T) {
	vorbis := []string{"MUSICBRAINZ_ARTISTID=" + artistMBID, "MUSICBRAINZ_ARTISTID=" + releaseMBID, "MUSICBRAINZ_ARTISTID=" + strings.ToUpper(artistMBID)}
	joined := func(separator string) string {
		return strings.Join([]string{artistMBID, releaseMBID, strings.ToUpper(artistMBID)}, separator)
	}
	// 同一个自由格式条目中写三个 data 子原子。
	item := repairM4AFreeform("MusicBrainz Artist Id", artistMBID)
	children := append(item[8:], repairM4AData(1, []byte(releaseMBID))...)
	children = append(children, repairM4AData(1, []byte(strings.ToUpper(artistMBID)))...)
	for _, tc := range []struct {
		name, file string
		content    []byte
	}{
		{"Vorbis重复字段", "track.flac", parsertest.FLACWithComments(t, vorbis)},
		{"APEv2-NUL", "track.ape", append(parsertest.APE([16]byte{}, []byte{1, 2}), identifierAPEv2([]identifierTag{{"Musicbrainz_Artistid", joined("\x00")}})...)},
		{"ID3v2.4-NUL", "track.mp3", identifierID3(4, []identifierTag{{"MusicBrainz Artist Id", joined("\x00")}})},
		{"ID3v2.3-斜杠", "track.mp3", identifierID3(3, []identifierTag{{"MusicBrainz Artist Id", joined("/")}})},
		{"ID3v2.3-分号空格", "track.mp3", identifierID3(3, []identifierTag{{"MusicBrainz Artist Id", joined("; ")}})},
		{"M4A多个data", "track.m4a", lyricsM4A("alac", repairM4AAtom("----", children))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLyricsFixture(t, tc.file, tc.content)
			requireParsedIdentifiers(t, got,
				metadata.Identifier{Kind: "mb_artist", Value: artistMBID},
				metadata.Identifier{Kind: "mb_artist", Value: releaseMBID})
		})
	}
}

func TestParseIdentifierInvalidValuesKeepFileReadable(t *testing.T) {
	data := parsertest.FLACWithComments(t, []string{
		"TITLE=Readable Track",
		"MUSICBRAINZ_ALBUMID=invalid",
		"MUSICBRAINZ_TRACKID=00000000-0000-0000-0000-000000000000 / " + strings.ToUpper(recordingMBID),
		"ISRC=gb-a1b-25-00003",
		"ISRC=bad; us-x1z-25-00001",
		"MUSICBRAINZ_DISCID=+bCdEf0123456789._-abcdefgh-",
		"MUSICBRAINZ_DISCID=" + testDiscID,
	})
	got := parseLyricsFixture(t, "track.flac", data)
	if got.Title != "Readable Track" || got.ISRC != "bad; us-x1z-25-00001" {
		t.Errorf("标题或原始 ISRC 字段错误，实际 %q / %q", got.Title, got.ISRC)
	}
	requireParsedIdentifiers(t, got,
		metadata.Identifier{Kind: "mb_recording", Value: recordingMBID},
		metadata.Identifier{Kind: "isrc", Value: "GBA1B2500003"},
		metadata.Identifier{Kind: "isrc", Value: "USX1Z2500001"},
		metadata.Identifier{Kind: "mb_disc", Value: testDiscID})
}

func TestParseIdentifierReleaseFieldsAndLegacyISRC(t *testing.T) {
	tags := []identifierTag{{"BARCODE", "012345678901"}, {"CATALOGNUMBER", "TEST-001"}, {"ISRC", "gb-a1b-25-00003"}}
	withLegacy := identifierID3(4, tags,
		identifierID3Text(4, "TPUB", "Fixture Label"),
		identifierID3Text(4, "TSRC", "us-x1z-25-00001\x00USX1Z2500002"))
	for _, tc := range []struct {
		name, file  string
		content     []byte
		isrc, label string
		values      []string
	}{
		{"MP3-TXXX", "track.mp3", identifierID3(4, tags), "", "", []string{"GBA1B2500003"}},
		{"MP3-TXXX与TSRC及TPUB", "track.mp3", withLegacy, "us-x1z-25-00001", "Fixture Label", []string{"USX1Z2500001", "USX1Z2500002", "GBA1B2500003"}},
		{"M4A自由格式", "track.m4a", identifierM4A(tags), "", "", []string{"GBA1B2500003"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLyricsFixture(t, tc.file, tc.content)
			if got.Barcode != "012345678901" || got.Catalog != "TEST-001" {
				t.Errorf("条码 / 厂牌编号 = %q / %q", got.Barcode, got.Catalog)
			}
			if got.ISRC != tc.isrc || got.Label != tc.label {
				t.Errorf("原字段 ISRC / 厂牌 = %q / %q，期望 %q / %q", got.ISRC, got.Label, tc.isrc, tc.label)
			}
			var want []metadata.Identifier
			for _, value := range tc.values {
				want = append(want, metadata.Identifier{Kind: "isrc", Value: value})
			}
			requireParsedIdentifiers(t, got, want...)
		})
	}
}

func requireParsedIdentifiers(t *testing.T, got AudioObservation, want ...metadata.Identifier) {
	t.Helper()
	if !slices.Equal(got.Identifiers, want) {
		t.Fatalf("标识 = %+v，期望 %+v", got.Identifiers, want)
	}
}

func identifierComments(tags []identifierTag) []string {
	comments := make([]string, 0, len(tags))
	for _, tag := range tags {
		comments = append(comments, tag.key+"="+tag.value)
	}
	return comments
}

func identifierAPEv2(tags []identifierTag) []byte {
	items := make([]parsertest.APEv2Item, 0, len(tags))
	for _, tag := range tags {
		items = append(items, parsertest.APEv2Item{Key: tag.key, Value: []byte(tag.value)})
	}
	return parsertest.APEv2(2000, false, items...)
}

func identifierID3(major byte, tags []identifierTag, frames ...[]byte) []byte {
	for _, tag := range tags {
		frames = append(frames, identifierID3Text(major, "TXXX", tag.key+"\x00"+tag.value))
	}
	return id3Tag(major, 0, frames...)
}

func identifierID3Text(major byte, frame, value string) []byte {
	// 夹具内容均为 ASCII，ID3v2.3 和 v2.4 都可用 ISO-8859-1 编码。
	return id3Frame(major, frame, [2]byte{}, append([]byte{0}, value...))
}

func identifierUFID(major byte, owner, value string) []byte {
	return id3Frame(major, "UFID", [2]byte{}, []byte(owner+"\x00"+value))
}

func identifierM4A(tags []identifierTag) []byte {
	var items []byte
	for _, tag := range tags {
		items = append(items, repairM4AFreeform(tag.key, tag.value)...)
	}
	return lyricsM4A("alac", items)
}
