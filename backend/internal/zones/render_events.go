package zones

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

type renderHandle struct {
	session      RenderSession
	stream       *Stream
	generation   uint64
	entryID      string
	startSeconds float64
	taken        bool
	wake         chan struct{}
	done         chan struct{}
	once         sync.Once
	// 接续流只有一个公开槽位：提前宣布的后继只保留最新目标，晋升后再开流。
	boundaryEntryID string
	signal          json.RawMessage
	rebuild         Timer
	rebuildVersion  uint64
}

func (h *renderHandle) close() {
	h.once.Do(func() {
		if h.rebuild != nil {
			h.rebuild.Stop()
			h.rebuild = nil
		}
		close(h.done)
		h.session.Close()
	})
}
func (z *zoneRuntime) handleFor(seq *int64) *renderHandle {
	if seq == nil {
		return nil
	}
	if h := z.currentHandle; h != nil && h.stream.Seq == *seq {
		return h
	}
	if h := z.nextHandle; h != nil && h.stream.Seq == *seq {
		return h
	}
	return nil
}
func (z *zoneRuntime) hasRenderSeq(seq int64) bool { return z.handleFor(&seq) != nil }
func (s *Service) createRender(userID string, z *zoneRuntime, entryID string, seconds float64) (*renderHandle, string) {
	if s.options.Renderer == nil {
		return nil, "transient"
	}
	format, err := s.options.Renderer.Negotiate(cloneCapabilities(z.device.Capabilities), z.persisted.Settings.Quality)
	if err != nil {
		return nil, "no_stream_format"
	}
	entry := renderEntry(z.persisted.Queue, entryID, z.cursorTracks)
	spec := RenderSpec{UserID: userID, ZoneID: z.persisted.ZoneID, Seq: z.persisted.Revision, Start: entry, StartSeconds: seconds, Settings: settingsOf(z.persisted.Settings), Format: format, Cursor: &renderCursor{service: s, zone: z, generation: z.generation, seq: z.persisted.Revision}}
	session, err := s.options.Renderer.Open(s.ctx, spec)
	if err != nil || session == nil {
		if session != nil {
			session.Close()
		}
		return nil, "transient"
	}
	concrete := session.Format()
	var projected *StreamAudioFormat
	if concrete.Codec != "" || concrete.SampleRate != 0 || concrete.BitDepth != 0 || concrete.Channels != 0 {
		var bits *int
		if concrete.BitDepth > 0 {
			bits = cloneValue(&concrete.BitDepth)
		}
		projected = &StreamAudioFormat{Codec: concrete.Codec, SampleRate: concrete.SampleRate, BitDepth: bits, Channels: concrete.Channels}
	}
	quality := concrete.Quality
	if quality == "" {
		quality = format.Quality
	}
	mimeType := session.MimeType()
	if mimeType == "" {
		mimeType = format.MimeType
	}
	url := fmt.Sprintf("/api/v1/me/playback/zones/%s/streams/%d", spec.ZoneID, spec.Seq)
	if format.Delivery == "hls" {
		url += "/index.m3u8"
	}
	stream := &Stream{Seq: spec.Seq, URL: url, MimeType: mimeType, Quality: quality, Format: projected, Segments: []StreamSegment{{EntryID: entryID, Track: z.cursorTracks[entry.TrackID], TrackStartSeconds: seconds}}}
	return &renderHandle{session: session, stream: stream, generation: z.generation, entryID: entryID, startSeconds: seconds, wake: make(chan struct{}, 1), done: make(chan struct{})}, ""
}
func (s *Service) replaceNext(userID string, z *zoneRuntime, entryID string, seconds float64) string {
	awaiting := z.nextHandle != nil && z.resumeAwaitingSeq == z.nextHandle.stream.Seq
	if z.nextHandle != nil {
		z.nextHandle.close()
	}
	z.nextHandle, z.nextSession, z.nextStream = nil, nil, nil
	handle, kind := s.createRender(userID, z, entryID, seconds)
	if handle == nil {
		return kind
	}
	z.nextHandle, z.nextSession, z.nextStream = handle, handle.session, handle.stream
	if awaiting {
		z.resumeAwaitingSeq = handle.stream.Seq
	}
	s.consumeRender(userID, z, handle)
	return ""
}
func (s *Service) openNext(userID string, z *zoneRuntime, entryID string, seconds float64) {
	s.cancelResume(z)
	if kind := s.replaceNext(userID, z, entryID, seconds); kind != "" {
		s.renderFault(z, kind)
		s.schedulePause(userID, z)
	}
}

func (s *Service) promoteNext(userID string, z *zoneRuntime) {
	if z.currentHandle != nil {
		z.currentHandle.close()
	}
	h := z.nextHandle
	z.currentHandle, z.session, z.stream = h, h.session, h.stream
	z.nextHandle, z.nextSession, z.nextStream = nil, nil, nil
	z.signal = append(json.RawMessage(nil), h.signal...)
	s.beginVisit(z, z.persisted.Queue.CurrentEntryID, true)
	z.event = &ZoneEvent{Seq: z.persisted.Revision, Kind: "track_started", EntryID: z.persisted.Queue.CurrentEntryID}
	if id := h.boundaryEntryID; id != "" {
		h.boundaryEntryID = ""
		if entryIndex(z.persisted.Queue.Entries, id) >= 0 && !failed(z, id) {
			s.openNext(userID, z, id, 0)
		}
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// 每条流一个消费者；旧代次即使仍有缓冲事件也只排空，不再改变播放区。
// 接续预载的播放头事件暂存，只有输出端证实换上后才应用，最多保留 64 条。
func (s *Service) consumeRender(userID string, z *zoneRuntime, h *renderHandle) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		events := h.session.Events()
		pending := []Event{}
		for {
			var e Event
			var have bool
			select {
			case <-s.Done():
				return
			case <-h.done:
				if events == nil {
					return
				}
				for {
					select {
					case _, ok := <-events:
						if !ok {
							return
						}
					case <-s.Done():
						return
					}
				}
			case <-h.wake:
			case event, ok := <-events:
				if !ok {
					events = nil
					if len(pending) == 0 {
						return
					}
				} else {
					e = event
					have = true
				}
			}
			z.mu.Lock()
			active := !s.closed.Load() && !z.deleted && z.generation == h.generation && (z.currentHandle == h || z.nextHandle == h)
			current := z.currentHandle == h
			empty := h.stream.Format == nil
			z.mu.Unlock()
			if !active {
				pending = nil
				if events == nil {
					return
				}
				continue
			}
			// 空流没有能到达的播放头；失败与后续边界不能等一个永远不会出现的 playing。
			if !current && !empty {
				if have && (e.Kind == "signal" || e.Kind == "format_boundary" || e.Kind == "rebuild_needed") {
					s.deliverRenderEvent(userID, z, h, e)
				} else if have {
					if len(pending) == 64 {
						// 不丢播放头事件，也不无限攒内存；异常提前派发受原通道背压限制。
						select {
						case <-h.wake:
						case <-h.done:
							return
						case <-s.Done():
							return
						}
						for _, event := range pending {
							s.deliverRenderEvent(userID, z, h, event)
						}
						pending = nil
						s.deliverRenderEvent(userID, z, h, e)
						continue
					}
					pending = append(pending, e)
				}
			} else {
				for _, event := range pending {
					s.deliverRenderEvent(userID, z, h, event)
				}
				pending = nil
				if have {
					s.deliverRenderEvent(userID, z, h, e)
				}
			}
			if events == nil && len(pending) == 0 {
				return
			}
		}
	}()
}

// 单个事件失败时等待后重试，保持该会话内的事件顺序；等待期间不持区锁。
func (s *Service) deliverRenderEvent(userID string, z *zoneRuntime, h *renderHandle, e Event) {
	for {
		z.dispatch.Lock()
		z.mu.Lock()
		valid := !s.closed.Load() && !z.deleted && z.generation == h.generation && (z.currentHandle == h || z.nextHandle == h)
		applied := true
		var result *Zone
		if valid {
			applied, result = s.applyRenderEvent(userID, z, h, e)
		}
		z.mu.Unlock()
		if result != nil {
			s.publishZone(userID, *result)
		}
		z.dispatch.Unlock()
		if applied {
			return
		}
		ready := make(chan struct{})
		timer := s.AfterFunc(time.Second, func() { close(ready) })
		select {
		case <-ready:
		case <-h.done:
			timer.Stop()
			return
		case <-s.Done():
			timer.Stop()
			return
		}
	}
}

func validFailure(reason string) bool {
	switch reason {
	case "not_found", "root_disabled", "unsupported_format", "source_unavailable", "source_changed", "decode_failed":
		return true
	}
	return false
}
func (s *Service) applyRenderEvent(userID string, z *zoneRuntime, h *renderHandle, e Event) (bool, *Zone) {
	if z.rebuild != nil && !(e.Kind == "signal" && e.Segments == nil) {
		return true, nil
	}
	if h.rebuild != nil && e.Kind != "rebuild_needed" && !(e.Kind == "signal" && e.Segments == nil) {
		return true, nil
	}
	if e.Kind == "time" {
		return true, s.applyTime(userID, z, h, e)
	}
	p := z.persisted
	p.Revision++
	tracks := z.cursorTracks
	var nextQueue queue.State
	var signal json.RawMessage
	nextEmptyEnded := e.Kind == "stream_ended" && z.nextHandle == h && h.stream.Format == nil
	switch e.Kind {
	case "track_started":
		if !validSeconds(e.Seconds) || !validSeconds(e.StreamSeconds) {
			return true, nil
		}
		step := queue.AdvanceTo(p.Queue, e.EntryID, func(id string) bool { return failed(z, id) })
		if step.EntryID == "" {
			return true, nil
		}
		nextQueue = step.State
		p.Queue = nextQueue
		p.PositionSeconds = max(0, e.Seconds)
		p.PositionSavedAt = s.Now()
	case "track_failed":
		if entryIndex(p.Queue.Entries, e.EntryID) < 0 || !validFailure(e.Reason) {
			return true, nil
		}
	case "format_boundary":
		if entryIndex(p.Queue.Entries, e.NextEntryID) < 0 || failed(z, e.NextEntryID) || (e.Reason != "format" && e.Reason != "not_ready") {
			return true, nil
		}
	case "stream_ended":
		if (z.nextHandle != nil && !nextEmptyEnded) || z.resumeRetry != nil {
			return true, nil
		}
		// 预载失败不能把仍在播放的有效当前流归零。
		if !nextEmptyEnded || z.stream == nil || z.stream.Format == nil {
			p.PositionSeconds = 0
			p.PositionSavedAt = s.Now()
		}
	case "rebuild_needed":
	case "signal":
		var err error
		signal, err = json.Marshal(e.Signal)
		if err != nil {
			s.reportAsync("render_signal", userID, p.ZoneID, err)
			return true, nil
		}
	default:
		return true, nil
	}
	// 异步事件也必须先提交；失败保留原状态，并由消费者重试该条事件。
	if err := s.transact(s.ctx, func(st *Store) error { return st.UpdateZone(s.ctx, userID, p) }); err != nil {
		s.reportAsync("render_"+e.Kind, userID, p.ZoneID, err)
		return false, nil
	}
	z.persisted = p
	z.positionWrittenAt = s.Now()
	switch e.Kind {
	case "track_started":
		s.beginVisit(z, e.EntryID, true)
		z.event = &ZoneEvent{Seq: p.Revision, Kind: "track_started", EntryID: e.EntryID}
		if e.Segments == nil {
			entry := renderEntry(p.Queue, e.EntryID, tracks)
			segments := []Segment{{EntryID: e.EntryID, TrackID: entry.TrackID, StreamStartSeconds: e.StreamSeconds, TrackStartSeconds: max(0, e.Seconds)}}
			for _, segment := range h.stream.Segments {
				if segment.StreamStartSeconds > e.StreamSeconds {
					segments = append(segments, Segment{EntryID: segment.EntryID, TrackID: segment.Track.TrackID, StreamStartSeconds: segment.StreamStartSeconds, TrackStartSeconds: segment.TrackStartSeconds})
				}
			}
			s.replaceSegments(z, h, segments)
		}
	case "track_failed":
		if !failed(z, e.EntryID) {
			z.failures = append(z.failures, Failure{e.EntryID, e.Reason})
		}
		if e.EntryID == p.Queue.CurrentEntryID {
			suspendVisit(z)
		}
		z.consecutiveFailures++
		z.event = &ZoneEvent{Seq: p.Revision, Kind: "track_failed", EntryID: e.EntryID, Reason: e.Reason}
		if z.consecutiveFailures >= 5 {
			s.cancelTimers(z)
			s.closeSession(z)
			s.pauseState(z, s.Now())
			z.event = &ZoneEvent{Seq: p.Revision, Kind: "failures_stopped"}
			z.visit = nil
		}
	case "format_boundary":
		if z.nextHandle == h && h.stream.Format != nil {
			// B 尚在预载时不能用 C 覆盖 B；立即更新 B 的最新后继，晋升 B 时兑现。
			h.boundaryEntryID = e.NextEntryID
		} else {
			s.openNext(userID, z, e.NextEntryID, 0)
		}
	case "stream_ended":
		if nextEmptyEnded && z.stream != nil && z.stream.Format != nil {
			h.close()
			z.nextHandle, z.nextSession, z.nextStream = nil, nil, nil
			break
		}
		s.cancelTimers(z)
		s.closeSession(z)
		s.pauseState(z, s.Now())
		z.visit = nil
		all := len(p.Queue.Entries) > 0
		for _, entry := range p.Queue.Entries {
			if !failed(z, entry.EntryID) {
				all = false
				break
			}
		}
		if all {
			z.event = &ZoneEvent{Seq: p.Revision, Kind: "all_failed"}
		}
	case "rebuild_needed":
		if z.nextHandle == h {
			h.boundaryEntryID = ""
			s.scheduleNextRebuild(userID, z, h, 300*time.Millisecond)
		} else {
			s.scheduleRebuild(userID, z)
		}
	case "signal":
		h.signal = signal
		if z.currentHandle == h {
			z.signal = signal
		}
	}
	if e.Segments != nil && (z.currentHandle == h || z.nextHandle == h) {
		s.replaceSegments(z, h, e.Segments)
	}
	result := s.zoneOf(z, tracks)
	return true, &result
}

func (s *Service) replaceSegments(z *zoneRuntime, h *renderHandle, segments []Segment) {
	projected := make([]StreamSegment, 0, len(segments))
	for _, segment := range segments {
		index := entryIndex(z.persisted.Queue.Entries, segment.EntryID)
		if index < 0 || !validSeconds(segment.StreamStartSeconds) || !validSeconds(segment.TrackStartSeconds) {
			continue
		}
		trackID := z.persisted.Queue.Entries[index].TrackID
		if segment.TrackID != "" && segment.TrackID != trackID {
			continue
		}
		track, ok := z.cursorTracks[trackID]
		if !ok {
			continue
		}
		projected = append(projected, StreamSegment{segment.EntryID, track, segment.StreamStartSeconds, segment.TrackStartSeconds})
	}
	sort.SliceStable(projected, func(i, j int) bool { return projected[i].StreamStartSeconds < projected[j].StreamStartSeconds })
	stream := *h.stream
	stream.Segments = projected
	h.stream = &stream
	if z.currentHandle == h {
		z.stream = &stream
	} else if z.nextHandle == h {
		z.nextStream = &stream
	}
}

func (s *Service) applyTime(userID string, z *zoneRuntime, h *renderHandle, e Event) *Zone {
	if z.currentHandle != h || e.EntryID != z.persisted.Queue.CurrentEntryID || !validSeconds(e.Seconds) || z.intent != "play" || z.status != "playing" || !z.online {
		return nil
	}
	now := s.Now()
	at := e.At
	if at.IsZero() || at.After(now) {
		at = now
	}
	if at.Before(z.persisted.PositionSavedAt) {
		return nil
	}
	p := z.persisted
	p.PositionSeconds = clampPosition(e.EntryID, e.Seconds, p.Queue.Entries, z.cursorTracks)
	p.PositionSavedAt = at
	p.Revision++
	if now.Sub(z.positionWrittenAt) >= PositionSaveInterval {
		if err := s.transact(s.ctx, func(st *Store) error { return st.UpdateZone(s.ctx, userID, p) }); err != nil {
			s.reportAsync("position", userID, p.ZoneID, err)
			return nil
		}
		z.positionWrittenAt = now
	}
	z.persisted = p
	z.consecutiveFailures = 0
	if z.resumeAwaitingSeq == 0 || z.resumeAwaitingSeq == h.stream.Seq {
		z.resumeFailures = 0
		z.resumeAwaitingSeq = 0
	}
	if z.visit == nil {
		s.beginVisit(z, e.EntryID, false)
	}
	if !z.visit.sample(p.PositionSeconds, at) || s.options.PlayRecorder == nil {
		return nil
	}
	if err := s.options.PlayRecorder.Record(s.ctx, userID, z.visit.id, z.visit.trackID, *z.visit.pendingSeconds); err != nil {
		s.reportAsync("play_record", userID, p.ZoneID, err)
		return nil
	}
	z.visit.recorded = true
	z.event = &ZoneEvent{Seq: p.Revision, Kind: "play_recorded", TrackID: z.visit.trackID}
	result := s.zoneOf(z, z.cursorTracks)
	return &result
}

// 只统计续流本身的失败：首次原流断开不是一次失败的续流尝试。
func (s *Service) resumeFailed(z *zoneRuntime) bool {
	z.resumeFailures++
	if z.resumeFailures < 3 {
		return false
	}
	s.cancelTimers(z)
	s.closeSession(z)
	s.renderFault(z, "transient")
	return true
}

func (s *Service) resumeNext(userID string, z *zoneRuntime, entryID string, seconds float64) {
	kind := s.replaceNext(userID, z, entryID, seconds)
	if kind == "" {
		z.resumeAwaitingSeq = z.nextStream.Seq
		return
	}
	if kind == "transient" {
		if s.resumeFailed(z) {
			return
		}
		if z.intent == "play" {
			s.scheduleResume(userID, z, entryID, seconds)
			return
		}
	}
	s.renderFault(z, kind)
	s.schedulePause(userID, z)
}

func (s *Service) resumeStream(userID string, z *zoneRuntime, tracks map[string]catalog.TrackSummary, h *renderHandle) {
	if h != nil && z.resumeAwaitingSeq == h.stream.Seq && s.resumeFailed(z) {
		return
	}
	z.resumeAwaitingSeq = -1
	if h != nil && h == z.nextHandle {
		s.resumeNext(userID, z, h.entryID, h.startSeconds)
		return
	}
	s.cancelTimers(z)
	intent := z.intent
	s.openSession(userID, z, tracks, false)
	if z.stream != nil {
		z.resumeAwaitingSeq = z.stream.Seq
		if intent == "pause" {
			s.schedulePause(userID, z)
		}
	}
	if z.session == nil && z.fault != nil && z.fault.Kind == "transient" {
		if s.resumeFailed(z) {
			return
		}
		if intent == "play" {
			z.intent = "play"
			z.status = "buffering"
			z.pausedAt = nil
			z.fault = nil
			s.scheduleResume(userID, z, "", 0)
		}
	}
}
func (s *Service) scheduleResume(userID string, z *zoneRuntime, nextEntryID string, seconds float64) {
	s.cancelResume(z)
	version, generation := z.resumeVersion, z.generation
	z.resumeRetry = s.AfterFunc(time.Second, func() {
		z.dispatch.Lock()
		defer z.dispatch.Unlock()
		z.mu.Lock()
		var result *Zone
		defer func() {
			z.mu.Unlock()
			if result != nil {
				s.publishZone(userID, *result)
			}
		}()
		if s.closed.Load() || z.deleted || z.resumeVersion != version || z.generation != generation {
			return
		}
		z.resumeRetry = nil
		if z.intent != "play" || !z.online {
			return
		}
		if nextEntryID != "" && entryIndex(z.persisted.Queue.Entries, nextEntryID) < 0 {
			return
		}
		p := z.persisted
		p.Revision++
		if err := s.transact(s.ctx, func(st *Store) error { return st.UpdateZone(s.ctx, userID, p) }); err != nil {
			s.reportAsync("resume", userID, p.ZoneID, err)
			s.scheduleResume(userID, z, nextEntryID, seconds)
			return
		}
		z.persisted = p
		z.positionWrittenAt = s.Now()
		z.resumeRetry = nil
		if nextEntryID == "" {
			s.resumeStream(userID, z, z.cursorTracks, nil)
		} else {
			s.resumeNext(userID, z, nextEntryID, seconds)
		}
		zone := s.zoneOf(z, z.cursorTracks)
		result = &zone
	})
}
