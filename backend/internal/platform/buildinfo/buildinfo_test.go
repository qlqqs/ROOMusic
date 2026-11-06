package buildinfo

import (
	"strings"
	"testing"
)

func TestCurrentValidatesBuildMetadata(t *testing.T) {
	oldVersion, oldCommit := version, commit
	t.Cleanup(func() { version, commit = oldVersion, oldCommit })
	sha := strings.Repeat("0123456789abcdef", 2) + "01234567"
	for _, tc := range []struct{ name, version, commit, wantVersion, wantCommit string }{
		{"开发构建", "", "", "", ""},
		{"正式版本", "0.1.0", sha, "0.1.0", sha},
		{"演练预发布", "1.20.300-drill-a.2", sha, "1.20.300-drill-a.2", sha},
		{"非法版本保留提交", "v0.1.0", sha, "", sha},
		{"非法提交保留版本", "0.1.0", "bad", "0.1.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, commit = tc.version, tc.commit
			if got := Current(); got != (Info{tc.wantVersion, tc.wantCommit}) {
				t.Fatalf("构建信息：%+v", got)
			}
		})
	}
	for _, bad := range []string{"v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2", "1.2.3+meta", "1.2.3-", "1.2.3-a..b", " 1.2.3", "1.2.3\n", "1.2.3-中文"} {
		version, commit = bad, sha
		if got := Current(); got.Version != "" || got.Commit != sha {
			t.Errorf("非法版本 %q 未降级：%+v", bad, got)
		}
	}
	for _, bad := range []string{sha[:39], sha + "0", strings.ToUpper(sha), " " + sha, sha + "\n", strings.Repeat("g", 40)} {
		version, commit = "1.2.3", bad
		if got := Current(); got.Commit != "" || got.Version != "1.2.3" {
			t.Errorf("非法提交未清空：%+v", got)
		}
	}
}
