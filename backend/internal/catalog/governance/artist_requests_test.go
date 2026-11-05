package governance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

const testArtistA = "ab000000-0000-4000-8000-000000000001"
const testArtistB = "ab000000-0000-4000-8000-000000000002"

func TestArtistRequestNormalization(t *testing.T) {
	for _, name := range []string{"", " \u00a0 ", "a\nb", "a\tb", "a\x00", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if _, err := NormalizeArtistName(name); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
	}
	name, err := NormalizeArtistName("  Beyoncé\u00a0  张三  ")
	if err != nil || name != "Beyoncé 张三" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	a := ArtistSplitRequest{ArtistID: testArtistA, ExpectedRevision: 2, ArtistSplitScope: ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{strings.ToUpper(testArtistB), testArtistA, testArtistB}}, Basis: strings.Repeat("a", 64), Target: ArtistSplitTarget{Kind: "restore", ArtistID: testArtistB, ExpectedRevision: 3}}
	b := a
	b.NameKeyIDs = []string{testArtistA, testArtistB}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("set ordering affects fingerprint")
	}
	b.Target.ExpectedRevision++
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("restore revision omitted")
	}
	b = a
	b.ArtistID = testArtistB
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("route target omitted")
	}
	for _, scope := range []ArtistSplitScope{{Mode: "aliases"}, {Mode: "albums", NameKeyIDs: []string{testArtistA}}, {Mode: "aliases", NameKeyIDs: []string{testArtistA}, GroupIDs: []string{testArtistB}}, {Mode: "other", NameKeyIDs: []string{testArtistA}}, {Mode: "aliases", NameKeyIDs: []string{"bad"}}} {
		if scope.Normalize() == nil {
			t.Fatalf("accepted scope %+v", scope)
		}
	}
	ignored := true
	p := ArtistCandidateIgnoreRequest{ArtistID: testArtistA, ExpectedRevision: 1, OtherArtistID: testArtistB, OtherExpectedRevision: 1, Ignored: &ignored, KeyPairs: []ArtistKeyPair{{testArtistB, testArtistA}, {testArtistA, testArtistB}}}
	if err := p.Normalize(); err != nil || len(p.KeyPairs) != 1 || p.KeyPairs[0].LeftKeyID != testArtistA {
		t.Fatalf("pairs %+v: %v", p, err)
	}
}
func TestArtistKindsNeverRevert(t *testing.T) {
	for _, kind := range []operations.Kind{ArtistMergeKind{}, ArtistSplitKind{}, ArtistRenameKind{}, ArtistCandidateIgnoreKind{}} {
		_, err := kind.Revert(context.Background(), nil, "", operations.Record{})
		status, code, _ := operations.Problem(err)
		if status != 409 || code != "not_revertible" {
			t.Fatalf("%s: %v", kind.Type(), err)
		}
	}
}
func TestArtistDescriptionRejectsDamagedState(t *testing.T) {
	before := emptyArtistState()
	after := emptyArtistState()
	after.Artists = []artistIdentity{{ID: testArtistA, Name: "甲", Revision: 2}}
	after.Changes = map[string]any{"kind": operations.TypeArtistRename, "from": map[string]string{"name": "原名", "source": "auto"}, "to": map[string]string{"name": "甲", "source": "manual"}}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	record := operations.Record{TargetID: testArtistA, Type: operations.TypeArtistRename, Before: b, After: a}
	if value, name := decodeArtistChanges(record); value == nil || name != "甲" {
		t.Fatal("valid description rejected")
	}
	for _, raw := range []string{`{}`, `{"schema_version":1,"changes":{"kind":"artist_merge","source":null}}`, `{"schema_version":1,"changes":{"kind":"artist_rename","path":"/private/music"}}`, `{"schema_version":2}`, `{"schema_version":1,"changes":{"kind":"artist_rename","from":{"name":"x","source":"invalid"},"to":{"name":"y","source":"auto"}}}`} {
		record.After = []byte(raw)
		if value, _ := decodeArtistChanges(record); value != nil {
			t.Fatalf("bad state accepted %s", raw)
		}
	}
}
