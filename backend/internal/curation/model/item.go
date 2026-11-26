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
	case p.Kind == "name_patrol" || p.Kind == "local_cleanup":
		return PatrolAndLocal
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

func rulesKind(kind TaskKind, p curation.PendingItem) bool {
	return kind == SelectCandidate || kind == VerifyEvidence || p.Kind == "local_cleanup"
}

func itemLimits(kind TaskKind) (calls int, tokens int64) {
	switch kind {
	case LookupCandidate:
		return 20, 150000
	case WebFallback:
		return 30, 300000
	}
	return 0, 0
}

func itemLimit(kind TaskKind, calls int, tokens int64) bool {
	maxCalls, maxTokens := itemLimits(kind)
	return maxCalls > 0 && (calls > maxCalls || tokens > maxTokens)
}

func itemAtLimit(kind TaskKind, calls int, tokens int64) bool {
	maxCalls, maxTokens := itemLimits(kind)
	return maxCalls > 0 && (calls >= maxCalls || tokens >= maxTokens)
}

// 准备好的证据和实际发送的候选保留在该项副本中。
func (s *Scheduler) prepareItem(ctx context.Context, matcher *program.ModelMatcher, p *curation.PendingItem) error {
	if p.Kind == "local_cleanup" {
		return nil
	}
	items := []curation.PendingItem{*p}
	if taskKind(*p) == SelectCandidate {
		if err := matcher.PrepareSelection(ctx, items); err != nil {
			return err
		}
		*p = items[0]
		return nil
	}
	if err := matcher.AnnotatePendingEvidence(ctx, items); err != nil {
		return err
	}
	if err := matcher.AddCanonicalNames(ctx, items); err != nil {
		return err
	}
	*p = items[0]
	if !strings.HasPrefix(p.Kind, "verify_") {
		return nil
	}
	if p.Kind == "verify_name" {
		entries, err := s.writer.LoadCurationEvidence(ctx, []string{p.TargetID})
		if err != nil {
			return err
		}
		if e, ok := entries[p.TargetID]; ok && program.IdentityDigest(e) == p.IdentityDigest {
			p.PreparedLocal = program.PendingEvidence(e)
		}
	}
	proof, err := matcher.VerificationPlan(ctx, *p, curation.VerificationInput{})
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) {
			switch failure.Code {
			case "item_stale", "no_evidence", "no_strong_evidence", "manual_structure", "nothing_to_apply", "source_disabled":
				return nil
			}
		}
		return err
	}
	p.PreparedVerification = &proof
	return nil
}

func (s *Scheduler) taskMessage(ctx context.Context, items []curation.PendingItem) (string, error) {
	matcher := program.NewModelMatcher(s.db, s.writer, s.sources)
	for i := range items {
		if err := s.prepareItem(ctx, matcher, &items[i]); err != nil {
			return "", err
		}
	}
	return firstMessage(items)
}

func firstMessage(items []curation.PendingItem) (string, error) {
	type messageItem struct {
		ID              string                     `json:"item_id"`
		Kind            string                     `json:"kind"`
		Route           TaskKind                   `json:"route"`
		ReleaseID       string                     `json:"release_id,omitempty"`
		ArtistID        string                     `json:"artist_id,omitempty"`
		Evidence        json.RawMessage            `json:"evidence"`
		Candidates      json.RawMessage            `json:"candidates,omitempty"`
		Conflicts       json.RawMessage            `json:"conflicts,omitempty"`
		CanonicalTitle  string                     `json:"canonical_title,omitempty"`
		CanonicalArtist string                     `json:"canonical_artist,omitempty"`
		Requirements    string                     `json:"write_requirements"`
		Local           map[string]any             `json:"local,omitempty"`
		Verification    *curation.VerificationPlan `json:"verification,omitempty"`
	}
	rows := make([]messageItem, 0, len(items))
	for _, p := range items {
		if p.Kind == "local_cleanup" {
			var source map[string]json.RawMessage
			if err := json.Unmarshal(p.Evidence, &source); err != nil {
				return "", err
			}
			evidence := map[string]json.RawMessage{}
			for _, key := range []string{"directory_key", "release_ids", "directories", "releases", "grouping_requested"} {
				if v, ok := source[key]; ok {
					evidence[key] = v
				}
			}
			raw, err := json.Marshal(evidence)
			if err != nil {
				return "", err
			}
			rows = append(rows, messageItem{ID: p.ID, Kind: p.Kind, Route: taskKind(p), ReleaseID: p.TargetID, Evidence: raw, Requirements: writeRequirements(p)})
			continue
		}
		rows = append(rows, messageItem{
			ID: p.ID, Kind: p.Kind, Route: taskKind(p), ReleaseID: p.TargetID, ArtistID: p.ArtistID,
			Evidence: p.Evidence, Candidates: p.Candidates, Conflicts: p.Conflicts,
			CanonicalTitle: p.CanonicalTitle, CanonicalArtist: p.CanonicalArtist,
			Requirements: writeRequirements(p), Local: p.PreparedLocal, Verification: p.PreparedVerification,
		})
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return "<roomusic-source type=\"curation_pending\" trust=\"untrusted\">\n" + string(data) + "\n</roomusic-source>", nil
}

func writeRequirements(p curation.PendingItem) string {
	switch p.Kind {
	case "release_match", "recording_match":
		return "提交本项核对过的 MusicBrainz 编号。核对逐碟位置、曲名、时长和真实署名，署名可用登记别名、巡检规范名核实。完整专辑直接写入须超过四首、有可比时长且全部在容差内，短专辑须独立审查。有反证时须有独立标识。同组多版结构吻合可提交任一吻合发行，只写组级对应。子集须逐首以曲名和时长一一配对、至少四首且达到外部一半，超集须完整保留外部曲序前缀。部分吻合只接受唯一发行组。年份、厂牌、目录号、条码以外部版本实际值为准；只有具体建议时用 shelve_pending_item 的 fact。"
	case "verify_name":
		return "错字须有已确认结构和外部字段或独立库内多数写法，并经审查。专辑标题可由生效的组级对应与组标题佐证，须核对同组各版标题均与原值不同。"
	case "local_cleanup":
		return "根据本项完整目录，仅提交可核对的文字、候选编码、署名分段或文件归组。"
	case "name_patrol":
		return "依据本块提供的原名和上下文提交巡检判断。"
	case "release_merge":
		return "发行并入须有生效的版本或录音对应与完整结构证据。"
	default:
		return "身份须有准确外部对象的署名或别名、库内独立署名，或本项实际获取且正文能核实的网页引用。"
	}
}

func itemIDs(items []curation.PendingItem) []string {
	ids := make([]string, len(items))
	for i, p := range items {
		ids[i] = p.ID
	}
	return ids
}

type sourceFailures struct {
	count    int
	recovery time.Time
}

func (f *sourceFailures) record(recovery time.Time) {
	f.count++
	if recovery.After(f.recovery) {
		f.recovery = recovery
	}
}

func (f sourceFailures) retryAt(now time.Time, previous int) time.Time {
	if f.count == 0 {
		return time.Time{}
	}
	return curation.NextSourceRetry(now, f.recovery, previous)
}

func (s *Scheduler) deferSourceItem(ctx context.Context, p curation.PendingItem, conversation string, retryAt time.Time) error {
	kind := taskKind(p)
	// 普通执行允许刚好用完本档额度。来源重试要在领取前留出完整的新一轮额度。
	atLimit := itemAtLimit(kind, p.ItemToolCalls, p.ItemTokens)
	return s.writer.DeferModelItem(ctx, p, conversation, string(kind), retryAt, atLimit)
}

func (s *Scheduler) abandon(ctx context.Context, p curation.PendingItem, model, conversation, reason string, retryAt time.Time) error {
	if run, ok := curation.ModelRunFrom(ctx); ok {
		if err := s.saveRejections(ctx, run.ActorID, conversation, []curation.PendingItem{p}); err != nil {
			return err
		}
	}
	if !retryAt.IsZero() {
		return s.deferSourceItem(ctx, p, conversation, retryAt)
	}
	if rulesKind(taskKind(p), p) {
		return s.writer.FinishUndetermined(ctx, p, conversation, reason)
	}
	if taskKind(p) != WebFallback && p.Kind != "release_merge" {
		_, web, err := s.sources.MatchFallbackReadiness(ctx)
		if err != nil {
			return err
		}
		if web {
			return s.writer.AdvanceModelItem(ctx, p, conversation, string(taskKind(p)), "web", reason)
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
func (s *Scheduler) finishDisabledWeb(ctx context.Context, actor, model string, now time.Time, excluded []string) error {
	ready, err := s.writer.HasWaitingWebRoute(ctx, now, excluded)
	if err != nil || !ready {
		return err
	}
	id, err := s.launcher.CreateConversation(ctx, actor, "自动整理：联网不可用", model)
	if err != nil {
		return err
	}
	items, err := s.writer.ClaimModelKind(ctx, id, now, s.options.BatchSize, []string{"release", "artist"}, "unavailable_web", "", excluded...)
	if err != nil || len(items) == 0 {
		return errors.Join(err, s.launcher.DeleteConversation(ctx, actor, id))
	}
	for _, p := range items {
		conditions, enabler, err := s.conditions(ctx, false)
		if err != nil || enabler != actor || conditions.State != "idle" || conditions.allows(WebFallback) {
			return errors.Join(err, s.writer.ReleaseClaimed(ctx, id, false, nil))
		}
		if err := s.abandon(ctx, p, model, id, "web_unavailable", time.Time{}); err != nil {
			return errors.Join(err, s.writer.ReleaseClaimed(ctx, id, false, nil))
		}
	}
	return nil
}
