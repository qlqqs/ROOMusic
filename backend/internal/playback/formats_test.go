package playback

import "testing"

func TestFormatTableContainsOnlyTheApprovedCombinations(t *testing.T) {
	type combination struct{ sourceKind, codec, mimeType, codecs string }
	want := []combination{
		{"flac_vorbis_comment", "flac", "audio/flac", ""},
		{"mp3_id3v2", "mp3", "audio/mpeg", ""},
		{"wav_info", "pcm", "audio/wav", "1"},
		{"m4a", "aac", "audio/mp4", "mp4a.40.2"},
		{"ogg_vorbis_comment", "vorbis", "audio/ogg", "vorbis"},
		{"opus_tags", "opus", "audio/ogg", "opus"},
	}
	got := Formats()
	if len(got) != len(want) {
		t.Fatalf("format table has %d rows, want %d", len(got), len(want))
	}
	for index, format := range got {
		expected := want[index]
		if format.SourceKind != expected.sourceKind || format.Codec != expected.codec || format.MIMEType != expected.mimeType || format.Codecs != expected.codecs {
			t.Errorf("row %d = %+v, want %+v", index, format, expected)
		}
		if format.container == 0 {
			t.Errorf("row %d (%s/%s) has no container to verify", index, format.SourceKind, format.Codec)
		}
	}
}

func TestFindFormatRejectsEverythingOutsideTheTable(t *testing.T) {
	for _, testCase := range []struct {
		sourceKind, codec string
		ok                bool
	}{
		{"flac_vorbis_comment", "flac", true},
		// 没有 Safari 实播证据之前 ALAC 不直出。
		{"m4a", "alac", false},
		{"m4a", "aac", true},
		{"wav_info", "pcm", true},
		{"cue_virtual", "flac", false},
		{"cue_virtual", "", false},
		{"wav_info", "ieee_float", false},
		{"wav_info", "wav_extensible", false},
		{"wav_info", "wav", false},
		{"wav_info", "wav_85", false},
		{"flac_vorbis_comment", "", false},
		{"flac_vorbis_comment", "alac", false},
		{"mp3_id3v2", "aac", false},
		{"physical", "flac", false},
		{"", "", false},
	} {
		format, ok := FindFormat(testCase.sourceKind, testCase.codec)
		if ok != testCase.ok {
			t.Errorf("FindFormat(%q, %q) ok = %v, want %v", testCase.sourceKind, testCase.codec, ok, testCase.ok)
		}
		if ok && (format.SourceKind != testCase.sourceKind || format.Codec != testCase.codec) {
			t.Errorf("FindFormat(%q, %q) returned %+v", testCase.sourceKind, testCase.codec, format)
		}
	}
}

func TestFormatsReturnsACopy(t *testing.T) {
	first := Formats()
	first[0].MIMEType = "text/html"
	first = append(first[:1], first[2:]...)
	if second := Formats(); len(second) != len(formatTable) || second[0].MIMEType != "audio/flac" {
		t.Fatalf("mutating the returned slice changed the table: %+v", second[0])
	}
}
