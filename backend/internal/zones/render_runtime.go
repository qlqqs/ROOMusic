package zones

import (
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (s *Service) closeSession(z *zoneRuntime) {
	z.generation++
	s.cancelResume(z)
	suspendVisit(z)
	if z.currentHandle != nil {
		z.currentHandle.close()
	}
	if z.nextHandle != nil {
		z.nextHandle.close()
	}
	z.currentHandle, z.nextHandle = nil, nil
	if z.session != nil {
		z.session.Close()
		z.session = nil
	}
	if z.nextSession != nil {
		z.nextSession.Close()
		z.nextSession = nil
	}
	z.stream = nil
	z.nextStream = nil
	s.invalidateCursor(z)
}
func (s *Service) invalidateCursor(z *zoneRuntime) {
	z.prepared = map[string]string{}
	z.cursorState = z.persisted.Queue
}
func (s *Service) cancelRebuild(z *zoneRuntime) {
	z.rebuildVersion++
	if z.rebuild != nil {
		z.rebuild.Stop()
		z.rebuild = nil
	}
}
func (s *Service) cancelPause(z *zoneRuntime) {
	z.pauseVersion++
	if z.pause != nil {
		z.pause.Stop()
		z.pause = nil
	}
}
func (s *Service) cancelResume(z *zoneRuntime) {
	z.resumeVersion++
	if z.resumeRetry != nil {
		z.resumeRetry.Stop()
		z.resumeRetry = nil
	}
}
func (s *Service) cancelTimers(z *zoneRuntime) {
	s.cancelRebuild(z)
	s.cancelPause(z)
	s.cancelResume(z)
}
func renderEntry(q queue.State, id string, tracks map[string]catalog.TrackSummary) Entry {
	at := entryIndex(q.Entries, id)
	if at < 0 {
		return Entry{}
	}
	e := q.Entries[at]
	out := Entry{EntryID: e.EntryID, TrackID: e.TrackID}
	if q.Mode == queue.ModeShuffle {
		return out
	}
	release := tracks[e.TrackID].Release.ID
	if release == "" {
		return out
	}
	for _, neighbor := range []int{at - 1, at + 1} {
		if neighbor >= 0 && neighbor < len(q.Entries) && tracks[q.Entries[neighbor].TrackID].Release.ID == release {
			out.AlbumContext = true
		}
	}
	return out
}
func (s *Service) openSession(userID string, z *zoneRuntime, tracks map[string]catalog.TrackSummary, newVisit bool) {
	s.closeSession(z)
	z.cursorTracks = tracks
	if z.persisted.Queue.CurrentEntryID == "" {
		return
	}
	s.beginVisit(z, z.persisted.Queue.CurrentEntryID, newVisit)
	handle, kind := s.createRender(userID, z, z.persisted.Queue.CurrentEntryID, z.persisted.PositionSeconds)
	if handle == nil {
		s.renderFault(z, kind)
		return
	}
	z.currentHandle = handle
	z.session, z.stream = handle.session, handle.stream
	z.status = "loading"
	z.fault = nil
	z.lostStream = nil
	if newVisit {
		z.event = &ZoneEvent{Seq: z.persisted.Revision, Kind: "track_started", EntryID: handle.entryID}
	}
	s.consumeRender(userID, z, handle)
}

func (s *Service) renderFault(z *zoneRuntime, kind string) {
	z.fault = &Fault{Kind: kind}
	s.pauseState(z, s.options.Now())
	event := "transient_failure"
	if kind == "no_stream_format" {
		event = kind
	}
	z.event = &ZoneEvent{Seq: z.persisted.Revision, Kind: event}
}
func (s *Service) schedulePause(userID string, z *zoneRuntime) {
	delay := 5 * time.Second
	if z.pausedAt != nil {
		delay = max(0, delay-s.Now().Sub(z.pausedAt.Time))
	}
	s.schedulePauseAfter(userID, z, delay)
}
func (s *Service) schedulePauseAfter(userID string, z *zoneRuntime, delay time.Duration) {
	s.cancelPause(z)
	if z.session == nil {
		return
	}
	version := z.pauseVersion
	z.pause = s.options.AfterFunc(delay, func() {
		z.dispatch.Lock()
		defer z.dispatch.Unlock()
		z.mu.Lock()
		if s.closed.Load() || z.deleted || z.pauseVersion != version || z.intent != "pause" || z.session == nil {
			z.mu.Unlock()
			return
		}
		tracks, err := s.loadSummaries(s.ctx, z.persisted.Queue.Entries)
		p := z.persisted
		p.Revision++
		if err == nil {
			err = s.transact(s.ctx, func(store *Store) error { return store.UpdateZone(s.ctx, userID, p) })
		}
		if err != nil {
			s.reportAsync("pause_close", userID, p.ZoneID, err)
			// 失败不能发布未提交状态；仍暂停时五秒后重试，避免永久占着渲染会话。
			s.schedulePauseAfter(userID, z, 5*time.Second)
			z.mu.Unlock()
			return
		}
		z.persisted = p
		z.positionWrittenAt = s.Now()
		s.cancelRebuild(z)
		s.closeSession(z)
		if z.persisted.Queue.CurrentEntryID == "" {
			z.status = "idle"
		} else {
			z.status = "paused"
		}
		z.pause = nil
		result := s.zoneOf(z, tracks)
		z.mu.Unlock()
		s.publishZone(userID, result)
	})
}
func (s *Service) scheduleRebuild(userID string, z *zoneRuntime) {
	s.scheduleRebuildAfter(userID, z, 300*time.Millisecond)
}
func (s *Service) scheduleRebuildAfter(userID string, z *zoneRuntime, delay time.Duration) {
	// 普通设置、定位及渲染事件仍只按旧会话是否存在决定重建资格。
	s.scheduleRebuildWhile(userID, z, delay, func() bool { return z.session != nil })
}

// scheduleRebuildWhile 复用去抖、事务与重试；资格检查均在区锁内执行。
func (s *Service) scheduleRebuildWhile(userID string, z *zoneRuntime, delay time.Duration, eligible func() bool) {
	s.cancelRebuild(z)
	if !eligible() {
		return
	}
	z.status = "loading"
	version := z.rebuildVersion
	z.rebuild = s.options.AfterFunc(delay, func() {
		z.dispatch.Lock()
		defer z.dispatch.Unlock()
		z.mu.Lock()
		if s.closed.Load() || z.deleted || z.rebuildVersion != version || !eligible() {
			z.mu.Unlock()
			return
		}
		tracks, err := s.loadSummaries(s.ctx, z.persisted.Queue.Entries)
		p := z.persisted
		p.Revision++
		if err == nil {
			err = s.transact(s.ctx, func(store *Store) error { return store.UpdateZone(s.ctx, userID, p) })
		}
		if err != nil {
			s.reportAsync("rebuild", userID, p.ZoneID, err)
			s.scheduleRebuildWhile(userID, z, time.Second, eligible)
			z.mu.Unlock()
			return
		}
		z.persisted = p
		z.positionWrittenAt = s.Now()
		z.rebuild = nil
		s.openSession(userID, z, tracks, false)
		result := s.zoneOf(z, tracks)
		z.mu.Unlock()
		s.publishZone(userID, result)
	})
}

type renderCursor struct {
	service    *Service
	zone       *zoneRuntime
	generation uint64
	seq        int64
}

func (c *renderCursor) Next(afterEntryID string) (Entry, bool) {
	z, s := c.zone, c.service
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.deleted || s.closed.Load() || z.generation != c.generation || (c.seq != 0 && !z.hasRenderSeq(c.seq)) {
		return Entry{}, false
	}
	q := z.cursorState
	if entryIndex(q.Entries, afterEntryID) < 0 {
		return Entry{}, false
	}
	id, ok := z.prepared[afterEntryID]
	if ok && afterEntryID != q.CurrentEntryID && !failed(z, id) && entryIndex(q.Entries, id) >= 0 {
		// 回查尚在同一轮预取链上的条目时复用结果，不再次消耗随机袋。
		return renderEntry(q, id, z.cursorTracks), true
	}
	if ok && afterEntryID == q.CurrentEntryID {
		// 前沿回到已经经过的条目，开始新一轮；不能永久复用上一轮的随机边。
		z.prepared = map[string]string{}
	}
	q.CurrentEntryID = afterEntryID
	id = queue.PeekNatural(q, s.env(), func(id string) bool { return failed(z, id) })
	if id == "" {
		return Entry{}, false
	}
	step := queue.AdvanceTo(q, id, func(id string) bool { return failed(z, id) })
	if step.EntryID == "" {
		return Entry{}, false
	}
	z.prepared[afterEntryID] = id
	z.cursorState = step.State
	return renderEntry(step.State, id, z.cursorTracks), true
}

// 不让会话反向调用控制命令。
var _ Cursor = (*renderCursor)(nil)
