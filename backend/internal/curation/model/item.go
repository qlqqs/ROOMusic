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
	case p.Kind == "local_cleanup":
		return LocalCleanup
	case p.ArtistWebGeneration != nil:
		return ArtistWeb
	case p.Kind == "release_match" || p.Kind == "recording_match":
		return SelectCandidate
	default:
		return VerifyEvidence
	}
}

func artistAtLimit(calls int, tokens int64) bool {
	return calls >= curation.ArtistWebCallLimit || tokens >= curation.ArtistWebTokenLimit
}

// 准备好的证据和实际发送的候选保留在该项副本中。
func (s *Scheduler) prepareItem(ctx context.Context, matcher *program.ModelMatcher, p *curation.PendingItem) error {
	if p.Kind == "local_cleanup" {
		return nil
	}
	items := []curation.PendingItem{*p}
	if err := matcher.AnnotatePendingEvidence(ctx, items); err != nil {
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
	case "artist_identity":
		return "核实本地署名，搜索网页并提交 MusicBrainz 艺人编号、依据和本项实际查到的引用。程序核对发行结构。"
	case "release_match", "recording_match":
		return "从本项候选选择，或提出名字、艺人、编号、版本、类型线索；编号必须来自本地原文。"
	case "verify_name":
		return "判断是否错字，保留译名、罗马字、版本后缀和风格差异。"
	case "local_cleanup":
		return "根据本项完整目录，仅提交可核对的文字、候选编码、署名分段或文件归组。"
	case "release_merge":
		return "发行并入须有生效的版本或录音对应与完整结构证据。"
	default:
		return "按本地署名及对应的外部艺人身份资料判断。"
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
