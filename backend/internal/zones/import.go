package zones

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (s *Service) planImport(ctx context.Context, userID string, z *zoneRuntime, c Command, tracks map[string]catalog.TrackSummary) (commandPlan, error) {
	imported := false
	p := commandPlan{persisted: z.persisted, imported: &imported}
	if len(z.persisted.Queue.Entries) != 0 || z.persisted.ImportedAt != nil {
		p.noop = true
		return p, nil
	}
	requested := make([]queue.Entry, len(c.Entries))
	for i, e := range c.Entries {
		requested[i] = queue.Entry{EntryID: e.EntryID, TrackID: e.TrackID}
	}
	available, err := s.loadSummaries(ctx, requested)
	if err != nil {
		return p, err
	}
	entries := make([]queue.Entry, 0, len(c.Entries))
	mapping := map[string]string{}
	for _, old := range c.Entries {
		track, ok := available[old.TrackID]
		if !ok {
			continue
		}
		id := s.newID()
		mapping[old.EntryID] = id
		entries = append(entries, queue.Entry{EntryID: id, TrackID: old.TrackID})
		tracks[old.TrackID] = track
	}
	current := ""
	seconds := 0.0
	if c.CurrentEntryID != nil {
		current = mapping[*c.CurrentEntryID]
		if current != "" {
			seconds = c.PositionSeconds
		} else {
			// 原当前项不存在时只取其后的第一个保留项，不把旧位置带到别的曲目。
			at := -1
			for i, e := range c.Entries {
				if e.EntryID == *c.CurrentEntryID {
					at = i
					break
				}
			}
			for i := at + 1; i < len(c.Entries); i++ {
				if id := mapping[c.Entries[i].EntryID]; id != "" {
					current = id
					break
				}
			}
		}
	}
	history := make([]string, 0, len(c.History))
	for _, oldID := range c.History {
		if id := mapping[oldID]; id != "" {
			history = append(history, id)
		}
	}
	restored := queue.State{Entries: entries, CurrentEntryID: current, History: history, Mode: queue.ModeSequential}
	restored = queue.SetMode(restored, c.Mode)
	now := s.options.Now()
	p.persisted.Queue = restored
	p.persisted.PositionSeconds = clampPosition(current, seconds, entries, tracks)
	p.persisted.PositionSavedAt = now
	p.persisted.ImportedAt = &now
	p.close = true
	p.pause = true
	p.clearFailures = true
	p.change = replaceChange(entries, tracks)
	count, dropped := len(entries), len(c.Entries)-len(entries)
	p.event = &ZoneEvent{Kind: "queue_restored", Restored: &count, Dropped: &dropped}
	imported = true
	p.writeEntries = func(store *Store) error { return store.ReplaceEntries(ctx, userID, z.persisted.ZoneID, entries) }
	return p, nil
}
