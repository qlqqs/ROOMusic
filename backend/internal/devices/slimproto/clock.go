package slimproto

import "time"

// Jiffies 展开一条有序控制连接上的 32 位毫秒计数。
type Jiffies struct {
	previous uint32
	wraps    uint64
}

func (j *Jiffies) Unwrap(value uint32) uint64 {
	if value < j.previous {
		j.wraps += 1 << 32
	}
	j.previous = value
	return j.wraps + uint64(value)
}

// ClockEstimator 按单向延迟的下包络估计同一控制连接的时钟零点。
type ClockEstimator struct {
	jiffies     Jiffies
	epoch       time.Time
	offsets     [50]time.Time
	count, next int
}

func (c *ClockEstimator) Observe(jiffies uint32, received time.Time) time.Time {
	elapsed := time.Duration(c.jiffies.Unwrap(jiffies)) * time.Millisecond
	offset := received.Add(-elapsed)
	if c.epoch.IsZero() || offset.Before(c.epoch) {
		c.epoch = offset
		c.count, c.next = 0, 0
		return received
	}
	c.offsets[c.next] = offset
	c.next = (c.next + 1) % len(c.offsets)
	c.count = min(c.count+1, len(c.offsets))
	if c.count >= 10 {
		floor := c.offsets[0]
		for _, sample := range c.offsets[1:c.count] {
			if sample.Before(floor) {
				floor = sample
			}
		}
		drift := floor.Sub(c.epoch)
		if drift > time.Millisecond && (drift <= 5*time.Millisecond || c.count == len(c.offsets)) {
			c.epoch = floor
			c.count, c.next = 0, 0
		}
	}
	return c.epoch.Add(elapsed)
}

// Target 将未来的服务器时刻换为协议的无符号毫秒数，零在协议中表示立即播放。
func (c *ClockEstimator) Target(now, target time.Time) (uint32, bool) {
	if c.epoch.IsZero() || !target.After(now) || target.Sub(now) > 10*time.Second {
		return 0, false
	}
	ms := uint32(target.Sub(c.epoch).Milliseconds())
	if ms == 0 {
		if target.Add(time.Millisecond).Sub(now) > 10*time.Second {
			return 0, false
		}
		ms = 1
	}
	return ms, true
}
