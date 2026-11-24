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
	subject, id            string
	generation             int
	releaseID, inputDigest string
	releaseState           curation.WorkState
	checked                bool
	fixedGeneration        bool
}

// 以目标 UUID 为命名空间，名称中的字段顺序固定。
func (u *unit) key(step string) string {
	name := fmt.Sprintf("roomusic/curation/%s/%d/%d/%s", u.subject, StoredRuleVersion, u.generation, step)
	return curation.DerivedKey(u.id, name)
}

func (w *Worker) closeBatch(ctx context.Context) error {
	id, _, n, open := w.CurrentBatch()
	if !open {
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
	w.mu.Lock()
	w.batch = curation.Batch{}
	w.mu.Unlock()
	return nil
}

func (w *Worker) rotateBatch(ctx context.Context) error {
	if w.batch.ID != "" && (w.batch.Operations >= 500 || w.opts.Now().Sub(w.batchStarted) >= time.Hour) {
		return w.closeBatch(ctx)
	}
	return nil
}

func (u *unit) write(ctx context.Context, step string, write func(batch, key string) error) error {
	w := u.w
	if !u.checked {
		// 网络查询期间可能完成了扫描或人工修正，写入前使用当前证据核对本次请求。
		entries, err := catalog.LoadCurationEvidence(ctx, w.db, []string{u.releaseID}, w.writer.ArtworkExists)
		if err != nil {
			return err
		}
		e, ok := entries[u.releaseID]
		if !ok || subjectDigest(u.subject, subjectEvidence(u.subject, e, u.releaseState), u.id) != u.inputDigest {
			return errReleaseChanged
		}
		u.checked = true
	}
	if err := w.sources.Check(ctx, subjectSource(u.subject)); err != nil {
		return err
	}
	if err := w.rotateBatch(ctx); err != nil {
		return err
	}
	return u.writeInBatch(ctx, step, write)
}

// 同一艺人的两项写入在单元开始时预留批次容量。
func (u *unit) writeInBatch(ctx context.Context, step string, write func(batch, key string) error) error {
	w := u.w
	if w.batch.ID == "" {
		b, err := w.writer.OpenBatch(ctx, "program")
		if err != nil {
			return err
		}
		w.mu.Lock()
		w.batch, w.batchStarted = b, w.opts.Now()
		w.mu.Unlock()
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
	w.mu.Lock()
	w.batch.Operations = b.Operations
	w.mu.Unlock()
	if b.Operations == 0 {
		return w.closeBatch(ctx)
	}
	if u.subject == "artist_media" {
		return nil
	}
	return w.rotateBatch(ctx)
}
