package zones

import (
	"context"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func (s *Service) scheduleOfflineScan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	s.offlineScan = s.AfterFunc(OfflineScanInterval, func() {
		s.scanOffline()
		s.scheduleOfflineScan()
	})
}
func (s *Service) scanOffline() {
	s.mu.Lock()
	users := make(map[string]*userRuntime, len(s.users))
	for id, u := range s.users {
		users[id] = u
	}
	s.mu.Unlock()
	for userID, u := range users {
		u.mu.RLock()
		for _, z := range u.order {
			z.dispatch.Lock()
			z.mu.Lock()
			if z.deleted || !z.online || z.output == nil {
				z.mu.Unlock()
				z.dispatch.Unlock()
				continue
			}
			now := s.Now()
			offline := z.output.connections.Load() == 0 && now.Sub(z.output.lastReport) > OutputLease
			if !offline {
				// 只有 SSE、没有回报的暂停设备同样持续在线，不能保留数月前的上线时间。
				if now.Sub(z.device.LastSeenAt) >= DeviceSeenSaveInterval {
					d := z.device
					d.LastSeenAt = now
					if err := s.transact(s.ctx, func(st *Store) error { return st.UpdateDevice(s.ctx, userID, d) }); err != nil {
						s.reportAsync("online_seen", userID, z.persisted.ZoneID, err)
					} else {
						z.device = d
						device := deviceOf(z)
						z.mu.Unlock()
						s.publishDevice(userID, device)
						z.dispatch.Unlock()
						continue
					}
				}
				z.mu.Unlock()
				z.dispatch.Unlock()
				continue
			}
			tracks, err := s.loadSummaries(s.ctx, z.persisted.Queue.Entries)
			p := z.persisted
			p.Revision++
			// 无输出采样的离线期不外推，也不计次。
			if err == nil {
				err = s.transact(s.ctx, func(st *Store) error { return st.UpdateZone(s.ctx, userID, p) })
			}
			if err != nil {
				s.reportAsync("offline", userID, p.ZoneID, err)
				z.mu.Unlock()
			} else {
				z.persisted = p
				z.positionWrittenAt = now
				s.markOffline(z)
				device, zone := deviceOf(z), s.zoneOf(z, tracks)
				z.mu.Unlock()
				s.publishDevice(userID, device)
				s.publishZone(userID, zone)
			}
			z.dispatch.Unlock()
		}
		u.mu.RUnlock()
	}
}
func (s *Service) markOffline(z *zoneRuntime) {
	z.online = false
	wasPlaying := z.intent == "play"
	if z.stream != nil {
		z.lostStream = cloneStream(z.stream)
	}
	s.cancelTimers(z)
	s.closeSession(z)
	s.pauseState(z, s.Now())
	suspendVisit(z)
	z.offlinePaused = wasPlaying || z.offlinePaused
}

// SubscribeOutput 在用户注册表与所有区 dispatch 锁内建立快照边界。
// 已提交但尚未发布的事件先排完，再原子入队快照，不会出现快照之后的旧增量。
func (s *Service) SubscribeOutput(ctx context.Context, userID, sessionID string) (*Subscription, error) {
	userID = strings.ToLower(userID)
	u, err := s.user(ctx, userID)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	ids := make([]string, 0, len(u.zones))
	for id := range u.zones {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		u.zones[id].dispatch.Lock()
	}
	defer func() {
		for i := len(ids) - 1; i >= 0; i-- {
			u.zones[ids[i]].dispatch.Unlock()
		}
	}()
	for _, id := range ids {
		u.zones[id].mu.Lock()
	}
	locked := true
	unlockZones := func() {
		if !locked {
			return
		}
		for i := len(ids) - 1; i >= 0; i-- {
			u.zones[ids[i]].mu.Unlock()
		}
		locked = false
	}
	defer unlockZones()
	if s.closed.Load() {
		return nil, ErrClosed
	}
	var associated *outputSession
	if validID(sessionID) {
		s.outputMu.Lock()
		out := s.outputs[strings.ToLower(sessionID)]
		s.outputMu.Unlock()
		// 先查归属，不可读取其他用户区锁保护的可变字段。
		if out != nil && out.userID == userID && u.zones[out.zone.persisted.ZoneID] == out.zone && outputError(out) == nil {
			associated = out
		}
	}
	snapshot := Snapshot{ServerTime: timestamp(s.Now()), Protocol: 1, Devices: []Device{}, Zones: []Zone{}}
	tracksByZone := make(map[string]map[string]catalog.TrackSummary, len(u.order))
	for _, z := range u.order {
		tracks, e := s.loadSummaries(ctx, z.persisted.Queue.Entries)
		if e != nil {
			return nil, e
		}
		tracksByZone[z.persisted.ZoneID] = tracks
	}
	var updated *OutputResult
	if associated != nil {
		z := associated.zone
		d, p := z.device, z.persisted
		d.LastSeenAt = s.Now()
		p.Revision++
		if err = s.transact(ctx, func(st *Store) error {
			if e := st.UpdateDevice(ctx, userID, d); e != nil {
				return e
			}
			return st.UpdateZone(ctx, userID, p)
		}); err != nil {
			return nil, err
		}
		z.device, z.persisted = d, p
		z.positionWrittenAt = s.Now()
		z.online = true
		updated = &OutputResult{Device: deviceOf(z), Zone: s.zoneOf(z, tracksByZone[p.ZoneID])}
	}
	for _, z := range u.order {
		snapshot.Devices = append(snapshot.Devices, deviceOf(z))
		snapshot.Zones = append(snapshot.Zones, s.zoneOf(z, tracksByZone[z.persisted.ZoneID]))
	}
	var detach func()
	if associated != nil {
		associated.connections.Add(1)
		detach = func() { associated.connections.Add(-1) }
	}
	// DTO 已冻结；继续持有注册表和全部 dispatch，解区锁后发布仍不会跨过快照边界。
	unlockZones()
	if updated != nil {
		s.publishDevice(userID, updated.Device)
		s.publishZone(userID, updated.Zone)
	}
	sub, err := s.hub.subscribeSnapshot(userID, snapshot, detach)
	if err != nil && detach != nil {
		detach()
	}
	return sub, err
}
