package chain

import (
	"errors"
	"math/big"
	"math/bits"

	"github.com/qlqq/roomusic/backend/internal/dsp/dither"
)

// Stage 描述一个会话独占节点及其输入、输出格式。
type Stage struct {
	Node     Node
	In, Out  Format
	Modifies bool
}

// Spec 按 Headroom、Stages、User、Tail、量化出口的顺序装配共享链。
type Spec struct {
	Format             Format
	Stages, User, Tail []Stage
	Headroom           HeadroomSettings
	OutputBits         int // 16/24 为定点出口，0 为直接向有损编码器交付浮点。
	Seed               uint32
}

// Chain 由渲染循环独占，跨曲目不复位。Quantizer 为 nil 时没有定点出口。
// 使用 Quantize 而非直接调用 Quantizer，可统一按 Buffer 判定直通。
type Chain struct {
	Quantizer     *dither.Quantizer
	nodes         []Node
	steps         []SignalStep
	bits          int
	draining      bool
	drainNode     int
	input, output Format
	stages        []Stage
	latency       int
	modifies      bool
}

// New 组装显式节点，节点顺序在构造后不变；输入必须已施加电平化。
// 生产渲染应使用 Build，以获得完整的格式与帧映射。
func New(nodes ...Node) *Chain {
	c := &Chain{nodes: append([]Node(nil), nodes...)}
	for _, node := range c.nodes {
		if step := node.Step(); step.Kind != "" {
			c.steps = append(c.steps, step)
		}
	}
	return c
}

// Build 校验逐节点格式并冻结列表；带状态的节点本身不克隆。
func Build(spec Spec) (*Chain, error) {
	valid := func(f Format) bool {
		return f.SampleRate > 0 && f.Channels > 0 && (f.Layout == 0 || bits.OnesCount32(f.Layout) == f.Channels)
	}
	if !valid(spec.Format) {
		return nil, errors.New("chain: 输入格式无效")
	}
	if spec.OutputBits != 0 && spec.OutputBits != 16 && spec.OutputBits != 24 {
		return nil, errors.New("chain: 定点出口只支持 16 或 24 位")
	}
	stages := append([]Stage(nil), spec.Stages...)
	stages = append(stages, spec.User...)
	stages = append(stages, spec.Tail...)
	format := spec.Format
	var nodes []Node
	if spec.Headroom.Enabled {
		h, err := NewHeadroom(spec.Headroom.DB)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, h)
	}
	modifies := spec.Headroom.Enabled
	var duration big.Rat
	for _, stage := range stages {
		if stage.Node == nil || !valid(stage.In) || !valid(stage.Out) || stage.In != format {
			return nil, errors.New("chain: 节点格式不连续或无效")
		}
		if node, ok := stage.Node.(interface{ Latency() int }); ok {
			latency := node.Latency()
			if latency < 0 {
				return nil, errors.New("chain: 节点延迟不能为负数")
			}
			duration.Add(&duration, new(big.Rat).SetFrac64(int64(latency), int64(stage.Out.SampleRate)))
		}
		nodes = append(nodes, stage.Node)
		format = stage.Out
		modifies = modifies || stage.Modifies
	}
	duration.Mul(&duration, new(big.Rat).SetInt64(int64(format.SampleRate)))
	var latency, remainder big.Int
	latency.QuoRem(duration.Num(), duration.Denom(), &remainder)
	if remainder.Sign() > 0 {
		latency.Add(&latency, big.NewInt(1))
	}
	c := New(nodes...)
	c.input, c.output, c.stages = spec.Format, format, stages
	c.latency, c.modifies = int(latency.Int64()), modifies
	if spec.OutputBits != 0 {
		c.bits = spec.OutputBits
		c.Quantizer = dither.NewQuantizer(c.bits, spec.Seed)
	}
	return c, nil
}

func (c *Chain) InputFormat() Format   { return c.input }
func (c *Chain) OutputFormat() Format  { return c.output }
func (c *Chain) Latency() int          { return c.latency }
func (c *Chain) ModifiesSamples() bool { return c.modifies }

// OutputFrame 逐级换算绝对边界，不累加逐块舍入误差。
func (c *Chain) OutputFrame(frame int64) int64 {
	for _, stage := range c.stages {
		frame = scaleFrames(frame, stage.In.SampleRate, stage.Out.SampleRate)
	}
	return frame
}

func (c *Chain) Process(b *Buffer) *Buffer {
	if c.draining {
		panic("chain: 冲刷开始后不能再输入")
	}
	requireApplied(b)
	for _, node := range c.nodes {
		b = node.Process(b)
	}
	return b
}

// Drain 依次冲刷每个节点，并把尾块继续送入下游；返回 nil 表示结束。
// 调用方应循环读取并及时量化，不能保留跨调用的缓冲。
func (c *Chain) Drain() *Buffer {
	c.draining = true
	for c.drainNode < len(c.nodes) {
		node, ok := c.nodes[c.drainNode].(interface{ Drain() *Buffer })
		if !ok {
			c.drainNode++
			continue
		}
		b := node.Drain()
		if b == nil {
			c.drainNode++
			continue
		}
		for _, downstream := range c.nodes[c.drainNode+1:] {
			b = downstream.Process(b)
		}
		if b.Frames > 0 {
			return b
		}
	}
	return nil
}

// Steps 返回构造时确定的只读路径；逐曲目的电平化和抖动步骤由渲染会话添加。
func (c *Chain) Steps() []SignalStep { return c.steps }

func (c *Chain) Quantize(dst []byte, b *Buffer) ([]byte, int) {
	requireApplied(b)
	if c.Quantizer == nil {
		panic("chain: 此链没有定点量化出口")
	}
	passthrough := b.Exact && b.SourceBits > 0 && b.SourceBits <= c.bits
	return c.Quantizer.Quantize(dst, b.Samples, b.Frames, passthrough)
}

func (c *Chain) Reset() {
	for _, node := range c.nodes {
		node.Reset()
	}
	if c.Quantizer != nil {
		c.Quantizer.Reset()
	}
	c.draining, c.drainNode = false, 0
}

func requireApplied(b *Buffer) {
	if len(b.Regions) != 0 {
		panic("chain: 进入共享链前必须施加并清空增益区域")
	}
}
