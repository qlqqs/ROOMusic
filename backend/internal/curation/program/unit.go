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
	return w.rotateBatch(ctx)
}
