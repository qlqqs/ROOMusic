package chain

import (
	"errors"
	"math"
)

const DefaultHeadroomDB = -3.0

// HeadroomSettings 的零值为关闭；默认设置应显式采用 DefaultHeadroomDB。
// 削波指示属于渲染会话，不影响浮点节点的装配。
type HeadroomSettings struct {
	Enabled bool
	DB      float64
}

// Headroom 在处理链头施加固定增益；即使为 0 dB，启用也会清除 Exact。
type Headroom struct {
	gain float64
	step SignalStep
}

func NewHeadroom(db float64) (*Headroom, error) {
	if math.IsNaN(db) || math.IsInf(db, 0) || db < -30 || db > 0 || math.Abs(db*10-math.Round(db*10)) > 1e-9 {
		return nil, errors.New("chain: Headroom 必须在 -30 到 0 dB 之间，步进为 0.1 dB")
	}
	return &Headroom{
		gain: math.Pow(10, db/20),
		step: SignalStep{Kind: "headroom", Quality: "enhanced", Params: map[string]any{"gain_db": db}},
	}, nil
}

func (h *Headroom) Process(b *Buffer) *Buffer {
	for _, samples := range b.Samples {
		for i := range samples[:b.Frames] {
			samples[i] *= h.gain
		}
	}
	b.Exact = false
	return b
}

func (h *Headroom) Reset()           {}
func (h *Headroom) Step() SignalStep { return h.step }
