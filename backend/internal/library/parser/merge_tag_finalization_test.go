package parser

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestLosslessParsersFinalizeMergedTags(t *testing.T) {
	const work = "Cello Suite No. 1, BWV 1007"
	const movement = "II. Allemande"
	const lyrics = "作词：Lyric Writer\n作曲：Ignored Composer\n编曲：Arranger\n正文"
	apeTags := parsertest.APEv2(2000, false,
		parsertest.APEv2Item{Key: "Artist", Value: []byte("A\x00B")},
		parsertest.APEv2Item{Key: "Album Artist", Value: []byte("Fallback")},
		parsertest.APEv2Item{Key: "AlbumArtist", Value: []byte("Ensemble A\x00Ensemble B")},
		parsertest.APEv2Item{Key: "Compilation", Value: []byte("1")},
		parsertest.APEv2Item{Key: "Composer", Value: []byte("Tagged Composer")},
		parsertest.APEv2Item{Key: "Performer", Value: []byte("Soloist (violin)")},
		parsertest.APEv2Item{Key: "Mixer", Value: []byte("Mixer")},
		parsertest.APEv2Item{Key: "Lyrics", Value: []byte(lyrics)},
		parsertest.APEv2Item{Key: "Work", Value: []byte(work)},
		parsertest.APEv2Item{Key: "MovementName", Value: []byte(movement)},
		parsertest.APEv2Item{Key: "MovementNumber", Value: []byte("2/6")},
	)
	id3 := workMP3Fixture([][2]string{
		{"TPE1", "A\x00B"},
		{"TXXX", "ALBUM ARTIST\x00Fallback"},
		{"TPE2", "Ensemble A\x00Ensemble B"},
		{"TCMP", "1"},
		{"TCOM", "Tagged Composer"},
		{"TXXX", "PERFORMER\x00Soloist (violin)"},
		{"TXXX", "MIXER\x00Mixer"},
		{"TIT1", work},
		{"MVNM", movement},
		{"MVIN", "2/6"},
	})
	// USLT 帧包含编码、语言与空描述符，复用既有合成容器而不绕过格式入口。
	frames := append([]byte(nil), id3[10:]...)
	frames = append(frames, id3Frame(3, "USLT", [2]byte{}, append([]byte{3, 'e', 'n', 'g', 0}, []byte(lyrics)...))...)
	id3 = parsertest.ID3v23(frames)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"track.ape", append(parsertest.APE([16]byte{}, []byte{1, 2}), apeTags...)},
		{"track.wv", append(parsertest.WavPackBlock(wavpackStereo44100, 4410, 4410, parsertest.WavPackMetadata(0x0a, []byte{1, 2})), apeTags...)},
		{"track.aiff", parsertest.AIFF("", 44100, 16, 2, 4410, []byte{1, 2}, parsertest.AIFFChunk("ID3 ", id3))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := parseLyricsFixture(t, tc.name, tc.data)
			if observation.Artist != "A / B" || observation.AlbumArtist != "Ensemble A / Ensemble B" || !observation.Compilation {
				t.Fatalf("多值艺人、专辑艺人优先级或合辑标记丢失：%+v", observation)
			}
			var artists []string
			for _, evidence := range observation.ArtistTagEvidence {
				if evidence.Role == "track_artist" {
					artists = append(artists, evidence.RawValue)
				}
				if evidence.RawValue == "Lyric Writer" || evidence.RawValue == "Arranger" {
					t.Fatalf("歌词署名不应写入标签原文证据：%+v", evidence)
				}
			}
			if !reflect.DeepEqual(artists, []string{"A", "B"}) {
				t.Fatalf("多值艺人原文证据丢失：%q", artists)
			}
			if observation.Lyrics != lyrics {
				t.Fatalf("歌词原文 = %q，期望 %q", observation.Lyrics, lyrics)
			}
			// 标签作曲压过歌词作曲；其余歌词角色与标签细分均须保留。
			requireCredits(t, observation.Credits, []metadata.Credit{
				{Role: "lyricist", Name: "Lyric Writer", Source: "lyrics"},
				{Role: "composer", Name: "Tagged Composer", Source: "tag"},
				{Role: "arranger", Name: "Arranger", Source: "lyrics"},
				{Role: "performer", Name: "Soloist", Detail: "violin", Source: "tag"},
				{Role: "producer", Name: "Mixer", Detail: "mix", Source: "tag"},
			})
			if observation.Work != work || observation.MovementName != movement || observation.MovementNumber != 2 {
				t.Fatalf("作品/乐章 = %q/%q/%d，期望 %q/%q/2", observation.Work, observation.MovementName, observation.MovementNumber, work, movement)
			}
		})
	}
}
