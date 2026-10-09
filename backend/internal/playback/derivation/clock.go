package derivation

import "time"

// clock 是管理器与缓存使用的时间来源：最近使用时间、宽限期与失败记忆都经它读取，测试注入可手动推进的实现。
type clock interface {
	now() time.Time
	// afterFunc 在 d 之后调用 f；返回的函数取消尚未触发的调用，已触发或已取消时返回 false。
	afterFunc(d time.Duration, f func()) (stop func() bool)
}

type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

func (realClock) afterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}
