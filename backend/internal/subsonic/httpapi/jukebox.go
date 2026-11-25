package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

var errNoJukeboxZone = errors.New("no jukebox zone")

type jukeboxAction struct {
	name    string
	command zones.Command
	index   int
	gain    float64
}

func parseJukeboxAction(values url.Values) (jukeboxAction, error) {
	a := jukeboxAction{name: values.Get("action")}
	switch a.name {
	case "get", "status":
	case "set", "add":
		a.command.TrackIDs = values["id"]
		for _, id := range a.command.TrackIDs {
			if !uuid.Valid(id) {
				return a, errNotFound
			}
		}
		if a.name == "set" {
			a.command.Command = "replace_queue"
			if len(a.command.TrackIDs) == 0 {
				a.command.Command = "clear_queue"
			}
		} else if len(a.command.TrackIDs) != 0 {
			a.command.Command = "enqueue"
		} else {
			return a, errParameter
		}
	case "clear":
		a.command.Command = "clear_queue"
	case "remove", "skip":
		index, err := number(values, "index", -1)
		if err != nil || index < 0 {
			return a, errParameter
		}
		a.index = index
		a.command.Command = "remove_entry"
		if a.name == "skip" {
			offset, err := number(values, "offset", 0)
			if err != nil {
				return a, err
			}
			seconds := float64(offset)
			a.command.Command = "play_entry"
			a.command.StartPositionSeconds = &seconds
		}
	case "start":
		a.command.Command = "play"
	case "stop":
		a.command.Command = "pause"
	case "shuffle":
		a.command.Command = "shuffle_queue"
	case "setGain":
		gain, err := strconv.ParseFloat(values.Get("gain"), 64)
		if err != nil || !(gain >= 0 && gain <= 1) {
			return a, errParameter
		}
		a.gain = gain
	default:
		return a, errParameter
	}
	return a, nil
}

func (h *Handlers) jukeboxControl(r *http.Request) protocol.Response {
	ctx, userID := r.Context(), memberID(r)
	action, err := parseJukeboxAction(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	selected, err := h.service.JukeboxZone(ctx, userID)
	if err != nil {
		return browseFailure(err)
	}
	state, err := h.readJukebox(ctx, userID, selected)
	if err != nil {
		return jukeboxFailure(err)
	}
	if action.name == "get" {
		return h.jukeboxPlaylist(r, state)
	}
	if action.name != "status" {
		// 冲突时重读同一播放区，按最新队列重新解析下标。
		zoneID := state.zone.ZoneID
		err = h.writeJukebox(ctx, userID, state, action)
		if errors.Is(err, zones.ErrRevisionConflict) {
			state, err = h.readJukebox(ctx, userID, &zoneID)
			if err == nil {
				err = h.writeJukebox(ctx, userID, state, action)
			}
		}
		if err != nil {
			return jukeboxFailure(err)
		}
		state, err = h.readJukebox(ctx, userID, &zoneID)
		if err != nil {
			return jukeboxFailure(err)
		}
	}
	status := state.status()
	return protocol.Response{JukeboxStatus: &status}
}

type jukeboxState struct {
	zone    zones.Zone
	devices []zones.Device
	queue   zones.QueueResult
	now     zones.Timestamp
}

func (h *Handlers) readJukebox(ctx context.Context, userID string, selected *string) (jukeboxState, error) {
	for range 2 {
		snapshot, err := h.browse.Zones.Snapshot(ctx, userID)
		if err != nil {
			return jukeboxState{}, err
		}
		zone, found := jukeboxZone(snapshot, selected)
		if !found {
			return jukeboxState{}, errNoJukeboxZone
		}
		q, err := h.browse.Zones.ReadQueue(ctx, userID, zone.ZoneID)
		if err != nil {
			return jukeboxState{}, err
		}
		if q.QueueRevision == zone.QueueRevision {
			return jukeboxState{zone: zone, devices: snapshot.Devices, queue: q, now: snapshot.ServerTime}, nil
		}
	}
	return jukeboxState{}, zones.ErrRevisionConflict
}

func jukeboxZone(snapshot zones.Snapshot, selected *string) (zones.Zone, bool) {
	var recent zones.Zone
	for i, zone := range snapshot.Zones {
		if selected != nil {
			if zone.ZoneID == *selected {
				return zone, true
			}
			continue
		}
		device := snapshot.Devices[i]
		if device.Group != nil && device.Group.Role == "member" {
			continue
		}
		if zone.LastPlayedAt != nil && (recent.LastPlayedAt == nil || zone.LastPlayedAt.After(*recent.LastPlayedAt)) {
			recent = zone
		}
	}
	return recent, recent.LastPlayedAt != nil
}

func (s jukeboxState) device() zones.Device {
	index := slices.IndexFunc(s.devices, func(device zones.Device) bool { return device.DeviceID == s.zone.DeviceID })
	return s.devices[index]
}

func (s jukeboxState) status() protocol.JukeboxStatus {
	status := protocol.JukeboxStatus{CurrentIndex: -1, Playing: s.zone.Intent == "play", Gain: s.device().Volume}
	if s.zone.Group != nil {
		status.Gain = s.groupGain()
	}
	if s.zone.Current != nil {
		status.CurrentIndex = slices.IndexFunc(s.queue.Entries, func(entry zones.QueueEntry) bool {
			return entry.EntryID == s.zone.Current.EntryID
		})
		position := s.zone.Position.Seconds
		if s.zone.Status == "playing" {
			position += max(0, s.now.Sub(s.zone.Position.At.Time).Seconds())
			if duration := s.zone.Position.DurationSeconds; duration != nil {
				position = min(position, *duration)
			}
		}
		status.Position = int(position)
	}
	return status
}

func (s jukeboxState) groupGain() float64 {
	peak := -1.0
	for _, device := range s.devices {
		if device.Group != nil && device.Group.ZoneID == s.zone.ZoneID && device.Capabilities.Volume != "fixed" {
			peak = max(peak, device.Volume)
		}
	}
	if peak < 0 {
		return 1
	}
	return peak
}

func (h *Handlers) writeJukebox(ctx context.Context, userID string, state jukeboxState, action jukeboxAction) error {
	zone := state.zone
	service := h.browse.Zones
	if action.name == "setGain" {
		if zone.Group != nil {
			_, err := service.SetGroupVolume(ctx, userID, zone.ZoneID, action.gain, zone.Revision)
			return err
		}
		device := state.device()
		_, err := service.SetVolume(ctx, userID, device.DeviceID, action.gain, device.Muted, zone.Revision)
		return err
	}
	command := action.command
	command.ExpectedRevision = &zone.Revision
	if action.name == "remove" || action.name == "skip" {
		if action.index >= len(state.queue.Entries) {
			return errParameter
		}
		command.EntryID = state.queue.Entries[action.index].EntryID
	}
	_, err := service.Command(ctx, userID, zone.ZoneID, command)
	return err
}

func (h *Handlers) jukeboxPlaylist(r *http.Request, state jukeboxState) protocol.Response {
	entries := state.queue.Entries
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.Track.TrackID
	}
	browseTracks, err := h.browse.Catalog.LoadBrowseTracks(r.Context(), ids)
	if err != nil {
		return browseFailure(err)
	}
	byID := make(map[string]catalog.BrowseTrack, len(browseTracks))
	for _, track := range browseTracks {
		byID[track.TrackID] = track
	}
	tracks := make([]catalog.BrowseTrack, len(entries))
	for i, entry := range entries {
		track, found := byID[entry.Track.TrackID]
		if !found {
			// 队列保留扫描后缺失的条目，下标与网页端保持一致。
			track = catalog.BrowseTrack{TrackSummary: entry.Track}
		}
		tracks[i] = track
	}
	mapped, err := h.ratedEntities(r, nil, nil, tracks)
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{JukeboxPlaylist: &protocol.JukeboxPlaylist{JukeboxStatus: state.status(), Entries: mapped.Songs}}
}

func jukeboxFailure(err error) protocol.Response {
	switch {
	case errors.Is(err, errNoJukeboxZone), errors.Is(err, zones.ErrNotFound):
		return protocol.Failure(protocol.General, "没有可控制的播放区")
	case errors.Is(err, zones.ErrRevisionConflict):
		return protocol.Failure(protocol.General, "Playback zone changed concurrently")
	case errors.Is(err, zones.ErrTrackNotFound):
		return browseFailure(errNotFound)
	case errors.Is(err, zones.ErrInvalidInput):
		return browseFailure(errParameter)
	default:
		return browseFailure(err)
	}
}
