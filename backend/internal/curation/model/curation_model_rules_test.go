package model

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

type curationRuleEstimateLauncher struct {
	Launcher
	size int64
}

func (l curationRuleEstimateLauncher) Estimate(_ context.Context, p RunParams) (int64, error) {
	return l.size, nil
}

func TestCurationModelRulesBatchBudgetBoundary(t *testing.T) {
	items := []curation.PendingItem{{ID: "合成甲", Kind: "release_match"}, {ID: "合成乙", Kind: "release_match"}}
	const budget = 32000
	for _, tc := range []struct {
		name string
		size int64
		fits bool
	}{
		{"刚好可发", budget, true},
		{"多一需拆", budget + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Scheduler{launcher: curationRuleEstimateLauncher{size: tc.size}}
			got, err := s.batchFits(t.Context(), &modelWork{budget: budget}, items)
			if err != nil || got != tc.fits {
				t.Fatalf("请求估算 %d、预算 %d，期望可发=%t，实际 %t，错误 %v", tc.size, budget, tc.fits, got, err)
			}
		})
	}
}

func TestCurationModelRulesReadyKindOrder(t *testing.T) {
	ready := []TaskKind{VerifyEvidence, SelectCandidate, localQueue}
	for _, want := range []TaskKind{localQueue, SelectCandidate, VerifyEvidence} {
		queue := QueueOrder{Ready: func(_ context.Context, kind TaskKind, _ time.Time) (bool, error) {
			return slices.Contains(ready, kind), nil
		}}
		got, err := queue.NextModelKind(t.Context(), time.Time{})
		if err != nil || got != want {
			t.Fatalf("就绪 %v，期望先派 %s，实际 %s，错误 %v", ready, want, got, err)
		}
		ready = slices.DeleteFunc(ready, func(kind TaskKind) bool { return kind == got })
	}
}

func TestCurationModelRulesFirstMessageLocalEvidence(t *testing.T) {
	item := curation.PendingItem{
		ID: "synthetic-id", Kind: "release_match",
		Evidence:   json.RawMessage(`{"title":"中日한\"","directory":"盘一","comment":"注释","tracks":[{"position":1,"length_ms":123456}]}`),
		Candidates: json.RawMessage(`[{"external_id":"保留候选"}]`),
		Conflicts:  json.RawMessage(`["合成冲突"]`),
	}
	// 用已裁过的发出副本验证本地字段及冲突仍原样进入请求。
	message, err := firstMessage([]curation.PendingItem{item})
	if err != nil {
		t.Fatal(err)
	}
	_, content, _ := strings.Cut(message, "\n")
	content, _, _ = strings.Cut(content, "\n")
	var sent []struct {
		Evidence, Candidates, Conflicts json.RawMessage
	}
	if err := json.Unmarshal([]byte(content), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || string(sent[0].Evidence) != string(item.Evidence) ||
		string(sent[0].Candidates) != string(item.Candidates) || string(sent[0].Conflicts) != string(item.Conflicts) {
		t.Fatalf("本地信息、冲突和候选应原样进入请求，实际 %s", content)
	}
}
