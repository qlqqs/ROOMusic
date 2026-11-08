package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func artistFixture(format string, values []string) []byte {
	comments := make([]string, len(values))
	for i, value := range values {
		comments[i] = "ARTIST=" + value
	}
	switch format {
	case "flac":
		return repairFLACComments(comments...)
	case "ogg":
		return lyricsVorbisStream(comments...)
	case "opus":
		return lyricsOpusStream(comments...)
	case "mp3":
		var frames []byte
		for _, value := range values {
			frames = append(frames, repairMP3BinaryFrame("TPE1", append([]byte{3}, []byte(value)...))[10:]...)
		}
		size := len(frames)
		return append([]byte{'I', 'D', '3', 3, 0, 0, byte(size>>21) & 127, byte(size>>14) & 127, byte(size>>7) & 127, byte(size) & 127}, frames...)
	case "m4a":
		var items []byte
		for _, value := range values {
			items = append(items, repairM4AAtom("\xa9ART", repairM4AData(1, []byte(value)))...)
		}
		return append(repairM4AAtom("ftyp", []byte("M4A \x00\x00\x00\x00")), repairM4AMoovWithItems("mp4a", 2, 16, 44100, 88200, "Track", "", "Album", "", items)...)
	case "wav":
		info := []byte("INFO")
		for _, value := range values {
			info = append(info, testWAVChunk("IART", append([]byte(value), 0))...)
		}
		body := bytes.Join([][]byte{[]byte("WAVE"), testWAVChunk("fmt ", testWAVFormat()), testWAVChunk("LIST", info), testWAVChunk("data", []byte{0, 0, 0, 0})}, nil)
		return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
	}
	panic("测试不支持的格式")
}

func parseArtistFixture(t *testing.T, format string, values []string) (AudioObservation, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track."+format)
	if err := os.WriteFile(path, artistFixture(format, values), 0o600); err != nil {
		t.Fatal(err)
	}
	return ParseAudioFile(path)
}

func TestArtistEvidenceRetainsRepeatedRawTagsAcrossFormats(t *testing.T) {
	formats := []struct{ name, key, source string }{
		{"flac", "ARTIST", "flac_vorbis_comment"}, {"ogg", "ARTIST", "ogg_vorbis_comment"},
		{"opus", "ARTIST", "opus_tags"}, {"mp3", "TPE1", "mp3_id3v2"}, {"m4a", "©ART", "m4a"}, {"wav", "IART", "wav_info"},
	}
	values := []string{" Jay Chou ", "周杰倫", "Jay Chou", "Guest / Other", "jay chou"}
	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			observation, err := parseArtistFixture(t, format.name, values)
			if err != nil {
				t.Fatal(err)
			}
			if observation.Artist != "Guest / Jay Chou / Other / jay chou / 周杰倫" {
				t.Fatalf("署名集合丢失：%q", observation.Artist)
			}
			if len(observation.ArtistTagEvidence) != len(values) {
				t.Fatalf("原始证据被去重：%+v", observation.ArtistTagEvidence)
			}
			for i, raw := range values {
				want := metadata.ArtistTagEvidence{Role: "track_artist", TagKey: format.key, RawValue: raw, SourceKind: format.source, OccurrenceIndex: i}
				if got := observation.ArtistTagEvidence[i]; got != want {
					t.Fatalf("证据[%d]=%+v，期望%+v", i, got, want)
				}
			}
		})
	}
}

func TestArtistEvidenceLimitsFailWithoutSilentTruncation(t *testing.T) {
	for _, format := range []string{"flac", "ogg", "opus", "mp3", "m4a", "wav"} {
		for _, tc := range []struct {
			name        string
			count, size int
			valid       bool
		}{
			{"最大项目数", 100, 5, true}, {"项目数溢出", 101, 5, false},
			{"最大原文", 1, 4096, true}, {"原文溢出", 1, 4097, false},
			{"跨页最大集合", 100, 4096, true},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				values := make([]string, tc.count)
				for i := range values {
					values[i] = strings.Repeat("a", tc.size)
				}
				observation, err := parseArtistFixture(t, format, values)
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%t，err=%v", tc.valid, err)
				}
				if tc.valid && (len(observation.ArtistTagEvidence) != tc.count || observation.ArtistTagEvidence[0].RawValue != values[0]) {
					t.Fatal("预算内原文被截断")
				}
			})
		}
	}
	_, err := parseArtistFixture(t, "flac", []string{strings.Repeat("名", 1366)})
	if err == nil {
		t.Fatal("预算必须按字节而非字符校验")
	}
}

func TestArtistDisplayPreservesAggregateBeyondSingleValueBudget(t *testing.T) {
	values := []string{strings.Repeat("a", 3000), strings.Repeat("b", 3000)}
	observation, err := parseArtistFixture(t, "flac", values)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := metadata.CanonicalTrackArtist(observation.Artist), strings.Join(values, " / "); got != want {
		t.Fatalf("聚合署名被截断：%d，期望%d", len(got), len(want))
	}
}

func TestArtistEvidenceAllRolesAndAlbumArtistSelection(t *testing.T) {
	observation := AudioObservation{SourceKind: "flac_vorbis_comment"}
	for _, tag := range [][2]string{{"ALBUMARTIST", "First"}, {"ALBUMARTIST", " Second "}, {"ALBUM ARTIST", "Last"}, {"Composer", "Jay Chou"}, {"Composer", "周杰倫"}, {"CONDUCTOR", "Conductor"}, {"PERFORMER", "One / Two"}, {"PRODUCER", "Producer"}} {
		if err := setAudioTag(&observation, tag[0], tag[1]); err != nil {
			t.Fatal(err)
		}
	}
	finalizeTags(&observation)
	if observation.AlbumArtist != "First / Second" {
		t.Fatalf("专辑艺人未按优先级合并同级多值：%q", observation.AlbumArtist)
	}
	if len(observation.ArtistTagEvidence) != 8 || observation.ArtistTagEvidence[3].TagKey != "Composer" || observation.ArtistTagEvidence[4].OccurrenceIndex != 1 {
		t.Fatalf("标签键或次序丢失：%+v", observation.ArtistTagEvidence)
	}
	if !reflect.DeepEqual(observation.Credits[:2], []metadata.Credit{{Role: "composer", Name: "Jay Chou", Source: "tag"}, {Role: "composer", Name: "周杰倫", Source: "tag"}}) {
		t.Fatalf("署名被套用别名：%+v", observation.Credits)
	}
}

func TestID3NullSeparatedArtistValuesPreserveEncodingAndAlbumSelection(t *testing.T) {
	for _, encoding := range []byte{0, 1, 2, 3} {
		t.Run(fmt.Sprint(encoding), func(t *testing.T) {
			raw := " First \x00Second\x00"
			payload := []byte(raw)
			if encoding == 1 {
				payload = repairUTF16(raw, true)
			}
			if encoding == 2 {
				payload = repairUTF16(raw, false)[2:]
			}
			frame := repairMP3BinaryFrame("TPE1", append([]byte{encoding}, payload...))
			observation := AudioObservation{SourceKind: "mp3_id3v2"}
			if err := parseID3Frames(frame[10:], &observation, 3, 0); err != nil {
				t.Fatal(err)
			}
			if observation.Artist != "First / Second" || len(observation.ArtistTagEvidence) != 2 || observation.ArtistTagEvidence[0].RawValue != " First " {
				t.Fatalf("ID3多值丢失：%+v", observation)
			}
			frame = repairMP3BinaryFrame("TPE2", append([]byte{encoding}, payload...))
			if err := parseID3Frames(frame[10:], &observation, 3, 0); err != nil {
				t.Fatal(err)
			}
			finalizeTags(&observation)
			if observation.AlbumArtist != "First / Second" || len(observation.ArtistTagEvidence) != 4 {
				t.Fatalf("专辑选择或原文丢失：%+v", observation)
			}
		})
	}
}

func TestM4AMultipleDataAtomsAndFreeformCreditsRetainEvidence(t *testing.T) {
	items := repairM4AAtom("\xa9ART", append(repairM4AData(1, []byte("Jay Chou")), repairM4AData(2, repairUTF16("周杰倫", false))...))
	items = append(items, repairM4AFreeform("Conductor", "Conductor")...)
	items = append(items, repairM4AFreeform("PRODUCER", "Producer")...)
	observation := parseLyricsFixture(t, "track.m4a", lyricsM4A("mp4a", items))
	if observation.Artist != "Artist / Jay Chou / 周杰倫" || len(observation.ArtistTagEvidence) != 5 {
		t.Fatalf("多data项丢失：%+v", observation)
	}
	if got := observation.ArtistTagEvidence[3].TagKey; got != "----:Conductor" {
		t.Fatalf("freeform原始键=%q", got)
	}
	if got := observation.ArtistTagEvidence[2].RawValue; got != "周杰倫" {
		t.Fatalf("UTF16原文=%q", got)
	}
}

func TestArtistEvidenceDistinguishesFallbackFromLiteralUnknownName(t *testing.T) {
	missing, err := parseArtistFixture(t, "flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	literal, err := parseArtistFixture(t, "flac", []string{"未知艺术家"})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Artist != literal.Artist || !missing.InferredFields["artist"] || literal.InferredFields["artist"] || len(missing.ArtistTagEvidence) != 0 || len(literal.ArtistTagEvidence) != 1 {
		t.Fatalf("占位与真实署名没有按来源区分：missing=%+v literal=%+v", missing, literal)
	}
}

func TestM4AAlbumArtistFallbackLosesToPrimarySelection(t *testing.T) {
	items := repairM4AAtom("aART", append(repairM4AData(1, []byte("First")), repairM4AData(1, []byte("Last"))...))
	items = append(items, repairM4AFreeform("ALBUMARTIST", "Evidence Only")...)
	observation := parseLyricsFixture(t, "track.m4a", lyricsM4A("mp4a", items))
	if observation.AlbumArtist != "First / Last" || len(observation.ArtistTagEvidence) != 4 {
		t.Fatalf("主级多值或回退优先级错误：%+v", observation)
	}
}

func TestVorbisAlbumArtistFallbackLevelsAndNewlineValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags [][2]string
		want string
	}{
		{"第三级", [][2]string{{"ALBUM ARTIST", " X "}, {"ALBUM ARTIST", "Y"}}, "X / Y"},
		{"第二级压过第三级", [][2]string{{"ALBUM_ARTIST", "A"}, {"ALBUM ARTIST", "B"}}, "A"},
		{"第一级空值回退", [][2]string{{"ALBUMARTIST", " \n "}, {"ALBUM_ARTIST", "A\nB"}, {"ALBUM ARTIST", "C"}}, "A / B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := AudioObservation{SourceKind: "flac_vorbis_comment"}
			for _, tag := range tc.tags {
				if err := setAudioTag(&observation, tag[0], tag[1]); err != nil {
					t.Fatal(err)
				}
			}
			finalizeTags(&observation)
			if observation.AlbumArtist != tc.want {
				t.Fatalf("AlbumArtist=%q，期望 %q", observation.AlbumArtist, tc.want)
			}
		})
	}
}

func TestID3TXXXAlbumArtistFallback(t *testing.T) {
	for _, encoding := range []byte{0, 1, 2, 3} {
		t.Run(fmt.Sprint(encoding), func(t *testing.T) {
			text := "albumartist\x00A\x00B"
			payload := []byte(text)
			if encoding == 1 {
				payload = repairUTF16(text, true)
			}
			if encoding == 2 {
				payload = repairUTF16(text, false)[2:]
			}
			frame := repairMP3BinaryFrame("TXXX", append([]byte{encoding}, payload...))
			observation := AudioObservation{SourceKind: "mp3_id3v2"}
			if err := parseID3Frames(frame[10:], &observation, 3, 0); err != nil {
				t.Fatal(err)
			}
			finalizeTags(&observation)
			if observation.AlbumArtist != "A / B" || len(observation.ArtistTagEvidence) != 2 || observation.ArtistTagEvidence[0].TagKey != "TXXX:ALBUMARTIST" {
				t.Fatalf("TXXX 专辑艺人回退错误：%+v", observation)
			}
		})
	}
}

func TestCompilationTagsAndOriginalYearFallback(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       bool
	}{
		{"COMPILATION", " YES ", true}, {"COMPILATION", "0", false}, {"ITUNESCOMPILATION", "no", false},
	} {
		observation := AudioObservation{}
		if err := setAudioTag(&observation, tc.key, tc.value); err != nil {
			t.Fatal(err)
		}
		finalizeTags(&observation)
		if observation.Compilation != tc.want {
			t.Errorf("%s=%q Compilation=%t，期望 %t", tc.key, tc.value, observation.Compilation, tc.want)
		}
	}
	for _, tc := range []struct {
		name    string
		current int
		want    int
	}{
		{"缺失时回退", 0, 1999}, {"本版年份优先", 2020, 2020},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := AudioObservation{Year: tc.current}
			if err := setAudioTag(&observation, "ORIGINALDATE", "1999-04-03"); err != nil {
				t.Fatal(err)
			}
			finalizeTags(&observation)
			if observation.Year != tc.want {
				t.Fatalf("Year=%d，期望 %d", observation.Year, tc.want)
			}
		})
	}
	for _, value := range []string{"1", "0"} {
		frame := repairMP3BinaryFrame("TCMP", append([]byte{3}, []byte(value)...))
		observation := AudioObservation{SourceKind: "mp3_id3v2"}
		if err := parseID3Frames(frame[10:], &observation, 3, 0); err != nil {
			t.Fatal(err)
		}
		finalizeTags(&observation)
		if got, want := observation.Compilation, value == "1"; got != want {
			t.Errorf("ID3 TCMP=%s Compilation=%t，期望 %t", value, got, want)
		}
	}
	for _, tc := range []struct {
		value byte
		want  bool
	}{{0, false}, {1, true}} {
		observation := AudioObservation{}
		if err := parseM4AItemValue("cpil", []byte{tc.value}, []byte{0, 0, 0, 21}, &observation); err != nil {
			t.Fatal(err)
		}
		finalizeTags(&observation)
		if observation.Compilation != tc.want {
			t.Errorf("cpil=%d Compilation=%t，期望 %t", tc.value, observation.Compilation, tc.want)
		}
	}
}

func TestNewlineArtistCreditsRemainSeparatedAcrossTagEntrances(t *testing.T) {
	observation := AudioObservation{}
	if err := setAudioTag(&observation, "COMPOSER", "一\n二\r\n三"); err != nil {
		t.Fatal(err)
	}
	frame := repairMP3BinaryFrame("TCOM", append([]byte{3}, []byte("四\n五")...))
	if err := parseID3Frames(frame[10:], &observation, 3, 0); err != nil {
		t.Fatal(err)
	}
	items := repairM4AAtom(string([]byte{0xa9, 'w', 'r', 't'}), repairM4AData(1, []byte("六\r\n七")))
	items = append(items, repairM4AFreeform("COMPOSER", "八\n九")...)
	parseM4AAtoms(repairM4AAtom("ilst", items), &observation)
	finalizeTags(&observation)
	want := []metadata.Credit{
		{Role: "composer", Name: "一", Source: "tag"}, {Role: "composer", Name: "七", Source: "tag"}, {Role: "composer", Name: "三", Source: "tag"},
		{Role: "composer", Name: "九", Source: "tag"}, {Role: "composer", Name: "二", Source: "tag"}, {Role: "composer", Name: "五", Source: "tag"},
		{Role: "composer", Name: "八", Source: "tag"}, {Role: "composer", Name: "六", Source: "tag"}, {Role: "composer", Name: "四", Source: "tag"},
	}
	if !reflect.DeepEqual(observation.Credits, want) {
		t.Fatalf("换行署名粘连或丢失：%+v", observation.Credits)
	}
}

func TestM4AAlbumArtistFreeformFallback(t *testing.T) {
	items := repairM4AFreeform("ALBUMARTIST", "A")
	observation := parseLyricsFixture(t, "track.m4a", lyricsM4A("mp4a", items))
	if observation.AlbumArtist != "A" {
		t.Fatalf("缺少 aART 时未采用 freeform 专辑艺人：%q", observation.AlbumArtist)
	}
}

func TestID3OriginalYearFallback(t *testing.T) {
	for _, tc := range []struct {
		name, current string
		want          int
	}{{"缺本版年份", "", 1999}, {"本版年份存在", "2020", 2020}} {
		t.Run(tc.name, func(t *testing.T) {
			frames := repairMP3BinaryFrame("TDOR", append([]byte{3}, []byte("1999-04-03")...))[10:]
			if tc.current != "" {
				frames = append(frames, repairMP3BinaryFrame("TDRC", append([]byte{3}, []byte(tc.current)...))[10:]...)
			}
			observation := AudioObservation{SourceKind: "mp3_id3v2"}
			if err := parseID3Frames(frames, &observation, 3, 0); err != nil {
				t.Fatal(err)
			}
			finalizeTags(&observation)
			if observation.Year != tc.want {
				t.Fatalf("ID3 年份 = %d，期望 %d", observation.Year, tc.want)
			}
		})
	}
}
