package cast

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Output struct {
	Zones                                                 *zones.Service
	UserID, DeviceID, ZoneID, SessionID, BaseURL, Address string
	Online                                                func() bool
	Offline                                               func()
	Logger                                                *slog.Logger
}

type session struct {
	*Output
	client                    *castv2.Client
	ctx                       context.Context
	zone                      zones.Zone
	transport                 string
	receiverOrder, mediaOrder uint64
	volume                    castv2.Volume
	sentVolume                *float64
	seq                       *int64
	path, loadingPath         string
	mediaID                   int
	state                     string
	seconds, offset           float64
	started                   time.Time
}

var errTaken = errors.New("Cast 输出已被接管")

func (o Output) Run(ctx context.Context, sub *zones.Subscription) zones.ReleaseOutputInput {
	c, err := castv2.Dial(ctx, o.Address)
	if err != nil {
		sub.Close()
		if ctx.Err() == nil {
			o.Logger.Warn("连接 Cast 接收端失败", "address", o.Address, "error", err)
			o.Offline()
		}
		return zones.ReleaseOutputInput{}
	}
	defer c.Close()
	s := session{Output: &o, client: c, ctx: ctx}
	err = s.run(sub)
	if errors.Is(err, errTaken) {
		if _, err := o.Zones.CommandFromOutput(ctx, o.UserID, o.SessionID, "pause"); err != nil && ctx.Err() == nil {
			o.Logger.Warn("Cast 接管后回报暂停失败", "zone_id", o.ZoneID, "error", err)
		}
	} else {
		if err != nil && ctx.Err() == nil {
			o.Logger.Warn("Cast 输出已断开", "address", o.Address, "error", err)
			select {
			case <-c.Done():
				o.Offline()
			default:
			}
		}
		s.stopOwnedMedia()
	}
	return zones.ReleaseOutputInput{StreamSeq: s.seq, StreamSeconds: s.position()}
}

func (s *session) stopOwnedMedia() {
	if s.mediaID == 0 && s.loadingPath == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := s.mediaID
	if s.loadingPath != "" {
		// 关闭输出可能发生在 LOAD 已发出、回执尚未到达时。核对本次令牌再停止。
		e, err := s.client.Request(ctx, s.transport, castv2.Media, map[string]any{"type": "GET_STATUS"})
		var rows []mediaStatus
		if err == nil && e.DecodeStatus("MEDIA_STATUS", &rows) == nil && len(rows) > 0 && rows[0].Media.ContentID == s.BaseURL+s.loadingPath {
			id = rows[0].MediaSessionID
		}
	}
	if id != 0 {
		_, _ = s.client.Request(ctx, s.transport, castv2.Media, map[string]any{"type": "STOP", "mediaSessionId": id})
	}
}

func (s *session) run(sub *zones.Subscription) error {
	defer func() { sub.Close() }()
	e, err := s.client.Request(s.ctx, castv2.ReceiverID, castv2.Receiver, map[string]any{"type": "GET_STATUS"})
	if err != nil {
		return err
	}
	if err = s.receiver(e, false); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		var subscriptionClosed bool
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.client.Done():
			return net.ErrClosed
		case <-sub.Done:
			subscriptionClosed = true
		case m, ok := <-sub.Events:
			if !ok {
				subscriptionClosed = true
				break
			}
			if s.receive(m) {
				err = s.apply()
			}
		case e := <-s.client.Events():
			err = s.event(e)
		case <-ticker.C:
			if !s.Online() {
				return nil
			}
			err = s.client.Poll(castv2.ReceiverID, castv2.Receiver)
			if err == nil && s.transport != "" && s.mediaID != 0 {
				err = s.client.Poll(s.transport, castv2.Media)
			}
		}
		if subscriptionClosed {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			s.Logger.Warn("托管输出订阅已关闭，正在重新订阅", "event", "devices.output.resubscribing", "module", "cast", "zone_id", s.ZoneID)
			next, err := s.Zones.SubscribeOutput(s.ctx, s.UserID, s.SessionID)
			if err != nil {
				return err
			}
			sub = next
			continue
		}
		if errors.Is(err, zones.ErrStreamGone) {
			err = nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) receive(msg zones.Message) bool {
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
		return json.Unmarshal(msg.Data, &update) == nil && update.Device.DeviceID == s.DeviceID
	default:
		return false
	}
	if z.ZoneID != s.ZoneID || z.Revision < s.zone.Revision {
		return false
	}
	s.zone = z
	return true
}

func (s *session) receiver(e castv2.Event, launch bool) error {
	if e.Order <= s.receiverOrder {
		return nil
	}
	var status castv2.ReceiverStatus
	if err := e.DecodeStatus("RECEIVER_STATUS", &status); err != nil {
		return err
	}
	s.receiverOrder = e.Order
	s.volume = status.Volume
	for _, app := range status.Applications {
		if app.AppID != castv2.DefaultApp || app.TransportID == "" {
			continue
		}
		if launch {
			s.transport = app.TransportID
			return s.client.Connect(s.transport)
		}
		if app.TransportID == s.transport {
			return nil
		}
	}
	if launch {
		return errors.New("Cast 默认媒体接收端没有启动")
	}
	if s.transport != "" {
		return errTaken
	}
	return nil
}

func (s *session) apply() error {
	if v, ok := s.Zones.ManagedOutputVolume(s.UserID, s.SessionID); ok && s.volume.ControlType != "fixed" && (s.sentVolume == nil || *s.sentVolume != v) {
		if err := s.setVolume(map[string]any{"level": v}); err != nil {
			return err
		}
		if v > 0 && s.volume.Muted {
			if err := s.setVolume(map[string]any{"muted": false}); err != nil {
				return err
			}
		}
		s.sentVolume = &v
	}
	z := s.zone
	if z.Stream == nil {
		if s.mediaID != 0 {
			_, err := s.client.Request(s.ctx, s.transport, castv2.Media, map[string]any{"type": "STOP", "mediaSessionId": s.mediaID})
			if err != nil {
				return err
			}
		}
		s.seq, s.path, s.mediaID = nil, "", 0
		s.seconds, s.offset, s.started = 0, 0, time.Time{}
		return nil
	}
	if z.Intent == "play" && (s.seq == nil || (*s.seq != z.Stream.Seq && (z.NextStream == nil || *s.seq != z.NextStream.Seq))) {
		return s.load(z.Stream)
	}
	if s.mediaID == 0 {
		if s.state == "FINISHED" && z.NextStream != nil && z.Intent == "play" {
			return s.load(z.NextStream)
		}
		return nil
	}
	if z.Intent == "pause" && (s.state == "PLAYING" || s.state == "BUFFERING") {
		return s.control("PAUSE")
	}
	if z.Intent == "play" && s.state == "PAUSED" {
		return s.control("PLAY")
	}
	return nil
}

func (s *session) setVolume(volume map[string]any) error {
	e, err := s.client.Request(s.ctx, castv2.ReceiverID, castv2.Receiver, map[string]any{"type": "SET_VOLUME", "volume": volume})
	if err != nil {
		return err
	}
	return s.receiver(e, false)
}

func (s *session) control(kind string) error {
	e, err := s.client.Request(s.ctx, s.transport, castv2.Media, map[string]any{"type": kind, "mediaSessionId": s.mediaID})
	if err != nil {
		return err
	}
	return s.media(e, false, false)
}

type mediaInfo struct {
	ContentID   string         `json:"contentId"`
	ContentType string         `json:"contentType"`
	StreamType  string         `json:"streamType"`
	Metadata    map[string]any `json:"metadata"`
	Duration    float64        `json:"duration,omitempty"`
}

func (s *session) load(stream *zones.Stream) error {
	info, err := s.Zones.ManagedStream(s.UserID, s.SessionID, stream.Seq)
	if err != nil {
		return err
	}
	if info.Path == s.path && s.mediaID != 0 {
		if s.seq == nil || *s.seq != stream.Seq {
			s.offset += s.position()
			s.seconds = 0
			if s.state == "PLAYING" {
				s.started = time.Now()
			}
		}
		s.seq = &stream.Seq
		return nil
	}
	if s.transport == "" {
		e, err := s.client.Request(s.ctx, castv2.ReceiverID, castv2.Receiver, map[string]any{"type": "LAUNCH", "appId": castv2.DefaultApp})
		if err != nil {
			return err
		}
		if err = s.receiver(e, true); err != nil {
			return err
		}
	}
	uri := s.BaseURL + info.Path
	metadata := map[string]any{"metadataType": 3}
	item := mediaInfo{ContentID: uri, ContentType: stream.MimeType, StreamType: "BUFFERED", Metadata: metadata}
	if len(stream.Segments) > 0 {
		seg := stream.Segments[0]
		t := seg.Track
		metadata["title"], metadata["artist"], metadata["albumName"] = t.Title, t.Artist, t.Release.Title
		if t.Release.Artwork != nil {
			metadata["images"] = []map[string]string{{"url": uri + "/artwork"}}
		}
		if t.DurationSeconds != nil {
			item.Duration = max(0, *t.DurationSeconds-seg.TrackStartSeconds)
		}
	}
	s.loadingPath = info.Path
	e, err := s.client.Request(s.ctx, s.transport, castv2.Media, map[string]any{"type": "LOAD", "autoplay": true, "currentTime": 0, "media": item})
	if err != nil {
		return err
	}
	s.path, s.seq, s.mediaID = info.Path, &stream.Seq, 0
	s.seconds, s.offset, s.started = 0, 0, time.Time{}
	if err := s.media(e, true, false); err != nil {
		return err
	}
	s.loadingPath = ""
	return nil
}

func (s *session) event(e castv2.Event) error {
	switch e.Namespace {
	case castv2.Connection:
		if e.Type == "CLOSE" && (e.Source == s.transport || e.Source == castv2.ReceiverID) {
			return errTaken
		}
	case castv2.Receiver:
		if e.Source == castv2.ReceiverID && e.Type == "RECEIVER_STATUS" {
			return s.receiver(e, false)
		}
	case castv2.Media:
		if e.Source == s.transport && s.mediaID != 0 && e.Type == "MEDIA_STATUS" {
			return s.media(e, false, true)
		}
	}
	return nil
}

type mediaStatus struct {
	MediaSessionID int       `json:"mediaSessionId"`
	PlayerState    string    `json:"playerState"`
	IdleReason     string    `json:"idleReason"`
	CurrentTime    *float64  `json:"currentTime"`
	Media          mediaInfo `json:"media"`
}

func (s *session) media(e castv2.Event, loading, remote bool) error {
	if e.Order <= s.mediaOrder {
		return nil
	}
	s.mediaOrder = e.Order
	var rows []mediaStatus
	if err := e.DecodeStatus("MEDIA_STATUS", &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		if loading {
			return errors.New("Cast 未返回媒体会话")
		}
		// GET_STATUS 没有媒体会话时，设备已经释放或换掉了当前媒体。
		return errTaken
	}
	m := rows[0]
	if !loading && m.MediaSessionID != s.mediaID {
		return errTaken
	}
	if m.Media.ContentID != "" && m.Media.ContentID != s.BaseURL+s.path {
		return errTaken
	}
	if m.MediaSessionID <= 0 {
		return errors.New("Cast 媒体会话无效")
	}
	s.mediaID = m.MediaSessionID
	previous := s.state
	s.seconds = s.position()
	// 部分接收端在退出媒体会话时清零 currentTime，最后的有效播放头仍用于释放。
	if m.CurrentTime != nil && (m.PlayerState != "IDLE" || *m.CurrentTime > 0) {
		s.seconds = max(0, *m.CurrentTime-s.offset)
	}
	s.state = m.PlayerState
	s.started = time.Time{}
	var status string
	switch m.PlayerState {
	case "PLAYING":
		s.started = time.Now()
		status = "playing"
	case "PAUSED":
		status = "paused"
	case "BUFFERING":
		status = "buffering"
	case "IDLE":
		s.mediaID = 0
		switch m.IdleReason {
		case "FINISHED":
			s.state = "FINISHED"
			if s.zone.NextStream != nil && s.zone.Intent == "play" {
				return s.load(s.zone.NextStream)
			}
			return s.report("ended", "paused", nil)
		case "CANCELLED", "INTERRUPTED":
			return errTaken
		case "ERROR":
			fault := "stream_lost"
			return s.report("fault", "paused", &fault)
		}
		return nil
	default:
		return nil
	}
	if remote && previous != s.state {
		command := ""
		if s.state == "PAUSED" && s.zone.Intent == "play" {
			command = "pause"
		}
		if s.state == "PLAYING" && previous == "PAUSED" && s.zone.Intent == "pause" {
			command = "play"
		}
		if command != "" {
			r, err := s.Zones.CommandFromOutput(s.ctx, s.UserID, s.SessionID, command)
			if err != nil {
				return err
			}
			s.zone = r.Zone
		}
	}
	return s.report("status", status, nil)
}

func (s *session) position() float64 {
	if !s.started.IsZero() {
		return s.seconds + time.Since(s.started).Seconds()
	}
	return s.seconds
}

func (s *session) report(kind, status string, fault *string) error {
	r, err := s.Zones.ReportOutput(s.ctx, s.UserID, s.SessionID, zones.ReportOutputInput{Kind: kind, Status: status, Fault: fault, StreamSeq: s.seq, StreamSeconds: s.position()})
	if err == nil {
		s.zone = r.Zone
	}
	return err
}
