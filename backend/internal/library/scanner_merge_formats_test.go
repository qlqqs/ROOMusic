package library

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestScannerMergeFormatsFilenameInference(t *testing.T) {
	for _, format := range mergeLosslessFixtures() {
		t.Run(format.extension, func(t *testing.T) {
			root := t.TempDir()
			var observations []ingest.SourceObservation
			for number := 1; number <= 2; number++ {
				relative := fmt.Sprintf("Album/1-%02d 合成艺人 - 合成曲目%d%s", number, number, format.extension)
				observation := parseMergeLosslessObservation(t, root, relative, format.data)
				if observation.Codec != format.codec || observation.SourceKind != format.sourceKind {
					t.Fatalf("解析来源或编码丢失：%+v", observation)
				}
				if observation.FieldSources["title"] != "filename_fallback" || observation.FieldSources["artist"] != "display_fallback" {
					t.Fatalf("扫描转换未保留无标签来源：%+v", observation.FieldSources)
				}
				observations = append(observations, observation)
			}
			candidates := ingest.OrganizeObservations(observations)
			if len(candidates) != 1 || len(candidates[0].Mediums) != 1 || len(candidates[0].Mediums[1]) != 2 {
				t.Fatalf("同格式应保持单发行单碟：%+v", candidates)
			}
			for _, track := range candidates[0].Mediums[1] {
				observation := track.Observation
				if track.Position < 1 || track.Position > 2 || track.Disc != 1 || observation.Title != fmt.Sprintf("合成曲目%d", track.Position) || observation.Artist != "合成艺人" {
					t.Fatalf("新格式文件名推断不正确：%+v", track)
				}
				for _, field := range []string{"title", "artist", "track_number", "disc_number"} {
					if !observation.InferredFields[field] || observation.FieldSources[field] != "filename_fallback" {
						t.Fatalf("%s 未保留文件名推断证据：%+v", field, observation)
					}
				}
			}
		})
	}
}

func TestScannerMergeFormatsSplitByCodec(t *testing.T) {
	root := t.TempDir()
	var observations []ingest.SourceObservation
	for _, format := range mergeLosslessFixtures() {
		for number := 1; number <= 2; number++ {
			// AIFF 的两种扩展名各放一首，必须按相同编码合为一个版本。
			if format.extension == ".aif" && number != 1 || format.extension == ".aiff" && number != 2 {
				continue
			}
			relative := fmt.Sprintf("Album/%02d 合成艺人 - 合成曲目%d%s", number, number, format.extension)
			observations = append(observations, parseMergeLosslessObservation(t, root, relative, format.data))
		}
	}
	candidates := ingest.OrganizeObservations(observations)
	if len(candidates) != 3 {
		t.Fatalf("APE、WavPack、AIFF 应各成一个版本：%+v", candidates)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Anchor.Kind != ingest.CandidateFormatSplit || candidate.Anchor.Scope != "Album" || candidate.VersionGroup != "format:Album" {
			t.Fatalf("新格式未共享同一版本组：%+v", candidate)
		}
		tracks := candidate.Mediums[1]
		if len(candidate.Mediums) != 1 || len(tracks) != 2 {
			t.Fatalf("格式版本轨目不完整：%+v", candidate)
		}
		codec := tracks[0].Observation.Codec
		if seen[codec] || candidate.Anchor.Partition != "format:"+codec {
			t.Fatalf("格式锚点错误或重复：%+v", candidate.Anchor)
		}
		seen[codec] = true
		positions := map[int]bool{}
		for _, track := range tracks {
			if track.Observation.Codec != codec || track.Position < 1 || track.Position > 2 || positions[track.Position] || track.Observation.FieldSources["track_number"] != "filename_fallback" {
				t.Fatalf("跨格式重复轨号不应触发自然序降级：%+v", track)
			}
			positions[track.Position] = true
		}
	}
	for _, codec := range []string{"ape", "wavpack", "aiff"} {
		if !seen[codec] {
			t.Errorf("缺少 %s 版本", codec)
		}
	}
}

func mergeLosslessFixtures() []struct {
	extension, codec, sourceKind string
	data                         []byte
} {
	audio := []byte{1, 2, 3, 4}
	aiff := parsertest.AIFF("", 44100, 16, 2, 1, audio)
	return []struct {
		extension, codec, sourceKind string
		data                         []byte
	}{
		{".ape", "ape", "ape_apev2", parsertest.APE([16]byte{}, audio)},
		{".wv", "wavpack", "wavpack_apev2", parsertest.WavPackBlock(1|9<<23|0x1800, 4410, 4410, parsertest.WavPackMetadata(0x0a, audio))},
		{".aif", "aiff", "aiff_id3", aiff},
		{".aiff", "aiff", "aiff_id3", aiff},
	}
}

func parseMergeLosslessObservation(t *testing.T, root, relative string, data []byte) ingest.SourceObservation {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseAudioFile(path)
	if err != nil {
		t.Fatalf("解析合成音频失败：%v", err)
	}
	return buildPhysicalSourceObservation(parsed, relative)
}
