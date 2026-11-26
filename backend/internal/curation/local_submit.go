package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type LocalText struct {
	Target string `json:"target_id"`
	Field  string `json:"field"`
	Value  string `json:"value"`
}
type LocalEncoding struct {
	Release  string `json:"release_id"`
	Encoding string `json:"encoding"`
}
type LocalSplit struct {
	Target string   `json:"target_id"`
	Role   string   `json:"role"`
	Ref    string   `json:"source_ref"`
	Parts  []string `json:"parts"`
	Joins  []string `json:"joins"`
}

func (v LocalSplit) raw() string {
	var b strings.Builder
	for i, part := range v.Parts {
		if i > 0 {
			b.WriteString(v.Joins[i-1])
		}
		b.WriteString(part)
	}
	return b.String()
}
func (v LocalSplit) splitKey() string { return operations.Fingerprint([]any{v.Parts, v.Joins}) }

type LocalPosition struct {
	TrackID string `json:"track_id,omitempty"`
	Disc    int    `json:"disc"`
	Track   int    `json:"track"`
}
type LocalGroupFile struct {
	FileID string `json:"file_id"`
	LocalPosition
	Tracks []LocalPosition `json:"tracks,omitempty"`
}
type LocalGroup struct {
	Albums []struct {
		Files []LocalGroupFile `json:"files"`
	} `json:"albums"`
}
type LocalInput struct {
	ItemID    string          `json:"item_id"`
	Decision  string          `json:"decision"`
	Reason    string          `json:"reason,omitempty"`
	Texts     []LocalText     `json:"texts,omitempty"`
	Encodings []LocalEncoding `json:"encodings,omitempty"`
	Splits    []LocalSplit    `json:"splits,omitempty"`
	Grouping  *LocalGroup     `json:"grouping,omitempty"`
}
type localTarget struct {
	Release string                              `json:"release_id"`
	Track   string                              `json:"track,omitempty"`
	Fields  map[string]catalog.CorrectionIntent `json:"fields"`
	Splits  []LocalSplit                        `json:"splits,omitempty"`
}
type localPlan struct {
	Provider string           `json:"provider"`
	Targets  []localTarget    `json:"targets"`
	Grouping []groupPlacement `json:"grouping,omitempty"`
}
type localSubmission struct{ item PendingItem }

func (s localSubmission) read(ctx context.Context, tx *sql.Tx) (localAlbum, error) {
	scopes, err := localScopes(ctx, tx, []string{s.item.TargetID}, nil, nil)
	if err != nil {
		return localAlbum{}, err
	}
	for _, a := range scopes {
		if operations.Fingerprint(a) != s.item.InputDigest {
			continue
		}
		var ready bool
		err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT FROM scan_runs WHERE status='running') AND NOT EXISTS(SELECT unnest($1::uuid[]) EXCEPT SELECT id FROM curation_stable_releases)`, a.ReleaseIDs).Scan(&ready)
		if err != nil {
			return localAlbum{}, err
		}
		if ready {
			return a, nil
		}
	}
	return localAlbum{}, operations.Conflict("item_stale", "本地内容、署名或分组已变化")
}
func localTextKey(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, textnorm.Text(value))), " ")
}
func trackFragments(t localTrack) []string {
	parts := strings.Split(t.Path, "/")
	for _, o := range t.Observations {
		if !o.Inferred && o.Source != "garbled" {
			parts = append(parts, o.Value)
		}
	}
	for _, c := range t.ScanTags.Comments {
		parts = append(parts, c.Value)
	}
	for _, credit := range t.Credits {
		if t.Observations["artist"].Source != "garbled" && t.Observations["album_artist"].Source != "garbled" {
			parts = append(parts, credit.RawValue)
		}
	}
	return parts
}
func validLocalText(v string, fragments []string) bool {
	key := localTextKey(v)
	return key != "" && slices.ContainsFunc(fragments, func(s string) bool { return strings.Contains(localTextKey(s), key) })
}
func (r localRelease) checkedTargets(in LocalInput) []localTarget {
	targets := map[string]*localTarget{}
	fragments := map[string][]string{}
	allowed := map[string][]string{r.ID: r.Fields}
	garbled := map[string]map[string]bool{}
	set := func(id, field, value string) {
		t := targets[id]
		if t == nil {
			t = &localTarget{Release: r.ID, Fields: map[string]catalog.CorrectionIntent{}}
			if id != r.ID {
				t.Track = id
			}
			targets[id] = t
		}
		if old, ok := t.Fields[field]; ok && old.Value != value {
			t.Fields[field] = catalog.CorrectionIntent{}
			return
		}
		if value != "" {
			t.Fields[field] = catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: value}
		}
	}
	garbled[r.ID] = map[string]bool{}
	for _, t := range r.Tracks {
		fr := trackFragments(t)
		fragments[t.ID] = fr
		fragments[r.ID] = append(fragments[r.ID], fr...)
		allowed[t.ID] = t.Fields
		garbled[t.ID] = map[string]bool{"title": t.Observations["title"].Source == "garbled", "artist": t.Observations["artist"].Source == "garbled"}
		for tag, field := range map[string]string{"album": "title", "album_artist": "album_artist"} {
			if t.Observations[tag].Source == "garbled" || field == "album_artist" && t.Observations["artist"].Source == "garbled" && t.Observations["artist"].Value == r.Artist {
				garbled[r.ID][field] = true
			}
		}
	}
	for _, v := range in.Texts {
		if !slices.Contains(allowed[v.Target], v.Field) || garbled[v.Target][v.Field] || !validLocalText(v.Value, fragments[v.Target]) {
			continue
		}
		normalized := governance.NormalizeTrackAutoFields
		if v.Target == r.ID {
			normalized = governance.NormalizeAutoFields
		}
		fields, err := normalized(map[string]catalog.CorrectionIntent{v.Field: {Mode: catalog.CorrectionSet, Value: v.Value}})
		if err == nil {
			set(v.Target, v.Field, fields[v.Field].Value.(string))
		}
	}
	var encs []string
	for _, v := range in.Encodings {
		if v.Release == r.ID {
			encs = append(encs, v.Encoding)
		}
	}
	if len(encs) == 1 && slices.Contains(r.Encodings, encs[0]) {
		for _, t := range r.Tracks {
			for f, v := range t.Readings[encs[0]] {
				target, field := t.ID, f
				switch f {
				case "album":
					target, field = r.ID, "title"
				case "album_artist":
					target, field = r.ID, "album_artist"
				}
				if target == r.ID {
					selected := r.Title
					if field == "album_artist" {
						selected = r.Artist
					}
					if strings.TrimSpace(t.Observations[f].Value) != selected {
						continue
					}
				}
				if slices.Contains(allowed[target], field) {
					set(target, field, v)
				}
				if f == "artist" && slices.Contains(r.Fields, "album_artist") && t.Observations["artist"].Value == r.Artist {
					set(r.ID, "album_artist", v)
				}
			}
		}
	}
	// 相同原署名在多条标签出现时，分段必须一致。
	type spelling struct{ target, raw string }
	splitChoices := map[spelling]string{}
	var checked []LocalSplit
	for _, c := range r.Credits {
		var matches []LocalSplit
		for _, v := range in.Splits {
			if v.Target == c.Target && v.Role == c.Role && v.Ref == c.Ref {
				matches = append(matches, v)
			}
		}
		if len(matches) != 1 {
			continue
		}
		v := matches[0]
		if len(v.Parts) < 2 || len(v.Parts) > 100 || len(v.Joins)+1 != len(v.Parts) || slices.ContainsFunc(v.Parts, func(s string) bool { return strings.TrimSpace(s) == "" }) {
			continue
		}
		if v.raw() != c.Raw {
			continue
		}
		key := spelling{c.Target, c.Raw}
		hash := v.splitKey()
		if prior, ok := splitChoices[key]; ok && prior != hash {
			splitChoices[key] = ""
		} else {
			splitChoices[key] = hash
		}
		checked = append(checked, v)
	}
	for _, v := range checked {
		c := localCredit{Target: v.Target, Raw: v.raw()}
		if splitChoices[spelling{c.Target, c.Raw}] == "" {
			continue
		}
		field, display := "album_artist", r.Artist
		if c.Target != r.ID {
			field = "artist"
			for _, t := range r.Tracks {
				if t.ID == c.Target {
					display = t.Artist
					break
				}
			}
		}
		if display == "" {
			continue
		}
		set(c.Target, field, display)
		targets[c.Target].Splits = append(targets[c.Target].Splits, v)
	}
	var result []localTarget
	for _, id := range slices.Sorted(maps.Keys(targets)) {
		t := targets[id]
		maps.DeleteFunc(t.Fields, func(_ string, v catalog.CorrectionIntent) bool { return v.Mode != catalog.CorrectionSet })
		if len(t.Fields) > 0 {
			result = append(result, *t)
		}
	}
	return result
}

func (w *Writer) SubmitLocal(ctx context.Context, in LocalInput) error {
	run, ok := ModelRunFrom(ctx)
	if !ok || !uuid.Valid(in.ItemID) || run.TaskKind != "local" {
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
	if item.RuleVersion != LocalRuleVersion || item.Kind != "local_cleanup" || item.Status != "in_progress" || item.ConversationID != run.ConversationID || digest != item.ModelClaimDigest() {
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
	if in.Decision == "undetermined" && strings.TrimSpace(in.Reason) != "" && len(in.Texts)+len(in.Encodings)+len(in.Splits) == 0 && in.Grouping == nil {
		return w.FinishUndetermined(ctx, item, run.ConversationID, in.Reason)
	}
	if in.Decision != "apply" {
		return operations.Invalid("invalid_input", "请选择提交或说明无法判断的原因")
	}
	plan := localPlan{Provider: NormalizeProvider(run.Model), Grouping: a.checkGrouping(in.Grouping)}
	for _, r := range a.Releases {
		plan.Targets = append(plan.Targets, r.checkedTargets(in)...)
	}
	if len(plan.Targets) == 0 && len(plan.Grouping) == 0 {
		return operations.Invalid("invalid_input", "没有可由本地原文核实的结果")
	}
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
		r := Request{ReleaseID: t.Release, Fields: t.Fields, localSplits: t.Splits, local: &submission,
			Source: Source{Kind: "model", Provider: saved.Provider, Basis: "模型从本地文件名、目录名或标签推断", WriterLevel: catalog.LevelModelLocal},
			Key:    DerivedKey(item.ID, "local:"+t.Release+":"+t.Track), BatchID: batch, ConversationID: run.ConversationID}
		_, err = w.applyFields(ctx, r, t.Track, t.Track != "")
		var failure *operations.Failure
		if err != nil && !(errors.As(err, &failure) && failure.Code == "nothing_to_apply") {
			return err
		}
	}
	if len(saved.Grouping) > 0 {
		if batch == "" {
			batch, err = run.Batch(ctx)
			if err != nil {
				return err
			}
		}
		return w.writeLocalGrouping(ctx, submission, a, saved, batch, run.ConversationID)
	}
	return w.FinishModelItem(ctx, item, run.ConversationID, "local_cleanup")
}
