package smoke

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Version = SnapshotVersion
	snapshot.Implementation = "test"
	snapshot.CorpusDigest = "corpus"
	return snapshot
}

func testGraphSnapshot(tracks ...Track) Snapshot {
	trackKeys := make([]string, 0, len(tracks))
	files := make([]File, 0, len(tracks))
	for index := range tracks {
		if tracks[index].Key == "" {
			tracks[index].Key = "track-" + string(rune('a'+index))
		}
		tracks[index].MediumKey = "medium"
		if tracks[index].Position == 0 {
			tracks[index].Position = index + 1
		}
		if tracks[index].SourceKey == "" {
			tracks[index].SourceKey = "source-" + tracks[index].Key
		}
		tracks[index].ParentSourceKey = tracks[index].SourceKey
		tracks[index].SourceKind = "physical"
		trackKeys = append(trackKeys, tracks[index].Key)
		files = append(files, File{Key: "file-" + tracks[index].Key, ReleaseKey: "release", SourceKey: tracks[index].SourceKey})
	}
	return testSnapshot(Snapshot{
		Releases: []Release{{Key: "release", MediumKeys: []string{"medium"}}},
		Media:    []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: trackKeys}},
		Tracks:   tracks,
		Files:    files,
	})
}

func standaloneV0Snapshot(snapshot Snapshot) Snapshot {
	snapshot.Version = SnapshotVersion
	snapshot.Implementation = V0GeneratedCorrectedImplementation
	snapshot.CorpusDigest = "corpus"
	snapshot.CodeHash = strings.Repeat("a", 64)
	snapshot.SchemaDigest = strings.Repeat("b", 64)
	snapshot.AdapterHash = strings.Repeat("c", 64)
	snapshot.GenerationMode = "standalone_scanner"
	snapshot.BaselineScope = "release_graph_only"
	snapshot.ExcludedEvidence = append([]string(nil), v0ExcludedEvidence...)
	return snapshot
}

func TestSummarizeTreeDetectsMutation(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "a.flac"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := SummarizeTree(d)
	if err != nil {
		t.Fatal(err)
	}
	if a.Files != 1 || a.Entries != 1 || a.Bytes != 3 {
		t.Fatalf("unexpected initial summary: %#v", a)
	}
	if err := os.WriteFile(filepath.Join(d, "a.flac"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := SummarizeTree(d)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == b.Digest || a.Bytes != b.Bytes {
		t.Fatalf("summary mutation not detected: %#v %#v", a, b)
	}
}

func TestSummarizeTreeIncludesDirectoryMetadata(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "empty-disc")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := SummarizeTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Files != 0 || before.Entries != 1 || before.Bytes != 0 {
		t.Fatalf("unexpected directory summary: %#v", before)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	after, err := SummarizeTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatalf("directory mode mutation was not detected: %#v %#v", before, after)
	}
}

func TestStableKeys(t *testing.T) {
	if SourceKey("/music", "/music/disc/track.flac") != SourceKey("/other", "/other/disc/track.flac") {
		t.Fatal("source key depends on root")
	}
	if CueSourceKey("sheet.cue", "disc.flac", 1, "00:01:00") != CueSourceKey("sheet.cue", "disc.flac", 1, "00:01:00") {
		t.Fatal("cue key unstable")
	}
	if ReleaseKey([]string{"b", "a"}) != ReleaseKey([]string{"a", "b"}) {
		t.Fatal("release key order dependent")
	}
}

func TestCompareUUIDIndependentAndDeterministic(t *testing.T) {
	a := testGraphSnapshot(Track{Key: "stable", Title: "Old", Artist: "A"})
	b := testGraphSnapshot(Track{Key: "stable", Title: "New", Artist: "A"})
	r := Compare(a, b)
	if len(r.Differences) != 1 || r.Differences[0].Field != "title" {
		t.Fatalf("unexpected diff: %#v", r)
	}
	if r.Differences[0].Category != "current_regression" {
		t.Fatal("wrong category")
	}
}

func TestBaselineAndReportRedaction(t *testing.T) {
	if _, err := SelectBaseline([]Snapshot{{}, {}}); err != ErrMultipleBaselines {
		t.Fatalf("expected multiple baseline error, got %v", err)
	}
	r := Compare(testGraphSnapshot(Track{Key: "digest", Title: "/music/private.flac"}), testGraphSnapshot(Track{Key: "digest", Title: "other"}))
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "/music/") || strings.Contains(s, "private.flac") || strings.Contains(s, "other") {
		t.Fatalf("report leaked path data: %s", s)
	}
}

func TestCompareCoversGraphAndFields(t *testing.T) {
	a := testSnapshot(Snapshot{
		Releases: []Release{{Key: "r", Title: "same", MediumKeys: []string{"m"}, Fields: map[string]string{"year": "2020"}}},
		Media:    []Medium{{Key: "m", ReleaseKey: "r", Position: 1, TrackKeys: []string{"t"}}},
		Tracks:   []Track{{Key: "t", MediumKey: "m", Position: 1, SourceKey: "s", ParentSourceKey: "s", Title: "same", Artist: "a", SourceKind: "physical", Fields: map[string]string{"codec": "flac"}}},
		Files:    []File{{Key: "f", ReleaseKey: "r", SourceKey: "s", Size: 10}},
	})
	b := a
	b.Media = []Medium{{Key: "m", ReleaseKey: "r", Position: 2, TrackKeys: []string{"t"}}}
	b.Tracks = []Track{{Key: "t", MediumKey: "m", Position: 1, SourceKey: "s", ParentSourceKey: "s", Title: "same", Artist: "a", SourceKind: "physical", Fields: map[string]string{"codec": "wav"}}}
	r := Compare(a, b)
	if len(r.Differences) != 2 {
		t.Fatalf("expected medium and track field differences, got %#v", r.Differences)
	}
	if r.Differences[0].Entity != "medium" || r.Differences[1].Entity != "track" {
		t.Fatalf("differences are not deterministically ordered: %#v", r.Differences)
	}
}

func TestCompareRejectsIncompatibleSnapshots(t *testing.T) {
	a := testSnapshot(Snapshot{})
	b := a
	b.CorpusDigest = "other-corpus"
	if _, err := CompareStrict(a, b); err == nil {
		t.Fatal("expected corpus compatibility error")
	}
	report := Compare(a, b)
	if report.Metadata.Comparable || report.Metadata.CorpusCompatible || len(report.Differences) != 0 {
		t.Fatalf("incompatible report should fail closed: %#v", report)
	}
}

func TestComparePreservesSemanticTrackOrderAndPosition(t *testing.T) {
	a := testGraphSnapshot(Track{Key: "t1", Position: 1}, Track{Key: "t2", Position: 2})
	b := a
	b.Media = []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"t2", "t1"}}}
	b.Tracks = append([]Track(nil), a.Tracks...)
	b.Tracks[0].Position = 2
	b.Tracks[1].Position = 1
	r := Compare(a, b)
	if len(r.Differences) != 3 {
		t.Fatalf("expected relation and two position differences, got %#v", r.Differences)
	}
	if r.Differences[0].Field != "track_keys" {
		t.Fatalf("expected semantic relation diff first, got %#v", r.Differences)
	}
}

func TestCategoryHintsAreExplicit(t *testing.T) {
	hint := "track\x00t\x00title"
	a := testSnapshot(Snapshot{
		Releases:      []Release{{Key: "r", MediumKeys: []string{"m"}}},
		Media:         []Medium{{Key: "m", ReleaseKey: "r", Position: 1, TrackKeys: []string{"t"}}},
		Tracks:        []Track{{Key: "t", MediumKey: "m", Position: 1, SourceKey: "s", ParentSourceKey: "s", SourceKind: "physical", Title: "old"}},
		Files:         []File{{Key: "f", ReleaseKey: "r", SourceKey: "s"}},
		CategoryHints: map[string]DifferenceCategory{hint: CategorySchemaMappingGap},
	})
	b := a
	b.Tracks = []Track{{Key: "t", MediumKey: "m", Position: 1, SourceKey: "s", ParentSourceKey: "s", SourceKind: "physical", Title: "new"}}
	r := Compare(a, b)
	if len(r.Differences) != 1 || r.Differences[0].Category != string(CategorySchemaMappingGap) {
		t.Fatalf("category hint not applied: %#v", r.Differences)
	}
	invalid := a
	invalid.CategoryHints = map[string]DifferenceCategory{hint: "unclassified"}
	if err := ValidateSnapshot(invalid); err == nil {
		t.Fatal("expected unknown category to be rejected")
	}
}

func TestSelectBaselineRequiresSingleValidSnapshot(t *testing.T) {
	if _, err := SelectBaseline(nil); err != ErrNoBaseline {
		t.Fatalf("expected no-baseline error, got %v", err)
	}
	if _, err := SelectBaseline([]Snapshot{{}}); err == nil {
		t.Fatal("expected invalid baseline error")
	}
	baseline := testGraphSnapshot(Track{Key: "t"})
	selected, err := SelectBaseline([]Snapshot{baseline})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Version != SnapshotVersion || selected.CorpusDigest != "corpus" {
		t.Fatalf("unexpected selected baseline: %#v", selected)
	}
}

func TestValidateSnapshotRequiresStandaloneV0Scope(t *testing.T) {
	valid := Snapshot{
		Version:          SnapshotVersion,
		Implementation:   V0GeneratedCorrectedImplementation,
		CorpusDigest:     "corpus",
		CodeHash:         strings.Repeat("a", 64),
		SchemaDigest:     strings.Repeat("b", 64),
		AdapterHash:      strings.Repeat("c", 64),
		GenerationMode:   "standalone_scanner",
		BaselineScope:    "release_graph_only",
		ExcludedEvidence: append([]string(nil), v0ExcludedEvidence...),
	}
	if err := ValidateSnapshot(valid); err != nil {
		t.Fatalf("valid standalone V0 snapshot rejected: %v", err)
	}
	invalid := valid
	invalid.Degraded = true
	if err := ValidateSnapshot(invalid); err == nil {
		t.Fatal("degraded standalone V0 snapshot was accepted")
	}
	invalid = valid
	invalid.GenerationMode = "production_runtime"
	if err := ValidateSnapshot(invalid); err == nil {
		t.Fatal("production V0 snapshot was accepted as standalone baseline")
	}
	invalid = valid
	invalid.AdapterHash = strings.Repeat("A", 64)
	if err := ValidateSnapshot(invalid); err == nil {
		t.Fatal("non-canonical V0 adapter hash was accepted")
	}
}

func TestStandaloneV0ComparisonExcludesRuntimeOnlySummary(t *testing.T) {
	v0 := standaloneV0Snapshot(Snapshot{})
	current := testSnapshot(Snapshot{
		Diagnostics:    map[string]int{"unsupported_audio": 2},
		AttentionCount: 3,
	})
	report, err := CompareStrict(v0, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("runtime-only current fields were compared with standalone V0: %#v", report.Differences)
	}

	currentA := current
	currentB := current
	currentB.AttentionCount = 4
	report, err = CompareStrict(currentA, currentB)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 1 || report.Differences[0].Entity != "snapshot" {
		t.Fatalf("current/current summary comparison was skipped: %#v", report.Differences)
	}
}

func TestValidateSnapshotRejectsDuplicateAndOpenGraphs(t *testing.T) {
	valid := testGraphSnapshot(Track{Key: "one"}, Track{Key: "two"})
	if err := ValidateSnapshot(valid); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}

	duplicate := valid
	duplicate.Tracks = append(append([]Track(nil), valid.Tracks...), valid.Tracks[0])
	if err := ValidateSnapshot(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate track key") {
		t.Fatalf("duplicate track was not rejected safely: %v", err)
	}

	orphan := valid
	orphan.Tracks = append([]Track(nil), valid.Tracks...)
	orphan.Tracks[0].MediumKey = "missing"
	if err := ValidateSnapshot(orphan); err == nil || !strings.Contains(err.Error(), "missing medium") {
		t.Fatalf("orphan track was not rejected safely: %v", err)
	}

	wrongOrder := valid
	wrongOrder.Media = append([]Medium(nil), valid.Media...)
	wrongOrder.Media[0].TrackKeys = []string{"two", "one"}
	if err := ValidateSnapshot(wrongOrder); err == nil || !strings.Contains(err.Error(), "not closed or ordered") {
		t.Fatalf("invalid relation order was not rejected safely: %v", err)
	}

	crossRelease := valid
	crossRelease.Releases = append(append([]Release(nil), valid.Releases...), Release{Key: "other", MediumKeys: []string{"other-medium"}})
	crossRelease.Media = append(append([]Medium(nil), valid.Media...), Medium{Key: "other-medium", ReleaseKey: "other", Position: 1})
	crossRelease.Files = append([]File(nil), valid.Files...)
	crossRelease.Files[0].ReleaseKey = "other"
	if err := ValidateSnapshot(crossRelease); err == nil || !strings.Contains(err.Error(), "another release") {
		t.Fatalf("cross-release parent source was not rejected safely: %v", err)
	}
}

func TestCanonicalTrackFactsDistinguishUnknownZeroFromCueFrameZero(t *testing.T) {
	want := testGraphSnapshot(Track{Key: "track", Fields: map[string]string{"cue_index_frames": "0"}})
	want = standaloneV0Snapshot(want)
	got := testGraphSnapshot(Track{Key: "track", Fields: map[string]string{
		"cue_index_frames": "0",
		"duration_ms":      "0",
		"sample_rate":      "0",
		"channels":         "0",
		"bitrate":          "0",
		"bit_depth":        "0",
		"cue_end_frames":   "0",
	}})
	got.Implementation = "current"

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("unknown zero-valued audio facts were compared: %#v", report.Differences)
	}

	got.Tracks[0].Fields["cue_index_frames"] = "1"
	report, err = CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 1 || report.Differences[0].Field != "field.cue_index_frames" || report.Differences[0].Category != string(CategoryCurrentRegression) {
		t.Fatalf("meaningful CUE frame zero was folded into absence: %#v", report.Differences)
	}
}

func TestStandaloneV0ComparisonUsesEquivalentEvidenceFacts(t *testing.T) {
	want := testGraphSnapshot(Track{Key: "track", Fields: map[string]string{"codec": "FLAC"}})
	want.Releases[0].Fields = map[string]string{"album_title": "Album", "candidate_kind": "release"}
	want.Releases[0].Evidence = []Evidence{{Field: "album_title", Value: "Album", Source: "tag", Confidence: "high", Action: "auto_apply", RuleID: "v0-rule"}}
	want = standaloneV0Snapshot(want)

	got := testGraphSnapshot(Track{Key: "track", Fields: map[string]string{"codec": "flac"}})
	got.Implementation = "current"
	got.Releases[0].Fields = map[string]string{"title": "Album", "candidate_kind": "ordinary_directory"}
	got.Releases[0].Evidence = []Evidence{{Field: "title", Value: "Album", Source: "tag", Confidence: "medium", Action: "uncertain_apply", RuleID: "current-rule"}}

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("implementation-only evidence fields were compared: %#v", report.Differences)
	}
}

func TestStandaloneV0ParsedButOmittedPhysicalTracksAreIntentional(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{
		Releases: []Release{{Key: "release", MediumKeys: []string{"medium"}, Fields: map[string]string{"grouping_track_count": "1"}}},
		Media:    []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"kept"}}},
		Tracks:   []Track{{Key: "kept", MediumKey: "medium", Position: 1, SourceKey: "source-kept", ParentSourceKey: "source-kept", SourceKind: "physical"}},
		Files: []File{
			{Key: "file-kept", ReleaseKey: "release", SourceKey: "source-kept"},
			{Key: "file-omitted", ReleaseKey: "release", SourceKey: "source-omitted"},
		},
	})
	got := testSnapshot(Snapshot{
		Implementation: "current",
		Releases:       []Release{{Key: "release", MediumKeys: []string{"medium"}, Fields: map[string]string{"grouping_track_count": "2"}}},
		Media:          []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"omitted", "kept"}}},
		Tracks: []Track{
			{Key: "omitted", MediumKey: "medium", Position: 1, SourceKey: "source-omitted", ParentSourceKey: "source-omitted", SourceKind: "physical"},
			{Key: "kept", MediumKey: "medium", Position: 2, SourceKey: "source-kept", ParentSourceKey: "source-kept", SourceKind: "physical"},
		},
		Files: append([]File(nil), want.Files...),
	})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 4 || report.Counts[string(CategoryIntentionalContractChange)] != 4 {
		t.Fatalf("V0 silent omission was not isolated as an intentional difference: %#v", report)
	}
}

func TestStandaloneV0CueReplacementDoesNotExcusePhysicalRegression(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{
		Releases: []Release{{Key: "release", MediumKeys: []string{"medium"}}},
		Media:    []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"cue"}}},
		Tracks:   []Track{{Key: "cue", MediumKey: "medium", Position: 1, SourceKey: "cue-source", ParentSourceKey: "physical-source", SourceKind: "cue_virtual"}},
		Files:    []File{{Key: "file", ReleaseKey: "release", SourceKey: "physical-source"}},
	})
	got := testSnapshot(Snapshot{
		Implementation: "current",
		Releases:       []Release{{Key: "release", MediumKeys: []string{"medium"}}},
		Media:          []Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"physical"}}},
		Tracks:         []Track{{Key: "physical", MediumKey: "medium", Position: 1, SourceKey: "physical-source", ParentSourceKey: "physical-source", SourceKind: "physical"}},
		Files:          append([]File(nil), want.Files...),
	})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts[string(CategoryCurrentRegression)] == 0 {
		t.Fatalf("missing CUE virtual replacement was excused: %#v", report)
	}
}

func TestStandaloneV0RipLogOnlyDifferencesAreIntentional(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{Releases: []Release{{
		Key:        "release",
		SourceType: "CD",
		MediaType:  "CD",
		Evidence: []Evidence{
			{Field: "source_type", Value: "cd", Source: "rule", RuleID: "source.rip_log.cd"},
			{Field: "media_type", Value: "cd", Source: "rule", RuleID: "media.rip_log.cd"},
		},
	}}})
	got := testSnapshot(Snapshot{Implementation: "current", Releases: []Release{{Key: "release"}}})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 4 || report.Counts[string(CategoryIntentionalContractChange)] != 4 {
		t.Fatalf("V0 rip-log-only differences were not isolated: %#v", report)
	}
	for _, difference := range report.Differences {
		if difference.Category != string(CategoryIntentionalContractChange) {
			t.Fatalf("difference %s was not intentional: %#v", difference.Field, report.Differences)
		}
	}
}

func TestStandaloneV0RipLogClassificationKeepsOtherMetadataRegressions(t *testing.T) {
	tests := []struct {
		name      string
		want      Release
		got       Release
		wantDiffs int
	}{
		{
			name: "tag source remains required",
			want: Release{Key: "release", SourceType: "CD", Evidence: []Evidence{{
				Field: "source_type", Value: "cd", Source: "tag", RuleID: "tag.SOURCE",
			}}},
			got:       Release{Key: "release"},
			wantDiffs: 2,
		},
		{
			name: "folder media remains required",
			want: Release{Key: "release", MediaType: "CD", Evidence: []Evidence{{
				Field: "media_type", Value: "cd", Source: "folder", RuleID: "folder.media_type",
			}}},
			got:       Release{Key: "release"},
			wantDiffs: 2,
		},
		{
			name: "explicit current conflict is not excused",
			want: Release{Key: "release", SourceType: "CD", Evidence: []Evidence{{
				Field: "source_type", Value: "cd", Source: "rule", RuleID: "source.rip_log.cd",
			}}},
			got: Release{Key: "release", SourceType: "Vinyl", Evidence: []Evidence{{
				Field: "source_type", Value: "vinyl", Source: "tag", RuleID: "tag.SOURCE",
			}}},
			wantDiffs: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := standaloneV0Snapshot(Snapshot{Releases: []Release{tt.want}})
			got := testSnapshot(Snapshot{Implementation: "current", Releases: []Release{tt.got}})
			report, err := CompareStrict(want, got)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Differences) != tt.wantDiffs {
				t.Fatalf("expected %d differences, got %#v", tt.wantDiffs, report.Differences)
			}
			if report.Counts[string(CategoryCurrentRegression)] != len(report.Differences) {
				t.Fatalf("non-rip-log-only metadata difference was excused: %#v", report)
			}
		})
	}
}

func TestStandaloneV0RipLogEvidenceSourcesMapOnlyWithExactRules(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{Releases: []Release{{
		Key:        "release",
		SourceType: "CD",
		MediaType:  "CD",
		Evidence: []Evidence{
			{Field: "source_type", Value: "cd", Source: "rule", Confidence: "high", Action: "auto_apply", RuleID: "source.rip_log.cd"},
			{Field: "media_type", Value: "cd", Source: "rule", Confidence: "high", Action: "auto_apply", RuleID: "media.rip_log.cd"},
		},
	}}})
	got := testSnapshot(Snapshot{Implementation: "current", Releases: []Release{{
		Key:        "release",
		SourceType: "CD",
		MediaType:  "CD",
		Evidence: []Evidence{
			{Field: "source_type", Value: "CD", Source: "rip_log", Confidence: "high", Action: "auto_apply", RuleID: "rip_log_cd_v1"},
			{Field: "media_type", Value: "CD", Source: "rip_log", Confidence: "high", Action: "auto_apply", RuleID: "rip_log_cd_v1"},
		},
	}}})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("同一抓轨规则的跨 schema 来源未映射：%#v", report)
	}

	got.Releases[0].Evidence[0].Source = "folder"
	report, err = CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	differences := differencesForField(report.Differences, "evidence.source_type.source")
	if len(differences) != 1 || differences[0].Category != string(CategoryCurrentRegression) {
		t.Fatalf("folder 来源被错误当作抓轨规则映射：%#v", report)
	}
}

func TestStandaloneV0MultiValueGenreMemberIsSchemaMappingGap(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{Releases: []Release{{
		Key:   "release",
		Genre: "Jazz / Rock",
		Evidence: []Evidence{{
			Field: "genre", Value: "Jazz / Rock", Source: "tag", RuleID: "v0.genre",
		}},
	}}})
	got := testSnapshot(Snapshot{Implementation: "current", Releases: []Release{{
		Key:   "release",
		Genre: "Rock",
		Evidence: []Evidence{{
			Field: "genre", Value: "Rock", Source: "tag", RuleID: "current.genre",
		}},
	}}})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 2 || report.Counts[string(CategorySchemaMappingGap)] != 2 {
		t.Fatalf("multi-value genre member was not mapped explicitly: %#v", report)
	}
}

func TestGenreMappingKeepsNonMembersAndCurrentComparisonsAsRegressions(t *testing.T) {
	tests := []struct {
		name          string
		expectedGenre string
		actualGenre   string
		v0Comparison  bool
	}{
		{name: "disjoint value", expectedGenre: "Jazz / Rock", actualGenre: "Pop", v0Comparison: true},
		{name: "case mismatch", expectedGenre: "Jazz / Rock", actualGenre: "rock", v0Comparison: true},
		{name: "single expected value", expectedGenre: "Rock", actualGenre: "Jazz", v0Comparison: true},
		{name: "current idempotency", expectedGenre: "Jazz / Rock", actualGenre: "Rock", v0Comparison: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantRelease := Release{Key: "release", Genre: tt.expectedGenre}
			gotRelease := Release{Key: "release", Genre: tt.actualGenre}
			want := testSnapshot(Snapshot{Releases: []Release{wantRelease}})
			if tt.v0Comparison {
				want = standaloneV0Snapshot(Snapshot{Releases: []Release{wantRelease}})
			}
			got := testSnapshot(Snapshot{Implementation: "current", Releases: []Release{gotRelease}})

			report, err := CompareStrict(want, got)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Differences) != 1 || report.Differences[0].Category != string(CategoryCurrentRegression) {
				t.Fatalf("genre difference was incorrectly excused: %#v", report)
			}
		})
	}
}

func TestStandaloneV0ALACCurrentOnlyComposerIsIntentional(t *testing.T) {
	want := standaloneV0Snapshot(testGraphSnapshot(Track{
		Key:     "track",
		Fields:  map[string]string{"codec": "aac"},
		Credits: []Credit{{Role: "performer", Name: "Performer"}},
	}))
	got := testGraphSnapshot(Track{
		Key:    "track",
		Fields: map[string]string{"codec": "alac"},
		Credits: []Credit{
			{Role: "composer", Name: "Composer"},
			{Role: "performer", Name: "Performer"},
		},
	})
	got.Implementation = "current"

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	creditDifferences := differencesForField(report.Differences, "credits")
	if len(creditDifferences) != 1 || creditDifferences[0].Category != string(CategoryIntentionalContractChange) {
		t.Fatalf("ALAC 的 current-only composer 未被精确归类：%#v", report)
	}
}

func TestStandaloneV0ALACCurrentOnlyAudioFactsAreIntentional(t *testing.T) {
	want := standaloneV0Snapshot(testGraphSnapshot(Track{
		Key:    "track",
		Fields: map[string]string{"codec": "aac"},
	}))
	got := testGraphSnapshot(Track{
		Key: "track",
		Fields: map[string]string{
			"codec":       "alac",
			"duration_ms": "180000",
			"sample_rate": "44100",
			"channels":    "2",
			"bitrate":     "900",
			"bit_depth":   "16",
		},
	})
	got.Implementation = "current"

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Differences) != 6 || report.Counts[string(CategoryIntentionalContractChange)] != 6 {
		t.Fatalf("ALAC current-only audio facts were not narrowly classified: %#v", report)
	}
}

func TestCurrentOnlyUnknownFieldsRemainRegressions(t *testing.T) {
	tests := []struct {
		name string
		want Snapshot
		got  Snapshot
	}{
		{
			name: "release catalog",
			want: standaloneV0Snapshot(Snapshot{Releases: []Release{{Key: "release"}}}),
			got:  testSnapshot(Snapshot{Implementation: "current", Releases: []Release{{Key: "release", Catalog: "CAT-1"}}}),
		},
		{
			name: "ALAC cue isrc",
			want: standaloneV0Snapshot(testGraphSnapshot(Track{Key: "track", Fields: map[string]string{"codec": "aac"}})),
			got:  testGraphSnapshot(Track{Key: "track", Fields: map[string]string{"codec": "alac", "cue_isrc": "USAAA0000001"}}),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "ALAC cue isrc" {
				testCase.got.Implementation = "current"
			}
			report, err := CompareStrict(testCase.want, testCase.got)
			if err != nil {
				t.Fatal(err)
			}
			regressions := report.Counts[string(CategoryCurrentRegression)]
			if regressions != 1 {
				t.Fatalf("unknown current-only field was excused: %#v", report)
			}
		})
	}
}

func TestALACCreditClassificationKeepsOtherCreditRegressions(t *testing.T) {
	tests := []struct {
		name string
		want Track
		got  Track
	}{
		{
			name: "FLAC composer",
			want: Track{Key: "track", Fields: map[string]string{"codec": "flac"}},
			got:  Track{Key: "track", Fields: map[string]string{"codec": "flac"}, Credits: []Credit{{Role: "composer", Name: "Composer"}}},
		},
		{
			name: "ALAC producer",
			want: Track{Key: "track", Fields: map[string]string{"codec": "aac"}},
			got:  Track{Key: "track", Fields: map[string]string{"codec": "alac"}, Credits: []Credit{{Role: "producer", Name: "Producer"}}},
		},
		{
			name: "ALAC replaces V0 composer",
			want: Track{Key: "track", Fields: map[string]string{"codec": "aac"}, Credits: []Credit{{Role: "composer", Name: "Expected"}}},
			got:  Track{Key: "track", Fields: map[string]string{"codec": "alac"}, Credits: []Credit{{Role: "composer", Name: "Actual"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := standaloneV0Snapshot(testGraphSnapshot(tt.want))
			got := testGraphSnapshot(tt.got)
			got.Implementation = "current"
			report, err := CompareStrict(want, got)
			if err != nil {
				t.Fatal(err)
			}
			creditDifferences := differencesForField(report.Differences, "credits")
			if len(creditDifferences) != 1 || creditDifferences[0].Category != string(CategoryCurrentRegression) {
				t.Fatalf("非 current-only ALAC composer 被错误放行：%#v", report)
			}
		})
	}
}

func TestStandaloneV0StaleGroupingMediumCountIsSchemaMappingGap(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{
		Releases: []Release{{Key: "release", MediumKeys: []string{"medium-1", "medium-2"}, Fields: map[string]string{"grouping_medium_count": "1"}}},
		Media: []Medium{
			{Key: "medium-1", ReleaseKey: "release", Position: 1},
			{Key: "medium-2", ReleaseKey: "release", Position: 2},
		},
	})
	got := testSnapshot(Snapshot{
		Implementation: "current",
		Releases:       []Release{{Key: "release", MediumKeys: []string{"medium-1", "medium-2"}, Fields: map[string]string{"grouping_medium_count": "2"}}},
		Media: []Medium{
			{Key: "medium-1", ReleaseKey: "release", Position: 1},
			{Key: "medium-2", ReleaseKey: "release", Position: 2},
		},
	})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	differences := differencesForField(report.Differences, "field.grouping_medium_count")
	if len(differences) != 1 || differences[0].Category != string(CategorySchemaMappingGap) {
		t.Fatalf("V0 stale grouping medium count 未被精确归类：%#v", report)
	}
}

func TestGroupingMediumCountClassificationRequiresMatchingGraph(t *testing.T) {
	want := standaloneV0Snapshot(Snapshot{
		Releases: []Release{{Key: "release", MediumKeys: []string{"medium-1"}, Fields: map[string]string{"grouping_medium_count": "1"}}},
		Media:    []Medium{{Key: "medium-1", ReleaseKey: "release", Position: 1}},
	})
	got := testSnapshot(Snapshot{
		Implementation: "current",
		Releases:       []Release{{Key: "release", MediumKeys: []string{"medium-1", "medium-2"}, Fields: map[string]string{"grouping_medium_count": "2"}}},
		Media: []Medium{
			{Key: "medium-1", ReleaseKey: "release", Position: 1},
			{Key: "medium-2", ReleaseKey: "release", Position: 2},
		},
	})

	report, err := CompareStrict(want, got)
	if err != nil {
		t.Fatal(err)
	}
	differences := differencesForField(report.Differences, "field.grouping_medium_count")
	if len(differences) != 1 || differences[0].Category != string(CategoryCurrentRegression) {
		t.Fatalf("真实 Medium 图差异被错误归为 mapping gap：%#v", report)
	}
}

func differencesForField(values []Difference, field string) []Difference {
	result := make([]Difference, 0)
	for _, value := range values {
		if value.Field == field {
			result = append(result, value)
		}
	}
	return result
}
