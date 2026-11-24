package ptp

import "time"

type syncPair struct {
	received           time.Time
	origin, correction int64
	originKnown        bool
}

type follower struct {
	source                  portID
	grandmaster             uint64
	rank                    [14]byte
	steps                   uint16
	timeSource              byte
	syncs                   map[uint16]syncPair
	syncSeq                 uint16
	hasSync                 bool
	forward                 int64
	lastSync                time.Time
	delaySeq                uint16
	delaySent               time.Time
	offset                  int64
	samples                 int
	firstSample, lastSample time.Time
}

func (f *follower) reset(source portID) {
	*f = follower{source: source, grandmaster: source.clock, syncs: make(map[uint16]syncPair)}
}

func (f *follower) sync(p arrival) {
	v, pending := f.syncs[p.seq]
	if f.hasSync {
		// 按 16 位序号的半区间比较，65535 后的 0 是新一轮 Sync。
		delta := int16(p.seq - f.syncSeq)
		if delta < 0 || delta == 0 && (p.kind == msgSync || !pending) {
			return
		}
	}
	if p.kind == msgSync {
		v.received = p.at
	} else if v.originKnown {
		return
	}
	if p.kind == msgFollowUp || p.flags&twoStep == 0 {
		v.origin, v.originKnown = readStamp(p.b[34:])
		if !v.originKnown {
			return
		}
		if p.kind == msgSync {
			// 单步 Sync 自带源时间，不使用提前到达的 Follow_Up 修正值。
			v.correction = 0
		}
	}
	v.correction += p.correction
	if p.kind == msgSync {
		f.syncSeq, f.hasSync = p.seq, true
	}
	// 保留当前 Sync 和近期先到的 Follow_Up，淘汰的序号不能跨一轮回绕重新配对。
	for seq := range f.syncs {
		if f.hasSync && seq == f.syncSeq {
			continue
		}
		distance := int16(seq - p.seq)
		obsolete := f.hasSync && int16(seq-f.syncSeq) < 0
		if obsolete || distance < -16 || distance > 16 {
			delete(f.syncs, seq)
		}
	}
	if v.received.IsZero() || !v.originKnown {
		f.syncs[p.seq] = v
		return
	}
	delete(f.syncs, p.seq)
	f.forward = v.origin - v.received.UnixNano() + v.correction
	f.lastSync = v.received
}

func (f *follower) measure(p arrival, id portID) {
	if f.delaySent.IsZero() || p.seq != f.delaySeq || readID(p.b[44:]) != id || f.lastSync.IsZero() || p.at.Sub(f.lastSync) > 2*time.Second {
		return
	}
	t4, ok := readStamp(p.b[34:])
	if !ok {
		return
	}
	backward := t4 - f.delaySent.UnixNano() - p.correction
	f.delaySent = time.Time{}
	path := (backward - f.forward) / 2
	if path < -int64(time.Millisecond) || path > int64(250*time.Millisecond) {
		return
	}
	sample := f.forward + path
	if f.samples == 0 || p.at.Sub(f.lastSample) > 3*time.Second || sample-f.offset > int64(5*time.Millisecond) || f.offset-sample > int64(5*time.Millisecond) {
		f.offset = sample
		f.samples = 0
		f.firstSample = p.at
	} else {
		f.offset += (sample - f.offset) / 4
	}
	f.samples++
	f.lastSample = p.at
}

func (f *follower) status(local uint64, now time.Time) Status {
	s := Status{Role: "follower", ClockID: local, GrandmasterID: f.grandmaster, OffsetNS: f.offset}
	s.Available = f.samples >= 3 && f.lastSample.Sub(f.firstSample) >= 250*time.Millisecond && now.Sub(f.lastSample) < 3*time.Second && now.Sub(f.lastSync) < 3*time.Second
	if !s.Available {
		s.Reason = "clock_unlocked"
	}
	return s
}
