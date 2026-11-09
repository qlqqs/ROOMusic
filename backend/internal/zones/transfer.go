package zones

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (s *Service) transfer(ctx context.Context, userID string, source, target *zoneRuntime) (CommandResult, error) {
	first, second := sortedZones(source, target)
	first.dispatch.Lock()
	defer first.dispatch.Unlock()
	second.dispatch.Lock()
	defer second.dispatch.Unlock()
	first.mu.Lock()
	second.mu.Lock()
	result, sourceChange, targetChange, err := s.transferLocked(ctx, userID, source, target)
	second.mu.Unlock()
	first.mu.Unlock()
	if err != nil {
		return CommandResult{}, err
	}
	// 转移的两条队列增量均先于两条区状态；发布时仍持有两区 dispatch。
	s.publishQueue(userID, source.persisted.ZoneID, sourceChange)
	s.publishQueue(userID, target.persisted.ZoneID, targetChange)
	s.publishZone(userID, result.Zone)
	s.publishZone(userID, *result.TargetZone)
	return result, nil
}
func (s *Service) transferLocked(ctx context.Context, userID string, source, target *zoneRuntime) (CommandResult, *QueueChange, *QueueChange, error) {
	if err := s.startAllowed(target); err != nil {
		return CommandResult{}, nil, nil, err
	}
	tracks, err := s.loadSummaries(ctx, source.persisted.Queue.Entries)
	if err != nil {
		return CommandResult{}, nil, nil, err
	}
	now := s.options.Now()
	sp, tp := source.persisted, target.persisted
	tp.Queue = sp.Queue
	tp.PositionSeconds = positionAt(source, now, tracks)
	tp.PositionSavedAt = now
	sp.Queue = queue.ClearQueue(sp.Queue)
	sp.PositionSeconds = 0
	sp.PositionSavedAt = s.options.Now()
	sp.Revision++
	sp.QueueRevision++
	tp.Revision++
	tp.QueueRevision++
	if err = s.transact(ctx, func(store *Store) error {
		if e := store.TransferEntries(ctx, userID, sp.ZoneID, tp.ZoneID); e != nil {
			return e
		}
		if e := store.UpdateZone(ctx, userID, sp); e != nil {
			return e
		}
		return store.UpdateZone(ctx, userID, tp)
	}); err != nil {
		return CommandResult{}, nil, nil, err
	}
	// 库提交之后才释放旧流、变更工作副本；目标自己的设置和设备音量始终不动。
	s.cancelTimers(source)
	s.closeSession(source)
	s.cancelTimers(target)
	s.closeSession(target)
	source.persisted = sp
	source.failures = []Failure{}
	source.consecutiveFailures = 0
	source.fault = nil
	source.event = nil
	source.offlinePaused = false
	s.pauseState(source, s.options.Now())
	target.persisted = tp
	target.failures = []Failure{}
	target.consecutiveFailures = 0
	target.fault = nil
	target.event = nil
	target.offlinePaused = false
	if tp.Queue.CurrentEntryID != "" {
		target.intent = "play"
		target.pausedAt = nil
		s.openSession(userID, target, tracks, true)
	} else {
		s.pauseState(target, s.options.Now())
	}
	sourceChange := &QueueChange{Kind: "clear", QueueRevision: sp.QueueRevision}
	targetChange := replaceChange(tp.Queue.Entries, tracks)
	targetChange.QueueRevision = tp.QueueRevision
	tz := s.zoneOf(target, tracks)
	return CommandResult{ServerTime: timestamp(s.options.Now()), Zone: s.zoneOf(source, nil), QueueChange: sourceChange, TargetZone: &tz}, sourceChange, targetChange, nil
}
