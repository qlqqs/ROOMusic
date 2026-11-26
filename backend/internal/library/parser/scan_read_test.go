package parser

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"unicode/utf16"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestScanReadFormat(t *testing.T) {
	audio := bytes.Repeat([]byte{0x23, 0x45}, 1000)
	for _, fixture := range fingerprintFormats() {
		t.Run(fixture.name, func(t *testing.T) {
			names := map[string]string{
				".flac": "wrong.mp3", ".m4a": "extensionless", ".mp3": "valid.aac",
				".ogg": "vorbis.opus", ".opus": "opus.ogg", ".wav": "wrong.wavpack",
			}
			path := writeFingerprintFixture(t, names[fixture.extension], fixture.build(t, audio, "Synthetic"))
			got, err := ParseAudioFile(path)
			if err != nil || got.Format != fixture.extension {
				t.Fatalf("真实格式 = %q，错误 = %v，期望 %q", got.Format, err, fixture.extension)
			}
			if fp, err := ContentFingerprint(context.Background(), path); err != nil || fp == "" {
				t.Fatalf("按真实格式计算指纹 = %q，错误 = %v", fp, err)
			}
		})
	}

	standalone := id3Tag(4, 0, titleFrame(4, "Standalone"))
	for _, tc := range []struct {
		name, format, codec string
		data                []byte
	}{
		{"prefixed.flac", ".ape", "ape", append(bytes.Clone(standalone), parsertest.APE(md5.Sum(audio), audio)...)},
		{"wavpack.mp3", ".wv", "wavpack", parsertest.WavPackBlock(wavpackStereo44100, 44100, 44100, parsertest.WavPackMetadata(0x0a, audio[:4]))},
		{"aiff.aif", ".aiff", "aiff", parsertest.AIFF("", 48000, 16, 2, 24000, audio)},
		{"aifc", ".aiff", "aiff", parsertest.AIFF("sowt", 48000, 16, 2, 24000, audio)},
		{"dsf", ".dsf", "dsf", dsdTestDSF(audio, nil, 0)},
		{"dff", ".dff", "dff", dsdTestDFF(audio, "DSD ", "DSD ")},
		{"standalone", ".mp3", "mp3", standalone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFingerprintFixture(t, tc.name, tc.data)
			got, err := ParseAudioFile(path)
			if err != nil || got.Format != tc.format || got.Codec != tc.codec {
				t.Fatalf("格式/编码 = %q/%q，错误 = %v，期望 %q/%q", got.Format, got.Codec, err, tc.format, tc.codec)
			}
			if tc.name == "standalone" {
				fp, err := ContentFingerprint(context.Background(), path)
				if got.Title != "Standalone" || fp != "" || err != nil {
					t.Fatalf("独立 ID3 标题 = %q，指纹 = %q，错误 = %v", got.Title, fp, err)
				}
			}
		})
	}

	adts := []byte{0xff, 0xf1, 0x50, 0x80, 0x00, 0xff, 0xfc}
	for _, tc := range []struct {
		name, format string
		data         []byte
	}{
		{"empty", "", nil},
		{"random.mp3", "", []byte{1, 5, 34, 73, 0, 99, 23, 56}},
		{"raw.aac", "", adts},
		{"prefixed.aac", "", append(bytes.Clone(standalone), adts...)},
		{"truncated.mp3", ".flac", []byte("fLaC\x80\x00\x00\x22")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAudioFile(writeFingerprintFixture(t, tc.name, tc.data))
			if err == nil || errors.Is(err, ErrUnknownFormat) != (tc.format == "") || got.Format != tc.format {
				t.Fatalf("格式/错误 = %q/%v，期望格式 %q，未知格式 = %t", got.Format, err, tc.format, tc.format == "")
			}
		})
	}
	t.Run("open_failure", func(t *testing.T) {
		_, err := ParseAudioFile(filepath.Join(t.TempDir(), "absent.flac"))
		if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("打开失败被分类为 %v", err)
		}
	})
	t.Run("read_failure", func(t *testing.T) {
		_, err := ParseAudioFile(t.TempDir())
		var pathError *fs.PathError
		if !errors.As(err, &pathError) || pathError.Op != "read" || errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("读取失败被分类为 %v", err)
		}
	})
}

func scanReadVorbis(t *testing.T, tags ...string) AudioObservation {
	t.Helper()
	return parseLyricsFixture(t, "tags.flac", parsertest.FLACWithComments(t, tags))
}

func TestScanReadTagForms(t *testing.T) {
	t.Run("totals", func(t *testing.T) {
		for _, tc := range []struct {
			keys []string
			disc bool
			raw  string
			want int
		}{
			{[]string{"TRCK", "TRACK", "TRACKNUMBER"}, false, "2/12", 12},
			{[]string{"TRACKTOTAL", "TRACKCOUNT", "TOTALTRACKS"}, false, "12", 12},
			{[]string{"TPOS", "DISCNUMBER", "DISC", "MEDIA", "DISC #"}, true, "3/2", 3},
			{[]string{"DISCTOTAL", "DISCCOUNT", "TOTALDISCS", "DISCC", "MEDIACOUNT"}, true, "4", 4},
			{[]string{"TRACKNUMBER"}, false, "2/0", 0},
			{[]string{"DISCNUMBER"}, true, "3/0", 0},
		} {
			for _, key := range tc.keys {
				t.Run(key+"="+tc.raw, func(t *testing.T) {
					got := scanReadVorbis(t, key+"="+tc.raw).ScanTags
					total := 0
					if got != nil {
						total = got.TrackTotal
						if tc.disc {
							total = got.DiscTotal
						}
					}
					if total != tc.want {
						t.Errorf("总数 = %d，期望 %d", total, tc.want)
					}
				})
			}
		}
		for _, attached := range [][]string{
			{"TRACKNUMBER=2/9", "TRACKTOTAL=12", "TRACKCOUNT=15", "DISCNUMBER=3/6", "DISCTOTAL=8", "MEDIACOUNT=2"},
			{"TRACKNUMBER=1", "TRACKTOTAL=bad", "TOTALTRACKS=12", "DISCNUMBER=2", "DISCTOTAL=bad", "TOTALDISCS=8", "ISSINGLE=no"},
		} {
			wantTrack, wantDisc := 15, 3
			if attached[0] == "TRACKNUMBER=1" {
				wantTrack, wantDisc = 0, 0
			}
			for range 2 {
				tags := scanReadVorbis(t, attached...).ScanTags
				if tags == nil || tags.TrackTotal != wantTrack || tags.DiscTotal != wantDisc {
					t.Errorf("%v 的总数 = %+v，期望 %d/%d", attached, tags, wantTrack, wantDisc)
				}
				slices.Reverse(attached)
			}
		}
	})
	t.Run("native_numbers_and_dates", func(t *testing.T) {
		native := id3Tag(4, 0, identifierID3Text(4, "TRCK", "2/12"), identifierID3Text(4, "TPOS", "3/2"), identifierID3Text(4, "TYER", "1995"))
		id3 := parseLyricsFixture(t, "tags.mp3", native)
		if s := id3.ScanTags; s == nil || s.TrackTotal != 12 || s.DiscTotal != 3 || !reflect.DeepEqual(s.ReleaseDate, &metadata.ScanDate{Year: 1995}) {
			t.Fatalf("ID3 快照 = %+v", s)
		}
		atom := func(key string, number, total uint16) []byte {
			b := make([]byte, 8)
			binary.BigEndian.PutUint16(b[2:4], number)
			binary.BigEndian.PutUint16(b[4:6], total)
			return repairM4AAtom(key, repairM4AData(0, b))
		}
		items := bytes.Join([][]byte{
			atom("trkn", 2, 12),
			atom("disk", 3, 2),
			repairM4AAtom("\xa9day", repairM4AData(1, []byte("1999-04-08"))),
		}, nil)
		got := parseLyricsFixture(t, "tags.m4a", lyricsM4A("alac", items))
		if s := got.ScanTags; s == nil || got.TrackNumber != 2 || got.DiscNumber != 3 || s.TrackTotal != 12 || s.DiscTotal != 3 ||
			!reflect.DeepEqual(s.ReleaseDate, &metadata.ScanDate{Year: 1999, Month: 4, Day: 8}) {
			t.Fatalf("MP4 编号 = %d/%d，快照 = %+v", got.TrackNumber, got.DiscNumber, s)
		}
	})
	t.Run("date_aliases", func(t *testing.T) {
		for _, group := range []struct {
			keys []string
			get  func(*metadata.ScanTags) *metadata.ScanDate
		}{
			{[]string{"TDOR", "TDRC", "TDRL", "RELEASED", "DATE", "YEAR", "RELEASEDATE"}, func(s *metadata.ScanTags) *metadata.ScanDate { return s.ReleaseDate }},
			{[]string{"ORIGINALDATE", "TDOR", "TORY", "ORIGINALRELEASEDATE"}, func(s *metadata.ScanTags) *metadata.ScanDate { return s.OriginalReleaseDate }},
			{[]string{"RECORDING DATE", "PERFORMANCE DATE", "RECORDINGDATE", "PERFORMANCEDATE"}, func(s *metadata.ScanTags) *metadata.ScanDate { return s.RecordingDate }},
			{[]string{"RECORDING START DATE", "PERFORMANCE START DATE", "RECORDINGSTARTDATE", "PERFORMANCESTARTDATE"}, func(s *metadata.ScanTags) *metadata.ScanDate { return s.RecordingStartDate }},
			{[]string{"RECORDING END DATE", "PERFORMANCE END DATE", "RECORDINGENDDATE", "PERFORMANCEENDDATE"}, func(s *metadata.ScanTags) *metadata.ScanDate { return s.RecordingEndDate }},
		} {
			for _, key := range group.keys {
				t.Run(key, func(t *testing.T) {
					for _, tc := range []struct {
						raw  string
						want metadata.ScanDate
					}{
						{"1995", metadata.ScanDate{Year: 1995}},
						{"Mar:1997", metadata.ScanDate{Year: 1997, Month: 3}},
						{"04/1997", metadata.ScanDate{Year: 1997, Month: 4}},
						{"1996-05", metadata.ScanDate{Year: 1996, Month: 5}},
						{"1999-04-08T10:11:12Z", metadata.ScanDate{Year: 1999, Month: 4, Day: 8}},
						{"1999-02-31", metadata.ScanDate{}},
					} {
						tags := scanReadVorbis(t, key+"="+tc.raw).ScanTags
						if tags == nil || !reflect.DeepEqual(group.get(tags), &tc.want) {
							t.Errorf("%s=%s 期望日期 %+v，快照 = %+v", key, tc.raw, tc.want, tags)
						}
					}
				})
			}
		}
		input := []string{"TDOR=1999", "TDRC=2013", "DATE=2019", "RELEASEDATE=2024-03", "ORIGINALDATE=1990", "TORY=1980",
			"ORIGINALRELEASEDATE=bad", "RECORDING DATE=1981", "PERFORMANCEDATE=1982"}
		for range 2 {
			s := scanReadVorbis(t, input...).ScanTags
			if s == nil || !reflect.DeepEqual(s.ReleaseDate, &metadata.ScanDate{Year: 2024, Month: 3}) ||
				!reflect.DeepEqual(s.OriginalReleaseDate, &metadata.ScanDate{}) ||
				!reflect.DeepEqual(s.RecordingDate, &metadata.ScanDate{Year: 1982}) {
				t.Errorf("日期优先级输入 %v 的快照 = %+v", input, s)
			}
			slices.Reverse(input)
		}
	})
	t.Run("flags_and_replaygain", func(t *testing.T) {
		for _, key := range []string{"MEDIATYPE", "MEDIA_TYPE", "RELEASETYPE", "RELEASE_TYPE", "MUSICBRAINZ ALBUM TYPE"} {
			t.Run(key, func(t *testing.T) {
				for _, tc := range []struct {
					tags []string
					want bool
				}{
					{[]string{key + "=bootleg"}, true},
					{[]string{key + "=bootleg", "BOOTLEG=no"}, false},
					{[]string{"BOOTLEG=no", key + "=bootleg"}, false},
				} {
					s := scanReadVorbis(t, tc.tags...).ScanTags
					if s == nil || s.Bootleg == nil || *s.Bootleg != tc.want {
						t.Errorf("私录输入 %v 的快照 = %+v，期望 %t", tc.tags, s, tc.want)
					}
				}
			})
		}
		input := []string{"ISSINGLE= YeS ", "LIVE=0", "BOOTLEG=TRUE", "ISEXTENDEDPLAY=1", "ISSOUNDTRACK=no",
			"PRIMARYVERSION=false", "BESTVERSION=true", "REPLAYGAIN_TRACK_GAIN= -3.5 dB ",
			"REPLAYGAIN_ALBUM_GAIN=0DB", "REPLAYGAIN_TRACK_PEAK=0", "REPLAYGAIN_ALBUM_PEAK=0.875"}
		for range 2 {
			s := scanReadVorbis(t, input...).ScanTags
			if s == nil {
				t.Fatal("缺少快照")
			}
			for _, v := range []struct {
				got  *bool
				want bool
			}{
				{s.Single, true}, {s.Live, false}, {s.Bootleg, true}, {s.EP, true}, {s.Soundtrack, false}, {s.PrimaryVersion, false},
			} {
				if v.got == nil || *v.got != v.want {
					t.Errorf("标记 = %v，期望 %v", v.got, v.want)
				}
			}
			for _, v := range []struct {
				got  *float64
				want float64
			}{
				{s.TrackGain, -3.5}, {s.AlbumGain, 0}, {s.TrackPeak, 0}, {s.AlbumPeak, .875},
			} {
				if v.got == nil || *v.got != v.want {
					t.Errorf("增益或峰值 = %v，期望 %v", v.got, v.want)
				}
			}
			slices.Reverse(input)
		}
		s := scanReadVorbis(t, "BESTVERSION=yes", "REPLAYGAIN_TRACK_GAIN=NaN", "REPLAYGAIN_TRACK_PEAK=1 dB", "REPLAYGAIN_ALBUM_PEAK=Inf").ScanTags
		if s == nil || s.PrimaryVersion == nil || !*s.PrimaryVersion || s.TrackGain != nil || s.TrackPeak != nil || s.AlbumPeak != nil || s.Single != nil {
			t.Errorf("备用主版本、无效增益和缺失标记的快照 = %+v", s)
		}
	})
}

func TestScanReadCommentsLyricsAndMedia(t *testing.T) {
	t.Run("comments", func(t *testing.T) {
		utf16BE := func(s string) []byte {
			var result []byte
			for _, u := range utf16.Encode([]rune(s)) {
				result = binary.BigEndian.AppendUint16(result, u)
			}
			return result
		}
		comm := append([]byte{2, 'e', 'n', 'g'}, utf16BE("描述\x00条码\n正文")...)
		ape := append(parsertest.APE([16]byte{}, []byte{1, 2, 3}), identifierAPEv2([]identifierTag{{"Comment", "APE comment"}})...)
		mp4 := bytes.Join([][]byte{
			repairM4AAtom("\xa9cmt", repairM4AData(1, []byte("MP4\nbody"))),
			repairM4AFreeform("COMMENT", "freeform body"),
		}, nil)
		for _, tc := range []struct {
			name string
			data []byte
			want []metadata.ScanComment
		}{
			{"comment.flac", parsertest.FLACWithComments(t, []string{"COMMENT=first\nline", "DESCRIPTION=middle", "COMMENT=last"}),
				[]metadata.ScanComment{{Key: "COMMENT", Value: "first\nline"}, {Key: "DESCRIPTION", Value: "middle"}, {Key: "COMMENT", Value: "last"}}},
			{"comment.ape", ape, []metadata.ScanComment{{Key: "COMMENT", Value: "APE comment"}}},
			{"comment.m4a", lyricsM4A("alac", mp4), []metadata.ScanComment{{Key: "©CMT", Value: "MP4\nbody"}, {Key: "COMMENT", Value: "freeform body"}}},
			{"comment.mp3", id3Tag(4, 0, id3Frame(4, "COMM", [2]byte{}, comm), id3Frame(4, "COMM", [2]byte{}, []byte("\x03eng\x00another"))),
				[]metadata.ScanComment{{Key: "COMM", Value: "条码\n正文"}, {Key: "COMM", Value: "another"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tags := parseLyricsFixture(t, tc.name, tc.data).ScanTags
				if tags == nil || !slices.Equal(tags.Comments, tc.want) {
					t.Fatalf("注释快照 = %+v，期望 %+v", tags, tc.want)
				}
			})
		}
	})
	t.Run("text_lyrics", func(t *testing.T) {
		three := "[00:01.10]One\n[0:2.2]Two\n[01:02.003]Three\nordinary"
		four := three + "\n\n [01:04.0]Four"
		for _, tc := range []struct {
			name           string
			got            AudioObservation
			lyrics, synced string
		}{
			{"exactly_three_quarters", scanReadVorbis(t, "LYRICS="+three), three, ""},
			{"more_than_three_quarters", scanReadVorbis(t, "LYRICS="+four), four, four},
			{"independent_ordinary_lyrics", scanReadVorbis(t, "SYNCED LYRICS="+four, "UNSYNCED LYRICS=ordinary lyrics"), "ordinary lyrics", four},
			{"old_mp4_lyrics", parseLyricsFixture(t, "plain.m4a", lyricsM4A("alac", repairM4AAtom("\xa9lyr", repairM4AData(1, []byte("normal MP4"))))), "normal MP4", ""},
			{"old_uslt", parseLyricsFixture(t, "plain.mp3", id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{}, usltPayload(3, nil, []byte("normal ID3"))))), "normal ID3", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var synced string
				if tc.got.ScanTags != nil {
					synced = tc.got.ScanTags.SyncedLyrics
				}
				if tc.got.Lyrics != tc.lyrics || synced != tc.synced {
					t.Fatalf("显示/同步 = %q/%q，期望 %q/%q", tc.got.Lyrics, synced, tc.lyrics, tc.synced)
				}
			})
		}
	})
	t.Run("binary_sylt", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			unit  byte
			stamp uint32
			want  string
		}{
			{"milliseconds", 2, 1234, "[00:01.234]First"},
			{"mpeg_frames", 1, 50, "[00:01.306]First"},
			{"unsupported_unit", 3, 50, ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				b := []byte{3, 'e', 'n', 'g', tc.unit, 1}
				b = append(b, []byte("description\x00First\x00")...)
				b = binary.BigEndian.AppendUint32(b, tc.stamp)
				audio := append(id3Tag(4, 0, id3Frame(4, "SYLT", [2]byte{}, b)), testMP3Frames(bytes.Repeat([]byte{1}, 2048))...)
				got := parseLyricsFixture(t, "timed.mp3", audio)
				var synced string
				if got.ScanTags != nil {
					synced = got.ScanTags.SyncedLyrics
				}
				if got.Lyrics != "" || synced != tc.want {
					t.Fatalf("SYLT 单位 %d 的显示/同步 = %q/%q，期望空/%q", tc.unit, got.Lyrics, synced, tc.want)
				}
			})
		}
	})
	t.Run("media_and_genre", func(t *testing.T) {
		for _, tc := range []struct {
			raw                string
			genre              []string
			podcast, audiobook bool
		}{
			{"Podcast\x00(17)", []string{"Rock"}, true, false},
			{"Literature\x00Rock", []string{"Rock"}, false, true},
			{"Rock\x00Podcast\x00Literature", []string{"Rock", "Podcast", "Literature"}, false, false},
			{"podcast\x00literature", []string{"podcast", "literature"}, false, false},
			{"(17)\x0099\x00(80)", []string{"Rock", "99", "(80)"}, false, false},
			{"Rock", []string{"Rock"}, false, false},
		} {
			t.Run(tc.raw, func(t *testing.T) {
				got := parseLyricsFixture(t, "genre.mp3", id3Tag(4, 0, identifierID3Text(4, "TCON", tc.raw)))
				requireScanReadMedia(t, got, tc.genre, tc.podcast, tc.audiobook)
			})
		}
		t.Run("presence_markers", func(t *testing.T) {
			for _, obs := range []AudioObservation{
				scanReadVorbis(t, "PCST=0", "PCSp=", "GENRE=Rock"),
				scanReadVorbis(t, "ISPODCAST=0", "ISAUDIOBOOK=0", "GENRE=Rock"),
				parseLyricsFixture(t, "genre.mp3", identifierID3(4, []identifierTag{{"ISPODCAST", "0"}, {"ISAUDIOBOOK", "0"}}, identifierID3Text(4, "TCON", "Rock"))),
				parseLyricsFixture(t, "genre.m4a", lyricsM4A("alac", bytes.Join([][]byte{
					repairM4AAtom("pcst", repairM4AData(21, []byte{0})),
					repairM4AFreeform("ISAUDIOBOOK", "0"),
					repairM4AAtom("\xa9gen", repairM4AData(1, []byte("Rock"))),
				}, nil))),
			} {
				requireScanReadMedia(t, obs, []string{"Rock"}, true, true)
			}
		})
	})
}

func requireScanReadMedia(t *testing.T, got AudioObservation, genre []string, podcast, audiobook bool) {
	t.Helper()
	var p, a bool
	if got.ScanTags != nil {
		p, a = got.ScanTags.Podcast, got.ScanTags.Audiobook
	}
	if !slices.Equal(got.Genres, genre) || p != podcast || a != audiobook {
		t.Errorf("流派/播客/有声书 = %v/%t/%t，期望 %v/%t/%t", got.Genres, p, a, genre, podcast, audiobook)
	}
}
