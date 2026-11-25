package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

var (
	ErrItemLimit     = errors.New("本项已超过用量上限")
	ErrItemCompleted = errors.New("本项已结束")
)

func taskKind(p curation.PendingItem) TaskKind {
	switch {
	case p.Route == "lookup":
		return LookupCandidate
	case p.Route == "web" || p.Kind == "artist_identity":
		return WebFallback
	case p.Kind == "release_match" || p.Kind == "recording_match":
		return SelectCandidate
	default:
		return VerifyEvidence
	}
}

func itemLimit(kind TaskKind, calls int, tokens int64) bool {
	switch kind {
	case LookupCandidate:
		return calls > 20 || tokens > 150000
	case WebFallback:
		return calls > 30 || tokens > 300000
	}
	return false
}

// 首条消息按整项拆包；不截断曲目清单。大小沿用管家消息请求的 1 MiB 边界。
const selectionMessageBytes = 1 << 20

func selectionBatchSize(items []curation.PendingItem) (int, error) {
	for n := len(items); n > 0; n-- {
		message, err := firstMessage(items[:n], nil)
		if err != nil {
			return 0, err
		}
		if len(message) <= selectionMessageBytes {
			return n, nil
		}
	}
	return 0, nil
}

func (s *Scheduler) taskMessage(ctx context.Context, items []curation.PendingItem) (string, error) {
	if len(items) != 1 || !strings.HasPrefix(items[0].Kind, "verify_") {
		return firstMessage(items, nil)
	}
	// 首次判断和独立审查使用相同的后端取证入口。此处的快照只供阅读。
	matcher := program.NewModelMatcher(s.db, s.writer, s.sources)
	proof, err := matcher.VerificationPlan(ctx, items[0], curation.VerificationInput{})
	if err != nil {
		var failure *operations.Failure
		if !errors.As(err, &failure) {
			return "", err
		}
		switch failure.Code {
		case "item_stale", "no_evidence", "no_strong_evidence", "manual_structure", "nothing_to_apply", "source_disabled":
			return firstMessage(items, nil)
		default:
			return "", err
		}
	}
	return firstMessage(items, &proof)
}

func firstMessage(items []curation.PendingItem, proof *curation.VerificationPlan) (string, error) {
	type messageItem struct {
		ID           string                     `json:"item_id"`
		Kind         string                     `json:"kind"`
		ReleaseID    string                     `json:"release_id,omitempty"`
		ArtistID     string                     `json:"artist_id,omitempty"`
		Evidence     json.RawMessage            `json:"evidence"`
		Candidates   json.RawMessage            `json:"candidates"`
		Conflicts    json.RawMessage            `json:"conflicts"`
		Verification *curation.VerificationPlan `json:"verification,omitempty"`
	}
	rows := make([]messageItem, 0, len(items))
	for _, p := range items {
		rows = append(rows, messageItem{p.ID, p.Kind, p.TargetID, p.ArtistID, p.Evidence, p.Candidates, p.Conflicts, proof})
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return "<roomusic-source type=\"curation_pending\" trust=\"untrusted\">\n" + string(data) + "\n</roomusic-source>", nil
}

func itemIDs(items []curation.PendingItem) []string {
	ids := make([]string, len(items))
	for i, p := range items {
		ids[i] = p.ID
	}
	return ids
}

func (s *Scheduler) abandon(ctx context.Context, p curation.PendingItem, model, conversation, reason string) error {
	if taskKind(p) == SelectCandidate {
		return s.writer.AdvanceModelItem(ctx, p, conversation, "lookup")
	}
	if taskKind(p) != WebFallback && p.Kind != "release_merge" {
		_, web, err := s.sources.MatchFallbackReadiness(ctx)
		if err != nil {
			return err
		}
		if web {
			return s.writer.AdvanceModelItem(ctx, p, conversation, "web")
		}
	}
	switch {
	case p.Kind == "release_match" || p.Kind == "recording_match":
		return s.writer.FinishUnlisted(ctx, p.ID, p.IdentityDigest, conversation, reason)
	case strings.HasPrefix(p.Kind, "verify_"):
		verdict := "same"
		if p.Kind == "verify_name" {
			verdict = "typo"
		}
		input, err := json.Marshal(map[string]string{"item_id": p.ID, "verdict": verdict, "reason": reason})
		if err != nil {
			return err
		}
		_, err = s.writer.RecordSuggestion(ctx, curation.ModelException{PendingItemID: p.ID, ExpectedClaimDigest: p.ModelClaimDigest(), Tool: "submit_verification", Input: input, Preview: p.Evidence, Evidence: p.Evidence, Provider: model, ConversationID: &conversation, ReasonCode: "no_evidence", Reasons: []string{reason}})
		return err
	case p.Kind == "release_merge":
		input, err := json.Marshal(map[string]any{"item_id": p.ID, "basis": map[string]string{"note": reason}})
		if err != nil {
			return err
		}
		_, err = s.writer.RecordSuggestion(ctx, curation.ModelException{PendingItemID: p.ID, ExpectedClaimDigest: p.ModelClaimDigest(), Tool: "merge_releases", Input: input, Preview: p.Candidates, Evidence: p.Evidence, Provider: model, ConversationID: &conversation, ReasonCode: "no_evidence", Reasons: []string{reason}})
		return err
	default:
		return s.writer.FinishModelItem(ctx, p, conversation, "no_result")
	}
}

func (s *Scheduler) itemCheckpoint(ctx context.Context, actor string, items []curation.PendingItem) error {
	for _, p := range items {
		point, err := s.writer.BeforeModelKind(ctx, actor, s.options.Now(), string(taskKind(p)))
		if err != nil {
			return err
		}
		if point.Reason != "" {
			return fmt.Errorf("%w: %s", ErrStopped, point.Reason)
		}
	}
	return nil
}

// 停用网络搜索后结清已转入这条路线的行，沿用正常的项锁、审计和未收录逻辑。
func (s *Scheduler) finishDisabledWeb(ctx context.Context, actor, model string, now time.Time) error {
	ready, err := s.writer.HasWaitingWebRoute(ctx, now)
	if err != nil || !ready {
		return err
	}
	id, err := s.launcher.CreateConversation(ctx, actor, "自动整理：联网不可用", model)
	if err != nil {
		return err
	}
	items, err := s.writer.ClaimModelKind(ctx, id, now, s.options.BatchSize, []string{"release", "artist"}, "unavailable_web", "")
	if err != nil || len(items) == 0 {
		return errors.Join(err, s.launcher.DeleteConversation(ctx, actor, id))
	}
	for _, p := range items {
		conditions, enabler, err := s.conditions(ctx, false)
		if err != nil || enabler != actor || conditions.State != "idle" || conditions.allows(WebFallback) {
			return errors.Join(err, s.writer.ReleaseClaimed(ctx, id, false, nil))
		}
		if err := s.abandon(ctx, p, model, id, "web_unavailable"); err != nil {
			return errors.Join(err, s.writer.ReleaseClaimed(ctx, id, false, nil))
		}
	}
	return nil
}
