package identity

import (
	"crypto/sha256"
	"net/netip"
	"sync"
	"time"
)

const (
	accountThreshold = 5
	baseWait         = time.Minute
	maxWait          = 15 * time.Minute
	accountForget    = 24 * time.Hour
	sourceLimit      = 30
	sourceWindow     = time.Minute
	sourcePause      = time.Minute
	maxAccounts      = 10000
	maxSources       = 4096
)

// SourceOf 只解析 TCP 对端；IPv4 保留完整地址，IPv6 按 /64 合并，不读取转发头。
func SourceOf(remoteAddr string) string {
	addressPort, err := netip.ParseAddrPort(remoteAddr)
	address := addressPort.Addr()
	if err != nil {
		address, err = netip.ParseAddr(remoteAddr)
		if err != nil {
			return "unknown"
		}
	}
	address = address.WithZone("").Unmap()
	if address.Is4() {
		return address.String()
	}
	return netip.PrefixFrom(address, 64).Masked().String()
}

// LoginThrottle 拥有单实例内的有界限速状态；时钟由装配方注入，没有后台协程。
type LoginThrottle struct {
	mu        sync.Mutex
	now       func() time.Time
	accounts  map[accountKey]*accountState
	sources   map[string]*sourceState
	lastSweep time.Time
	nextStamp uint64
}

type accountKey struct {
	username [32]byte
	source   string
	password bool // 改密码使用独立命名空间，不能与登录用户名碰撞。
}

type accountState struct {
	fingerprint [8]byte
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

type sourceStamp struct {
	id uint64
	at time.Time
}

type sourceState struct {
	stamps      []sourceStamp
	pausedUntil time.Time
	lastFailure time.Time
}

// ThrottledError 表示尚需等待；Started 仅在来源暂停刚开始的那次拒绝时为真。
type ThrottledError struct {
	Scope      string
	RetryAfter time.Duration
	Started    bool
}

func (e *ThrottledError) Error() string { return "too many attempts" }

// Attempt 是预先计入失败的尝试；失败无需回调，密码正确时调用 Succeeded。
type Attempt struct {
	throttle  *LoginThrottle
	key       accountKey
	account   *accountState
	source    *sourceState
	stampID   uint64
	failures  int
	lockedFor time.Duration
	succeeded bool
}

// NewLoginThrottle 创建限速器；nil 时钟使用真实时间。
func NewLoginThrottle(now func() time.Time) *LoginThrottle {
	if now == nil {
		now = time.Now
	}
	return &LoginThrottle{now: now, accounts: make(map[accountKey]*accountState), sources: make(map[string]*sourceState)}
}

// Begin 在核对密码前原子检查来源与账号，并预先计数；source 应来自 SourceOf。
func (t *LoginThrottle) Begin(username, source string, fingerprint [8]byte) (*Attempt, error) {
	return t.begin(accountKey{username: sha256.Sum256([]byte(username)), source: source}, fingerprint)
}

// BeginPassword 按用户 ID 限制当前密码核对，不占用登录的来源额度。
func (t *LoginThrottle) BeginPassword(userID string, fingerprint [8]byte) (*Attempt, error) {
	return t.begin(accountKey{username: sha256.Sum256([]byte("password:" + userID)), password: true}, fingerprint)
}

func (t *LoginThrottle) begin(key accountKey, fingerprint [8]byte) (*Attempt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if now.Sub(t.lastSweep) >= time.Minute {
		t.sweep(now)
	}
	if !key.password {
		if err := t.checkSourceLocked(key.source, now); err != nil {
			return nil, err
		}
	}
	state := t.accounts[key]
	if state != nil && (state.fingerprint != fingerprint || !now.Before(state.lastFailure.Add(accountForget))) {
		delete(t.accounts, key)
		state = nil
	}
	if state != nil && now.Before(state.lockedUntil) {
		return nil, &ThrottledError{Scope: "account", RetryAfter: state.lockedUntil.Sub(now)}
	}
	if state == nil {
		t.makeAccountRoom(now)
		state = &accountState{fingerprint: fingerprint}
		t.accounts[key] = state
	}
	state.failures++
	state.lastFailure = now
	attempt := &Attempt{throttle: t, key: key, account: state, failures: state.failures}
	if state.failures >= accountThreshold {
		wait := maxWait
		if shift := state.failures - accountThreshold; shift < 4 {
			wait = baseWait << shift
		}
		state.lockedUntil = now.Add(wait)
		attempt.lockedFor = wait
	}
	if !key.password {
		src := t.sources[key.source]
		if src == nil {
			t.makeSourceRoom(now)
			src = &sourceState{stamps: make([]sourceStamp, 0, sourceLimit)}
			t.sources[key.source] = src
		}
		t.nextStamp++
		src.stamps = append(src.stamps, sourceStamp{id: t.nextStamp, at: now})
		src.lastFailure = now
		attempt.source, attempt.stampID = src, t.nextStamp
	}
	return attempt, nil
}

// checkSource 不预占额度，在查用户之前挡住已暂停或已耗尽额度的来源。
// Begin 会在同一把锁下再次核对，覆盖查库期间的并发尝试。
func (t *LoginThrottle) checkSource(source string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.checkSourceLocked(source, t.now())
}

func (t *LoginThrottle) checkSourceLocked(source string, now time.Time) error {
	src := t.sources[source]
	if src == nil {
		return nil
	}
	if now.Before(src.pausedUntil) {
		return &ThrottledError{Scope: "source", RetryAfter: src.pausedUntil.Sub(now)}
	}
	src.prune(now)
	if len(src.stamps) >= sourceLimit {
		src.pausedUntil = now.Add(sourcePause)
		return &ThrottledError{Scope: "source", RetryAfter: sourcePause, Started: true}
	}
	return nil
}

func (s *sourceState) prune(now time.Time) {
	retained := s.stamps[:0]
	for _, stamp := range s.stamps {
		if stamp.at.After(now.Add(-sourceWindow)) {
			retained = append(retained, stamp)
		}
	}
	s.stamps = retained
}

// Succeeded 只清除此组合；旧尝试不能删除指纹更新、过期或淘汰后重建的状态。
func (a *Attempt) Succeeded() {
	t := a.throttle
	t.mu.Lock()
	defer t.mu.Unlock()
	if a.succeeded {
		return
	}
	a.succeeded = true
	if t.accounts[a.key] == a.account {
		delete(t.accounts, a.key)
	}
	if a.source != nil {
		for i, stamp := range a.source.stamps {
			if stamp.id == a.stampID {
				a.source.stamps = append(a.source.stamps[:i], a.source.stamps[i+1:]...)
				break
			}
		}
	}
}

// Failures 返回本次开始时的计数快照，避免日志随并发尝试变化。
func (a *Attempt) Failures() int { return a.failures }

// LockedFor 返回本次预计数触发的等待；只有核对失败时才应据此记日志。
func (a *Attempt) LockedFor() time.Duration { return a.lockedFor }

func (t *LoginThrottle) sweep(now time.Time) {
	for key, state := range t.accounts {
		if !now.Before(state.lockedUntil) && !now.Before(state.lastFailure.Add(accountForget)) {
			delete(t.accounts, key)
		}
	}
	for source, state := range t.sources {
		state.prune(now)
		if len(state.stamps) == 0 && !now.Before(state.pausedUntil) {
			delete(t.sources, source)
		}
	}
	t.lastSweep = now
}

func (t *LoginThrottle) makeAccountRoom(now time.Time) {
	if len(t.accounts) < maxAccounts {
		return
	}
	t.sweep(now)
	if len(t.accounts) < maxAccounts {
		return
	}
	var oldest accountKey
	var oldestTime time.Time
	for key, state := range t.accounts {
		if oldestTime.IsZero() || state.lastFailure.Before(oldestTime) {
			oldest, oldestTime = key, state.lastFailure
		}
	}
	delete(t.accounts, oldest)
}

func (t *LoginThrottle) makeSourceRoom(now time.Time) {
	if len(t.sources) < maxSources {
		return
	}
	t.sweep(now)
	if len(t.sources) < maxSources {
		return
	}
	var oldest string
	var oldestTime time.Time
	for source, state := range t.sources {
		if oldestTime.IsZero() || state.lastFailure.Before(oldestTime) {
			oldest, oldestTime = source, state.lastFailure
		}
	}
	delete(t.sources, oldest)
}
