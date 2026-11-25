package slimproto

import "time"

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

func (p *groupPeer) apparentStart() time.Time {
	return p.point.Add(-p.elapsed + time.Duration(p.SyncDelayMS)*time.Millisecond)
}

func (g *groupCoordinator) correct(now time.Time) error {
	var earliest time.Time
	for _, p := range g.peers {
		if p.comparable(now, g.current.seq) {
			start := p.apparentStart()
			if earliest.IsZero() || start.Before(earliest) {
				earliest = start
			}
		}
	}
	for _, p := range g.peers {
		if !p.comparable(now, g.current.seq) || now.Sub(p.corrected) < time.Second || !p.point.After(p.corrected) {
			continue
		}
		lag := p.apparentStart().Sub(earliest)
		if lag >= 10*time.Millisecond && lag <= 10*time.Second {
			if err := p.Player.command("strm", controlMS('a', uint32(lag.Milliseconds()))); err != nil {
				if err = g.drop(p, "offline"); err != nil {
					return err
				}
			}
			p.corrected = now
		}
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
		at = ref.point.Add(-ref.elapsed)
	} else {
		if g.current.duration <= 0 || ref.point.IsZero() || ref.paused {
			return nil
		}
		at = ref.point.Add(time.Duration(g.current.duration*float64(time.Second)) - ref.elapsed)
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
