package render

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
)

// IRSource 只提供当前成员的只读 IR 快照；资源解码与缓存归装配层。
type IRSource interface {
	Load(ctx context.Context, userID, irID string, format chain.Format) (LoadedIR, error)
}

type LoadedIR struct {
	Name          string
	ResampledFrom int
	Config        convolution.Config
}

// userFormat 是 SRC、矩阵降混之后，Tail 声道选择之前的真实格式。
func (p renderPlan) userFormat() chain.Format {
	format := p.sourcePCM
	if p.sampleRate.To > 0 {
		format.SampleRate = p.sampleRate.To
	}
	if len(p.stages) > 0 {
		format = p.stages[len(p.stages)-1].Out
	}
	return format
}

func buildConvolution(ir LoadedIR, format chain.Format) (chain.Stage, error) {
	conv, err := convolution.New(ir.Config)
	if err != nil {
		return chain.Stage{}, err
	}
	taps := 0
	for _, path := range ir.Config.Paths {
		taps = max(taps, len(path.IR))
	}
	params := map[string]any{"name": ir.Name, "taps": taps}
	if ir.ResampledFrom != 0 {
		params["resampled_from"] = ir.ResampledFrom
	}
	step := chain.SignalStep{Kind: "convolution", Quality: "enhanced", Params: params}
	return chain.Stage{In: format, Out: format, Modifies: true,
		Node: chain.Wrap(conv, format, format, step, true)}, nil
}

func (s *session) loadConvolution(plan *renderPlan) error {
	settings := plan.convolution
	if !settings.Enabled || settings.IRID == nil {
		return nil
	}
	var err error
	if s.owner.options.IRs == nil {
		err = ErrUnavailable
	} else {
		var ir LoadedIR
		ir, err = s.owner.options.IRs.Load(s.ctx, s.spec.UserID, *settings.IRID, plan.userFormat())
		if err == nil && s.ctx.Err() == nil {
			var stage chain.Stage
			stage, err = buildConvolution(ir, plan.userFormat())
			if err == nil {
				plan.convolutionStage = &stage
			}
		}
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if err != nil {
		// 不记录名称、原文件、cfg、进程诊断或底层错误；失败只省略本节点。
		s.owner.options.Logger.Warn("卷积暂时用不了，已跳过", "event", "playback.convolution.unavailable", "module", "playback")
	}
	return nil
}
