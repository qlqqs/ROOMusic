package catalog

import (
	"strings"
	"testing"
)

// 指纹含操作类型、小写的专辑 ID、预期版本与目标版本；设定与改回自动选择、不同的版本或预期版本都不能得到相同的指纹。
func TestGroupDefaultRequestFingerprint(t *testing.T) {
	base := GroupDefaultRequest{GroupID: "11111111-1111-4111-8111-111111111111", ExpectedRevision: 3, ReleaseID: "22222222-2222-4222-8222-222222222222"}
	if upper := (GroupDefaultRequest{GroupID: strings.ToUpper(base.GroupID), ExpectedRevision: base.ExpectedRevision, ReleaseID: strings.ToUpper(base.ReleaseID)}); upper.Fingerprint() != base.Fingerprint() {
		t.Error("the fingerprint depends on the case of the IDs")
	}
	for name, variant := range map[string]GroupDefaultRequest{
		"another album":     {GroupID: "33333333-3333-4333-8333-333333333333", ExpectedRevision: 3, ReleaseID: base.ReleaseID},
		"another revision":  {GroupID: base.GroupID, ExpectedRevision: 4, ReleaseID: base.ReleaseID},
		"another version":   {GroupID: base.GroupID, ExpectedRevision: 3, ReleaseID: "33333333-3333-4333-8333-333333333333"},
		"back to automatic": {GroupID: base.GroupID, ExpectedRevision: 3},
	} {
		if variant.Fingerprint() == base.Fingerprint() {
			t.Errorf("%s produced the same fingerprint", name)
		}
	}
	// 改回自动选择与设定同一个版本的指纹不同于已有的归组与修正指纹（操作类型参与指纹）。
	change := GroupChangeRequest{ReleaseID: base.GroupID, ExpectedRevision: 3, Action: GroupDetach}
	if change.Fingerprint() == (GroupDefaultRequest{GroupID: base.GroupID, ExpectedRevision: 3}).Fingerprint() {
		t.Error("a default-version fingerprint equals a group-change fingerprint")
	}
}
