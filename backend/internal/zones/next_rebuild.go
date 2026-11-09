package zones

import "time"

// 预载的字节失效只重建该接续流，当前流的位置、访问和播放状态不受影响。
func (s *Service) scheduleNextRebuild(userID string, z *zoneRuntime, h *renderHandle, delay time.Duration) {
	if h.rebuild != nil {
		h.rebuild.Stop()
	}
	h.rebuildVersion++
	version := h.rebuildVersion
	h.rebuild = s.AfterFunc(delay, func() {
		z.dispatch.Lock()
		defer z.dispatch.Unlock()
		z.mu.Lock()
		if s.closed.Load() || z.deleted || z.generation != h.generation || z.nextHandle != h || h.rebuildVersion != version {
			z.mu.Unlock()
			return
		}
		tracks, err := s.loadSummaries(s.ctx, z.persisted.Queue.Entries)
		p := z.persisted
		p.Revision++
		if err == nil {
			err = s.transact(s.ctx, func(st *Store) error { return st.UpdateZone(s.ctx, userID, p) })
		}
		if err != nil {
			s.reportAsync("next_rebuild", userID, p.ZoneID, err)
			s.scheduleNextRebuild(userID, z, h, time.Second)
			z.mu.Unlock()
			return
		}
		z.persisted = p
		z.positionWrittenAt = s.Now()
		z.cursorTracks = tracks
		h.rebuild = nil
		if entryIndex(p.Queue.Entries, h.entryID) < 0 || failed(z, h.entryID) {
			h.close()
			z.nextHandle, z.nextSession, z.nextStream = nil, nil, nil
		} else {
			s.openNext(userID, z, h.entryID, h.startSeconds)
		}
		result := s.zoneOf(z, tracks)
		z.mu.Unlock()
		s.publishZone(userID, result)
	})
}
