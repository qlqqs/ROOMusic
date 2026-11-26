package program

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

var subjects = curation.ProgramQueueSubjects[:]
var errAlbumWaiting = errors.New("等待专辑重试")
var errQueueUnchanged = errors.New("本轮无需写入")
var errWaitingForPrints = errors.New("等待抽样曲目的声纹")

type queueAttempt struct {
	item   queueItem
	worker *Worker
}

type attemptKey struct{}

func currentAttempt(ctx context.Context) *queueAttempt {
	a, _ := ctx.Value(attemptKey{}).(*queueAttempt)
	return a
}

func (w *Worker) projectionMarker(ctx context.Context, now time.Time) (curation.ProgramChanges, error) {
	marker, err := curation.LoadProgramChanges(ctx, w.db, now)
	// 程序自己的写入在处理结束时按目标刷新。等待到期由领取查询选取；90 天重查到期时先投影声纹需求。
	marker.Operation = sql.NullTime{}
	marker.Due, marker.ReleaseDue = 0, 0
	marker.LatestDue, marker.LatestReleaseDue = sql.NullTime{}, sql.NullTime{}
	return marker, err
}

// 盘点只维护本地投影；外部工作每次重新领取一项。
func (w *Worker) refreshChanged(ctx context.Context) error {
	w.projectionMu.Lock()
	defer w.projectionMu.Unlock()
	for {
		now := w.opts.Now()
		marker, err := w.projectionMarker(ctx, now)
		if err != nil {
			return err
		}
		if w.marker != nil && *w.marker == marker && now.Sub(w.projected) < w.opts.FullPassInterval {
			return nil
		}
		if err = w.refreshQueueProjection(ctx); err != nil {
			if retrySnapshot(err) {
				continue
			}
			return err
		}
		w.projected, w.marker = now, &marker
		// 跨页盘点期间可能结束了另一轮扫描；发布后再次比对再领取。
	}
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.writer.CloseOpenBatches(ctx, "program"); err != nil {
			w.rest(ctx, err)
			continue
		}
		if err := w.restorePauses(ctx); err != nil {
			w.rest(ctx, err)
			continue
		}
		break
	}
	for ctx.Err() == nil {
		err := w.ready(ctx)
		if err == nil && w.opts.ConfirmScan != nil {
			err = w.opts.ConfirmScan(ctx)
		}
		if err == nil {
			err = w.ready(ctx)
		}
		if err == nil {
			err = w.refreshChanged(ctx)
		}
		if err == nil {
			w.setState("running")
			err = w.drainQueues(ctx, subjects)
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = w.closeInactiveArtistItems(ctx, func() error { return w.ready(ctx) })
		}
		if err == nil {
			err = w.writer.AdvanceReleaseMergeQueues(ctx, resourceRuleVersion)
		}
		if closeErr := w.closeBatch(ctx); closeErr != nil {
			err = closeErr
		}
		if err != nil {
			w.rest(ctx, err)
			continue
		}
		w.mu.Lock()
		if w.lastPass.IsZero() || w.projected.After(w.lastPass) || w.lastCompletion.After(w.lastPass) {
			w.lastPass = w.opts.Now()
		}
		w.state = "idle"
		w.mu.Unlock()
		delay, err := w.nextDelay(ctx)
		if err != nil {
			w.rest(ctx, err)
			continue
		}
		w.wait(ctx, delay)
	}
}

type queueResult struct {
	item queueItem
	err  error
}

func (w *Worker) drainQueues(parent context.Context, kinds []string) error {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	results := make(chan queueResult, len(kinds))
	busy := make(map[string]bool)
	parked := make(map[string]bool)
	nextPoll := time.Now().Add(w.opts.PollInterval)
	poll := time.NewTimer(w.opts.PollInterval)
	defer poll.Stop()
	for {
		if err := w.ready(ctx); err != nil {
			return err
		}
		if err := w.refreshChanged(ctx); err != nil {
			return err
		}
		if ids := w.policy.RecoveredSources(); len(ids) > 0 {
			if _, err := w.db.ExecContext(ctx, `UPDATE curation_work_states SET next_attempt_at=$1 WHERE subject='release' AND status='try_later'
                AND result->'retry'->>'kind'='source' AND result->'retry'->>'source'=ANY($2::text[]) AND next_attempt_at>$1`, w.opts.Now(), ids); err != nil {
				return err
			}
		}
		for _, subject := range kinds {
			if busy[subject] || parked[subject] {
				continue
			}
			if subject != "release" && subject != "fingerprint" && w.cooling(subject) {
				continue
			}
			item, found, err := w.nextQueueItem(ctx, subject)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			busy[subject] = true
			w.setQueueState(subject, "running", "", nil)
			workers.Add(1)
			go func() {
				defer workers.Done()
				err := w.processQueueItem(ctx, item)
				results <- queueResult{item, err}
			}()
		}
		if len(busy) == 0 {
			return nil
		}
		delay, err := w.nextDelay(ctx)
		if err != nil {
			return err
		}
		poll.Reset(min(time.Until(nextPoll), delay))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.policy.Changes():
			continue
		case <-poll.C:
			if time.Now().Before(nextPoll) {
				continue
			}
			nextPoll = time.Now().Add(w.opts.PollInterval)
			clear(parked)
			if w.opts.ConfirmScan != nil {
				if err := w.opts.ConfirmScan(ctx); err != nil {
					return err
				}
			}
		case result := <-results:
			item, err := result.item, result.err
			delete(busy, item.Subject)
			var pause *sources.QueuePause
			switch {
			case errors.As(err, &pause):
				w.setQueueState(item.Subject, "paused", pause.Reason, pause)
			case failureCode(err) == "source_disabled" || failureCode(err) == "not_found":
				parked[item.Subject] = true
				w.setQueueState(item.Subject, "paused", "source_disabled", nil)
			case errors.Is(err, errAlbumWaiting):
				w.setQueueState(item.Subject, "idle", "waiting_retry", nil)
			case errors.Is(err, errWaitingForPrints):
				w.setQueueState(item.Subject, "idle", "waiting_for_fingerprint", nil)
			case errors.Is(err, errQueueUnchanged):
				if err = w.acknowledgeQueueItem(ctx, item); err != nil {
					return err
				}
				w.setQueueState(item.Subject, "idle", "", nil)
			case errors.Is(err, errReleaseChanged) || retrySnapshot(err):
				w.setQueueState(item.Subject, "idle", "", nil)
				if err = w.refreshItem(ctx, item); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				if err = w.acknowledgeQueueItem(ctx, item); err != nil {
					return err
				}
				if err = w.refreshItem(ctx, item); err != nil {
					return err
				}
				w.recordQueueEvent(item.Subject, false)
				w.setQueueState(item.Subject, "idle", "", nil)
			}
		}
	}
}

func (w *Worker) refreshItem(ctx context.Context, item queueItem) error {
	for {
		var err error
		switch item.Subject {
		case "artist_media":
			err = w.refreshArtistQueue(ctx, item.TargetID)
		case "fingerprint":
			return nil
		default:
			err = w.refreshReleaseQueue(ctx, item.ReleaseID)
		}
		if !retrySnapshot(err) {
			return err
		}
	}
}

func (w *Worker) processQueueItem(ctx context.Context, item queueItem) error {
	a := &queueAttempt{item: item, worker: w}
	ctx = context.WithValue(ctx, attemptKey{}, a)
	priority := sources.QueueNormal
	if item.Subject == "release" || item.Subject == "fingerprint" {
		ctx = sources.WithIdentification(ctx)
		if item.Rank == 4 {
			priority = sources.QueueLocalComplete
		}
	} else if item.Subject == "artist_media" {
		priority = sources.QueueArtistMedia
	}
	ctx = w.policy.Context(ctx, priority, func(string) { w.recordQueueEvent(item.Subject, true) })
	if err := w.ready(ctx); err != nil {
		return err
	}
	if item.Subject == "fingerprint" {
		return w.computePrint(ctx, item)
	}
	if item.Subject == "artist_media" {
		rows, err := artistMediaPage(ctx, w.db, `SELECT $1::uuid`, item.TargetID)
		if err != nil || len(rows) == 0 {
			return err
		}
		providers, err := w.artistProviders(ctx)
		if err != nil {
			return err
		}
		states, err := curation.LoadWorkStates(ctx, w.db, item.Subject, []string{item.TargetID})
		if err != nil {
			return err
		}
		state := states[item.TargetID]
		if err := w.processArtistMedia(ctx, rows[0], providers, state); err != nil {
			return err
		}
		return w.checkItemCompletion(ctx, item, state.UpdatedAt)
	}
	entries, err := w.writer.LoadCurationEvidence(ctx, []string{item.ReleaseID})
	if err != nil {
		return err
	}
	e, visible := entries[item.ReleaseID]
	if !visible {
		return nil
	}
	var previous time.Time
	states := pageStates{}
	states.releases, err = curation.LoadWorkStates(ctx, w.db, "release", []string{item.ReleaseID})
	if err != nil {
		return err
	}
	if item.Subject == "release" {
		step, err := w.fingerprintStep(ctx)
		if err != nil {
			return err
		}
		states.fingerprintAvailable = step != nil
		claimed := states.releases[item.TargetID]
		claimed.ManualRequestedAt = item.ManualRequestedAt
		states.releases[item.TargetID] = claimed
		previous = claimed.UpdatedAt
	} else {
		if item.Subject == "artwork" && !artworkGap(e) {
			return errQueueUnchanged
		}
		m, err := curation.LoadWorkStates(ctx, w.db, item.Subject, []string{item.TargetID})
		if err != nil {
			return err
		}
		previous = m[item.TargetID].UpdatedAt
		if item.Subject == "lyrics" {
			states.lyrics = m
		} else {
			states.artworks = m
		}
	}
	if item.Subject == "lyrics" {
		eligible := false
		for _, track := range e.Tracks {
			if track.ID == item.TargetID {
				eligible = lyricsGap(track)
				break
			}
		}
		if !eligible {
			return errQueueUnchanged
		}
	}
	err = w.process(ctx, &e, item.Subject, item.TargetID, states)
	if err != nil {
		return err
	}
	if item.Subject == "release" {
		w.dropPrints(item.ReleaseID)
	}
	return w.checkItemCompletion(ctx, item, previous)
}

// 保留真正失败的上游，媒体回退成功时不阻塞其它项。
func sourceFailure(ctx context.Context, id string, err error) error {
	a := currentAttempt(ctx)
	if a == nil || !curation.IsSourceUnavailable(err) {
		return err
	}
	var pause *sources.QueuePause
	if errors.As(err, &pause) {
		return pause
	}
	return a.worker.policy.Unavailable(id)
}

// 没有可执行的回填或待定项正被模型占用时，等下一次输入变化再盘点。
func (w *Worker) checkItemCompletion(ctx context.Context, item queueItem, previous time.Time) error {
	var changed bool
	err := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_work_states WHERE subject=$1 AND target_id=$2 AND updated_at<>$3)`, item.Subject, item.TargetID, previous).Scan(&changed)
	if err == nil && !changed {
		return errQueueUnchanged
	}
	return err
}

func retrySnapshot(err error) bool {
	var dbErr interface{ SQLState() string }
	return errors.As(err, &dbErr) && (dbErr.SQLState() == "40001" || dbErr.SQLState() == "40P01")
}

func (w *Worker) restorePauses(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `SELECT DISTINCT ON (result->'retry'->>'source') result->'retry'->>'source', result->'retry'->>'reason', next_attempt_at
       FROM curation_work_states WHERE subject='release' AND status='try_later' AND result->'retry'->>'kind'='source' AND next_attempt_at>$1
       ORDER BY result->'retry'->>'source',next_attempt_at DESC`, w.opts.Now())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pause sources.QueuePause
		if err := rows.Scan(&pause.Source, &pause.Reason, &pause.RetryAt); err != nil {
			return err
		}
		w.policy.RestorePause(pause)
	}
	return rows.Err()
}
