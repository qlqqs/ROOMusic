package zones

import (
	"context"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (s *Service) ClaimOutput(ctx context.Context, userID, deviceID string, input ClaimOutputInput) (result ClaimOutputResult, err error) {
	if !validID(deviceID) {
		return ClaimOutputResult{}, ErrInvalidInput
	}
	if input.Protocol != 1 {
		return ClaimOutputResult{}, ErrUnsupportedProtocol
	}
	if !validCapabilities(input.Capabilities) || (input.Playing != nil && (!validID(input.Playing.EntryID) || !validSeconds(input.Playing.PositionSeconds))) {
		return ClaimOutputResult{}, ErrInvalidInput
	}
	userID, deviceID = strings.ToLower(userID), strings.ToLower(deviceID)
	u, err := s.user(ctx, userID)
	if err != nil {
		return ClaimOutputResult{}, err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := u.devices[deviceID]
	if z == nil {
		return ClaimOutputResult{}, ErrNotFound
	}
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	defer func() {
		z.mu.Unlock()
		if err == nil {
			s.publishDevice(userID, result.Device)
			s.publishZone(userID, result.Zone)
		}
	}()
	tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
	if err != nil {
		return ClaimOutputResult{}, err
	}
	now := s.Now()
	p, d := z.persisted, z.device
	p.PositionSeconds = positionAt(z, now, tracks)
	p.PositionSavedAt = now
	p.Revision++
	resume := z.intent == "play"
	if z.offlinePaused {
		if input.Takeover && !z.releasedAt.IsZero() && now.Sub(z.releasedAt) >= 0 && now.Sub(z.releasedAt) <= TakeoverWindow {
			resume = true
		}
		if input.Playing != nil {
			id := strings.ToLower(input.Playing.EntryID)
			if entryIndex(p.Queue.Entries, id) >= 0 {
				p.Queue = queue.JumpTo(p.Queue, id)
				p.PositionSeconds = clampPosition(id, input.Playing.PositionSeconds, p.Queue.Entries, tracks)
				resume = true
			}
		}
	}
	d.Capabilities = cloneCapabilities(input.Capabilities)
	d.LastSeenAt = now
	if err = s.transact(ctx, func(st *Store) error {
		if e := st.UpdateDevice(ctx, userID, d); e != nil {
			return e
		}
		return st.UpdateZone(ctx, userID, p)
	}); err != nil {
		return ClaimOutputResult{}, err
	}
	if z.output != nil {
		z.output.state = "superseded"
	}
	out := &outputSession{id: s.newID(), userID: userID, zone: z, state: "active", lastReport: now}
	s.outputMu.Lock()
	s.outputs[out.id] = out
	s.outputMu.Unlock()
	s.cancelTimers(z)
	s.closeSession(z)
	z.persisted, z.device, z.output = p, d, out
	z.outputSessionID = &out.id
	z.online = true
	z.releasedAt = time.Time{}
	z.positionWrittenAt = now
	z.cursorTracks = tracks
	if resume && p.Queue.CurrentEntryID != "" {
		z.intent = "play"
		z.pausedAt = nil
		z.offlinePaused = false
		s.openSession(userID, z, tracks, false)
	} else {
		pausedAt := z.pausedAt
		s.pauseState(z, now)
		if pausedAt != nil {
			z.pausedAt = pausedAt
		}
	}
	result = ClaimOutputResult{timestamp(now), 1, out.id, deviceOf(z), s.zoneOf(z, tracks)}
	return result, nil
}

func validSeconds(n float64) bool { return finite(n) && n >= 0 && n <= 10000000 }
func validStreamSample(seq *int64, seconds float64) bool {
	return (seq == nil || *seq >= 0) && validSeconds(seconds)
}
func validReport(in ReportOutputInput) bool {
	if !validStreamSample(in.StreamSeq, in.StreamSeconds) {
		return false
	}
	switch in.Status {
	case "loading", "playing", "paused", "buffering":
	default:
		return false
	}
	switch in.Kind {
	case "status", "ended":
		return in.Fault == nil
	case "fault":
		return in.Fault != nil && (*in.Fault == "autoplay_blocked" || *in.Fault == "stream_lost")
	}
	return false
}
func (s *Service) findOutput(userID, sessionID string) (*outputSession, error) {
	if !validID(userID) || !validID(sessionID) {
		return nil, ErrInvalidInput
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	s.outputMu.Lock()
	out := s.outputs[strings.ToLower(sessionID)]
	s.outputMu.Unlock()
	if out == nil {
		return nil, ErrOutputUnknown
	}
	if out.userID != strings.ToLower(userID) {
		return nil, ErrNotFound
	}
	return out, nil
}
func outputError(out *outputSession) error {
	if out.state == "superseded" {
		return ErrOutputSuperseded
	}
	if out.state != "active" || out.zone.deleted || out.zone.output != out {
		return ErrOutputUnknown
	}
	return nil
}
func (s *Service) outputResult(z *zoneRuntime, tracks map[string]catalog.TrackSummary) OutputResult {
	return OutputResult{timestamp(s.Now()), deviceOf(z), s.zoneOf(z, tracks)}
}

func (s *Service) ReportOutput(ctx context.Context, userID, sessionID string, in ReportOutputInput) (result OutputResult, err error) {
	if !validReport(in) {
		return OutputResult{}, ErrInvalidInput
	}
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return OutputResult{}, err
	}
	userID = out.userID
	z := out.zone
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	publishDevice := false
	defer func() {
		z.mu.Unlock()
		if err == nil {
			if publishDevice {
				s.publishDevice(userID, result.Device)
			}
			s.publishZone(userID, result.Zone)
		}
	}()
	if err = outputError(out); err != nil {
		return OutputResult{}, err
	}
	tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
	if err != nil {
		return OutputResult{}, err
	}
	now := s.Now()
	p, d := z.persisted, z.device
	handle := z.handleFor(in.StreamSeq)
	restore := handle == nil && z.offlinePaused && in.Kind == "status" && in.Status == "playing" && z.lostStream != nil && in.StreamSeq != nil && *in.StreamSeq == z.lostStream.Seq
	matched := handle != nil || restore || (in.StreamSeq == nil && z.stream == nil && in.Kind == "status")
	promote := z.rebuild == nil && handle != nil && handle.rebuild == nil && handle == z.nextHandle && in.Kind == "status" && in.Status == "playing"
	// 接续流的预载回报不改变当前曲目、状态或播放头。
	apply := matched && (handle == nil || handle == z.currentHandle || promote || in.Kind == "fault")
	// 去抖重建已经接受新的目标，旧流结束或备用流失败不能撤销该目标。
	if (z.rebuild != nil && (in.Kind == "ended" || handle == z.nextHandle)) || (handle != nil && handle.rebuild != nil) {
		apply = false
	}
	// 首曲打不开的空 Body 可以先于失败/边界事件 EOF；收尾以渲染事件为准。
	// 媒体端把无音频 EOF 分类为 stream_lost 时也不能重新打开同一失败条目。
	if handle != nil && handle.stream.Format == nil && (in.Kind == "ended" || (in.Kind == "fault" && *in.Fault == "stream_lost")) {
		apply = false
	}
	if in.Kind == "fault" && *in.Fault == "autoplay_blocked" && z.fault != nil && z.fault.Kind == "autoplay_blocked" {
		apply = false
	}
	if promote {
		promotion := p
		// 新流 playing 证明旧流已经播完；先消费旧流尚未来得及应用的分段。
		if z.stream != nil && len(z.stream.Segments) > 0 {
			last := z.stream.Segments[len(z.stream.Segments)-1]
			mapPosition(&promotion, z.stream, last.StreamStartSeconds, tracks, func(id string) bool { return failed(z, id) })
		}
		step := queue.AdvanceTo(promotion.Queue, handle.entryID, func(id string) bool { return failed(z, id) })
		if step.EntryID == "" {
			apply = false
			promote = false
		} else {
			p.Queue = step.State
			p.PositionSeconds = handle.startSeconds
			p.PositionSavedAt = now
		}
	}
	newVisit := false
	if apply && (restore || in.Kind == "fault" || in.Kind == "ended" || in.Kind == "status") {
		stream := z.stream
		if handle != nil {
			stream = handle.stream
		}
		if restore {
			stream = z.lostStream
		}
		// status 的位置权威仍是渲染事件；故障/释放才用分段补齐最后播放头。
		if z.rebuild == nil && (restore || in.Kind == "fault" || in.Kind == "ended") && (handle == nil || handle == z.currentHandle) {
			newVisit = mapPosition(&p, stream, in.StreamSeconds, tracks, func(id string) bool { return failed(z, id) })
			p.PositionSavedAt = now
		}
	}
	if apply && in.Kind == "status" && !promote && !restore && handle != nil && z.rebuild == nil && in.Status != z.status {
		// 状态切换重新锚定时钟，不能把暂停/缓冲经过的墙钟时间算进恢复后的进度。
		p.PositionSeconds = positionAt(z, now, tracks)
		p.PositionSavedAt = now
	}
	if apply && in.Kind == "ended" && z.nextHandle == nil && z.resumeRetry == nil {
		p.PositionSeconds = 0
		p.PositionSavedAt = now
	}
	if apply || !z.online {
		p.Revision++
	}
	seenDue := now.Sub(d.LastSeenAt) >= DeviceSeenSaveInterval
	if seenDue {
		d.LastSeenAt = now
	}
	changed := apply && (promote || restore || in.Kind != "status" || in.Status != z.status)
	writeZone := changed || now.Sub(z.positionWrittenAt) >= PositionSaveInterval || !z.online
	if writeZone || seenDue {
		if err = s.transact(ctx, func(st *Store) error {
			if seenDue {
				if e := st.UpdateDevice(ctx, userID, d); e != nil {
					return e
				}
			}
			if writeZone {
				return st.UpdateZone(ctx, userID, p)
			}
			return nil
		}); err != nil {
			return OutputResult{}, err
		}
	}
	wasOnline := z.online
	previousEntry := z.persisted.Queue.CurrentEntryID
	z.persisted, z.device = p, d
	z.online = true
	out.lastReport = now
	z.cursorTracks = tracks
	if (newVisit || p.Queue.CurrentEntryID != previousEntry) && !promote {
		s.beginVisit(z, p.Queue.CurrentEntryID, true)
		z.event = &ZoneEvent{Seq: p.Revision, Kind: "track_started", EntryID: p.Queue.CurrentEntryID}
	}
	if writeZone {
		z.positionWrittenAt = now
	}
	if apply {
		if promote {
			s.promoteNext(userID, z)
		}
		switch in.Kind {
		case "status":
			if restore {
				z.intent = "play"
				z.pausedAt = nil
				z.offlinePaused = false
				s.cancelTimers(z)
				s.openSession(userID, z, tracks, false)
			} else if handle != nil {
				wasPlaying := z.status == "playing"
				if z.offlinePaused && in.Status == "playing" {
					z.intent = "play"
					z.pausedAt = nil
					z.offlinePaused = false
				}
				// 已接受的暂停命令不能被在途 playing 回报撤销。
				if z.rebuild != nil {
					z.status = "loading"
				} else if z.intent == "pause" && in.Status == "playing" {
					z.status = "paused"
				} else {
					z.status = in.Status
				}
				if z.status != "playing" {
					suspendVisit(z)
				} else if !wasPlaying && z.visit != nil {
					sample := z.persisted
					mapPosition(&sample, handle.stream, in.StreamSeconds, tracks, func(id string) bool { return failed(z, id) })
					if sample.Queue.CurrentEntryID == z.visit.entryID {
						z.visit.position = sample.PositionSeconds
						z.visit.at = now
						z.visit.baseline = true
					}
				}
				handle.session.Observe(PlayheadSample{handle.stream.Seq, in.StreamSeconds, now, z.status == "playing" && z.intent == "play"})
				if z.status == "playing" && z.intent == "play" && (z.resumeAwaitingSeq == 0 || z.resumeAwaitingSeq == handle.stream.Seq) {
					z.resumeFailures = 0
					z.resumeAwaitingSeq = 0
				}
			}
		case "ended":
			if z.nextHandle == nil && z.resumeRetry == nil {
				s.cancelTimers(z)
				s.closeSession(z)
				z.persisted.PositionSeconds = 0
				s.pauseState(z, now)
				z.visit = nil
			}
		case "fault":
			if *in.Fault == "autoplay_blocked" {
				s.renderFault(z, "autoplay_blocked")
				z.event = &ZoneEvent{Seq: p.Revision, Kind: "autoplay_blocked"}
				suspendVisit(z)
				s.schedulePause(userID, z)
			} else {
				s.resumeStream(userID, z, tracks, handle)
			}
		}
	}
	result = s.outputResult(z, tracks)
	publishDevice = !wasOnline || seenDue
	return result, nil
}

func (s *Service) ReleaseOutput(ctx context.Context, userID, sessionID string, in ReleaseOutputInput) error {
	if !validStreamSample(in.StreamSeq, in.StreamSeconds) {
		return ErrInvalidInput
	}
	out, err := s.findOutput(userID, sessionID)
	if err == ErrOutputUnknown {
		return nil
	}
	if err != nil {
		return err
	}
	z := out.zone
	z.dispatch.Lock()
	defer z.dispatch.Unlock()
	z.mu.Lock()
	var result *OutputResult
	defer func() {
		z.mu.Unlock()
		if result != nil {
			s.publishDevice(out.userID, result.Device)
			s.publishZone(out.userID, result.Zone)
		}
	}()
	if outputError(out) != nil {
		return nil
	}
	tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
	if err != nil {
		return err
	}
	p, d := z.persisted, z.device
	now := s.Now()
	p.Revision++
	p.PositionSavedAt = now
	d.LastSeenAt = now
	newVisit := false
	if h := z.handleFor(in.StreamSeq); h != nil && z.rebuild == nil {
		newVisit = mapPosition(&p, h.stream, in.StreamSeconds, tracks, func(id string) bool { return failed(z, id) })
	}
	if err = s.transact(ctx, func(st *Store) error {
		if e := st.UpdateDevice(ctx, out.userID, d); e != nil {
			return e
		}
		return st.UpdateZone(ctx, out.userID, p)
	}); err != nil {
		return err
	}
	z.persisted, z.device = p, d
	z.cursorTracks = tracks
	if newVisit {
		s.beginVisit(z, p.Queue.CurrentEntryID, true)
		z.event = &ZoneEvent{Seq: p.Revision, Kind: "track_started", EntryID: p.Queue.CurrentEntryID}
	}
	z.positionWrittenAt = now
	z.releasedAt = now
	out.state = "released"
	z.output = nil
	z.outputSessionID = nil
	s.markOffline(z)
	snapshot := s.outputResult(z, tracks)
	result = &snapshot
	return nil
}

// 分段是已渲染的自然顺序；逐段消费，不能把 A→B→C 当成一次 A→C，
// 也不能通过重新随机或 JumpTo 补造未观察到的队列历史。
// 返回是否跨过曲目分段（同条目循环也算新访问）。无法完整映射时不修改 p。
func mapPosition(p *PersistedZone, stream *Stream, seconds float64, tracks map[string]catalog.TrackSummary, isSkipped func(string) bool) bool {
	if stream == nil {
		return false
	}
	selected := -1
	for i, segment := range stream.Segments {
		if segment.StreamStartSeconds <= seconds && (selected < 0 || segment.StreamStartSeconds >= stream.Segments[selected].StreamStartSeconds) {
			selected = i
		}
	}
	if selected < 0 || entryIndex(p.Queue.Entries, stream.Segments[selected].EntryID) < 0 {
		return false
	}
	// 已播完的分段会被裁掉，保留表中首次出现的当前项就是本次访问。
	start := 0
	for i := 0; i <= selected; i++ {
		if stream.Segments[i].EntryID == p.Queue.CurrentEntryID {
			start = i + 1
			break
		}
	}
	q := p.Queue
	advanced := false
	for i := start; i <= selected; i++ {
		id := stream.Segments[i].EntryID
		if entryIndex(q.Entries, id) < 0 || (isSkipped != nil && isSkipped(id)) {
			continue
		}
		step := queue.AdvanceTo(q, id, isSkipped)
		if step.EntryID == "" {
			return false
		}
		q = step.State
		advanced = true
	}
	segment := stream.Segments[selected]
	if q.CurrentEntryID != segment.EntryID {
		return false
	}
	p.Queue = q
	p.PositionSeconds = clampPosition(segment.EntryID, segment.TrackStartSeconds+seconds-segment.StreamStartSeconds, q.Entries, tracks)
	return advanced
}
func (s *Service) TakeStream(ctx context.Context, userID, zoneID string, seq int64) (StreamBody, error) {
	session, mimeType, err := s.accessStream(ctx, userID, zoneID, seq, true)
	if err != nil {
		return StreamBody{}, err
	}
	return StreamBody{session.Body(), mimeType}, nil
}

// GetHLSMedia 核对当前输出会话与流序号，但不消耗单次取流标记。
func (s *Service) GetHLSMedia(ctx context.Context, userID, zoneID string, seq int64) (HLSMedia, error) {
	session, _, err := s.accessStream(ctx, userID, zoneID, seq, false)
	if err != nil {
		return nil, err
	}
	return session.HLS(), nil
}

// 资格核对与长流的单次占用在同一把播放区锁内完成；等待媒体时不持锁。
func (s *Service) accessStream(ctx context.Context, userID, zoneID string, seq int64, take bool) (RenderSession, string, error) {
	if !validID(zoneID) || seq < 0 {
		return nil, "", ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := u.zones[strings.ToLower(zoneID)]
	if z == nil {
		return nil, "", ErrNotFound
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	h := z.handleFor(&seq)
	if z.output == nil || outputError(z.output) != nil || h == nil {
		return nil, "", ErrStreamGone
	}
	if take {
		if h.taken || h.session.HLS() != nil {
			return nil, "", ErrStreamGone
		}
		h.taken = true
	} else if h.session.HLS() == nil {
		return nil, "", ErrStreamGone
	}
	return h.session, h.stream.MimeType, nil
}

// 把当前曲目锚点反算到流内；同曲循环时取最近仍保留的一段。
func streamPosition(stream *Stream, entryID string, seconds float64) float64 {
	result := 0.0
	if stream == nil {
		return result
	}
	for _, segment := range stream.Segments {
		if segment.EntryID == entryID {
			result = max(0, segment.StreamStartSeconds+seconds-segment.TrackStartSeconds)
		}
	}
	return result
}
