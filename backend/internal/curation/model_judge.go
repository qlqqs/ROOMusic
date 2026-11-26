package curation

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

type JudgeName struct {
	Release  string `json:"release,omitempty"`
	Artist   string `json:"artist,omitempty"`
	Track    string `json:"track,omitempty"`
	Composer string `json:"composer,omitempty"`
	Work     string `json:"work,omitempty"`
}

type JudgeGroup struct {
	Name       JudgeName `json:"name,omitempty"`
	ArtistID   string    `json:"artist_id,omitempty"`
	Identifier struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"identifier,omitempty"`
	Version struct {
		Year    string `json:"year,omitempty"`
		Country string `json:"country,omitempty"`
		Edition string `json:"edition,omitempty"`
		Medium  string `json:"medium,omitempty"`
	} `json:"version,omitempty"`
	AlbumType string `json:"album_type,omitempty"`
}

type JudgeAnswer struct {
	ItemID     string       `json:"item_id"`
	Decision   string       `json:"decision"`
	SelectedID string       `json:"selected_id,omitempty"`
	Groups     []JudgeGroup `json:"groups,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

type JudgeCursor struct {
	ItemID       string `json:"item_id"`
	Files        string `json:"files"`
	Generation   int    `json:"generation"`
	Model        string `json:"model"`
	Conversation string `json:"conversation"`
	Version      int16  `json:"version"`
	Next         int    `json:"next"`
	Used         int    `json:"used"`
	Status       string `json:"status"`
}

// 持久化模型答案和程序断点，在同一事务释放模型领取。
func (w *Writer) SubmitJudge(ctx context.Context, p PendingItem, answer JudgeAnswer) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = CheckMatchItem(ctx, tx, p, p.ConversationID, false); err != nil {
		return err
	}
	raw, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET evidence=evidence || jsonb_build_object('judge',$2::jsonb) WHERE id=$1`, p.ID, raw); err != nil {
		return err
	}
	run, _ := ModelRunFrom(ctx)
	model := ""
	if run != nil {
		model = run.Model
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_work_states SET result=result || jsonb_build_object('judge',jsonb_build_object(
 'item_id',$2::text,'files',file_digest,'model',$5::text,'conversation',$6::text,'generation',generation,'version',match_rule_version,'next',-1,'used',0,'status',status)),
 needs_work=true,updated_at=now() WHERE subject='release' AND target_id=$1 AND status IN ('unmatched','conflict')
 AND input_digest=$3 AND identity_digest=$4 AND file_digest=curation_release_file_digest(target_id)
 AND target_id IN (SELECT id FROM curation_eligible_releases)`, p.TargetID, p.ID, p.InputDigest, p.IdentityDigest, model, p.ConversationID)
	if err = requireChanged(r, err, "item_stale", "本地证据已变化"); err != nil {
		return err
	}
	if err = ResolvePendingItem(ctx, tx, p.ID, p.ConversationID, "judge_submitted"); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) FinishIdentityNegative(ctx context.Context, p PendingItem, reason, model string) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = CheckMatchItem(ctx, tx, p, p.ConversationID, false); err != nil {
		return err
	}
	var pair VerificationCandidate
	if p.Kind == "verify_artist_credit" {
		pair, err = creditCandidate(ctx, tx, p, false)
	} else if p.Kind == "verify_artist_merge" {
		err = json.Unmarshal(p.Evidence, &pair)
		if err == nil {
			err = artistPairNames(ctx, tx, pair)
		}
	} else {
		return operations.Invalid("invalid_input", "艺人核对项无效")
	}
	if err != nil {
		return err
	}
	a, b := normalizedName(pair.Before), normalizedName(pair.After)
	if a == "" || b == "" {
		return staleVerification()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_artist_negative_pairs(name_low,name_high,reason,model) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, min(a, b), max(a, b), reason, model)
	if err != nil {
		return err
	}
	if err = ResolvePendingItem(ctx, tx, p.ID, p.ConversationID, "different"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='different',resolved_at=now(),next_attempt_at=NULL WHERE id<>$1 AND kind IN ('verify_artist_credit','verify_artist_merge') AND status<>'resolved' AND evidence->>'pair_low'=$2 AND evidence->>'pair_high'=$3`, p.ID, min(a, b), max(a, b))
	if err != nil {
		return err
	}
	return tx.Commit()
}
