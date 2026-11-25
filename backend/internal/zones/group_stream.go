package zones

import (
	"crypto/rand"
	"slices"
)

type GroupMemberStream struct {
	DeviceID, Path     string
	Delivered, Dropped bool
}
type GroupStreamInfo struct {
	Members         []GroupMemberStream
	DurationSeconds float64
}

type groupStream struct {
	*groupDispatcher
	tokens  []*upnpStreamToken
	timeout Timer
}

func (s *Service) ManagedGroupStream(userID, sessionID string, seq int64) (GroupStreamInfo, error) {
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return GroupStreamInfo{}, err
	}
	z := out.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	h := z.handleFor(&seq)
	if !out.managed || outputError(out) != nil || z.device.Kind != "slimproto" || h == nil || len(h.groupIDs) == 0 || h.session.HLS() != nil {
		return GroupStreamInfo{}, ErrStreamGone
	}
	return s.groupStreamInfo(out, h), nil
}

// 只有首次签发才取走渲染 Body；后续查询与 HEAD 均不影响各读者的位置。
func (s *Service) groupStreamInfo(out *outputSession, h *renderHandle) GroupStreamInfo {
	if h.group == nil {
		g := &groupStream{groupDispatcher: newGroupDispatcher(h.groupIDs, h.session.Format())}
		for _, r := range g.readers {
			t := s.newStreamToken(out, h)
			t.reader = r
			g.tokens = append(g.tokens, t)
			s.upnpStreams.Store(t.key, t)
		}
		h.group = g
		h.taken = true
		g.timeout = s.AfterFunc(groupConnectTimeout, g.expire)
		source := h.session.Body()
		s.workers.Add(2)
		go func() { defer s.workers.Done(); g.pump(source) }()
		go s.watchGroupDrops(out, h, g)
	}
	g := h.group
	g.mu.Lock()
	defer g.mu.Unlock()
	info := GroupStreamInfo{Members: make([]GroupMemberStream, 0, len(g.tokens))}
	for _, t := range g.tokens {
		if t.handle == nil {
			continue
		}
		r := t.reader
		info.Members = append(info.Members, GroupMemberStream{r.id, "/upnp/streams/" + t.key, r.delivered.Load(), r.dropped})
	}
	if completed, ok := h.session.(interface{ CompletedDuration() float64 }); ok && g.readers[0].delivered.Load() {
		info.DurationSeconds = completed.CompletedDuration()
	}
	return info
}

func (s *Service) newStreamToken(out *outputSession, h *renderHandle) *upnpStreamToken {
	t := &upnpStreamToken{key: rand.Text(), service: s, output: out, handle: h}
	if len(h.stream.Segments) > 0 {
		if art := h.stream.Segments[0].Track.Release.Artwork; art != nil {
			t.artworkID = art.ResourceID
		}
	}
	return t
}

func (h *renderHandle) removeGroupMember(id string) {
	h.groupIDs = slices.DeleteFunc(h.groupIDs, func(other string) bool { return other == id })
	g := h.group
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range g.tokens {
		if t.reader.id == id {
			t.reader.dropLocked(false)
			t.remove()
		}
	}
	g.trimLocked()
	g.ready.Broadcast()
}

func (t *upnpStreamToken) openGroup(take bool) (UPnPStreamBody, error) {
	r := t.reader
	d := r.dispatch
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.dropped || d.closed || take && r.taken {
		return UPnPStreamBody{}, ErrStreamGone
	}
	body := UPnPStreamBody{StreamBody: StreamBody{MimeType: t.handle.stream.MimeType}, ArtworkID: t.artworkID, Done: r.done, delivered: &r.delivered}
	if take {
		r.taken = true
		body.Body = r
		body.reader = r
	}
	return body, nil
}

func (s *Service) watchGroupDrops(out *outputSession, h *renderHandle, g *groupStream) {
	defer s.workers.Done()
	z := out.zone
	for {
		select {
		case <-h.done:
			return
		case id := <-g.drops:
			z.dispatch.Lock()
			z.mu.Lock()
			if id != z.device.DeviceID && outputError(out) == nil && z.handleFor(&h.stream.Seq) == h && slices.Contains(h.groupIDs, id) {
				if err := s.setGroupStateLocked(s.ctx, out.userID, z, id, "dropped"); err != nil {
					s.reportAsync("group_stream_drop", out.userID, z.persisted.ZoneID, err)
				}
			}
			z.mu.Unlock()
			z.dispatch.Unlock()
		}
	}
}

func (g *groupStream) close() {
	g.timeout.Stop()
	g.groupDispatcher.close()
	for _, t := range g.tokens {
		t.remove()
	}
}
