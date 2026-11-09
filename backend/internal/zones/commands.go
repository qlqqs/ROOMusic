package zones

import (
	"context"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

type commandPlan struct {
	persisted                          PersistedZone
	change                             *QueueChange
	writeEntries                       func(*Store) error
	open, newVisit, close              bool
	pause, debounce, notify            bool
	clearFailures, clearCurrentFailure bool
	start                              bool
	imported                           *bool
	event                              *ZoneEvent
	noop                               bool
}

func (s *Service) Command(ctx context.Context, userID, zoneID string, command Command) (CommandResult, error) {
	if !validID(zoneID) {
		return CommandResult{}, ErrInvalidInput
	}
	if err := validateCommand(command); err != nil {
		return CommandResult{}, err
	}
	command = normalizeCommand(command)
	zoneID = strings.ToLower(zoneID)
	userID = strings.ToLower(userID)
	if command.Command == "transfer" && command.TargetZoneID == zoneID {
		return CommandResult{}, ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return CommandResult{}, err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := u.zones[zoneID]
	if z == nil {
		return CommandResult{}, ErrNotFound
	}
	if command.Command == "transfer" {
		target := u.zones[command.TargetZoneID]
		if target == nil {
			return CommandResult{}, ErrNotFound
		}
		return s.transfer(ctx, userID, z, target)
	}
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	revision := z.persisted.Revision
	result, err := s.commandLocked(ctx, userID, z, command)
	z.mu.Unlock()
	if err == nil && result.Zone.Revision != revision {
		s.publishQueue(userID, zoneID, result.QueueChange)
		s.publishZone(userID, result.Zone)
	}
	return result, err
}

func (s *Service) commandLocked(ctx context.Context, userID string, z *zoneRuntime, c Command) (CommandResult, error) {
	tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
	if err != nil {
		return CommandResult{}, err
	}
	plan, err := s.plan(ctx, userID, z, c, tracks)
	if err != nil {
		return CommandResult{}, err
	}
	if plan.noop {
		// 接受的控制命令撤销自动恢复资格，即使条目操作本身无需变更。
		z.offlinePaused = false
		return CommandResult{ServerTime: timestamp(s.options.Now()), Zone: s.zoneOf(z, tracks), Imported: plan.imported}, nil
	}
	now := s.options.Now()
	plan.persisted.Revision++
	if plan.change != nil {
		plan.persisted.QueueRevision++
		plan.change.QueueRevision = plan.persisted.QueueRevision
	}
	if err = s.transact(ctx, func(store *Store) error {
		if c.Command == "set_settings" {
			if err := s.checkIRReference(ctx, store.tx, userID, plan.persisted.Settings.Convolution); err != nil {
				return err
			}
		}
		if plan.writeEntries != nil {
			if e := plan.writeEntries(store); e != nil {
				return e
			}
		}
		return store.UpdateZone(ctx, userID, plan.persisted)
	}); err != nil {
		return CommandResult{}, err
	}
	z.persisted = plan.persisted
	z.positionWrittenAt = now
	if c.Command == "stop" || c.Command == "clear_queue" || (c.Command == "remove_entry" && z.visit != nil && z.visit.entryID == c.EntryID) {
		z.visit = nil
	}
	if c.Command == "seek" {
		suspendVisit(z)
	}
	z.cursorTracks = tracks
	z.offlinePaused = false
	if plan.event != nil {
		plan.event.Seq = z.persisted.Revision
		z.event = plan.event
	}
	if plan.clearFailures {
		z.failures = []Failure{}
		z.fault = nil
	}
	if plan.clearCurrentFailure {
		filtered := []Failure{}
		for _, f := range z.failures {
			if f.EntryID != z.persisted.Queue.CurrentEntryID {
				filtered = append(filtered, f)
			}
		}
		z.failures = filtered
	}
	if plan.close {
		s.cancelTimers(z)
		s.closeSession(z)
	}
	if plan.start {
		z.consecutiveFailures = 0
		z.resumeFailures = 0
		z.resumeAwaitingSeq = 0
		z.intent = "play"
		z.fault = nil
		z.pausedAt = nil
		s.cancelPause(z)
	}
	if plan.pause {
		s.pauseState(z, now)
		s.schedulePause(userID, z)
	}
	if !plan.start && z.intent != "play" {
		if z.persisted.Queue.CurrentEntryID == "" {
			z.status = "idle"
		} else {
			z.status = "paused"
		}
	}
	if plan.open {
		s.cancelRebuild(z)
		s.openSession(userID, z, tracks, plan.newVisit)
	}
	if plan.notify {
		s.invalidateCursor(z)
	}
	if plan.debounce {
		s.scheduleRebuild(userID, z)
	} else if plan.notify && z.session != nil {
		z.session.NextChanged()
		if z.nextSession != nil {
			z.nextSession.NextChanged()
		}
	}
	result := CommandResult{ServerTime: timestamp(now), Zone: s.zoneOf(z, tracks), QueueChange: plan.change, Imported: plan.imported}
	return result, nil
}
func (s *Service) startAllowed(z *zoneRuntime) error {
	if !z.online {
		return ErrZoneOffline
	}
	if s.options.Renderer == nil {
		return ErrRendererUnavailable
	}
	return nil
}
func entryIndex(entries []queue.Entry, id string) int {
	for i, e := range entries {
		if e.EntryID == id {
			return i
		}
	}
	return -1
}
func (s *Service) plan(ctx context.Context, userID string, z *zoneRuntime, c Command, tracks map[string]catalog.TrackSummary) (commandPlan, error) {
	p := commandPlan{persisted: z.persisted}
	q := z.persisted.Queue
	now := s.options.Now()
	p.persisted.PositionSeconds = positionAt(z, now, tracks)
	p.persisted.PositionSavedAt = now
	env := s.env()
	position := func(seconds float64) { p.persisted.PositionSeconds = seconds; p.persisted.PositionSavedAt = now }
	start := func(newVisit bool) error {
		if err := s.startAllowed(z); err != nil {
			return err
		}
		p.start = true
		p.open = true
		p.newVisit = newVisit
		return nil
	}
	switch c.Command {
	case "play_tracks", "play_next", "enqueue":
		extra := make([]queue.Entry, len(c.TrackIDs))
		for i, id := range c.TrackIDs {
			extra[i].TrackID = id
		}
		loaded, err := s.loadSummaries(ctx, extra)
		if err != nil {
			return p, err
		}
		for _, id := range c.TrackIDs {
			track, ok := loaded[id]
			if !ok {
				return p, ErrTrackNotFound
			}
			tracks[id] = track
		}
		if c.Command != "play_tracks" && len(q.Entries)+len(c.TrackIDs) > queue.MaxQueueEntries {
			return p, ErrQueueFull
		}
		if c.Command == "play_tracks" {
			if err := start(true); err != nil {
				return p, err
			}
			p.persisted.Queue = queue.ReplaceQueue(q, c.TrackIDs, c.StartIndex, env)
			position(0)
			p.clearFailures = true
			p.change = replaceChange(p.persisted.Queue.Entries, tracks)
			p.writeEntries = func(store *Store) error {
				return store.ReplaceEntries(ctx, userID, z.persisted.ZoneID, p.persisted.Queue.Entries)
			}
		} else {
			var next queue.State
			var err error
			index := len(q.Entries)
			anchor := ""
			if index > 0 {
				anchor = q.Entries[index-1].EntryID
			}
			if c.Command == "play_next" {
				index = entryIndex(q.Entries, q.CurrentEntryID) + 1
				anchor = q.CurrentEntryID
				next, err = queue.InsertAfterCurrent(q, c.TrackIDs, env)
			} else {
				next, err = queue.AppendEntries(q, c.TrackIDs, env)
			}
			if err != nil {
				return p, err
			}
			p.persisted.Queue = next
			added := next.Entries[index : index+len(c.TrackIDs)]
			entries, _ := queueProjection(added, tracks)
			p.change = &QueueChange{Kind: "insert", Index: &index, Entries: entries}
			p.notify = true
			p.writeEntries = func(store *Store) error { return store.InsertEntries(ctx, userID, z.persisted.ZoneID, anchor, added) }
			if len(q.Entries) == 0 {
				p.pause = true
				position(0)
			}
		}
	case "play_entry":
		if entryIndex(q.Entries, c.EntryID) < 0 {
			p.noop = true
			break
		}
		if c.EntryID == q.CurrentEntryID && !failed(z, c.EntryID) {
			c.Command = "play"
			return s.plan(ctx, userID, z, c, tracks)
		}
		if err := start(true); err != nil {
			return p, err
		}
		p.persisted.Queue = queue.JumpTo(q, c.EntryID)
		position(0)
		p.clearCurrentFailure = true
	case "remove_entry":
		if entryIndex(q.Entries, c.EntryID) < 0 {
			p.noop = true
			break
		}
		removed := queue.RemoveEntry(q, c.EntryID, env)
		p.persisted.Queue = removed.State
		p.change = &QueueChange{Kind: "remove", EntryID: c.EntryID}
		p.writeEntries = func(store *Store) error {
			_, err := store.DeleteEntry(ctx, userID, z.persisted.ZoneID, c.EntryID)
			return err
		}
		if removed.RemovedCurrent {
			position(0)
			p.close = true
			if z.intent == "play" && removed.State.CurrentEntryID != "" {
				if err := start(true); err != nil {
					return p, err
				}
			} else {
				p.pause = true
			}
		} else {
			p.notify = true
		}
	case "move_entry":
		i := entryIndex(q.Entries, c.EntryID)
		target := i + c.Delta
		if i < 0 || target < 0 || target >= len(q.Entries) {
			p.noop = true
			break
		}
		otherID := q.Entries[target].EntryID
		p.persisted.Queue = queue.MoveEntry(q, c.EntryID, c.Delta)
		p.notify = true
		p.change = &QueueChange{Kind: "move", EntryID: c.EntryID, Delta: c.Delta}
		p.writeEntries = func(store *Store) error {
			_, err := store.SwapEntries(ctx, userID, z.persisted.ZoneID, c.EntryID, otherID)
			return err
		}
	case "clear_queue":
		p.persisted.Queue = queue.ClearQueue(q)
		position(0)
		p.close = true
		p.pause = true
		p.clearFailures = true
		p.change = &QueueChange{Kind: "clear"}
		p.writeEntries = func(store *Store) error { return store.ReplaceEntries(ctx, userID, z.persisted.ZoneID, nil) }
	case "set_mode":
		if q.Mode == c.Mode {
			p.noop = true
			break
		}
		p.persisted.Queue = queue.SetMode(q, c.Mode)
		p.notify = true
	case "play":
		if q.CurrentEntryID == "" {
			if len(q.Entries) == 0 {
				p.noop = true
				break
			}
			p.persisted.Queue = queue.JumpTo(q, q.Entries[0].EntryID)
			position(0)
		}
		if err := s.startAllowed(z); err != nil {
			return p, err
		}
		p.start = true
		p.clearCurrentFailure = failed(z, p.persisted.Queue.CurrentEntryID)
		p.open = z.session == nil || p.clearCurrentFailure
	case "pause":
		if z.intent == "pause" {
			p.noop = true
			break
		}
		p.pause = true
	case "stop":
		p.pause = true
		p.close = true
		position(0)
	case "next", "previous":
		var step queue.Step
		if c.Command == "next" {
			step = queue.Advance(q, env, queue.AdvanceOptions{Reason: queue.ReasonManual, IsSkipped: func(id string) bool { return failed(z, id) }})
		} else {
			step = queue.Retreat(q, func(id string) bool { return failed(z, id) })
		}
		if step.EntryID == "" {
			p.noop = true
			break
		}
		if err := start(true); err != nil {
			return p, err
		}
		p.persisted.Queue = step.State
		position(0)
	case "seek":
		if q.CurrentEntryID == "" {
			p.noop = true
			break
		}
		seconds := clampPosition(q.CurrentEntryID, c.PositionSeconds, q.Entries, tracks)
		position(seconds)
		p.debounce = z.session != nil
	case "set_settings":
		p.persisted.Settings = applySettings(p.persisted.Settings, c.Settings)
		if err := s.checkIRReference(ctx, nil, userID, p.persisted.Settings.Convolution); err != nil {
			return p, err
		}
		if EqualSettings(p.persisted.Settings, z.persisted.Settings) {
			p.noop = true
			break
		}
		p.debounce = z.session != nil
	case "retry":
		if z.fault == nil || z.fault.Kind != "transient" || q.CurrentEntryID == "" {
			p.noop = true
			break
		}
		if err := start(false); err != nil {
			return p, err
		}
	case "import":
		return s.planImport(ctx, userID, z, c, tracks)
	}
	return p, nil
}

// positionAt 只在 playing 时外推；切到暂停或 loading 前将采样锚点一起保存。
func positionAt(z *zoneRuntime, now time.Time, tracks map[string]catalog.TrackSummary) float64 {
	p := z.persisted
	if p.Queue.CurrentEntryID == "" {
		return 0
	}
	seconds := p.PositionSeconds
	if z.status == "playing" {
		seconds += max(0, now.Sub(p.PositionSavedAt).Seconds())
	}
	return clampPosition(p.Queue.CurrentEntryID, seconds, p.Queue.Entries, tracks)
}

func clampPosition(id string, seconds float64, entries []queue.Entry, tracks map[string]catalog.TrackSummary) float64 {
	// 数据库位置的安全上界与 B1 迁移保持一致。
	seconds = min(seconds, 10000000)
	for _, e := range entries {
		if e.EntryID == id {
			if d := tracks[e.TrackID].DurationSeconds; d != nil {
				seconds = min(seconds, *d)
			}
			break
		}
	}
	return max(0, seconds)
}
func replaceChange(entries []queue.Entry, tracks map[string]catalog.TrackSummary) *QueueChange {
	if len(entries) > 500 {
		return &QueueChange{Kind: "reload"}
	}
	projected, _ := queueProjection(entries, tracks)
	return &QueueChange{Kind: "replace", Entries: projected}
}
func (s *Service) pauseState(z *zoneRuntime, now time.Time) {
	if z.session != nil && z.stream != nil {
		z.session.Observe(PlayheadSample{StreamSeq: z.stream.Seq, StreamSeconds: streamPosition(z.stream, z.persisted.Queue.CurrentEntryID, z.persisted.PositionSeconds), At: now, Playing: false})
	}
	suspendVisit(z)
	z.intent = "pause"
	if z.persisted.Queue.CurrentEntryID == "" {
		z.status = "idle"
	} else {
		z.status = "paused"
	}
	at := timestamp(now)
	z.pausedAt = &at
}
