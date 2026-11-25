package curation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type FactualSuggestion struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type factualClaim struct {
	Change   governance.RawCorrectionChange `json:"change"`
	Provider string                         `json:"provider"`
	Reason   string                         `json:"reason"`
}

// StageFactualSuggestion 只保存主张，后续联网项可以继续查找开放库条目。
func (w *Writer) StageFactualSuggestion(ctx context.Context, item PendingItem, in FactualSuggestion, model, reason string) error {
	var value any = in.Value
	switch in.Field {
	case "year":
		year, err := strconv.Atoi(strings.TrimSpace(in.Value))
		if err != nil {
			return operations.Invalid("invalid_input", "年份无效")
		}
		value = year
	case "label", "catalog_number", "barcode":
	default:
		return operations.Invalid("invalid_input", "事实建议只接受年份、厂牌、目录号或条码")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	change := governance.RawCorrectionChange{Mode: catalog.CorrectionSet, Value: raw}
	fields, err := governance.ParseCorrectionChanges(map[string]governance.RawCorrectionChange{in.Field: change})
	if err != nil {
		return err
	}
	normalized := fields[in.Field]
	if in.Field != "year" {
		change.Value, err = json.Marshal(normalized.Text)
		if err != nil {
			return err
		}
	}
	data, err := json.Marshal(map[string]factualClaim{in.Field: {change, NormalizeProvider(model), reason}})
	if err != nil {
		return err
	}
	result, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET evidence=jsonb_set(evidence,'{unverified_facts}',
 COALESCE(evidence->'unverified_facts','{}'::jsonb) || $5::jsonb), updated_at=now()
 WHERE id=$1 AND conversation_id=$2 AND input_digest=$3 AND COALESCE(identity_digest,'')=$4 AND status='in_progress'
 AND kind IN ('release_match','recording_match') AND route IN ('lookup','web')`,
		item.ID, item.ConversationID, item.InputDigest, item.IdentityDigest, data)
	return requireChanged(result, err, "item_stale", "事实建议的识别项已变更")
}

// FinishFactualSuggestions 在识别结论的事务里记下仍无外部值的主张，不写自动层。
func FinishFactualSuggestions(ctx context.Context, tx *sql.Tx, itemID, conversation string) error {
	var target, digest string
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT target_id::text,input_digest,evidence->'unverified_facts'
 FROM curation_pending_items WHERE id=$1 AND conversation_id=$2 AND evidence ? 'unverified_facts' AND kind IN ('release_match','recording_match') FOR UPDATE`, itemID, conversation).Scan(&target, &digest, &data)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var claims map[string]factualClaim
	if err = json.Unmarshal(data, &claims); err != nil {
		return err
	}
	state, err := governance.NewStore(tx).LoadCorrectionState(ctx, target)
	if err != nil {
		return err
	}
	for _, field := range state.Fields {
		claim, ok := claims[field.Field]
		if !ok {
			continue
		}
		before, err := json.Marshal(field.EffectiveValue)
		if err != nil {
			return err
		}
		if bytes.Equal(before, claim.Change.Value) {
			continue
		}
		input, err := json.Marshal(map[string]any{"release_id": target, "expected_revision": state.Revision, "changes": map[string]any{field.Field: claim.Change}, "basis": map[string]string{"note": claim.Reason}})
		if err != nil {
			return err
		}
		preview, err := json.Marshal(map[string]any{"changes": []map[string]any{{"field": field.Field, "before": field.EffectiveValue, "after": claim.Change.Value}}})
		if err != nil {
			return err
		}
		e := ModelException{PendingItemID: itemID, Tool: "correct_release", Input: input, Preview: preview, Provider: claim.Provider, ReasonCode: "unverified_fact", Reasons: []string{"未能从匹配的外部条目核实该字段", claim.Reason}, ConversationID: &conversation}
		if _, err = insertSuggestion(ctx, tx, e, digest); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET evidence=evidence-'unverified_facts' WHERE id=$1`, itemID)
	return err
}
