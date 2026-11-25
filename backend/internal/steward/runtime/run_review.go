package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func pendingPlan(plan Plan) Plan {
	items := make([]PlanItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		if item.Decision == "pending" {
			items = append(items, item)
		}
	}
	plan.Items = items
	return plan
}

// 一项身份只能绑定一个候选。主模型同时提交不同候选时，整组按歧义留档。
func competingArtistIdentities(plan Plan) map[string]bool {
	if !plan.Background {
		return nil
	}
	type identity struct {
		ItemID     string `json:"item_id"`
		Source     string `json:"source"`
		ExternalID string `json:"external_id"`
	}
	inputs := make(map[string]identity)
	first := make(map[string]identity)
	ambiguous := make(map[string]bool)
	for _, item := range plan.Items {
		var in identity
		if item.Tool != "resolve_artist_identity" || json.Unmarshal(item.Input, &in) != nil || in.ItemID == "" {
			continue
		}
		in.ItemID = strings.ToLower(in.ItemID)
		in.ExternalID = strings.ToLower(in.ExternalID)
		if other, ok := first[in.ItemID]; ok && in != other {
			ambiguous[in.ItemID] = true
		}
		first[in.ItemID] = in
		inputs[item.ID] = in
	}
	conflicts := make(map[string]bool)
	for id, in := range inputs {
		conflicts[id] = ambiguous[in.ItemID]
	}
	return conflicts
}

func (s *runState) reviewPlan(ctx context.Context, plan Plan) ([]provider.Message, EndReason, error) {
	store, ok := s.store.(ReviewStore)
	if !ok {
		return nil, "", ErrInvalidInput
	}
	plans, ok := s.store.(PlanStore)
	if !ok {
		return nil, "", ErrInvalidInput
	}
	model := s.params.ReviewModel
	if model == "" {
		model = s.params.Model
	}
	review := Review{ID: uuid.New(), PlanID: plan.ID, PlanDigest: plan.Digest, PolicyVersion: ReviewPolicyVersion, Model: model, Status: "completed", Items: []ReviewItem{}, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Truncate(time.Microsecond)}
	started := time.Now()
	s.reviewStarted = true
	s.sink(Event{Type: EventReviewStarted, Step: s.step, PlanID: plan.ID, Model: model})
	eligible := []PlanItem{}
	results := map[string]ReviewItem{}
	evidence := []reviewEvidence{}
	var reviewErr error
	for _, item := range plan.Items {
		if item.Decision != "pending" {
			continue
		}
		if policy := reviewPolicy(plan, item); policy != nil {
			results[item.ID] = *policy
			continue
		}
		facts, err := s.reviewEvidence(ctx, []PlanItem{item})
		if ctx.Err() != nil {
			return nil, EndCancelled, nil
		}
		if errors.Is(err, errStructuralEvidence) || errors.Is(err, ErrReviewEvidenceUnsupported) {
			reason := errStructuralEvidence.Error()
			if errors.Is(err, ErrReviewEvidenceUnsupported) {
				reason = "工具未登记后端证据读取方式，需要人工确认"
			}
			if plan.Background {
				reason = strings.TrimSuffix(reason, "，需要人工确认")
			}
			results[item.ID] = ReviewItem{ItemID: item.ID, Decision: "needs_human_confirmation", Risk: "high", Reasons: []string{reason}, Source: "policy"}
			continue
		}
		if err != nil {
			var failure *tool.Error
			if !errors.Is(err, ErrReviewUnavailable) && (!errors.As(err, &failure) || databaseUnavailable(err)) {
				return nil, "", err
			}
			reviewErr = ErrReviewUnavailable
			break
		}
		eligible = append(eligible, item)
		evidence = append(evidence, facts...)
	}
	if reviewErr == nil {
		review.EvidenceDigest, reviewErr = digestJSON(evidence)
	}
	if reviewErr == nil && len(eligible) > 0 {
		if s.params.OnToolCalls != nil {
			if end, err := s.beforeStep(ctx); err != nil || end.Reason != "" {
				return nil, end.Reason, err
			}
		}
		var returned []ReviewItem
		returned, review.Usage, reviewErr = callReview(ctx, s.params.Provider, model, eligible, evidence, plan.Background)
		s.reportUsage(ctx, UsageReview, review.Usage)
		for _, item := range returned {
			if s.params.Background && item.Decision == "approved" {
				for _, check := range evidence {
					if check.ItemID == item.ItemID && check.contradicted {
						item.Decision, item.Source = "rejected", "policy"
						item.Reasons = append(item.Reasons, "后端核对发现明确反证，缺少能解释该反证的独立依据")
						break
					}
				}
			}
			results[item.ItemID] = item
		}
	}
	if ctx.Err() != nil {
		return nil, EndCancelled, nil
	}
	if reviewErr == nil {
		competing := competingArtistIdentities(plan)
		for _, item := range plan.Items {
			if item.Decision != "pending" {
				continue
			}
			result := results[item.ID]
			if policy := reviewPolicy(plan, item); policy != nil {
				result = *policy
			} else if competing[item.ID] && result.Decision == "approved" {
				result.Decision, result.Source = "needs_human_confirmation", "policy"
				result.Reasons = append(result.Reasons, "同一艺人项同时提出不同身份，候选存在歧义")
			} else if result.Decision == "approved" && result.Risk == "high" {
				result.Decision = "needs_human_confirmation"
				result.Source = "policy"
				reason := "高风险批准需要人工确认"
				if plan.Background {
					reason = "高风险修改未采用"
				}
				result.Reasons = append(result.Reasons, reason)
			}
			review.Items = append(review.Items, result)
		}
	} else {
		review.Status = "unavailable"
		review.Items = []ReviewItem{}
	}
	cleanup, cancel := context.WithTimeout(ctx, finalizeTimeout)
	err := store.SaveReview(cleanup, s.params.Caller.User.ID, s.params.ConversationID, review)
	cancel()
	if ctx.Err() != nil {
		return nil, EndCancelled, nil
	}
	if err != nil {
		return nil, "", err
	}
	counts := map[string]int{}
	for _, item := range review.Items {
		counts[item.Decision]++
	}
	logger := s.logger
	if reviewErr != nil {
		logger = logger.With("reason_category", reviewReasonCategory(reviewErr))
	}
	logger.InfoContext(context.WithoutCancel(ctx), "管家审查结束", "event", "steward.review.finished", "review_id", review.ID, "plan_id", plan.ID, "model", model, "duration_ms", time.Since(started).Milliseconds(), "status", review.Status, "input_tokens", review.Usage.InputTokens, "output_tokens", review.Usage.OutputTokens, "approved", counts["approved"], "rejected", counts["rejected"], "needs_human_confirmation", counts["needs_human_confirmation"])
	s.sink(Event{Type: EventReviewFinished, Step: s.step, Review: &review})
	if s.params.OnToolCalls != nil {
		if end, err := s.beforeStep(ctx); err != nil || end.Reason != "" {
			return nil, end.Reason, err
		}
	}
	if review.Status == "unavailable" {
		return nil, EndReviewUnavailable, nil
	}
	// 先核对整份方案和审查结论；证据只在对应批准项执行前重读。
	if err = s.checkReviewSnapshot(ctx, plan, review, nil, nil); err != nil {
		if ctx.Err() != nil {
			return nil, EndCancelled, nil
		}
		if errors.Is(err, ErrReviewUnavailable) {
			return nil, EndReviewUnavailable, nil
		}
		return nil, "", err
	}
	for _, result := range review.Items {
		if !s.params.Background && result.Decision == "needs_human_confirmation" {
			continue
		}
		if ctx.Err() != nil {
			return nil, EndCancelled, nil
		}
		var item PlanItem
		for _, candidate := range plan.Items {
			if candidate.ID == result.ItemID {
				item = candidate
				break
			}
		}
		if result.Decision == "approved" {
			var bound []reviewEvidence
			for _, snapshot := range evidence {
				if snapshot.ItemID == item.ID {
					bound = append(bound, snapshot)
					break
				}
			}
			if err = s.checkReviewSnapshot(ctx, plan, review, []PlanItem{item}, bound); err != nil {
				if ctx.Err() != nil {
					return nil, EndCancelled, nil
				}
				if errors.Is(err, ErrReviewUnavailable) {
					return nil, EndReviewUnavailable, nil
				}
				return nil, "", err
			}
			item.Decision = "accepted"
		} else {
			item.Decision = "rejected_by_review"
			if s.params.Background {
				if s.params.Escalate == nil {
					return nil, "", ErrInvalidInput
				}
				item.ExceptionID, err = s.params.Escalate(ctx, item, review, result)
				if err != nil {
					return nil, "", err
				}
			}
		}
		item.ReviewID = review.ID
		item.ReviewDecision = result.Decision
		item.ReviewReasons = result.Reasons
		if err = s.executePlanItems(ctx, plans, []PlanItem{item}); err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil, EndCancelled, nil
			}
			return nil, "", err
		}
	}
	detail, err := s.store.GetConversation(ctx, s.params.Caller.User.ID, s.params.ConversationID)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil, EndCancelled, nil
		}
		return nil, "", err
	}
	for _, current := range detail.Plans {
		if current.ID == plan.ID && current.Status == "pending" {
			p := pendingPlan(current)
			s.sink(Event{Type: EventPlan, Step: s.step, Plan: &p})
			return nil, EndAwaitingConfirmation, nil
		}
	}
	// 从已持久化结果重建后续主对话，保持工具结果顺序和来源包装。
	return nil, "", nil
}

func (s *runState) checkReviewSnapshot(ctx context.Context, original Plan, review Review, remaining []PlanItem, evidence []reviewEvidence) error {
	if review.Status != "completed" || review.PolicyVersion != ReviewPolicyVersion || !time.Now().Before(review.ExpiresAt) {
		return ErrReviewUnavailable
	}
	detail, err := s.store.GetConversation(ctx, s.params.Caller.User.ID, s.params.ConversationID)
	if err != nil {
		return err
	}
	var current *Plan
	for i := range detail.Plans {
		if detail.Plans[i].ID == original.ID {
			current = &detail.Plans[i]
			break
		}
	}
	if current == nil || current.Status != "pending" || !time.Now().Before(current.ExpiresAt) {
		return ErrReviewUnavailable
	}
	digest, err := planDigest(current.Items)
	if err != nil {
		return err
	}
	if digest != review.PlanDigest || current.Digest != review.PlanDigest || current.Review == nil || current.Review.ID != review.ID || current.Review.EvidenceDigest != review.EvidenceDigest || current.Review.PlanDigest != review.PlanDigest || current.Review.Status != "completed" || current.Review.PolicyVersion != ReviewPolicyVersion || current.Review.Model != review.Model || !current.Review.ExpiresAt.Equal(review.ExpiresAt) || !time.Now().Before(current.Review.ExpiresAt) {
		return ErrReviewUnavailable
	}
	persistedItems, err := digestJSON(current.Review.Items)
	if err != nil {
		return err
	}
	approvedItems, err := digestJSON(review.Items)
	if err != nil {
		return err
	}
	if persistedItems != approvedItems {
		return ErrReviewUnavailable
	}
	if len(remaining) == 0 {
		return nil
	}
	// 使用重新读取的工具、参数、可撤销性；不以调用前的结构代替当前方案。
	fresh := make([]PlanItem, 0, len(remaining))
	for _, old := range remaining {
		found := false
		for _, item := range current.Items {
			if item.ID == old.ID && item.Decision == "pending" && reviewPolicy(*current, item) == nil {
				fresh = append(fresh, item)
				found = true
				break
			}
		}
		if !found {
			return ErrReviewUnavailable
		}
	}
	actual, err := s.reviewEvidence(ctx, fresh)
	if err != nil {
		var failure *tool.Error
		if errors.Is(err, errStructuralEvidence) || errors.Is(err, ErrReviewEvidenceUnsupported) || (errors.As(err, &failure) && !databaseUnavailable(err)) {
			return ErrReviewUnavailable
		}
		return err
	}
	got, err := digestJSON(actual)
	if err != nil {
		return err
	}
	want, err := digestJSON(evidence)
	if err != nil {
		return err
	}
	if got != want {
		return ErrReviewUnavailable
	}
	return nil
}
