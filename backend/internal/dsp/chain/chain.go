package chain

import (
	"errors"

	"github.com/qlqq/roomusic/backend/internal/dsp/dither"
)

// Spec 描述第一批的共享处理链；格式适配已经在上游完成。
type Spec struct {
	Format     Format
	Headroom   HeadroomSettings
	OutputBits int // 16/24 为定点出口，0 为直接向有损编码器交付浮点。
	Seed       uint32
}

// Chain 由渲染循环独占，跨曲目不复位。Quantizer 为 nil 时没有定点出口。
// 使用 Quantize 而非直接调用 Quantizer，可统一按 Buffer 判定直通。
type Chain struct {
	Quantizer *dither.Quantizer
	nodes     []Node
	steps     []SignalStep
	bits      int
	draining  bool
	drainNode int
}

// New 组装显式节点，节点顺序在构造后不变；输入必须已施加电平化。
// 第一批的渲染会话应使用 Build，以保证 Headroom 与量化出口的固定顺序。
func New(nodes ...Node) *Chain {
	c := &Chain{nodes: append([]Node(nil), nodes...)}
	for _, node := range c.nodes {
		if step := node.Step(); step.Kind != "" {
			c.steps = append(c.steps, step)
		}
	}
	return c
}

// Build 第一批只装 Headroom 和定点量化出口，不引入尚未实现的节点。
func Build(spec Spec) (*Chain, error) {
	if spec.Format.SampleRate <= 0 || spec.Format.Channels <= 0 {
		return nil, errors.New("chain: 采样率和声道数必须为正数")
	}
	if spec.OutputBits != 0 && spec.OutputBits != 16 && spec.OutputBits != 24 {
		return nil, errors.New("chain: 定点出口只支持 16 或 24 位")
	}
	c := New()
	if spec.Headroom.Enabled {
		h, err := NewHeadroom(spec.Headroom.DB)
		if err != nil {
			return nil, err
		}
		c = New(h)
	}
	if spec.OutputBits != 0 {
		c.bits = spec.OutputBits
		c.Quantizer = dither.NewQuantizer(c.bits, spec.Seed)
	}
	return c, nil
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
