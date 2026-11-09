package render

import (
	"errors"
	"io"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// player 只有渲染协程访问；换轨、静音与混音都在电平化之后、共享链之前。
type player struct {
	session           *session
	current, next     *decoder
	restartNext       *zones.Entry
	anchor            Track
	anchorKnown       bool
	last              zones.Entry
	selected          bool
	boundary          bool
	boundaryEntry     zones.Entry
	boundaryReason    string
	firstErr          error
	failures          []zones.Event
	consecutive       int
	cursorFrames      int64
	endFrame          int64
	started           bool
	currentTransition string
	plan              transitionPlan
	mix               *crossfadeWindow
	gapStarted        bool
	gapRemaining      int
	buffer            chain.Buffer
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
	p.closeMix()
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
		if err := p.session.queueSourceEvent(frame, event); err != nil {
			return err
		}
	}
	p.failures = nil
	return nil
}

func (p *player) promoteNext() {
	p.current, p.next = p.next, nil
	p.last = p.current.entry
	p.currentTransition = p.plan.kind
	p.plan = transitionPlan{}
	p.selected, p.started, p.gapStarted = false, false, false
	p.gapRemaining = 0
	// 已确认新来源有 PCM 才重置；不能让仍在播放的 A 清掉预取 B 的失败。
	p.consecutive = 0
}

func (p *player) startCurrent(frame int64) error {
	d := p.current
	if p.started {
		return nil
	}
	segment := zones.Segment{EntryID: d.entry.EntryID, TrackID: d.entry.TrackID, TrackStartSeconds: d.startSeconds}
	if err := p.session.addSourceSegment(frame, segment, p.session.segmentSignal(d), p.currentTransition != ""); err != nil {
		return err
	}
	if p.currentTransition != "" {
		if err := p.session.queueSourceEvent(frame, zones.Event{Kind: "track_started", EntryID: d.entry.EntryID, Transition: p.currentTransition}); err != nil {
			return err
		}
	}
	p.started = true
	return nil
}

// beginNext 在真实曲尾等待、跳过空解码，再填入跨量子的静音间隔。
func (p *player) beginNext(result *chain.Buffer, frames int) (bool, error) {
	ctx, cancel := p.nextDeadline()
	defer cancel()
	for {
		if p.restartNext != nil {
			entry := *p.restartNext
			p.restartNext = nil
			prepared, err := p.prepareNext(ctx, entry, false)
			if err != nil {
				return false, err
			}
			if !prepared {
				if err := p.selectNext(ctx, entry); err != nil {
					return false, err
				}
			}
		}
		if !p.selected {
			if err := p.selectNext(ctx, p.last); err != nil {
				return false, err
			}
		}
		if err := p.flushFailures(p.endFrame); err != nil {
			return false, err
		}
		if p.next == nil {
			return false, nil
		}
		ready, err := p.waitNext(ctx)
		if err != nil || !ready {
			return false, err
		}
		rejected, err := p.rejectEmptyNext(ctx)
		if err != nil {
			return false, err
		}
		if rejected {
			continue
		}
		if p.plan.kind == "gap" && !p.gapStarted {
			p.gapStarted = true
			p.gapRemaining = p.session.sourcePCM.SampleRate / 2
		}
		if p.gapRemaining > 0 {
			n := min(frames-result.Frames, p.gapRemaining)
			appendSilence(result, n)
			p.gapRemaining -= n
			if result.Frames == frames {
				return true, nil
			}
		}
		p.promoteNext()
		return true, nil
	}
}

func (p *player) read(frames int) (*chain.Buffer, time.Duration, error) {
	result := &p.buffer
	result.Frames, result.Exact, result.SourceBits = 0, true, 0
	result.Regions = result.Regions[:0]
	if len(result.Samples) != p.session.sourcePCM.Channels {
		result.Samples = make([][]float64, p.session.sourcePCM.Channels)
	}
	for channel := range result.Samples {
		result.Samples[channel] = result.Samples[channel][:0]
	}
	var processing time.Duration
	// 用迭代补读代替递归，极短曲目也不会耗尽调用栈或截短量子。
	for result.Frames < frames {
		if err := p.session.ctx.Err(); err != nil {
			return nil, processing, err
		}
		if p.firstErr != nil {
			if err := p.recordFailure(p.last, p.firstErr); err != nil {
				return nil, processing, err
			}
			p.firstErr = nil
		}
		if p.current == nil {
			more, err := p.beginNext(result, frames)
			if err != nil {
				return nil, processing, err
			}
			if !more || result.Frames == frames {
				break
			}
		}
		d := p.current
		lookahead := 10.0
		end := d.track.DurationSeconds
		if p.session.spec.Settings.Crossfade {
			lookahead += float64(p.session.spec.Settings.CrossfadeSeconds)
			end = effectiveEnd(d.track)
		}
		remaining := end - float64(d.position())/float64(p.session.sourcePCM.SampleRate)
		if !p.selected && p.mix == nil && d.track.DurationSeconds > 0 && remaining <= lookahead {
			if err := p.selectNext(p.session.ctx, d.entry); err != nil {
				return nil, processing, err
			}
		}
		frame := p.cursorFrames + int64(result.Frames)
		n := frames - result.Frames
		if p.plan.kind == "crossfade" {
			if d.position() >= p.plan.cut {
				if err := p.beginCrossfade(frame); err != nil {
					return nil, processing, err
				}
				continue
			}
			n = min(n, int(p.plan.cut-d.position()))
		}
		if p.mix != nil {
			n = min(n, p.mix.frames-p.mix.position)
		}
		b, cost, readErr := d.read(p.session.ctx, n, !p.started, p.session.owner.options.Clock)
		processing += cost
		if b != nil && b.Frames > 0 {
			if err := p.startCurrent(frame); err != nil {
				return nil, processing, err
			}
			if p.mix != nil {
				cost, err := p.mixPCM(b, frame)
				processing += cost
				if err != nil {
					return nil, processing, err
				}
			}
			begin := p.session.owner.options.Clock()
			appendPCM(result, b)
			processing += p.session.owner.options.Clock().Sub(begin)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				if err := p.recordFailure(d.entry, readErr); err != nil {
					return nil, processing, err
				}
			}
			d.close()
			p.current, p.started = nil, false
			p.endFrame = p.cursorFrames + int64(result.Frames)
			p.closeMix()
			// 实际 EOF 早于截断点时不能再使用已经跳过开头的淡化来源。
			if p.plan.kind == "crossfade" {
				p.abandonCrossfade()
			}
			if p.consecutive >= 5 {
				p.restartNext = nil
				if p.next != nil {
					p.next.close()
					p.next = nil
				}
				p.selected = true
			}
			if err := p.flushFailures(p.endFrame); err != nil {
				return nil, processing, err
			}
		}
		if (b == nil || b.Frames == 0) && readErr == nil {
			return nil, processing, &SourceError{Reason: "decode_failed"}
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

func appendSilence(dst *chain.Buffer, frames int) {
	// 精确零能用任意输出位深表示；纯静音块也不能因 SourceBits=0 被抖动。
	dst.SourceBits = max(dst.SourceBits, 1)
	for channel, samples := range dst.Samples {
		samples = append(samples, make([]float64, frames)...)
		dst.Samples[channel] = samples
	}
	dst.Frames += frames
}
