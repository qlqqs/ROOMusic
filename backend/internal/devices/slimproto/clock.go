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

// ClockEstimator 属于一条控制连接。预热期间直接使用接收时刻。
type ClockEstimator struct {
	jiffies           Jiffies
	samples           [50]time.Time
	count, next       int
	epoch, calculated time.Time
}

func (c *ClockEstimator) Observe(jiffies uint32, received time.Time) time.Time {
	elapsed := time.Duration(c.jiffies.Unwrap(jiffies)) * time.Millisecond
	c.samples[c.next] = received.Add(-elapsed)
	c.next = (c.next + 1) % len(c.samples)
	c.count = min(c.count+1, len(c.samples))
	if c.count < 10 {
		return received
	}
	if c.calculated.IsZero() || received.Sub(c.calculated) >= 30*time.Second {
		c.epoch = c.samples[0]
		for _, sample := range c.samples[1:c.count] {
			if sample.Before(c.epoch) {
				c.epoch = sample
			}
		}
		c.calculated = received
	}
	estimate := c.epoch.Add(elapsed)
	if estimate.After(received) {
		return received
	}
	return estimate
}
