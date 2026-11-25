package zones

import (
	"context"

	"sync/atomic"
)

// UPnPStreamInfo 供托管输出推送 URI 与区分自然流尾、设备端停止。
type UPnPStreamInfo struct {
	Path            string
	Delivered       bool
	DurationSeconds float64
}

type upnpStreamToken struct {
	reader    *groupReader
	key       string
	artworkID string
	service   *Service
	output    *outputSession
	handle    *renderHandle // 由播放区锁保护。
}

func (t *upnpStreamToken) remove() {
	t.service.upnpStreams.Delete(t.key)
	t.handle = nil
}

func (s *Service) UPnPStream(userID, sessionID string, seq int64) (UPnPStreamInfo, error) {
	return s.ManagedStream(userID, sessionID, seq)
}

// ManagedStream 只向当前托管输出签发令牌，同一渲染句柄共用一个路径。
func (s *Service) ManagedStream(userID, sessionID string, seq int64) (UPnPStreamInfo, error) {
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return UPnPStreamInfo{}, err
	}
	z := out.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	h := z.handleFor(&seq)
	if outputError(out) != nil || (z.device.Kind != "upnp" && z.device.Kind != "cast" && z.device.Kind != "slimproto") || h == nil || h.session.HLS() != nil {
		return UPnPStreamInfo{}, ErrStreamGone
	}
	if len(h.groupIDs) > 0 {
		info := s.groupStreamInfo(out, h)
		leader := info.Members[0]
		return UPnPStreamInfo{Path: leader.Path, Delivered: leader.Delivered, DurationSeconds: info.DurationSeconds}, nil
	}
	if h.upnp == nil {
		t := s.newStreamToken(out, h)
		h.upnp = t
		s.upnpStreams.Store(t.key, t)
	}
	info := UPnPStreamInfo{Path: "/upnp/streams/" + h.upnp.key, Delivered: h.delivered.Load()}
	if completed, ok := h.session.(interface{ CompletedDuration() float64 }); ok && info.Delivered {
		info.DurationSeconds = completed.CompletedDuration()
	}
	return info, nil
}

// UPnPStreamBody 的 Done 通知 HTTP 层终止旧连接，包括正在等待客户端接收的写操作。
type UPnPStreamBody struct {
	StreamBody
	ArtworkID string
	Done      <-chan struct{}
	reader    *groupReader
	delivered *atomic.Bool
}

func (b UPnPStreamBody) MarkDelivered() { b.delivered.Store(true) }

// FinishDelivery 在客户端中断时唤醒其读者，立即释放该成员保留的块。
func (b UPnPStreamBody) FinishDelivery() {
	r := b.reader
	if r == nil || r.delivered.Load() {
		return
	}
	d := r.dispatch
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || r.dropped {
		return
	}
	r.dropLocked(true)
	d.trimLocked()
	d.ready.Broadcast()
}

// OpenUPnPStream 在播放区锁内核对令牌、续流并取走唯一 reader。HEAD 只读取 MIME。
func (s *Service) OpenUPnPStream(ctx context.Context, key string, take bool) (UPnPStreamBody, error) {
	value, ok := s.upnpStreams.Load(key)
	if !ok || s.closed.Load() {
		return UPnPStreamBody{}, ErrStreamGone
	}
	t := value.(*upnpStreamToken)
	out, z := t.output, t.output.zone
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	var updated *Zone
	defer func() {
		z.mu.Unlock()
		if updated != nil {
			s.publishZone(out.userID, *updated)
		}
	}()
	h := t.handle
	if h == nil || outputError(out) != nil {
		return UPnPStreamBody{}, ErrStreamGone
	}
	if t.reader != nil {
		return t.openGroup(take)
	}
	if h.upnp != t {
		return UPnPStreamBody{}, ErrStreamGone
	}
	if take && h.taken {
		if len(z.group) > 0 || z.rebuild != nil || h.rebuild != nil {
			return UPnPStreamBody{}, ErrStreamGone
		}
		// 已获取的 Body 不能倒带。按当前播放头走 stream_lost 的恢复路径，
		// 并在发布新 seq 前将原 URI 移至新句柄，避免渲染器被重复推流。
		current := h == z.currentHandle
		p := z.persisted
		now := s.Now()
		if current {
			p.PositionSeconds = positionAt(z, now, z.cursorTracks)
			p.PositionSavedAt = now
		}
		p.Revision++
		if err := s.transact(ctx, func(st *Store) error { return st.UpdateZone(ctx, out.userID, p) }); err != nil {
			return UPnPStreamBody{}, err
		}
		z.persisted, z.positionWrittenAt = p, now
		h.upnp = nil
		s.resumeStream(out.userID, z, z.cursorTracks, h)
		zone := s.zoneOf(z, z.cursorTracks)
		updated = &zone
		h = z.currentHandle
		if !current {
			h = z.nextHandle
		}
		if h == nil {
			t.remove()
			return UPnPStreamBody{}, ErrStreamGone
		}
		t.handle, h.upnp = h, t
	}
	result := UPnPStreamBody{StreamBody: StreamBody{MimeType: h.stream.MimeType}, ArtworkID: t.artworkID, Done: h.done, delivered: &h.delivered}
	if take {
		h.taken = true
		result.Body = h.session.Body()
	}
	return result, nil
}
