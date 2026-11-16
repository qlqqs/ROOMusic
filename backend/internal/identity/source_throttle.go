package identity

import "time"

// SourceThrottle 复用登录的来源限速规则，拥有独立状态，不进行账号计数。
type SourceThrottle struct{ throttle *LoginThrottle }

// NewSourceThrottle 创建独立的来源限速器；nil 时钟使用真实时间。
func NewSourceThrottle(now func() time.Time) *SourceThrottle {
	return &SourceThrottle{throttle: NewLoginThrottle(now)}
}

// Check 在认证前检查是否暂停，不占用失败额度。
func (t *SourceThrottle) Check(source string) error {
	return t.throttle.checkSource(source)
}

// Failed 只记录已确认的认证失败；第 30 次失败开始暂停，并返回 Started=true。
func (t *SourceThrottle) Failed(source string) error {
	l := t.throttle
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= time.Minute {
		l.sweep(now)
	}
	if err := l.checkSourceLocked(source, now); err != nil {
		return err
	}
	state := l.sources[source]
	if state == nil {
		l.makeSourceRoom(now)
		state = &sourceState{stamps: make([]sourceStamp, 0, sourceLimit)}
		l.sources[source] = state
	}
	l.nextStamp++
	state.stamps = append(state.stamps, sourceStamp{id: l.nextStamp, at: now})
	state.lastFailure = now
	return l.checkSourceLocked(source, now)
}
