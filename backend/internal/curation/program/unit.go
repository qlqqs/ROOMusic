package program

import (
	"context"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

type unit struct {
	w                      *Worker
	subject, id, provider  string
	ruleVersion            int16
	generation             int
	releaseID, inputDigest string
	releaseState           curation.WorkState
	releaseInput           *catalog.CurationEvidence
	checked                bool
	offline, programOnly   bool
	identityDigest         string
	fixedGeneration        bool
}

// 以目标 UUID 为命名空间，名称中的字段顺序固定。
func (u *unit) source() string {
	if u.provider != "" {
		return u.provider
	}
	return subjectSource(u.subject)
}

func (u *unit) key(step string) string {
	name := fmt.Sprintf("roomusic/curation/%s/%d/%d/%s", u.subject, u.ruleVersion, u.generation, step)
	return curation.DerivedKey(u.id, name)
}

func (w *Worker) closeBatch(ctx context.Context) error {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()
	return w.closeLockedBatch(ctx)
}

func (w *Worker) closeLockedBatch(ctx context.Context) error {
	id, n := w.batch.ID, w.batch.Operations
	if id == "" {
		return nil
	}
	discarded := false
	var err error
	if n == 0 {
		discarded, err = w.writer.DiscardEmptyProgramBatch(ctx, id)
	}
	if err == nil && !discarded {
		err = w.writer.CloseBatch(ctx, id)
	}
	if err != nil {
		return err
	}
	w.batch = curation.Batch{}
	return nil
}

func (w *Worker) rotateLockedBatch(ctx context.Context) error {
	if w.batch.ID != "" && (w.batch.Operations >= 500 || w.opts.Now().Sub(w.batchStarted) >= time.Hour) {
		return w.closeLockedBatch(ctx)
	}
	return nil
}

func (u *unit) write(ctx context.Context, step string, write func(batch, key string) error) error {
	w := u.w
	if !u.checked {
		// 网络查询期间可能完成了扫描或人工修正，写入前使用当前证据核对本次请求。
		entries, err := w.writer.LoadCurationEvidence(ctx, []string{u.releaseID})
		if err != nil {
			return err
		}
		e, ok := entries[u.releaseID]
		if !ok || u.identityDigest != "" && IdentityDigest(e) != u.identityDigest {
			return errReleaseChanged
		}
		current := subjectDigest(u.subject, subjectEvidence(u.subject, e, u.releaseState), u.id)
		if current != u.inputDigest && (u.releaseInput == nil || ReleaseDigest(withAddedMedia(*u.releaseInput, e)) != current) {
			return errReleaseChanged
		}
		u.checked = true
	}
	if err := w.sources.Check(ctx, u.source()); err != nil {
		return err
	}
	return u.writeInBatch(ctx, step, write)
}

// 领取、轮换和记账在同一批次锁内完成，网络取证在锁外。
func (u *unit) writeInBatch(ctx context.Context, step string, write func(batch, key string) error) error {
	w := u.w
	w.batchMu.Lock()
	defer w.batchMu.Unlock()
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.rotateLockedBatch(ctx); err != nil {
		return err
	}
	if w.batch.ID == "" {
		b, err := w.writer.OpenBatch(ctx, "program")
		if err != nil {
			return err
		}
		w.batch, w.batchStarted = b, w.opts.Now()
	}
	err := write(w.batch.ID, u.key(step))
	// 同代回填可能已完成对应和部分合并后中断。重算的署名会解析到新身份，
	// 仅当原步骤确已提交时继续合并，其余幂等冲突仍交给调用方。
	if failureCode(err) == "idempotency_conflict" && u.fixedGeneration && step == "artist_links" {
		var completed bool
		lookupErr := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations o
 JOIN curation_settings s ON s.id=1 AND s.enabled_by=o.actor_id
 WHERE o.idempotency_key=$1 AND o.operation_type='external_link' AND o.target_type='release'
 AND o.target_id=$2 AND o.after_state->>'method'='artist_credit' AND o.after_state->>'source'='musicbrainz'
 AND o.origin->>'provider_kind'='source' AND o.origin->>'source'='curation')`, u.key(step), u.releaseID).Scan(&completed)
		if lookupErr != nil {
			return lookupErr
		}
		if completed {
			err = nil
		}
	}
	if failureCode(err) == "idempotency_conflict" && !u.fixedGeneration {
		u.generation++
		err = write(w.batch.ID, u.key(step))
	}
	if failureCode(err) == "item_stale" {
		return errReleaseChanged
	}
	if failureCode(err) == "nothing_to_apply" {
		err = nil
	}
	if err != nil {
		return err
	}
	// 回放的记录可能属于进程重启前的批次；只统计真实归属当前批次的记录。
	b, err := w.writer.Batch(ctx, w.batch.ID)
	if err != nil {
		return err
	}
	w.batch.Operations = b.Operations
	if b.Operations == 0 {
		return w.closeLockedBatch(ctx)
	}
	if u.subject == "artist_media" {
		return nil
	}
	return w.rotateLockedBatch(ctx)
}
