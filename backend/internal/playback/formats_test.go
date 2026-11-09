package playback

import "testing"

func TestFormatTableContainsOnlyTheApprovedCombinations(t *testing.T) {
	type combination struct {
		sourceKind, codec string
		delivery          Delivery
		mimeType, codecs  string
	}
	direct := []combination{
		{"flac_vorbis_comment", "flac", DeliveryOriginal, "audio/flac", ""},
		{"mp3_id3v2", "mp3", DeliveryOriginal, "audio/mpeg", ""},
		{"wav_info", "pcm", DeliveryOriginal, "audio/wav", "1"},
		{"m4a", "aac", DeliveryOriginal, "audio/mp4", "mp4a.40.2"},
		{"ogg_vorbis_comment", "vorbis", DeliveryOriginal, "audio/ogg", "vorbis"},
		{"opus_tags", "opus", DeliveryOriginal, "audio/ogg", "opus"},
	}
	for name, testCase := range map[string]struct {
		got  []Format
		want []combination
	}{
		"direct": {formatTable, direct},
		"DSD无需ffmpeg解码器": {
			(&Service{renderDecoders: noRenderDecoders{}}).RenderFormats(),
			[]combination{
				{"dsf_id3", "dsf", DeliveryDerived, "audio/flac", ""},
				{"dff_diin", "dff", DeliveryDerived, "audio/flac", ""},
				{"cue_virtual", "dsf", DeliveryDerived, "audio/flac", ""},
				{"cue_virtual", "dff", DeliveryDerived, "audio/flac", ""},
			},
		},
		"未装配渲染器也不发布DSD": {(&Service{}).RenderFormats(), nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := testCase.got
			if len(got) != len(testCase.want) {
				t.Fatalf("format table has %d rows, want %d", len(got), len(testCase.want))
			}
			for index, format := range got {
				expected := testCase.want[index]
				if format.SourceKind != expected.sourceKind || format.Codec != expected.codec || format.Delivery != expected.delivery ||
					format.MIMEType != expected.mimeType || format.Codecs != expected.codecs {
					t.Errorf("row %d = %+v, want %+v", index, format, expected)
				}
				if format.container == 0 {
					t.Errorf("row %d (%s/%s) has no container to verify", index, format.SourceKind, format.Codec)
				}
			}
		})
	}
}

func TestFindFormatRejectsEverythingOutsideTheTable(t *testing.T) {
	for _, testCase := range []struct {
		sourceKind, codec string
		direct            bool
	}{
		{"flac_vorbis_comment", "flac", true},
		// 旧接口不再接受 ALAC 等原派生格式。
		{"m4a", "alac", false},
		{"m4a", "aac", true},
		{"ape_apev2", "ape", false},
		{"wavpack_apev2", "wavpack", false},
		{"wavpack_apev2", "wavpack_hybrid", false},
		{"wavpack_apev2", "wavpack_float", false},
		{"wavpack_apev2", "wavpack_dsd", false},
		{"aiff_id3", "aiff", false},
		{"aiff_id3", "aiff_float", false},
		{"aiff_id3", "aifc_ulaw", false},
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
		for name, expectation := range map[string]struct {
			service *Service
			ok      bool
		}{
			"direct": {&Service{}, testCase.direct},
		} {
			format, ok := expectation.service.findFormat(testCase.sourceKind, testCase.codec)
			if ok != expectation.ok {
				t.Errorf("%s: findFormat(%q, %q) ok = %v, want %v", name, testCase.sourceKind, testCase.codec, ok, expectation.ok)
			}
			if ok && (format.SourceKind != testCase.sourceKind || format.Codec != testCase.codec) {
				t.Errorf("%s: findFormat(%q, %q) returned %+v", name, testCase.sourceKind, testCase.codec, format)
			}
		}
	}
}

func TestFormatsReturnsACopy(t *testing.T) {
	service := &Service{renderDecoders: allRenderDecoders{}}
	first := service.Formats()
	first[0].MIMEType = "text/html"
	first = append(first[:1], first[2:]...)
	second := service.Formats()
	if len(second) != len(formatTable)+len(renderFormatTable) || second[0].MIMEType != "audio/flac" {
		t.Fatalf("mutating the returned slice changed the table: %+v", second[0])
	}
}

// 格式表副本测试只需要解码器能力，不执行转换。
type allRenderDecoders struct{}

func (allRenderDecoders) HasDecoder(string) bool { return true }

// 已装配渲染器，但没有任何 ffmpeg 来源解码器；DSD 由内置转换负责。
type noRenderDecoders struct{}

func (noRenderDecoders) HasDecoder(string) bool { return false }
