package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

func TestCandidateSignatureMemberInputs(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		change func(*ingest.SourceObservation)
	}{
		{name: "size", change: func(observation *ingest.SourceObservation) { observation.SourceSize++ }},
		{name: "mtime", change: func(observation *ingest.SourceObservation) {
			observation.SourceModifiedAt = observation.SourceModifiedAt.Add(time.Microsecond)
		}},
		{name: "parser_version", change: func(observation *ingest.SourceObservation) { observation.ParserVersion = "1:2" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, candidate := candidateSignatureFixture(t)
			before := mustCandidateSignature(t, root, candidate)
			testCase.change(&candidate.Mediums[1][0].Observation)
			if after := mustCandidateSignature(t, root, candidate); after == before {
				t.Fatal("成员输入改变后，候选签名未变化")
			}
		})
	}
}

func TestCandidateSignatureSidecarMetadata(t *testing.T) {
	for _, file := range []struct {
		name, relativePath string
		cue                bool
	}{
		{name: "cue_sheet", relativePath: "Album/album.cue", cue: true},
		{name: "artwork", relativePath: "Album/cover.jpg"},
		{name: "rip_log", relativePath: "Album/rip.LOG"},
	} {
		for _, change := range []string{"size", "mtime"} {
			t.Run(file.name+"/"+change, func(t *testing.T) {
				root, candidate := candidateSignatureFixture(t)
				modified := candidate.Mediums[1][0].Observation.SourceModifiedAt
				if file.cue {
					observation := &candidate.Mediums[1][0].Observation
					observation.SourceKind = "cue"
					observation.CueSheetPath = file.relativePath
					observation.CueParentRelativePath = "Album/image.flac"
					observation.RelativePath = "Album/album.cue#track-1@image.flac#index-0"
				}
				// 签名只读取附件元信息，夹具无需包含真实音频或有效封面。
				writeCandidateSignatureFile(t, root, file.relativePath, "fixture", modified)
				before := mustCandidateSignature(t, root, candidate)
				contents := "fixture"
				if change == "size" {
					contents += "-larger"
				} else {
					modified = modified.Add(time.Second)
				}
				writeCandidateSignatureFile(t, root, file.relativePath, contents, modified)
				if after := mustCandidateSignature(t, root, candidate); after == before {
					t.Fatal("附件大小或修改时间改变后，候选签名未变化")
				}
			})
		}
	}
}

func TestCandidateSignatureAddedArtworkAndRipLog(t *testing.T) {
	for _, relativePath := range []string{
		"Album/cover.jpg",
		"Album/box - front.png",
		"Album/Scans/cover.jpg",
		"Album/Scans/box - front.png",
		"Album/rip.log",
	} {
		t.Run(relativePath, func(t *testing.T) {
			root, candidate := candidateSignatureFixture(t)
			modified := candidate.Mediums[1][0].Observation.SourceModifiedAt
			// 已有高优先级封面也不能遮蔽新增的低优先级封面签名输入。
			writeCandidateSignatureFile(t, root, "Album/cover.png", "existing", modified)
			before := mustCandidateSignature(t, root, candidate)
			writeCandidateSignatureFile(t, root, relativePath, "added", modified)
			if after := mustCandidateSignature(t, root, candidate); after == before {
				t.Fatal("新增封面候选或抓轨日志后，候选签名未变化")
			}
		})
	}
}

func TestCandidateSignatureIgnoresUnrelatedFiles(t *testing.T) {
	for _, relativePath := range []string{
		"Album/notes.txt",
		"Album/back.png",
		"Album/Scans/back.png",
		"Other/cover.jpg",
		"Other/rip.log",
	} {
		t.Run(relativePath, func(t *testing.T) {
			root, candidate := candidateSignatureFixture(t)
			before := mustCandidateSignature(t, root, candidate)
			writeCandidateSignatureFile(t, root, relativePath, "unrelated", candidate.Mediums[1][0].Observation.SourceModifiedAt)
			if after := mustCandidateSignature(t, root, candidate); after != before {
				t.Fatal("新增无关文件不应改变候选签名")
			}
		})
	}
}

func TestCandidateSignatureIgnoresFilteredArtwork(t *testing.T) {
	for _, testCase := range []struct {
		name, relativePath string
		exclusions         []string
	}{
		{name: "hidden_file", relativePath: "Album/.front.png"},
		{name: "hidden_subsidiary_file", relativePath: "Album/Scans/.front.png"},
		{name: "hidden_directory", relativePath: "Album/.Scans/box - front.png"},
		{name: "system_directory", relativePath: "Album/@eaDir/cover.jpg"},
		{name: "hidden_directory_in_scans", relativePath: "Album/Scans/.hidden/box - front.png"},
		{name: "system_directory_in_scans", relativePath: "Album/Scans/__MACOSX/cover.jpg"},
		{name: "user_excluded_scans", relativePath: "Album/Scans/box - front.png", exclusions: []string{"Album/Scans"}},
		{name: "user_excluded_artwork", relativePath: "Album/Artwork/cover.jpg", exclusions: []string{"Album/Artwork"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, candidate := candidateSignatureFixture(t)
			root.Exclusions = testCase.exclusions
			root.filter = newScanFilter(root.Exclusions)
			before := mustCandidateSignature(t, root, candidate)
			writeCandidateSignatureFile(t, root, testCase.relativePath, "filtered", candidate.Mediums[1][0].Observation.SourceModifiedAt)
			if after := mustCandidateSignature(t, root, candidate); after != before {
				t.Fatal("被过滤范围内新增图片不应改变候选签名")
			}
		})
	}
}

func candidateSignatureFixture(t *testing.T) (registeredRoot, ingest.OrganizedCandidate) {
	t.Helper()
	root := registeredRoot{ID: "signature-root", Path: t.TempDir(), filter: newScanFilter(nil)}
	if err := os.Mkdir(filepath.Join(root.Path, "Album"), 0o755); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1_700_000_000, 123_456_000).UTC()
	candidate := ingest.OrganizedCandidate{
		Anchor: ingest.CandidateAnchor{Kind: ingest.CandidateOrdinary, Scope: "Album"},
		Mediums: map[int][]ingest.OrganizedTrack{
			1: {
				{Position: 1, Disc: 1, Observation: ingest.SourceObservation{
					RelativePath: "Album/01.flac", Directory: "Album", SourceKind: "file",
					SourceSize: 100, SourceModifiedAt: modified, ParserVersion: "1:1",
				}},
				{Position: 2, Disc: 1, Observation: ingest.SourceObservation{
					RelativePath: "Album/02.flac", Directory: "Album", SourceKind: "file",
					SourceSize: 200, SourceModifiedAt: modified, ParserVersion: "1:1",
				}},
			},
		},
	}
	return root, candidate
}

func writeCandidateSignatureFile(t *testing.T, root registeredRoot, relativePath, contents string, modified time.Time) {
	t.Helper()
	fullPath := filepath.Join(root.Path, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fullPath, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func mustCandidateSignature(t *testing.T, root registeredRoot, candidate ingest.OrganizedCandidate) string {
	t.Helper()
	signature, err := candidateSignature(root, candidate)
	if err != nil {
		t.Fatalf("计算候选签名失败：%v", err)
	}
	if signature == "" {
		t.Fatal("候选签名为空")
	}
	return signature
}
