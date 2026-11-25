package slimproto

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type OutputZones interface {
	ManagedOutputVolume(userID, sessionID string) (float64, bool)
	ManagedStream(userID, sessionID string, seq int64) (zones.UPnPStreamInfo, error)
	ManagedGroupStream(userID, sessionID string, seq int64) (zones.GroupStreamInfo, error)
	GroupMembers(userID, sessionID string) []zones.GroupMember
	ReportGroupMember(context.Context, string, string, string, string) error
	ReportOutput(context.Context, string, string, zones.ReportOutputInput) (zones.OutputResult, error)
	SubscribeOutput(context.Context, string, string) (*zones.Subscription, error)
}

type Output struct {
	Zones                                        OutputZones
	UserID, DeviceID, ZoneID, SessionID, BaseURL string
	Player                                       *Player
	Logger                                       *slog.Logger
	Offline                                      func()
	Member                                       func(context.Context, zones.GroupMember, bool) (MemberLease, error)
}

type outputStream struct {
	seq              int64
	path             string
	decoded, started bool
	duration         float64
}

type outputSession struct {
	*Output
	ctx                    context.Context
	port                   uint16
	zone                   zones.Zone
	current, next          *outputStream
	state, controlIntent   string
	seconds, elapsedOffset float64
	anchor                 time.Time
	group                  *groupCoordinator
	memberEvents           chan groupEvent
	// 连续跳转时，以最后一个开流的 STMc 隔开旧流在途的状态。
	awaitingConnect int
}

func (o Output) Run(ctx context.Context, sub *zones.Subscription) zones.ReleaseOutputInput {
	s := outputSession{Output: &o, ctx: ctx}
	err := s.run(sub)
	position := zones.ReleaseOutputInput{StreamSeq: s.seq(), StreamSeconds: s.position()}
	if s.Player.ctx.Err() != nil {
		if o.Offline != nil {
			o.Offline()
		}
	} else if s.current != nil || (s.group != nil && s.group.leader.current != nil) {
		s.Player.stopOutput()
	}
	if ctx.Err() == nil && err != nil && !errors.Is(err, net.ErrClosed) {
		o.Logger.Warn("SlimProto 托管输出结束", "event", "slimproto.output.failed", "module", "slimproto", "zone_id", o.ZoneID, "error", err)
	}
	return position
}

func (s *outputSession) run(sub *zones.Subscription) error {
	defer func() { sub.Close() }()
	base, err := url.Parse(s.BaseURL)
	if err != nil {
		return err
	}
	port, err := strconv.ParseUint(base.Port(), 10, 16)
	if err != nil {
		return err
	}
	s.port = uint16(port)
	events := s.Player.subscribe()
	defer s.Player.unsubscribe(events)
	defer func() {
		if s.group != nil {
			s.seconds, s.anchor = s.position(), time.Time{}
			s.group.close()
		}
	}()
	ticker := time.NewTicker(time.Second)
	groupTicks := false
	defer ticker.Stop()
	for {
		if grouped := s.group != nil; grouped != groupTicks {
			groupTicks = grouped
			if grouped {
				ticker.Reset(250 * time.Millisecond)
			} else {
				ticker.Reset(time.Second)
			}
		}
		closed := false
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.Player.ctx.Done():
			return net.ErrClosed
		case <-sub.Done:
			closed = true
		case msg, ok := <-sub.Events:
			if !ok {
				closed = true
			} else if s.receive(msg) {
				err = s.apply()
			}
		case ev := <-events:
			if s.group != nil {
				err = s.group.event(s.group.leader, ev)
			} else {
				err = s.event(ev)
			}
		case ev := <-s.memberEvents:
			if s.group != nil && ev.peer.stopRelay == ev.lease && s.group.peers[ev.peer.DeviceID] == ev.peer {
				if ev.lost != "" {
					err = s.group.drop(ev.peer, ev.lost)
				} else {
					err = s.group.event(ev.peer, ev.event)
				}
			}
		case <-ticker.C:
			if s.group != nil {
				err = s.group.tick(time.Now())
				break
			}
			err = s.apply()
			if err == nil && s.state == "playing" {
				err = s.report("status", s.state, nil, s.seq(), s.position())
			}
		}
		if closed {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			s.Logger.Warn("托管输出订阅已关闭，正在重新订阅", "event", "devices.output.resubscribing", "module", "slimproto", "zone_id", s.ZoneID)
			next, err := s.Zones.SubscribeOutput(s.ctx, s.UserID, s.SessionID)
			if err != nil {
				return err
			}
			sub = next
		}
		if err != nil && !errors.Is(err, zones.ErrStreamGone) {
			return err
		}
		err = nil
	}
}

func (s *outputSession) receive(msg zones.Message) bool {
	var z zones.Zone
	switch msg.Kind {
	case "snapshot":
		var snapshot zones.Snapshot
		if json.Unmarshal(msg.Data, &snapshot) != nil {
			return false
		}
		for _, item := range snapshot.Zones {
			if item.ZoneID == s.ZoneID {
				z = item
				break
			}
		}
	case "zone":
		var update struct{ Zone zones.Zone }
		if json.Unmarshal(msg.Data, &update) != nil {
			return false
		}
		z = update.Zone
	case "device":
		var update struct{ Device zones.Device }
		return json.Unmarshal(msg.Data, &update) == nil && (update.Device.DeviceID == s.DeviceID || (s.group != nil && s.group.peers[update.Device.DeviceID] != nil))
	default:
		return false
	}
	if z.ZoneID != s.ZoneID || z.Revision < s.zone.Revision {
		return false
	}
	s.zone = z
	return true
}

func (s *outputSession) seq() *int64 {
	if s.current == nil {
		return nil
	}
	return &s.current.seq
}

func (s *outputSession) position() float64 {
	seconds := s.seconds
	if s.state == "playing" && !s.anchor.IsZero() {
		seconds += max(0, time.Since(s.anchor).Seconds())
	}
	if s.current != nil {
		duration := s.current.duration
		stream := s.zone.Stream
		if duration == 0 && stream != nil && stream.Seq == s.current.seq && len(stream.Segments) > 0 {
			last := stream.Segments[len(stream.Segments)-1]
			if last.Track.DurationSeconds != nil {
				duration = last.StreamStartSeconds + *last.Track.DurationSeconds - last.TrackStartSeconds
			}
		}
		if duration > 0 {
			seconds = min(seconds, duration)
		}
	}
	return max(0, seconds)
}

func (s *outputSession) report(kind, status string, fault *string, seq *int64, seconds float64) error {
	r, err := s.Zones.ReportOutput(s.ctx, s.UserID, s.SessionID, zones.ReportOutputInput{Kind: kind, Status: status, Fault: fault, StreamSeq: seq, StreamSeconds: seconds})
	if err == nil {
		s.zone = r.Zone
	}
	return err
}

func (s *outputSession) stop() error {
	if s.current != nil {
		if err := s.Player.command("strm", control('q')); err != nil {
			return err
		}
	}
	s.current, s.next = nil, nil
	s.seconds, s.elapsedOffset, s.anchor = 0, 0, time.Time{}
	s.state, s.controlIntent = "", ""
	return nil
}

func (s *outputSession) apply() error {
	if s.zone.ZoneID == "" {
		return nil
	}
	if s.zone.Group != nil && s.group == nil {
		if s.memberEvents == nil {
			s.memberEvents = make(chan groupEvent, 8*64)
		}
		s.group = newGroup(s)
	}
	if g := s.group; g != nil {
		if s.zone.Group != nil || g.buffering {
			return g.apply()
		}
		g.close()
		s.group = nil
		if g.reference != g.leader && s.zone.Stream != nil {
			if err := g.restart(); err != nil {
				return err
			}
			if err := s.stop(); err != nil {
				return err
			}
		}
	}
	if volume, ok := s.Zones.ManagedOutputVolume(s.UserID, s.SessionID); ok {
		if err := s.Player.setVolume(volume); err != nil {
			return err
		}
	}
	z := s.zone
	if z.Stream == nil {
		return s.stop()
	}
	if s.current == nil || s.current.seq != z.Stream.Seq {
		info, err := s.Zones.ManagedStream(s.UserID, s.SessionID, z.Stream.Seq)
		if err != nil {
			return err
		}
		if s.current != nil && info.Path == s.current.path {
			s.elapsedOffset += s.position()
			s.seconds, s.anchor = 0, time.Now()
			s.current.seq, s.current.duration, s.current.decoded = z.Stream.Seq, 0, false
		} else {
			if err := s.stop(); err != nil {
				return err
			}
			if z.Intent != "play" {
				return nil
			}
			if err := s.start(z.Stream, info, false); err != nil {
				return err
			}
			return s.report("status", "loading", nil, s.seq(), 0)
		}
	}
	if s.next != nil && (z.NextStream == nil || z.NextStream.Seq != s.next.seq) {
		samePath := false
		if z.NextStream != nil {
			info, err := s.Zones.ManagedStream(s.UserID, s.SessionID, z.NextStream.Seq)
			if err != nil {
				return err
			}
			samePath = info.Path == s.next.path
		}
		if samePath {
			s.next.seq = z.NextStream.Seq
			s.next.decoded = false
		} else {
			if err := s.Player.command("strm", control('f')); err != nil {
				return err
			}
			s.next = nil
		}
	}
	if z.Intent != s.controlIntent {
		cmd := byte('p')
		if z.Intent == "play" {
			cmd = 'u'
		}
		if err := s.Player.command("strm", control(cmd)); err != nil {
			return err
		}
		s.controlIntent = z.Intent
	}
	if s.current.decoded && s.next == nil && z.Intent == "play" {
		info, err := s.Zones.ManagedStream(s.UserID, s.SessionID, s.current.seq)
		if err != nil {
			return err
		}
		if info.Delivered {
			s.current.duration = info.DurationSeconds
			if z.NextStream != nil {
				info, err = s.Zones.ManagedStream(s.UserID, s.SessionID, z.NextStream.Seq)
				if err != nil {
					return err
				}
				return s.start(z.NextStream, info, true)
			}
		}
	}
	return nil
}

func (s *outputSession) start(stream *zones.Stream, info zones.UPnPStreamInfo, prefetch bool) error {
	rate := 0
	if stream.Format != nil {
		rate = stream.Format.SampleRate
	}
	frame, err := startStream(s.Player.local, s.port, info.Path, rate, '1', 255)
	if err != nil {
		return err
	}
	if err := s.Player.send(frame); err != nil {
		return err
	}
	s.awaitingConnect++
	opened := &outputStream{seq: stream.Seq, path: info.Path}
	if prefetch {
		s.next = opened
		return nil
	}
	s.current = opened
	s.state, s.controlIntent = "loading", "play"
	return nil
}

func (s *outputSession) event(e playerEvent) error {
	if e.Event == "STMc" {
		if s.awaitingConnect > 0 {
			s.awaitingConnect--
		}
		return nil
	}
	if s.current == nil {
		return nil
	}
	// 接续流建连时旧流仍在出声，位置继续归当前 seq。
	if s.awaitingConnect > 0 {
		if s.next == nil {
			return nil
		}
		switch e.Event {
		case "STMt", "STMp", "STMr", "STMo":
		case "STMs":
			if s.current.started {
				return nil
			}
		default:
			return nil
		}
	}
	switch e.Event {
	case "STMd":
		target := s.current
		if s.next != nil && s.awaitingConnect == 0 {
			target = s.next
		}
		target.decoded = true
		return s.apply()
	case "STMs":
		if s.current.started && s.next != nil {
			s.current, s.next = s.next, nil
		}
		s.current.started = true
		s.seconds, s.elapsedOffset = 0, 0
		s.anchor, s.state = e.at, "playing"
	case "STMp", "STMr", "STMo", "STMt", "STMu":
		if !s.current.started && e.Event == "STMt" && s.state != "buffering" {
			return nil
		}
		elapsed := float64(e.ElapsedMS) / 1000
		if uint64(e.ElapsedSeconds)*1000 > 1<<32 {
			elapsed = float64(e.ElapsedSeconds) + float64(e.ElapsedMS%1000)/1000
		}
		switch e.Event {
		case "STMp":
			s.state = "paused"
		case "STMr":
			s.state = "playing"
		case "STMo":
			s.state = "buffering"
			s.Logger.Warn("播放器输出缓冲耗尽", "event", "slimproto.output.underrun", "module", "slimproto", "zone_id", s.ZoneID)
		case "STMt":
			if s.state == "buffering" && s.controlIntent == "play" && (e.OutputFull > 0 || elapsed > s.elapsedOffset+s.seconds) {
				s.state = "playing"
			} else {
				if s.state == "playing" {
					s.seconds = max(0, elapsed-s.elapsedOffset)
					s.anchor = e.at
				}
				return nil
			}
		case "STMu":
			if s.next != nil || s.zone.NextStream != nil {
				return s.apply()
			}
			pos := s.position()
			s.state = "ended"
			s.seconds = max(pos, elapsed-s.elapsedOffset)
			return s.report("ended", "paused", nil, s.seq(), s.position())
		}
		s.seconds = max(0, elapsed-s.elapsedOffset)
		s.anchor = e.at
	case "STMn", "DSCO":
		fault := "stream_lost"
		if s.next != nil {
			seq := s.next.seq
			return s.report("fault", "paused", &fault, &seq, 0)
		}
		return s.report("fault", "paused", &fault, s.seq(), s.position())
	default:
		return nil
	}
	return s.report("status", s.state, nil, s.seq(), s.position())
}
