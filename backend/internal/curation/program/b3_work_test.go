package program

import (
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestB3FingerprintNeedsWork(t *testing.T) {
	const digest = "same-input"
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	for _, field := range []string{"", "unavailable", "used", "not_reached"} {
		for _, status := range []string{"matched", "no_match", "uncertain", "try_later"} {
			for _, available := range []bool{false, true} {
				t.Run(fmt.Sprintf("字段=%q/状态=%s/可用=%t", field, status, available), func(t *testing.T) {
					state := curation.WorkState{RuleVersion: StoredRuleVersion, InputDigest: digest, FileDigest: digest, Status: status, Result: jsonValue(map[string]any{"method": "search"})}
					if field != "" {
						state.Result = jsonValue(map[string]any{"method": "search", "fingerprint": field})
					}
					if status == "try_later" {
						state.NextAttemptAt = &future
					}
					want := available && (status == "no_match" || status == "uncertain") && (field == "" || field == "unavailable")
					if got := needsWork("release", state, digest, now, available); got != want {
						t.Errorf("needsWork=%t，期望 %t", got, want)
					}
					for _, subject := range []string{"artwork", "lyrics"} {
						if needsWork(subject, state, digest, now, available) {
							t.Errorf("指纹可用性重开了 %s", subject)
						}
					}
					if status == "try_later" && !needsWork("release", state, digest, future, available) {
						t.Error("到期的常规重试未重开")
					}
				})
			}
		}
	}
	for _, tc := range []struct {
		name, method string
		conflicts    []string
		want         bool
	}{
		{"空方法", "", []string{"insufficient_evidence"}, true},
		{"搜索", "search", []string{"search_only"}, true},
		{"轨数冲突", "", []string{"other", "track_count"}, false},
		{"搜索兼轨数冲突", "search", []string{"track_count"}, false},
		{"标签", "tag_id", []string{"duration_mismatch"}, false},
		{"条码", "barcode", nil, false},
		{"光盘", "disc_id", []string{"disc_fuzzy"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := curation.WorkState{
				RuleVersion: StoredRuleVersion, InputDigest: digest, FileDigest: digest, Status: "uncertain",
				Result: jsonValue(map[string]any{"method": tc.method, "conflicts": tc.conflicts}),
			}
			if got := needsWork("release", state, digest, now, true); got != tc.want {
				t.Errorf("旧结果 %s 的 needsWork=%t，期望 %t", state.Result, got, tc.want)
			}
		})
	}
}
