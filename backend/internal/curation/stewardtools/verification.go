package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (a adapter) verificationTool(allowed availability) tool.Definition {
	return tool.Definition{Name: "submit_verification", Description: "判断当前署名、艺人身份或真实名字差异。相同身份选 same，确切错字选 typo，亦可提交不同、写法差异、译名差异或无法判断。", Scope: tool.ScopeShared, MinRole: tool.RoleAdmin, Available: allowed, Revertible: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"verdict":{"type":"string","enum":["same","different","typo","style","translation","uncertain"]},"reason":` + noteSchema + `,"citation":{"type":"object","additionalProperties":false,"properties":{"url":` + urlSchema + `,"quote":{"type":"string","minLength":1,"maxLength":2000}},"required":["url","quote"]}},"required":["item_id","verdict","reason"]}`), Output: json.RawMessage(`{"type":"object"}`), Preview: a.previewVerification, Handler: a.submitVerification}
}

type verificationSubmission struct {
	run     *curation.ModelRun
	item    curation.PendingItem
	in      curation.VerificationInput
	plan    curation.VerificationPlan
	preview tool.Preview
	route   bool
	stale   bool
	blocked bool
}

func (a adapter) prepareVerification(ctx context.Context, c tool.Caller, raw json.RawMessage) (v verificationSubmission, err error) {
	if err = json.Unmarshal(raw, &v.in); err != nil || strings.TrimSpace(v.in.Reason) == "" {
		return v, invalid()
	}
	var rows []curation.PendingItem
	v.run, rows, err = a.claimed(ctx, c)
	if err != nil {
		return v, err
	}
	v.item, err = selectItem(rows, v.in.ItemID, false)
	if err != nil {
		return v, err
	}
	if v.run.PendingItemID != "" && v.run.PendingItemID != v.item.ID {
		return v, outOfBatch()
	}
	identity := v.item.Kind == "verify_artist_merge" || v.item.Kind == "verify_artist_credit"
	if !identity && v.item.Kind != "verify_name" {
		return v, invalid()
	}
	positive := v.in.Verdict == "typo" && !identity || v.in.Verdict == "same" && identity
	if !positive && !(v.in.Verdict == "different" && identity || !identity && (v.in.Verdict == "style" || v.in.Verdict == "translation") || v.in.Verdict == "uncertain") {
		return v, invalid()
	}
	v.plan, err = a.matcher.VerificationPlan(ctx, v.item, v.in)
	var failure *operations.Failure
	if errors.As(err, &failure) {
		switch failure.Code {
		case "no_evidence":
			v.route = positive || v.in.Verdict == "uncertain"
			err = nil
		case "no_strong_evidence", "manual_structure", "nothing_to_apply":
			v.blocked = true
			v.in.Reason = failure.Message
			err = nil
		case "item_stale":
			v.stale = true
			err = nil
		}
	}
	if err != nil {
		return v, toolError(err)
	}
	var facts any = v.plan
	if v.plan.NameMatch != nil {
		facts = struct {
			Source string                    `json:"source"`
			Data   curation.VerificationPlan `json:"data"`
		}{"backend_verified_musicbrainz", v.plan}
	}
	proof, err := json.Marshal(facts)
	if err != nil {
		return v, err
	}
	candidate := v.plan.Candidate
	v.preview = tool.Preview{Summary: "核对名字与身份", Target: candidate.Before, BackgroundDirect: !positive || v.route || v.stale || v.blocked, Verification: proof}
	if candidate.Field != "" {
		v.preview.Changes = []tool.Change{{Field: candidate.Field, Before: candidate.Before, After: candidate.After}}
	}
	return v, nil
}
func (a adapter) previewVerification(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
	v, err := a.prepareVerification(ctx, c, raw)
	return v.preview, err
}
func (a adapter) submitVerification(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	v, err := a.prepareVerification(ctx, c, raw)
	if err != nil {
		return nil, err
	}
	ctx = bindItem(ctx, v.run, c, v.item)
	if v.stale {
		return a.retryStaleVerification(ctx, v.item)
	}
	if v.route {
		if v.run.Abandon == nil {
			return nil, outOfBatch()
		}
		err = v.run.Abandon(ctx, v.item.ID, v.in.Reason)
		return json.RawMessage(`{"status":"handled"}`), toolError(err)
	}
	if v.preview.BackgroundDirect {
		suggestionTool := "submit_verification"
		if v.blocked || v.in.Verdict == "different" || v.in.Verdict == "style" || v.in.Verdict == "translation" {
			// 采纳“不同身份”或“保留原写法”不能反过来执行合并或改名。
			suggestionTool = "observation"
			v.preview.Changes = []tool.Change{}
		}
		preview, err := json.Marshal(v.preview)
		if err != nil {
			return nil, err
		}
		id, err := a.writer.RecordSuggestion(ctx, curation.ModelException{Tool: suggestionTool, Input: raw, Preview: preview, Evidence: v.preview.Verification, PendingItemID: v.item.ID, ConversationID: &v.run.ConversationID, Provider: v.run.Model, ReasonCode: "verification_declined", Reasons: []string{v.in.Reason}})
		if err != nil {
			return nil, toolError(err)
		}
		return json.Marshal(map[string]string{"suggestion_id": id})
	}
	if c.ReviewID == "" || c.ReviewDecision != "approved" {
		return nil, &tool.Error{Code: tool.CodePermissionDenied, Message: "这项修改需要独立审查"}
	}
	batch, err := v.run.Batch(ctx)
	if err != nil {
		return nil, toolError(err)
	}
	id, err := a.writer.ApplyVerification(ctx, v.item, v.plan, batch, c.IdempotencyKey, v.run.Model)
	var failure *operations.Failure
	if errors.As(err, &failure) && failure.Code == "item_stale" {
		return a.retryStaleVerification(ctx, v.item)
	}
	if err != nil {
		return nil, toolError(err)
	}
	return json.Marshal(map[string]string{"operation_id": id})
}

func (a adapter) retryStaleVerification(ctx context.Context, item curation.PendingItem) (json.RawMessage, error) {
	requeued, err := a.matcher.RetryStaleVerification(ctx, item)
	if err != nil {
		return nil, toolError(err)
	}
	if requeued {
		return json.RawMessage(`{"status":"requeued"}`), nil
	}
	return json.RawMessage(`{"status":"stale"}`), nil
}
