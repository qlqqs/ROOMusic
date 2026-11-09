package render

import (
	"errors"
	"io"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/fade"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type crossfadeWindow struct {
	outgoing         *decoder
	frames, position int
}

func (p *player) closeMix() {
	if p.mix != nil {
		if p.mix.outgoing != nil {
			p.mix.outgoing.close()
		}
		p.mix = nil
	}
}

func (p *player) beginCrossfade(frame int64) error {
	ready, _ := p.next.ring.readiness(p.session.pcm.Channels * 4)
	if !ready {
		p.abandonCrossfade()
		return nil
	}
	p.next.ring.mu.Lock()
	hasAudio := p.next.ring.size >= p.session.pcm.Channels*4
	p.next.ring.mu.Unlock()
	if !hasAudio {
		p.abandonCrossfade()
		return nil
	}
	if err := p.flushFailures(frame); err != nil {
		return err
	}
	p.mix = &crossfadeWindow{outgoing: p.current, frames: p.plan.frames}
	p.promoteNext()
	return nil
}

func (p *player) abandonCrossfade() {
	p.plan = p.ordinaryTransition()
	if p.next.startSeconds == 0 {
		return
	}
	// 放弃淡化后恢复完整曲首；不能把为淡入跳过的静音带进无缝拼接。
	entry := p.next.entry
	p.next.close()
	p.next = nil
	// 延至真实曲尾再打开，避免在截断点等待来源准备或进程名额。
	p.restartNext = &entry
}

// mixPCM 的两个输入都已由 decoder 施加各自电平化；就地改 B，绝不重置共享链。
func (p *player) mixPCM(b *chain.Buffer, frame int64) (time.Duration, error) {
	window := p.mix
	var cost time.Duration
	var a *chain.Buffer
	if window.outgoing != nil {
		var readErr error
		a, cost, readErr = window.outgoing.read(p.session.ctx, b.Frames, false, p.session.owner.options.Clock)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				if failureReason(readErr) == "transient" {
					return cost, readErr
				}
				frames := 0
				if a != nil {
					frames = a.Frames
				}
				if err := p.session.queueEvent(frame+int64(frames), zones.Event{Kind: "track_failed", EntryID: window.outgoing.entry.EntryID, Reason: failureReason(readErr)}); err != nil {
					return cost, err
				}
			}
			window.outgoing.close()
			window.outgoing = nil
		}
	}
	begin := p.session.owner.options.Clock()
	if a != nil && a.Frames == b.Frames {
		fade.Mix(b.Samples, a.Samples, b.Samples, window.position, window.frames)
	} else {
		// 来源提前 EOF 时只混实际存在的样本；窗口余下部分仍按 B 的淡入位置推进。
		for i := range b.Frames {
			x := float64(window.position+i) / float64(window.frames)
			in, out := fade.FadeIn(x), fade.FadeOut(x)
			for ch := range b.Samples {
				b.Samples[ch][i] *= in
				if a != nil && i < a.Frames {
					b.Samples[ch][i] += a.Samples[ch][i] * out
				}
			}
		}
	}
	b.Exact = false
	if a != nil {
		b.SourceBits = max(b.SourceBits, a.SourceBits)
	}
	window.position += b.Frames
	cost += p.session.owner.options.Clock().Sub(begin)
	if window.position == window.frames {
		p.closeMix()
	}
	return cost, nil
}
