package runtime

import (
	"context"
	"errors"
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
		if errors.Is(err, ErrReviewEvidenceUnsupported) {
			results[item.ID] = ReviewItem{ItemID: item.ID, Decision: "needs_human_confirmation", Risk: "high", Reasons: []string{"工具未登记后端证据读取方式，需要人工确认"}, Source: "policy"}
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
		var returned []ReviewItem
		returned, review.Usage, reviewErr = callReview(ctx, s.params.Provider, model, eligible, evidence)
		for _, item := range returned {
			results[item.ItemID] = item
		}
	}
	if ctx.Err() != nil {
		return nil, EndCancelled, nil
	}
	if reviewErr == nil {
		for _, item := range plan.Items {
			if item.Decision != "pending" {
				continue
			}
			result := results[item.ID]
			if policy := reviewPolicy(plan, item); policy != nil {
				result = *policy
			} else if result.Decision == "approved" && result.Risk == "high" {
				result.Decision = "needs_human_confirmation"
				result.Source = "policy"
				result.Reasons = append(result.Reasons, "高风险批准需要人工确认")
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
		if result.Decision == "needs_human_confirmation" {
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
		if errors.Is(err, ErrReviewEvidenceUnsupported) || (errors.As(err, &failure) && !databaseUnavailable(err)) {
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
