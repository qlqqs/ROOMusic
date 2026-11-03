package governance

import (
	"strings"
	"testing"
)

// 指纹含操作类型、小写的发行 ID、预期版本、问题、动作与依据令牌；其中任何一项不同都不能得到相同的指纹。
func TestIssueReviewRequestFingerprint(t *testing.T) {
	base := IssueReviewRequest{ReleaseID: "11111111-1111-4111-8111-111111111111", ExpectedRevision: 3, Issue: IssueGrouping, Action: IssueReview, Basis: "abc"}
	upper := base
	upper.ReleaseID = strings.ToUpper(base.ReleaseID)
	if upper.Fingerprint() != base.Fingerprint() {
		t.Error("the fingerprint depends on the case of the release ID")
	}
	for name, variant := range map[string]IssueReviewRequest{
		"another release":  {ReleaseID: "33333333-3333-4333-8333-333333333333", ExpectedRevision: 3, Issue: IssueGrouping, Action: IssueReview, Basis: "abc"},
		"another revision": {ReleaseID: base.ReleaseID, ExpectedRevision: 4, Issue: IssueGrouping, Action: IssueReview, Basis: "abc"},
		"another issue":    {ReleaseID: base.ReleaseID, ExpectedRevision: 3, Issue: IssueMissingTracks, Action: IssueReview, Basis: "abc"},
		"unreview":         {ReleaseID: base.ReleaseID, ExpectedRevision: 3, Issue: IssueGrouping, Action: IssueUnreview},
		"another basis":    {ReleaseID: base.ReleaseID, ExpectedRevision: 3, Issue: IssueGrouping, Action: IssueReview, Basis: "abd"},
	} {
		if variant.Fingerprint() == base.Fingerprint() {
			t.Errorf("%s produced the same fingerprint", name)
		}
	}
	if (GroupChangeRequest{ReleaseID: base.ReleaseID, ExpectedRevision: 3, Action: GroupDetach}).Fingerprint() == (IssueReviewRequest{ReleaseID: base.ReleaseID, ExpectedRevision: 3, Action: GroupDetach}).Fingerprint() {
		t.Error("an issue-review fingerprint equals a group-change fingerprint")
	}
}
