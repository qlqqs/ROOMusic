package slimproto

import (
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

const (
	driftDeadband   = 500 * time.Microsecond
	driftFallback   = 20 * time.Millisecond
	fallbackMinimum = 10 * time.Millisecond
	skipMaximum     = 10 * time.Second

	driftMinSpan    = 20 * time.Second // 首次回归所需的实测时间跨度。
	driftMinSamples = 15               // 每个样本来自一个新的成员播放点。
	driftRatePeriod = 10 * time.Second // 两次前馈更新的最短间隔。
	driftWindow     = 60 * time.Second // 仅拟合最近一分钟内的同流样本。
	driftPPMUnit    = 1e6              // 一秒内每微秒的相对漂移为 1 ppm。
	driftMaxPPM     = 500              // 每 2000 个输出帧最多调整一帧。
)

func (g *groupCoordinator) poll(p *groupPeer, now time.Time) error {
	if err := p.Player.command("strm", control('t')); err != nil {
		return g.drop(p, "offline")
	}
	interval := time.Second
	if now.Before(p.warmUntil) || g.buffering && p.current != nil {
		interval = warmInterval
	}
	p.nextPoll = now.Add(interval)
	return nil
}

func (g *groupCoordinator) tick(now time.Time) error {
	for _, p := range g.peers {
		if p.Player == nil {
			continue
		}
		if now.Sub(p.attached) >= 5*time.Second && p.Player.silentSince(now.Add(-5*time.Second)) {
			p.Player.cancel()
			if err := g.drop(p, "offline"); err != nil {
				return err
			}
			continue
		}
		if !now.Before(p.nextPoll) {
			if err := g.poll(p, now); err != nil {
				return err
			}
		}
	}
	if !now.Before(g.nextCheck) {
		g.nextCheck = now.Add(time.Second)
		if err := g.outputSession.apply(); err != nil {
			return err
		}
		if g.group != g {
			return nil
		}
		if g.state == "playing" && g.controlIntent == "play" {
			if err := g.correct(now); err != nil {
				return err
			}
			return g.report("status", "playing", nil, g.seq(), g.position())
		}
	}
	return g.advance(now)
}

func (g *groupCoordinator) restart() error {
	// 流令牌只能取一次，请播放区从参考成员的最后位置重建渲染句柄。
	fault := "stream_lost"
	return g.report("fault", "paused", &fault, g.seq(), g.position())
}

func (g *groupCoordinator) schedule(now time.Time, resume bool) error {
	var maxDelay int
	first := true
	for _, p := range g.peers {
		if p.current == nil {
			continue
		}
		if resume && !p.paused {
			return g.restart()
		}
		if first || p.SyncDelayMS > maxDelay {
			maxDelay = p.SyncDelayMS
		}
		first = false
	}
	// 先校验全部目标，避免继续到一半才发现另一台缺少时钟或超出协议的十秒窗口。
	type startTime struct {
		jiffies uint32
		at      time.Time
	}
	targets := make(map[*groupPeer]startTime)
	for _, p := range g.peers {
		if p.current == nil {
			continue
		}
		// 继续与开新流一样，以统一时刻为基准起播，并应用各台的同步微调。
		// 暂停命令在局域网内到达各台的亚毫秒差异由维持同步修正。
		// elapsed 按输出块累加，粒度太粗，不能用它的差值调整继续时刻。
		delay := time.Duration(maxDelay-p.SyncDelayMS) * time.Millisecond
		at := now.Add(startLead + delay)
		target, valid, wrap := p.Player.target(now, at)
		if !valid {
			return g.restart()
		}
		if wrap {
			g.resumePending = resume
			return nil
		}
		targets[p] = startTime{target, at}
	}
	for p, target := range targets {
		if err := p.Player.command("strm", controlMS('u', target.jiffies)); err != nil {
			if err = g.drop(p, "offline"); err != nil {
				return err
			}
			continue
		}
		p.resetDriftSamples()
		p.point, p.scheduled = time.Time{}, target.at
		p.paused = false
	}
	g.buffering, g.resumePending = false, false
	for p := range targets {
		if p.current == nil {
			continue
		}
		if err := g.setState(p, "active"); err != nil {
			return err
		}
	}
	return nil
}

func (p *groupPeer) comparable(now time.Time, seq int64) bool {
	return p.current != nil && p.current.seq == seq && p.current.started && !p.paused && !p.ended && !p.point.IsZero() && now.Sub(p.point) <= pointLifetime
}

func (p *groupPeer) outputFrame() int64 {
	return int64(math.Round(p.elapsed.Seconds() * float64(p.current.rate)))
}

func frameDuration(frames int64, rate int) time.Duration {
	return time.Duration(math.Round(float64(frames) * float64(time.Second) / float64(rate)))
}

func (p *groupPeer) sourceElapsed() time.Duration {
	if p.current.drift == nil {
		return p.elapsed
	}
	output := p.outputFrame()
	// 只换算插删造成的差值，保留毫秒回报在非整帧采样率下的小数余量。
	return p.elapsed + frameDuration(p.current.drift.Source(output)-output, p.current.rate)
}

func (p *groupPeer) apparentStart() time.Time {
	return p.point.Add(-p.sourceElapsed() + time.Duration(p.SyncDelayMS)*time.Millisecond)
}

type lagSample struct {
	at  time.Time
	lag time.Duration
}

func (p *groupPeer) resetDriftSamples() {
	p.driftSamples = p.driftSamples[:0]
	p.rateUpdated = time.Time{}
}

func (p *groupPeer) observeDrift(at time.Time, rawLag time.Duration) {
	setter, ok := p.current.drift.(zones.DriftRateSetter)
	if !ok {
		return
	}
	samples := p.driftSamples
	if len(samples) > 0 && !at.After(samples[len(samples)-1].at) {
		return
	}
	for len(samples) > 0 && at.Sub(samples[0].at) > driftWindow {
		samples = samples[1:]
	}
	samples = append(samples, lagSample{at, rawLag})
	p.driftSamples = samples
	if len(samples) < driftMinSamples || at.Sub(samples[0].at) < driftMinSpan || at.Sub(p.rateUpdated) < driftRatePeriod {
		return
	}
	// 以窗口起点为原点，带截距拟合。恒定的起播偏差只改变截距。
	var sx, sy, sxx, sxy float64
	for _, sample := range samples {
		x := sample.at.Sub(samples[0].at).Seconds()
		y := sample.lag.Seconds()
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	n := float64(len(samples))
	ppm := driftPPMUnit * (sxy - sx*sy/n) / (sxx - sx*sx/n)
	p.ppm = min(driftMaxPPM, max(-driftMaxPPM, ppm))
	p.rateUpdated = at
	setter.SetRate(p.ppm)
	if p.next != nil {
		if next, ok := p.next.drift.(zones.DriftRateSetter); ok {
			next.SetRate(p.ppm)
		}
	}
}

func (g *groupCoordinator) correct(now time.Time) error {
	if g.current == nil || !g.leader.comparable(now, g.current.seq) {
		return nil
	}
	start := g.leader.apparentStart()
	for _, p := range g.peers {
		if p == g.leader || !p.comparable(now, g.current.seq) || now.Sub(p.corrected) < time.Second || !p.point.After(p.corrected) {
			continue
		}
		lag := p.apparentStart().Sub(start)
		drift := p.current.drift
		interval := lag.Abs()
		if drift == nil || interval > driftFallback {
			if interval < fallbackMinimum || interval > skipMaximum {
				continue
			}
			command := byte('a')
			if lag < 0 {
				// 带正间隔的 p 插入静音，播放器自行继续，无需等待 STMp。
				command = 'p'
			}
			ms := uint32(interval.Milliseconds())
			if err := p.Player.command("strm", controlMS(command, ms)); err != nil {
				if err = g.drop(p, "offline"); err != nil {
					return err
				}
				continue
			}
			p.resetDriftSamples()
			if drift != nil {
				// 协议命令已经按偏差修正，抵消该播放点尚未生效的净调整。
				if pending := drift.Pending(p.outputFrame()); pending != 0 {
					drift.Adjust(-pending)
				}
			}
		} else {
			output := p.outputFrame()
			rate := p.current.rate
			// 加回已播净调整等价于使用原始输出 elapsed；保留毫秒回报的小数余量。
			rawLag := p.point.Add(-p.elapsed + time.Duration(p.SyncDelayMS)*time.Millisecond).Sub(start)
			p.observeDrift(p.point, rawLag)
			// 新请求最早作用于渲染头。把目标外推到那里，再扣全部在途调整，
			// 否则反馈会抵消缓冲区内为未来漂移准备的前馈量。
			var pending int64
			if renderer, ok := drift.(interface{ RenderState(int64) (int64, int64) }); ok {
				var head int64
				head, pending = renderer.RenderState(output)
				ahead := max(0, head-output)
				lag += time.Duration(math.Round(float64(time.Second) * float64(ahead) / float64(rate) * p.ppm / driftPPMUnit))
			} else {
				pending = drift.Pending(output)
			}
			if (lag - frameDuration(pending, rate)).Abs() <= driftDeadband {
				continue
			}
			drift.Adjust(int64(math.Round(lag.Seconds()*float64(rate))) - pending)
		}
		p.corrected = now
	}
	return nil
}

// 新成员没有可接续的输出缓冲，按参考设备的旧流结束时刻定时开始；旧成员继续无缝播放。
func (g *groupCoordinator) joinNext(p *groupPeer, now time.Time) error {
	if !p.ready {
		if now.Before(p.joinBy) {
			return nil
		}
		return g.drop(p, "dropped")
	}
	ref := g.reference
	var at time.Time
	if p.current.seq == g.current.seq {
		at = ref.point.Add(-ref.sourceElapsed())
	} else {
		if g.current.duration <= 0 || ref.point.IsZero() || ref.paused {
			return nil
		}
		at = ref.point.Add(time.Duration(g.current.duration*float64(time.Second)) - ref.sourceElapsed())
	}
	at = at.Add(time.Duration(ref.SyncDelayMS-p.SyncDelayMS) * time.Millisecond)
	if at.Sub(now) > time.Second {
		return nil
	}
	if soon := now.Add(startLead); at.Before(soon) {
		at = soon
	}
	target, valid, wrap := p.Player.target(now, at)
	if !valid || wrap {
		return nil
	}
	if err := p.Player.command("strm", controlMS('u', target)); err != nil {
		return g.drop(p, "offline")
	}
	p.scheduled, p.joinBy = at, time.Time{}
	return g.setState(p, "active")
}
