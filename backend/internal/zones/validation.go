package zones

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 64 && !strings.ContainsRune(name, 0)
}
func validMode(mode queue.Mode) bool {
	return mode == queue.ModeSequential || mode == queue.ModeRepeatAll || mode == queue.ModeRepeatOne || mode == queue.ModeShuffle
}
func defaultHeadroom() Headroom { return Headroom{DB: -3} }
func validHeadroom(h Headroom) bool {
	// 与一位小数的规范值比较，避免直接对二进制浮点取余误拒合法步进。
	return finite(h.DB) && h.DB >= -30 && h.DB <= 0 && h.DB == math.Round(h.DB*10)/10
}
func validateSettings(p SettingsPatch) error {
	if p.Leveling != nil && *p.Leveling != "off" && *p.Leveling != "auto" && *p.Leveling != "track" && *p.Leveling != "album" {
		return ErrInvalidInput
	}
	if p.CrossfadeSeconds != nil && *p.CrossfadeSeconds != 3 && *p.CrossfadeSeconds != 5 && *p.CrossfadeSeconds != 8 && *p.CrossfadeSeconds != 12 {
		return ErrInvalidInput
	}
	if p.Quality != nil && *p.Quality != "lossless" && *p.Quality != "lossy" {
		return ErrInvalidInput
	}
	if p.Headroom != nil && !validHeadroom(*p.Headroom) {
		return ErrInvalidInput
	}
	return nil
}
func applySettings(p PlaybackSettings, patch SettingsPatch) PlaybackSettings {
	if patch.Leveling != nil {
		p.Leveling = *patch.Leveling
	}
	if patch.Gapless != nil {
		p.Gapless = *patch.Gapless
	}
	if patch.Crossfade != nil {
		p.Crossfade = *patch.Crossfade
	}
	if patch.CrossfadeSeconds != nil {
		p.CrossfadeSeconds = *patch.CrossfadeSeconds
	}
	if patch.Quality != nil {
		p.Quality = *patch.Quality
	}
	if patch.Headroom != nil {
		p.Headroom = *patch.Headroom
	}
	return p
}
func validateCommand(c Command) error {
	switch c.Command {
	case "play_tracks", "play_next", "enqueue":
		if len(c.TrackIDs) == 0 || (c.Command == "play_tracks" && len(c.TrackIDs) > queue.MaxQueueEntries) {
			return ErrInvalidInput
		}
		for _, id := range c.TrackIDs {
			if !validID(id) {
				return ErrInvalidInput
			}
		}
	case "play_entry", "remove_entry":
		if !validID(c.EntryID) {
			return ErrInvalidInput
		}
	case "move_entry":
		if !validID(c.EntryID) || (c.Delta != -1 && c.Delta != 1) {
			return ErrInvalidInput
		}
	case "set_mode":
		if !validMode(c.Mode) {
			return ErrInvalidInput
		}
	case "seek":
		if !finite(c.PositionSeconds) || c.PositionSeconds < 0 {
			return ErrInvalidInput
		}
	case "set_settings":
		return validateSettings(c.Settings)
	case "transfer":
		if !validID(c.TargetZoneID) {
			return ErrInvalidInput
		}
	case "import":
		if len(c.Entries) > queue.MaxQueueEntries || len(c.History) > queue.MaxHistory || !validMode(c.Mode) || !finite(c.PositionSeconds) || c.PositionSeconds < 0 {
			return ErrInvalidInput
		}
		ids := map[string]bool{}
		for _, e := range c.Entries {
			if !utf8.ValidString(e.EntryID) || utf8.RuneCountInString(e.EntryID) < 1 || utf8.RuneCountInString(e.EntryID) > 64 || ids[e.EntryID] || !validID(e.TrackID) {
				return ErrInvalidInput
			}
			ids[e.EntryID] = true
		}
		if c.CurrentEntryID != nil && (!utf8.ValidString(*c.CurrentEntryID) || utf8.RuneCountInString(*c.CurrentEntryID) > 64) {
			return ErrInvalidInput
		}
		for _, id := range c.History {
			if !utf8.ValidString(id) || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 64 {
				return ErrInvalidInput
			}
		}
	case "clear_queue", "play", "pause", "stop", "next", "previous", "retry":
	default:
		return ErrInvalidInput
	}
	return nil
}
func normalizeCommand(c Command) Command {
	c.EntryID = strings.ToLower(c.EntryID)
	c.TargetZoneID = strings.ToLower(c.TargetZoneID)
	c.TrackIDs = append([]string(nil), c.TrackIDs...)
	for i, id := range c.TrackIDs {
		c.TrackIDs[i] = strings.ToLower(id)
	}
	c.Entries = append([]ImportEntry(nil), c.Entries...)
	for i := range c.Entries {
		c.Entries[i].TrackID = strings.ToLower(c.Entries[i].TrackID)
	}
	return c
}
