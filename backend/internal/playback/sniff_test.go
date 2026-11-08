package playback

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// atom 返回一个顶层 MP4 atom：8 字节头（大小与类型）加 payload 个零字节。
func atom(kind string, payload int) []byte {
	buffer := make([]byte, 8+payload)
	binary.BigEndian.PutUint32(buffer[0:4], uint32(len(buffer)))
	copy(buffer[4:8], kind)
	return buffer
}

func largeAtom(kind string, payload int) []byte {
	buffer := make([]byte, 16+payload)
	binary.BigEndian.PutUint32(buffer[0:4], 1)
	copy(buffer[4:8], kind)
	binary.BigEndian.PutUint64(buffer[8:16], uint64(len(buffer)))
	return buffer
}

func TestMatchesContainerAcceptsWhatTheScannerAccepts(t *testing.T) {
	riff := append([]byte("RIFF\x00\x00\x00\x00WAVE"), make([]byte, 20)...)
	for _, testCase := range []struct {
		name    string
		kind    container
		content []byte
		want    bool
	}{
		{"flac magic", containerFLAC, append([]byte("fLaC"), make([]byte, 40)...), true},
		{"flac magic only", containerFLAC, []byte("fLaC"), true},
		{"flac behind an id3 tag is not a stream the scanner indexes", containerFLAC, append([]byte("ID3\x04"), make([]byte, 40)...), false},
		{"not flac", containerFLAC, append([]byte("OggS"), make([]byte, 40)...), false},
		{"empty flac", containerFLAC, nil, false},
		{"ogg magic", containerOgg, append([]byte("OggS"), make([]byte, 40)...), true},
		{"not ogg", containerOgg, append([]byte("fLaC"), make([]byte, 40)...), false},
		{"mp3 with id3", containerMP3, append([]byte("ID3\x04\x00"), make([]byte, 40)...), true},
		{"mp3 frame sync", containerMP3, append([]byte{0xff, 0xfb, 0x90, 0x64}, make([]byte, 40)...), true},
		{"mp3 sync needs the three high bits", containerMP3, append([]byte{0xff, 0xc0, 0x90, 0x64}, make([]byte, 40)...), false},
		{"mp3 one byte", containerMP3, []byte{0xff}, false},
		{"not mp3", containerMP3, append([]byte("fLaC"), make([]byte, 40)...), false},
		{"wav", containerWAV, riff, true},
		{"wav truncated header", containerWAV, []byte("RIFF\x00\x00\x00\x00WAV"), false},
		{"riff but not wave", containerWAV, append([]byte("RIFF\x00\x00\x00\x00AVI "), make([]byte, 20)...), false},
		{"mp4 ftyp first", containerMP4, append(atom("ftyp", 12), atom("moov", 100)...), true},
		{"mp4 free atom before ftyp", containerMP4, append(append(atom("free", 8), atom("ftyp", 12)...), atom("moov", 100)...), true},
		{"mp4 64-bit size before ftyp", containerMP4, append(append(largeAtom("wide", 8), atom("ftyp", 12)...), atom("moov", 100)...), true},
		{"mp4 without ftyp", containerMP4, append(atom("moov", 100), atom("mdat", 100)...), false},
		{"mp4 atom that extends to the end of the file", containerMP4, append([]byte{0, 0, 0, 0}, []byte("mdat0000")...), false},
		{"mp4 atom smaller than its header", containerMP4, append([]byte{0, 0, 0, 4}, []byte("free0000")...), false},
		{"mp4 64-bit size beyond the file", containerMP4, func() []byte {
			broken := largeAtom("free", 4)
			binary.BigEndian.PutUint64(broken[8:16], 1<<40)
			return broken
		}(), false},
		{"mp4 too short", containerMP4, []byte("ftyp"), false},
		{"mp4 gives up after a bounded number of atoms", containerMP4, func() []byte {
			var content []byte
			for index := 0; index < maxLeadingMP4Atoms; index++ {
				content = append(content, atom("free", 0)...)
			}
			return append(content, atom("ftyp", 12)...)
		}(), false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := matchesContainer(bytes.NewReader(testCase.content), int64(len(testCase.content)), testCase.kind)
			if err != nil {
				t.Fatalf("matchesContainer returned %v", err)
			}
			if got != testCase.want {
				t.Fatalf("matchesContainer = %v, want %v", got, testCase.want)
			}
		})
	}
}

type failingReaderAt struct{}

func (failingReaderAt) ReadAt([]byte, int64) (int, error) { return 0, errors.New("input/output error") }

func TestContainerReportsReadFailuresInsteadOfAMismatch(t *testing.T) {
	for _, kind := range []container{containerFLAC, containerMP4, containerMP3, containerWAV, containerOgg, containerAPE, containerWavPack, containerAIFF} {
		if _, err := matchesContainer(failingReaderAt{}, 100, kind); err == nil {
			t.Errorf("container %d: a failing read was reported as a plain mismatch", kind)
		}
	}
}

func TestContainerRecognizesLosslessInputs(t *testing.T) {
	id3 := func(body []byte, footer bool, magic string) []byte {
		header := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, byte(len(body))}
		if footer {
			header[5] = 0x10
		}
		content := append(header, body...)
		if footer {
			content = append(content, make([]byte, 10)...)
		}
		return append(content, magic...)
	}
	for _, tc := range []struct {
		name    string
		kind    container
		content []byte
		want    bool
	}{
		{"APE", containerAPE, []byte("MAC "), true},
		{"APE after ID3v2", containerAPE, id3([]byte("tag"), false, "MAC "), true},
		{"APE after ID3v2 footer", containerAPE, id3([]byte("tag"), true, "MAC "), true},
		{"APE truncated ID3 header", containerAPE, []byte("ID3\x04"), false},
		{"APE truncated ID3 body", containerAPE, []byte("ID3\x04\x00\x00\x00\x00\x00\x20MAC "), false},
		{"APE invalid syncsafe length", containerAPE, []byte("ID3\x04\x00\x00\x80\x00\x00\x00MAC "), false},
		{"APE oversized prefix", containerAPE, []byte("ID3\x04\x00\x00\x08\x00\x00\x00MAC "), false},
		{"APE wrong content after ID3", containerAPE, id3(nil, false, "fLaC"), false},
		{"APE wrong magic", containerAPE, []byte("fLaC"), false},
		{"APE truncated magic", containerAPE, []byte("MAC"), false},
		{"WavPack", containerWavPack, []byte("wvpk"), true},
		{"WavPack truncated magic", containerWavPack, []byte("wvp"), false},
		{"WavPack wrong magic", containerWavPack, []byte("MAC "), false},
		{"WavPack prefixed magic", containerWavPack, append([]byte("ID3"), []byte("wvpk")...), false},
		{"AIFF", containerAIFF, []byte("FORM\x00\x00\x00\x04AIFF"), true},
		{"AIFF-C", containerAIFF, []byte("FORM\x00\x00\x00\x04AIFC"), true},
		{"AIFF wrong FORM type", containerAIFF, []byte("FORM\x00\x00\x00\x04WAVE"), false},
		{"AIFF missing FORM", containerAIFF, []byte("RIFF\x00\x00\x00\x04AIFF"), false},
		{"AIFF truncated FORM", containerAIFF, []byte("FORM\x00\x00\x00\x04AIF"), false},
		{"empty APE", containerAPE, nil, false},
		{"empty WavPack", containerWavPack, nil, false},
		{"empty AIFF", containerAIFF, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchesContainer(bytes.NewReader(tc.content), int64(len(tc.content)), tc.kind)
			if err != nil || got != tc.want {
				t.Fatalf("matchesContainer = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
