package parser

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestAudioParsersReturnMetadataAndRejectInvalidInput(t *testing.T) {
	temporaryDirectory := t.TempDir()
	flacPath := filepath.Join(temporaryDirectory, "fallback.flac")
	mp3Path := filepath.Join(temporaryDirectory, "fallback.mp3")
	if err := os.WriteFile(flacPath, parsertest.FLACWithComments(t, []string{"TITLE=FLAC 标题", "ARTIST=FLAC 艺术家", "ALBUM=FLAC 专辑", "TRACKNUMBER=2/9", "DISCNUMBER=1/2"}), 0o644); err != nil {
		t.Fatalf("write FLAC fixture: %v", err)
	}
	if err := os.WriteFile(mp3Path, createMP3Fixture(t, map[string]string{"TIT2": "MP3 标题", "TPE1": "MP3 艺术家", "TALB": "MP3 专辑", "TRCK": "3/10", "TPOS": "2/2"}), 0o644); err != nil {
		t.Fatalf("write MP3 fixture: %v", err)
	}

	flacObservation, err := ParseAudioFile(flacPath)
	if err != nil {
		t.Fatalf("parse valid FLAC fixture: %v", err)
	}
	if flacObservation.Title != "FLAC 标题" || flacObservation.Artist != "FLAC 艺术家" || flacObservation.Album != "FLAC 专辑" || flacObservation.TrackNumber != 2 || flacObservation.DiscNumber != 1 {
		t.Fatalf("unexpected FLAC observation: %+v", flacObservation)
	}

	mp3Observation, err := ParseAudioFile(mp3Path)
	if err != nil {
		t.Fatalf("parse valid MP3 fixture: %v", err)
	}
	if mp3Observation.Title != "MP3 标题" || mp3Observation.Artist != "MP3 艺术家" || mp3Observation.Album != "MP3 专辑" || mp3Observation.TrackNumber != 3 || mp3Observation.DiscNumber != 2 {
		t.Fatalf("unexpected MP3 observation: %+v", mp3Observation)
	}

	for _, invalidFixture := range []struct {
		name    string
		path    string
		content []byte
	}{
		{name: "damaged FLAC", path: filepath.Join(temporaryDirectory, "damaged.flac"), content: []byte("not-flac")},
		{name: "damaged MP3", path: filepath.Join(temporaryDirectory, "damaged.mp3"), content: []byte("not-an-mp3")},
		{name: "unsupported extension", path: filepath.Join(temporaryDirectory, "track.ogg"), content: []byte("OggS")},
	} {
		t.Run(invalidFixture.name, func(t *testing.T) {
			if err := os.WriteFile(invalidFixture.path, invalidFixture.content, 0o644); err != nil {
				t.Fatalf("write invalid fixture: %v", err)
			}
			if _, err := ParseAudioFile(invalidFixture.path); err == nil {
				t.Fatal("invalid or unsupported audio input was accepted")
			}
		})
	}
}

func TestFLACParserReadsAllCommentsAfterLongValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi-comment.flac")
	comments := []string{"TITLE=First", "ARTIST=Second", "ALBUM=Third"}
	if err := os.WriteFile(path, parsertest.FLACWithComments(t, comments), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	observation, err := ParseAudioFile(path)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if observation.Title != "First" || observation.Artist != "Second" || observation.Album != "Third" {
		t.Fatalf("comments were not all parsed: %+v", observation)
	}
}

func createMP3Fixture(t *testing.T, frames map[string]string) []byte {
	t.Helper()
	tagData := &bytes.Buffer{}
	for _, frameID := range []string{"TIT2", "TPE1", "TALB", "TRCK", "TPOS"} {
		value := frames[frameID]
		// 测试值包含中文，必须按 ID3v2 的 UTF-8 encoding=3 声明；
		// encoding=0 是 ISO-8859-1，不能承载这些字节。
		frameContent := append([]byte{3}, []byte(value)...)
		if _, err := tagData.WriteString(frameID); err != nil {
			t.Fatalf("write MP3 frame identifier: %v", err)
		}
		if err := binary.Write(tagData, binary.BigEndian, uint32(len(frameContent))); err != nil {
			t.Fatalf("write MP3 frame length: %v", err)
		}
		if _, err := tagData.Write([]byte{0, 0}); err != nil {
			t.Fatalf("write MP3 frame flags: %v", err)
		}
		if _, err := tagData.Write(frameContent); err != nil {
			t.Fatalf("write MP3 frame content: %v", err)
		}
	}
	tagLength := tagData.Len()
	header := []byte{'I', 'D', '3', 4, 0, 0, byte(tagLength >> 21), byte(tagLength >> 14), byte(tagLength >> 7), byte(tagLength)}
	return append(header, tagData.Bytes()...)
}
