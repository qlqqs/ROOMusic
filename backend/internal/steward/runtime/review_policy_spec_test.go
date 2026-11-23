package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewPolicyRulesMatchSpec(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", "..", ".trellis", "spec", "backend", "steward-review-contracts.md")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []struct{ name, heading, value string }{
		{"ReviewPolicyRules", "策略全文", ReviewPolicyRules},
		{"ReviewEvidenceFields", "证据摘要字段全文", ReviewEvidenceFields},
		{"ReviewSystemPrompt", "系统提示词全文", ReviewSystemPrompt},
	} {
		t.Run(section.name, func(t *testing.T) {
			_, body, found := strings.Cut(string(raw), "\n### "+section.heading+"\n")
			if !found {
				t.Fatalf("审查合同缺少章节 %q", section.heading)
			}
			body, _, _ = strings.Cut(body, "\n### ")
			_, body, found = strings.Cut(body, "\n```text\n")
			if !found {
				t.Fatal("审查合同缺少 text 代码块")
			}
			expected, _, found := strings.Cut(body, "\n```\n")
			if !found {
				t.Fatal("审查合同的 text 代码块未闭合")
			}
			if section.value != expected {
				t.Errorf("运行时常量与合同逐字比对失败\n实际：\n%s\n期望：\n%s", section.value, expected)
			}
		})
	}
}
