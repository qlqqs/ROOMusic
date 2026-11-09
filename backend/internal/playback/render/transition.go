package render

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/fade"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type transitionPlan struct {
	kind         string
	cut          int64
	frames       int
	startSeconds float64
}

func (p *player) ordinaryTransition() transitionPlan {
	if p.session.spec.Settings.Gapless {
		return transitionPlan{kind: "gapless"}
	}
	return transitionPlan{kind: "gap"}
}

func effectiveEnd(track Track) float64 {
	if track.TrailingStart != nil && *track.TrailingStart > 0 {
		return *track.TrailingStart
	}
	return track.DurationSeconds
}

func (p *player) planTransition(track Track) transitionPlan {
	plan := p.ordinaryTransition()
	d := p.current
	settings := p.session.spec.Settings
	if d == nil || !settings.Crossfade || settings.CrossfadeSeconds <= 0 || d.track.ReleaseID == track.ReleaseID {
		return plan
	}
	start := 0.0
	if track.LeadingSilence != nil && *track.LeadingSilence > 0 && *track.LeadingSilence <= 10 {
		start = *track.LeadingSilence
	}
	seconds := float64(settings.CrossfadeSeconds)
	end := effectiveEnd(d.track)
	if end <= 3*seconds || track.DurationSeconds-start <= 3*seconds {
		return plan
	}
	rate := float64(p.session.sourcePCM.SampleRate)
	frames := int(math.Round(seconds * rate))
	cut := int64(fade.AlignDown8(int(math.Round(end*rate)) - frames))
	// 除定位起点外，也不能在队列重选时回到已经渲染过的截断点。
	if int64(math.Round(d.startSeconds*rate)) >= cut || d.position() > cut {
		return plan
	}
	return transitionPlan{kind: "crossfade", cut: cut, frames: frames, startSeconds: start}
}

func (p *player) announceBoundary(entry zones.Entry, reason string) error {
	p.boundary, p.boundaryEntry, p.boundaryReason = true, entry, reason
	return p.session.immediate(zones.Event{Kind: "format_boundary", NextEntryID: entry.EntryID, Reason: reason})
}

// prepareNext 不推进 Cursor；失败重取和取消已选条目都由调用方决定。
func (p *player) prepareNext(ctx context.Context, entry zones.Entry, allowFade bool) (bool, error) {
	track, err := p.session.owner.options.Sources.Prepare(ctx, p.session.spec.UserID, entry.TrackID)
	if ctx.Err() != nil {
		if track.File != nil {
			_ = track.File.Close()
		}
		if p.session.ctx.Err() != nil {
			return false, p.session.ctx.Err()
		}
		return true, p.announceBoundary(entry, "not_ready")
	}
	if err == nil {
		err = validateTrack(track)
	}
	if err != nil {
		if track.File != nil {
			_ = track.File.Close()
		}
		return false, p.recordFailure(entry, err)
	}
	if !p.anchorKnown || !sameSourceFormat(p.anchor, track) || !p.session.acceptsSource(track, entry) {
		_ = track.File.Close()
		return true, p.announceBoundary(entry, "format")
	}
	plan := p.ordinaryTransition()
	if allowFade {
		plan = p.planTransition(track)
	}
	reservation, err := p.session.owner.options.FFmpeg.Reserve(ctx, 1)
	if err != nil {
		_ = track.File.Close()
		if p.session.ctx.Err() != nil {
			return false, p.session.ctx.Err()
		}
		return true, p.announceBoundary(entry, "not_ready")
	}
	next, err := openDecoder(p.session.ctx, reservation, p.session.owner.options.FFmpeg, track, entry, p.session.sourcePCM, plan.startSeconds, p.session.spec.Settings.Leveling)
	_ = reservation.Close()
	if err != nil {
		_ = track.File.Close()
		return false, p.recordFailure(entry, err)
	}
	p.next, p.plan = next, plan
	return true, nil
}

func (p *player) selectNext(ctx context.Context, after zones.Entry) error {
	p.selected = true
	p.plan = transitionPlan{}
	if p.session.spec.Cursor == nil {
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
		prepared, err := p.prepareNext(ctx, entry, true)
		if err != nil || prepared {
			return err
		}
		after = entry
	}
	return nil
}

// 曲尾准备、进程名额与预缓冲共用五秒期限，避免顺序等待叠加。
// 解码器的生命周期仍绑定会话，不能随这段等待结束而取消。
func (p *player) nextDeadline() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(p.session.ctx)
	timer := p.session.owner.options.NewTicker(5 * time.Second)
	go func() {
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C():
			cancel()
		}
	}()
	return ctx, cancel
}

// 曲尾超时才宣布格式边界；淡化切点复用就绪等待，但保留旧曲继续播放。
func (p *player) waitNext(ctx context.Context) (bool, error) {
	ready, err := p.waitNextReady(ctx)
	if ready || err != nil {
		return ready, err
	}
	err = p.announceBoundary(p.next.entry, "not_ready")
	p.next.close()
	p.next = nil
	return false, err
}

// 就绪等待不改变换轨状态，也不计入处理速度。
func (p *player) waitNextReady(ctx context.Context) (bool, error) {
	r := p.next.ring
	for {
		if ctx.Err() != nil {
			if p.session.ctx.Err() != nil {
				return false, p.session.ctx.Err()
			}
			return false, nil
		}
		ready, changed := r.readiness(p.session.sourcePCM.Channels * 4)
		if ready {
			return true, nil
		}
		select {
		case <-ctx.Done():
		case <-changed:
		}
	}
}

// 空解码不算成功；保留连续失败计数，最多跳过五首。
func (p *player) rejectEmptyNext(ctx context.Context) (bool, error) {
	d := p.next
	d.ring.mu.Lock()
	empty, terminal := d.ring.size < p.session.sourcePCM.Channels*4, d.ring.terminal
	d.ring.mu.Unlock()
	if !empty || terminal == nil {
		return false, nil
	}
	if errors.Is(terminal, io.EOF) {
		terminal = &SourceError{Reason: "decode_failed"}
	}
	d.close()
	p.next = nil
	if err := p.recordFailure(d.entry, terminal); err != nil {
		return true, err
	}
	return true, p.selectNext(ctx, d.entry)
}

func (p *player) nextChanged(finalized, transition bool) error {
	if transition || p.session.hasUnheardTransition() {
		return p.session.immediate(zones.Event{Kind: "rebuild_needed"})
	}
	announced := p.boundary
	if !p.selected && !finalized && !announced {
		return nil
	}
	if p.next != nil {
		p.next.close()
		p.next = nil
	}
	p.plan = transitionPlan{}
	p.restartNext = nil
	p.failures = nil
	p.consecutive = 0
	if !finalized && !announced {
		if p.current == nil {
			// 曲间重选交回同一量子的 beginNext，准备与预缓冲共享曲尾期限。
			p.selected = false
			return nil
		}
		// 即使原先取到的是队尾，也立即重判；不能再等另一个预取阈值。
		return p.selectNext(p.session.ctx, p.last)
	}
	// 已宣布的边界不能撤销；编码器冲刷后也不能追加字节。
	p.selected, p.boundary = true, true
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
	reason := p.boundaryReason
	if reason == "" {
		reason = "format"
	}
	return p.announceBoundary(entry, reason)
}
