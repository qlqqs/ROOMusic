package library

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

type artistEvidenceStageExecutor struct {
	failingScanExecutor
	payload []byte
}

func (e *artistEvidenceStageExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.HasPrefix(query, "INSERT INTO scan_observations") {
		return nil, errors.New("非预期写入")
	}
	e.payload = append([]byte(nil), args[4].([]byte)...)
	return nil, nil
}

func TestArtistEvidencePhysicalObservationAndStagingJSONRoundTrip(t *testing.T) {
	evidence := []metadata.ArtistTagEvidence{
		{Role: "track_artist", TagKey: "ARTIST", RawValue: " Jay Chou ", SourceKind: "flac_vorbis_comment", OccurrenceIndex: 0},
		{Role: "track_artist", TagKey: "ARTIST", RawValue: "周杰倫", SourceKind: "flac_vorbis_comment", OccurrenceIndex: 1},
	}
	parsed := parser.AudioObservation{Artist: "Jay Chou / 周杰倫", ArtistTagEvidence: evidence, SourceKind: "flac_vorbis_comment"}
	observation := buildPhysicalSourceObservation(parsed, "Album/01.flac")
	executor := &artistEvidenceStageExecutor{}
	if err := stageObservation(context.Background(), executor, "scan", "root", observation); err != nil {
		t.Fatal(err)
	}
	var staged ingest.SourceObservation
	if err := json.Unmarshal(executor.payload, &staged); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(staged.ArtistTagEvidence, evidence) || staged.Artist != parsed.Artist {
		t.Fatalf("暂存JSON丢失：%s", executor.payload)
	}
	observation.ArtistTagEvidence[0].RawValue = "changed"
	if parsed.ArtistTagEvidence[0].RawValue != " Jay Chou " {
		t.Fatal("扫描转换共享了可变证据slice")
	}
}

func TestCueArtistEvidenceUsesEffectiveSourcesAndIsolatesTracks(t *testing.T) {
	parent := parser.AudioObservation{Artist: "Parent", AlbumArtist: "Parent Album", SourceKind: "flac_vorbis_comment", ArtistTagEvidence: []metadata.ArtistTagEvidence{
		{Role: "track_artist", TagKey: "ARTIST", RawValue: "Parent", SourceKind: "flac_vorbis_comment"},
		{Role: "album_artist", TagKey: "ALBUMARTIST", RawValue: "Parent Album", SourceKind: "flac_vorbis_comment"},
	}}
	document := parser.CueDocument{Artist: "Sheet", ArtistTagEvidence: []metadata.ArtistTagEvidence{{Role: "album_artist", TagKey: "PERFORMER", RawValue: " Sheet ", SourceKind: "cue_sheet"}}}
	cue := parser.CueTrack{Artist: "Track", Number: 1, PerformerPresent: true, ReferencedFile: "image.flac", ArtistTagEvidence: []metadata.ArtistTagEvidence{{Role: "track_artist", TagKey: "PERFORMER", RawValue: " Track ", SourceKind: "cue_track"}}}
	first := buildCueVirtualObservation(parent, document, cue, "Album/album.cue")
	second := buildCueVirtualObservation(parent, document, parser.CueTrack{Number: 2, ReferencedFile: "image.flac"}, "Album/album.cue")
	if len(first.ArtistTagEvidence) != 2 || first.ArtistTagEvidence[1].SourceKind != "cue_track" {
		t.Fatalf("CUE有效来源错误：%+v", first.ArtistTagEvidence)
	}
	if len(second.ArtistTagEvidence) != 2 || second.ArtistTagEvidence[1].Role != "track_artist" || second.ArtistTagEvidence[1].SourceKind != "cue_sheet" {
		t.Fatalf("继承sheet署名错误：%+v", second.ArtistTagEvidence)
	}
	first.ArtistTagEvidence[0].RawValue = "changed"
	if document.ArtistTagEvidence[0].RawValue != " Sheet " || second.ArtistTagEvidence[0].RawValue != " Sheet " {
		t.Fatal("逐轨证据共享内存")
	}
}

func TestArtistEvidenceStagingRejectsBudgetOverflowBeforeDatabase(t *testing.T) {
	for _, evidence := range [][]metadata.ArtistTagEvidence{
		make([]metadata.ArtistTagEvidence, 101),
		{{Role: "track_artist", RawValue: strings.Repeat("x", 4097)}},
	} {
		executor := &artistEvidenceStageExecutor{}
		err := stageObservation(context.Background(), executor, "scan", "root", ingest.SourceObservation{RelativePath: "Album/01.flac", ArtistTagEvidence: evidence})
		if err == nil || executor.payload != nil {
			t.Fatal("超限证据进入暂存")
		}
	}
}

func TestArtistEvidenceScanRejectsOverflowWithDiagnostic(t *testing.T) {
	for _, format := range []string{"flac", "cue"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			value := strings.Repeat("x", 4097)
			data := []byte("PERFORMER \"" + value + "\"\nFILE \"image.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\n")
			kind := "unsupported_cue"
			if format == "flac" {
				comment := "ARTIST=" + value
				block := binary.LittleEndian.AppendUint32(make([]byte, 4), 1)
				block = binary.LittleEndian.AppendUint32(block, uint32(len(comment)))
				block = append(block, comment...)
				size := len(block)
				data = append([]byte{'f', 'L', 'a', 'C', 0x84, byte(size >> 16), byte(size >> 8), byte(size)}, block...)
				kind = "parse_failure"
			}
			if err := os.WriteFile(filepath.Join(root, "track."+format), data, 0o600); err != nil {
				t.Fatal(err)
			}
			diagnosticError := errors.New("诊断已捕获")
			executor := &cleanupFailureScanExecutor{diagnosticError: diagnosticError}
			err := (&Service{}).scanRoot(context.Background(), executor, "scan", registeredRoot{ID: "root", Path: root})
			if !errors.Is(err, diagnosticError) || strings.Join(executor.diagnosticKinds, ",") != kind {
				t.Fatalf("未拒绝超限候选并记录诊断：err=%v kinds=%v", err, executor.diagnosticKinds)
			}
		})
	}
}
