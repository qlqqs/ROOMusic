package playback

import (
	"context"
	"os"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// stubDerivation 让格式表测试启用派生，不需要真正的转换器。
type stubDerivation struct{}

func (stubDerivation) Supports(string) bool { return true }

func (stubDerivation) Key(derivation.SourceVersion) string { return "" }

func (stubDerivation) Await(context.Context, derivation.Request) (derivation.Entry, error) {
	return derivation.Entry{}, nil
}

func (stubDerivation) OpenEntry(string) (*os.File, int64, error) {
	return nil, 0, derivation.ErrNotCached
}

func serviceWithoutDerivation() *Service { return &Service{} }

func serviceWithDerivation() *Service { return &Service{derivation: stubDerivation{}} }

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
	// 派生可用时才追加格式：媒体类型描述转换后的 FLAC，而不是源文件。
	derived := append(append([]combination(nil), direct...),
		combination{"m4a", "alac", DeliveryDerived, "audio/flac", ""},
		combination{"ape_apev2", "ape", DeliveryDerived, "audio/flac", ""},
		combination{"wavpack_apev2", "wavpack", DeliveryDerived, "audio/flac", ""},
		combination{"wavpack_apev2", "wavpack_hybrid", DeliveryDerived, "audio/flac", ""},
		combination{"aiff_id3", "aiff", DeliveryDerived, "audio/flac", ""},
	)
	for name, testCase := range map[string]struct {
		service *Service
		want    []combination
	}{
		"without derivation": {serviceWithoutDerivation(), direct},
		"with derivation":    {serviceWithDerivation(), derived},
	} {
		t.Run(name, func(t *testing.T) {
			got := testCase.service.Formats()
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
		direct, derived   bool
	}{
		{"flac_vorbis_comment", "flac", true, true},
		// ALAC 浏览器大多不能解码：只有派生可用时才在表中。
		{"m4a", "alac", false, true},
		{"m4a", "aac", true, true},
		{"ape_apev2", "ape", false, true},
		{"wavpack_apev2", "wavpack", false, true},
		{"wavpack_apev2", "wavpack_hybrid", false, true},
		{"wavpack_apev2", "wavpack_float", false, false},
		{"wavpack_apev2", "wavpack_dsd", false, false},
		{"aiff_id3", "aiff", false, true},
		{"aiff_id3", "aiff_float", false, false},
		{"aiff_id3", "aifc_ulaw", false, false},
		{"wav_info", "pcm", true, true},
		{"cue_virtual", "flac", false, false},
		{"cue_virtual", "", false, false},
		{"wav_info", "ieee_float", false, false},
		{"wav_info", "wav_extensible", false, false},
		{"wav_info", "wav", false, false},
		{"wav_info", "wav_85", false, false},
		{"flac_vorbis_comment", "", false, false},
		{"flac_vorbis_comment", "alac", false, false},
		{"mp3_id3v2", "aac", false, false},
		{"physical", "flac", false, false},
		{"", "", false, false},
	} {
		for name, expectation := range map[string]struct {
			service *Service
			ok      bool
		}{
			"without derivation": {serviceWithoutDerivation(), testCase.direct},
			"with derivation":    {serviceWithDerivation(), testCase.derived},
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
	service := serviceWithDerivation()
	first := service.Formats()
	first[0].MIMEType = "text/html"
	first = append(first[:1], first[2:]...)
	second := service.Formats()
	if len(second) != len(formatTable)+len(derivedFormatTable) || second[0].MIMEType != "audio/flac" {
		t.Fatalf("mutating the returned slice changed the table: %+v", second[0])
	}
}

type decoderLimitedDerivation struct {
	stubDerivation
	decoders map[string]bool
}

func (d decoderLimitedDerivation) Supports(decoder string) bool { return d.decoders[decoder] }

func TestFormatsFilterDerivedFormatsByDecoder(t *testing.T) {
	for _, tc := range []struct {
		decoder string
		want    []string
	}{
		{"", nil},
		{"alac", []string{"alac"}},
		{"ape", []string{"ape"}},
		{"wavpack", []string{"wavpack", "wavpack_hybrid"}},
		{"pcm_s16be", []string{"aiff"}},
	} {
		t.Run(tc.decoder, func(t *testing.T) {
			service := &Service{derivation: decoderLimitedDerivation{decoders: map[string]bool{tc.decoder: true}}}
			formats := service.Formats()
			if len(formats) != 6+len(tc.want) {
				t.Fatalf("Formats = %+v, want six direct and %v derived", formats, tc.want)
			}
			for i, format := range formats[:6] {
				if format != formatTable[i] {
					t.Errorf("decoder selection changed direct format %d: %+v", i, format)
				}
			}
			for i, codec := range tc.want {
				if format := formats[6+i]; format.Codec != codec || format.Delivery != DeliveryDerived || format.MIMEType != "audio/flac" || format.Codecs != "" {
					t.Errorf("derived format %d = %+v, want %s as FLAC", i, format, codec)
				}
			}
			for _, format := range derivedFormatTable {
				_, ok := service.findFormat(format.SourceKind, format.Codec)
				if ok != (format.decoder == tc.decoder) {
					t.Errorf("findFormat(%s/%s) = %v", format.SourceKind, format.Codec, ok)
				}
			}
			for _, unsupported := range []struct{ kind, codec string }{
				{"wavpack_apev2", "wavpack_float"}, {"wavpack_apev2", "wavpack_dsd"},
				{"aiff_id3", "aiff_float"}, {"aiff_id3", "aifc_ulaw"},
			} {
				if _, ok := service.findFormat(unsupported.kind, unsupported.codec); ok {
					t.Errorf("unsupported source %s/%s entered the format table", unsupported.kind, unsupported.codec)
				}
			}
		})
	}
}
