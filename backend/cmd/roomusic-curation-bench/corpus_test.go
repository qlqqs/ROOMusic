package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func corpusFixture() sources.Release {
	ms := int64(1000)
	credits := []sources.Credit{{Name: "东灯", JoinPhrase: " & "}, {Name: "西桨"}}
	r := sources.Release{ID: "虚构发行", Title: "灯塔", Artists: credits}
	for disc := 1; disc <= 2; disc++ {
		m := sources.Medium{Position: disc, TrackCount: 3}
		for i := 1; i <= 3; i++ {
			side := rune('A' + (disc-1)*2)
			number := fmt.Sprintf("%c%d", side, i)
			if i == 3 {
				number = fmt.Sprintf("%c1", side+1)
			}
			m.Tracks = append(m.Tracks, sources.Track{Title: "潮声", Number: number, Position: i, LengthMS: &ms, RecordingID: fmt.Sprintf("录音%d-%d", disc, i)})
		}
		r.Media = append(r.Media, m)
	}
	return r
}

func TestCorpusTracks(t *testing.T) {
	r := corpusFixture()
	for _, mode := range []string{"multidisc", "double_lp", "compilation", "collaboration", "romanized", "subset", "superset", "gbk", "untagged", "picard"} {
		t.Run(mode, func(t *testing.T) {
			entry := corpusEntry{ID: r.ID, Tags: mode, LocalArtist: "Dong Deng", LocalTitle: "Deng Ta"}
			tracks, err := planTracks(entry, r)
			if err != nil {
				t.Fatal(err)
			}
			tags := tracks[0].Tags
			var ok bool
			switch mode {
			case "multidisc":
				ok = tags["DISCNUMBER"] == "" && tracks[3].Position == 1 && tracks[3].Tags["TRACKNUMBER"] == "1"
			case "double_lp":
				var numbers []string
				for _, track := range tracks {
					numbers = append(numbers, track.Tags["TRACKNUMBER"])
				}
				ok = tags["DISCNUMBER"] == "" && slices.Equal(numbers, []string{"A1", "A2", "B1", "C1", "C2", "D1"})
			case "compilation":
				ok = tags["ALBUMARTIST"] == "" && tags["ARTIST"] == "东灯 & 西桨"
			case "collaboration":
				ok = tags["ALBUMARTIST"] == "东灯、西桨" && tags["ARTIST"] == "东灯 feat. 西桨"
			case "romanized":
				ok = tags["ALBUMARTIST"] == entry.LocalArtist && tags["ARTIST"] == entry.LocalArtist && tags["ALBUM"] == entry.LocalTitle
			case "subset":
				ok = len(tracks) == 4 && tracks[3].RecordingID == "录音2-1" && len(r.Media[1].Tracks) == 3
			case "superset":
				last := tracks[len(tracks)-1]
				ok = len(tracks) == 7 && last.Synthetic && last.RecordingID == "" && last.Tags["ARTIST"] == "东灯 & 西桨" && last.Disc == 2 && last.Position == 4
			case "gbk":
				ok = tracks[0].File == "01-01 - 潮声.mp3"
			case "untagged":
				ok = len(tags) == 0
			case "picard":
				ok = tags["MUSICBRAINZ_ALBUMID"] == r.ID && tags["MUSICBRAINZ_TRACKID"] == tracks[0].RecordingID
			}
			if !ok {
				t.Fatalf("轨图或标签不符合 %s：%+v", mode, tracks)
			}
		})
	}
}

func TestFFmpegCorpusOffline(t *testing.T) {
	ffmpeg := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("需要固定版本 ffmpeg")
	}
	r := corpusFixture()
	r.Media = r.Media[:1]
	r.Media[0].TrackCount = 1
	r.Media[0].Tracks = r.Media[0].Tracks[:1]
	root := t.TempDir()
	ref := filepath.Join(root, "ref.json")
	if err := writeJSONFile(ref, []sources.Release{r}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "音频")
	entries := []corpusEntry{{ID: "未选择的发行"}, {ID: r.ID, Title: r.Title, Artist: "东灯", Tags: "gbk"}}
	if err := generateCorpus(t.Context(), entries, corpusOptions{out: out, ffmpeg: ffmpeg, referenceIn: ref, ids: r.ID}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out + "-tracks.json")
	if err != nil {
		t.Fatal(err)
	}
	var albums []albumMap
	if err = json.Unmarshal(raw, &albums); err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].ID != r.ID || !strings.HasPrefix(albums[0].Directory, "02 - ") || len(albums[0].Tracks) != 1 {
		t.Fatal(albums)
	}
	path := filepath.Join(out, albums[0].Directory, albums[0].Tracks[0].File)
	o, err := parser.ParseAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o.LegacyText["album"], []byte{0xb5, 0xc6, 0xcb, 0xfe}) || o.Album == r.Title || o.Title != "潮声" || o.DurationSeconds < 0.9 || o.DurationSeconds > 1.1 {
		t.Fatalf("实际编码的 MP3 解析结果：%+v", o)
	}
}
