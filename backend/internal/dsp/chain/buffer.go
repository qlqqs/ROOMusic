// Package chain 装配跨曲目连续运行的 PCM 处理链。
package chain

import "github.com/qlqq/roomusic/backend/internal/dsp/leveling"

// Format 在一条处理链的生命周期内保持不变。
type Format struct {
	SampleRate int
	Channels   int
	Layout     uint32
}

// GainRegion 与电平化共用同一类型，算法叶子包不反向依赖处理链。
type GainRegion = leveling.GainRegion

// Buffer 的样本按声道分开，每声道至少有 Frames 帧，满刻度为 ±1。
// 节点返回的缓冲只保证在下一次调用前有效；允许就地修改输入。
type Buffer struct {
	Samples    [][]float64
	Frames     int
	Regions    []GainRegion
	Exact      bool
	SourceBits int // 整数源有效位深；浮点源为 0。
}

// Processor 接受等长声道，输出可以变率或攒块。返回值在下次调用前有效。
// Latency 是按输出采样率计的固定延迟；流尾持续喂零必须能冲刷出剩余内容。
type Processor interface {
	Process(in [][]float64) [][]float64
	Latency() int
	Reset()
}

// Node 是浮点链节点。需要冲刷的节点另实现 Drain() *Buffer，结束时返回 nil。
type Node interface {
	Process(b *Buffer) *Buffer
	Reset()
	Step() SignalStep
}

// SignalStep 描述稳态信号路径；Kind 为空表示不显示的内部节点。
// Params 在构造后只读，不能在音频处理循环中修改。
type SignalStep struct {
	Kind    string         `json:"kind"`
	Quality string         `json:"quality"`
	Lossy   bool           `json:"lossy"`
	Compat  bool           `json:"compat"`
	Params  map[string]any `json:"params"`
}
