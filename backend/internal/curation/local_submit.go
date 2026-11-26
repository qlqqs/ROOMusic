package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type LocalInput struct {
	ItemID string `json:"item_id"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Tracks []struct {
		TrackID string `json:"track_id"`
		Title   string `json:"title"`
	} `json:"tracks,omitempty"`
}

type localTarget struct {
	Track  string                              `json:"track,omitempty"`
	Fields map[string]catalog.CorrectionIntent `json:"fields"`
}

type localPlan struct {
	Provider string        `json:"provider"`
	Targets  []localTarget `json:"targets"`
}

type localSubmission struct {
	item PendingItem
}

func (s localSubmission) read(ctx context.Context, tx *sql.Tx) (localAlbum, error) {
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_stable_releases WHERE id=$1)
 AND NOT EXISTS(SELECT FROM scan_runs WHERE status='running')`, s.item.TargetID).Scan(&ready)
	if err != nil {
		return localAlbum{}, err
	}
	if !ready {
		return localAlbum{}, operations.Conflict("item_stale", "本地整理的扫描依据不稳定")
	}
	entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{s.item.TargetID})
	if err != nil {
		return localAlbum{}, err
	}
	a, needed := localAlbumInput(entries[s.item.TargetID])
	if !needed || operations.Fingerprint(a) != s.item.InputDigest {
		return localAlbum{}, operations.Conflict("item_stale", "本地文字或可整理字段已变化")
	}
	return a, nil
}

// checkedLocalValue 使用写入入口的文字规范，再检查单个文件名、目录名或标签片段。
func checkedLocalValue(name, value string, field localField, fragments []string) (catalog.CorrectionIntent, bool) {
	if !field.Requested || value == "" {
		return catalog.CorrectionIntent{}, false
	}
	values, err := governance.NormalizeAutoFields(map[string]catalog.CorrectionIntent{
		name: {Mode: catalog.CorrectionSet, Value: value},
	})
	if err != nil {
		return catalog.CorrectionIntent{}, false
	}
	v := values[name]
	key := textnorm.Text(v.Value.(string))
	return v, key != "" && slices.ContainsFunc(fragments, func(f string) bool { return strings.Contains(f, key) })
}

func (w *Writer) SubmitLocal(ctx context.Context, in LocalInput) error {
	run, ok := ModelRunFrom(ctx)
	if !ok || !uuid.Valid(in.ItemID) || run.TaskKind != "patrol_local" {
		return operations.Conflict("out_of_batch", "缺少本地整理运行")
	}
	digest, owned := run.ClaimDigests[in.ItemID]
	if !owned || run.PendingItemID != "" && run.PendingItemID != in.ItemID {
		return operations.Conflict("out_of_batch", "待定项不属于本次运行")
	}
	item, err := w.PendingItem(ctx, in.ItemID)
	if err != nil {
		return err
	}
	if item.Kind != "local_cleanup" || item.Status != "in_progress" || item.ConversationID != run.ConversationID || digest != item.ModelClaimDigest() {
		return operations.Conflict("item_stale", "本地整理项已变化")
	}
	targetRun := *run
	targetRun.PendingItemID = item.ID
	ctx = WithModelRun(ctx, &targetRun)
	submission := localSubmission{item}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	a, err := submission.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	fragments := a.fragments()
	for i, raw := range fragments {
		fragments[i] = textnorm.Text(raw)
	}
	release := localTarget{Fields: map[string]catalog.CorrectionIntent{}}
	if v, valid := checkedLocalValue("title", in.Title, a.Title, fragments); valid {
		release.Fields["title"] = v
	}
	if v, valid := checkedLocalValue("album_artist", in.Artist, a.Artist, fragments); valid {
		release.Fields["album_artist"] = v
	}
	targets := []localTarget{release}
	titles := map[string]string{}
	for _, t := range in.Tracks {
		if first, seen := titles[t.TrackID]; seen && first != t.Title {
			titles[t.TrackID] = ""
		} else {
			titles[t.TrackID] = t.Title
		}
	}
	for _, dir := range a.Directories {
		for _, t := range dir.Tracks {
			req := localTarget{Track: t.ID, Fields: map[string]catalog.CorrectionIntent{}}
			if v, valid := checkedLocalValue("title", titles[t.ID], t.Title, fragments); valid {
				req.Fields["title"] = v
			}
			if v, valid := checkedLocalValue("album_artist", in.Artist, t.Artist, fragments); valid {
				req.Fields["artist"] = v
			}
			if len(req.Fields) > 0 {
				targets = append(targets, req)
			}
		}
	}
	// 一次提交可产生多条受治理操作。先固定已经核实的计划，重启后继续原来的字段与写入者。
	plan := localPlan{Provider: NormalizeProvider(run.Model), Targets: targets}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	err = w.db.QueryRowContext(ctx, `UPDATE curation_pending_items SET evidence=jsonb_set(evidence,'{local_submission}',COALESCE(evidence->'local_submission',$4::jsonb))
 WHERE id=$1 AND status='in_progress' AND conversation_id=$2 AND input_digest=$3
 RETURNING evidence->'local_submission'`, item.ID, run.ConversationID, item.InputDigest, raw).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return operations.Conflict("item_stale", "本地整理项已变化")
	}
	if err != nil {
		return err
	}
	var saved localPlan
	if err = json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	var batch string
	for _, t := range saved.Targets {
		if len(t.Fields) == 0 {
			continue
		}
		if batch == "" {
			batch, err = run.Batch(ctx)
			if err != nil {
				return err
			}
		}
		r := Request{ReleaseID: item.TargetID, Fields: t.Fields, local: &submission,
			Source: Source{Kind: "model", Provider: saved.Provider, Basis: "模型从本地文件名、目录名或标签推断", WriterLevel: catalog.LevelModelLocal},
			Key:    DerivedKey(item.ID, "local:"+t.Track), BatchID: batch, ConversationID: run.ConversationID}
		_, err = w.applyFields(ctx, r, t.Track, t.Track != "")
		var failure *operations.Failure
		if err != nil && !(errors.As(err, &failure) && failure.Code == "nothing_to_apply") {
			return err
		}
	}
	return w.FinishModelItem(ctx, item, run.ConversationID, "local_cleanup")
}
