package render

import (
	"errors"
	"io"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// player 只有渲染协程访问。G-2 可在 read 的来源读取前截断淡化窗口，
// 在 appendPCM 前混合两个已电平化的来源块；共享链不参与换轨状态。
type player struct {
	session       *session
	current, next *decoder
	anchor        Track
	anchorKnown   bool
	last          zones.Entry
	selected      bool
	boundary      bool
	boundaryEntry zones.Entry
	firstErr      error
	failures      []zones.Event
	consecutive   int
	cursorFrames  int64
	started       bool
	buffer        chain.Buffer
}

func (p *player) close() {
	if p.current != nil {
		p.current.close()
		p.current = nil
	}
	if p.next != nil {
		p.next.close()
		p.next = nil
	}
}

func (p *player) recordFailure(entry zones.Entry, err error) error {
	if failureReason(err) == "transient" {
		return err
	}
	p.failures = append(p.failures, zones.Event{Kind: "track_failed", EntryID: entry.EntryID, Reason: failureReason(err)})
	p.consecutive++
	return nil
}
func (p *player) flushFailures(frame int64) error {
	for _, event := range p.failures {
		if err := p.session.queueEvent(frame, event); err != nil {
			return err
		}
	}
	p.failures = nil
	return nil
}

func (p *player) selectNext(after zones.Entry) error {
	p.selected = true
	if p.session.spec.Cursor == nil || p.consecutive >= 5 {
		return nil
	}
	for p.consecutive < 5 {
		// 不持有会话、事件、环缓冲或 Renderer 的锁调用控制面。
		entry, ok := p.session.spec.Cursor.Next(after.EntryID)
		if err := p.session.ctx.Err(); err != nil {
			return err
		}
		if !ok {
			return nil
		}
		track, err := p.session.owner.options.Sources.Prepare(p.session.ctx, p.session.spec.UserID, entry.TrackID)
		if err == nil {
			err = validateTrack(track)
		}
		if err != nil {
			if track.File != nil {
				_ = track.File.Close()
			}
			if err = p.recordFailure(entry, err); err != nil {
				return err
			}
			after = entry
			continue
		}
		if !p.anchorKnown || !sameSourceFormat(p.anchor, track) {
			_ = track.File.Close()
			p.boundary = true
			p.boundaryEntry = entry
			return p.session.immediate(zones.Event{Kind: "format_boundary", NextEntryID: entry.EntryID, Reason: "format"})
		}
		reservation, err := p.session.owner.options.FFmpeg.Reserve(p.session.ctx, 1)
		if err != nil {
			_ = track.File.Close()
			if p.session.ctx.Err() != nil {
				return p.session.ctx.Err()
			}
			p.boundary = true
			p.boundaryEntry = entry
			return p.session.immediate(zones.Event{Kind: "format_boundary", NextEntryID: entry.EntryID, Reason: "not_ready"})
		}
		next, err := openDecoder(p.session.ctx, reservation, p.session.owner.options.FFmpeg, track, entry, p.session.pcm, 0, p.session.spec.Settings.Leveling)
		_ = reservation.Close()
		if err != nil {
			_ = track.File.Close()
			if err = p.recordFailure(entry, err); err != nil {
				return err
			}
			after = entry
			continue
		}
		p.next = next
		return nil
	}
	return nil
}

func (p *player) startCurrent(frame int64) error {
	d := p.current
	if d == nil || p.started {
		return nil
	}
	segment := zones.Segment{EntryID: d.entry.EntryID, TrackID: d.entry.TrackID, StreamStartSeconds: float64(frame) / float64(p.session.pcm.SampleRate), TrackStartSeconds: d.startSeconds}
	if err := p.session.addSegment(frame, segment); err != nil {
		return err
	}
	if frame != 0 || d.entry.EntryID != p.session.spec.Start.EntryID {
		if err := p.session.queueEvent(frame, zones.Event{Kind: "track_started", EntryID: d.entry.EntryID, Transition: "gapless"}); err != nil {
			return err
		}
	}
	p.started = true
	return nil
}

func (p *player) read(frames int) (*chain.Buffer, time.Duration, error) {
	result := &p.buffer
	result.Frames, result.Exact, result.SourceBits = 0, true, 0
	if len(result.Samples) != p.session.pcm.Channels {
		result.Samples = make([][]float64, p.session.pcm.Channels)
	}
	for channel := range result.Samples {
		result.Samples[channel] = result.Samples[channel][:0]
	}
	transitions := 0
	var processing time.Duration
	for result.Frames < frames {
		if err := p.session.ctx.Err(); err != nil {
			return nil, processing, err
		}
		frame := p.cursorFrames + int64(result.Frames)
		if p.firstErr != nil {
			if err := p.recordFailure(p.last, p.firstErr); err != nil {
				return nil, processing, err
			}
			p.firstErr = nil
		}
		if p.current == nil {
			if !p.selected {
				if err := p.selectNext(p.last); err != nil {
					return nil, processing, err
				}
			}
			if err := p.flushFailures(frame); err != nil {
				return nil, processing, err
			}
			if p.next == nil {
				break
			}
			if transitions >= 32 {
				break
			}
			transitions++
			p.current, p.next = p.next, nil
			p.last = p.current.entry
			p.selected = false
			p.started = false
		}
		d := p.current
		remaining := d.track.DurationSeconds - d.startSeconds - float64(d.consumed)/float64(p.session.pcm.SampleRate)
		if !p.selected && d.track.DurationSeconds > 0 && remaining <= 10 {
			if err := p.selectNext(d.entry); err != nil {
				return nil, processing, err
			}
		}
		b, cost, readErr := d.read(p.session.ctx, frames-result.Frames, !p.started, p.session.owner.options.Clock)
		processing += cost
		if b != nil && b.Frames > 0 {
			if err := p.startCurrent(frame); err != nil {
				return nil, processing, err
			}
			begin := p.session.owner.options.Clock()
			appendPCM(result, b)
			processing += p.session.owner.options.Clock().Sub(begin)
			// 只有真正读到音频才解除连续失败；准备成功但空解码不能解除。
			p.consecutive = 0
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				if err := p.recordFailure(d.entry, readErr); err != nil {
					return nil, processing, err
				}
			}
			d.close()
			p.current = nil
			p.started = false
			if p.consecutive >= 5 {
				if p.next != nil {
					p.next.close()
					p.next = nil
				}
				p.selected = true
			}
		}
		if b == nil || b.Frames == 0 {
			if readErr == nil {
				return nil, processing, &SourceError{Reason: "decode_failed"}
			}
		}
	}
	p.cursorFrames += int64(result.Frames)
	return result, processing, nil
}

func appendPCM(dst, src *chain.Buffer) {
	for channel := range dst.Samples {
		dst.Samples[channel] = append(dst.Samples[channel], src.Samples[channel][:src.Frames]...)
	}
	dst.Exact = dst.Exact && src.Exact
	dst.SourceBits = max(dst.SourceBits, src.SourceBits)
	dst.Frames += src.Frames
}

func (p *player) nextChanged(finalized bool) error {
	if p.session.hasUnheardTransition() {
		return p.session.immediate(zones.Event{Kind: "rebuild_needed"})
	}
	announced := p.boundary
	if p.next != nil {
		p.next.close()
		p.next = nil
	}
	p.selected = false
	p.failures = nil
	if !finalized && !announced {
		return nil
	}
	// 已宣布的边界不能撤销：控制面已经准备接续流，新的同格式条目也不能再拼回本流。
	// 编码器冲刷后同样不能追加字节；这两种情况只更新接续流或要求重建。
	p.selected = true
	p.boundary = true
	p.boundaryEntry = zones.Entry{}
	p.session.removePendingEnd()
	if p.session.spec.Cursor == nil {
		return p.session.immediate(zones.Event{Kind: "rebuild_needed"})
	}
	entry, ok := p.session.spec.Cursor.Next(p.last.EntryID)
	if err := p.session.ctx.Err(); err != nil {
		return err
	}
	if !ok {
		return p.session.immediate(zones.Event{Kind: "rebuild_needed"})
	}
	p.boundaryEntry = entry
	return p.session.immediate(zones.Event{Kind: "format_boundary", NextEntryID: entry.EntryID, Reason: "format"})
}

// 预留由 G-2 复用的解码器启动边界，所有名额仍由共享 Tool 管理。
var _ processStarter = (*ffmpeg.Reservation)(nil)
