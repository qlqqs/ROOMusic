// Package irload 解析卷积 IR 的 cfg、zip 与裸浮点数据，并将已解码 IR 重采样、匹配到流声道。
// Loader 负责有界音频解码；存储与渲染接线由调用方负责。
package irload

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CFGError 保留从一开始的物理行号；服务边界可映射为 ir_cfg_invalid。
// Message 只描述格式问题，不包含原始配置或文件路径。
type CFGError struct {
	Line    int
	Message string
}

func (e *CFGError) Error() string {
	return fmt.Sprintf("ir_cfg_invalid: line %d: %s", e.Line, e.Message)
}

// Weighted 是 cfg 中从零编号的声道及已解析的线性权重。
type Weighted struct {
	Channel int
	Weight  float64
}

// CFGPath 保留文件内声道与原始路由；File 相对 cfg 所在目录。
type CFGPath struct {
	File            string
	Channel         int
	Inputs, Outputs []Weighted
	Line            int
}

// CFG 的延时以毫秒保存，在目标流采样率确定后才换成样本。
// OutputMask 仅描述布局，不能覆盖路径块中的路由。
type CFG struct {
	SampleRate                    int
	InputChannels, OutputChannels int
	OutputMask                    uint32
	InDelayMS, OutDelayMS         []float64
	Paths                         []CFGPath
}

type cfgReader struct {
	lines  []string
	index  int
	fields []string
	line   int
}

func (r *cfgReader) nextLine() (string, int, bool) {
	for r.index < len(r.lines) {
		r.line = r.index + 1
		line := strings.TrimSpace(r.lines[r.index])
		r.index++
		if line != "" {
			return line, r.line, true
		}
	}
	return "", len(r.lines) + 1, false
}

func (r *cfgReader) token() (string, int, error) {
	if len(r.fields) == 0 {
		line, number, ok := r.nextLine()
		if !ok {
			return "", number, cfgError(number, "incomplete header or delays")
		}
		r.fields = strings.Fields(line)
	}
	value := r.fields[0]
	r.fields = r.fields[1:]
	return value, r.line, nil
}

func cfgError(line int, message string) error {
	return &CFGError{Line: line, Message: message}
}

// ParseCFG 按记号流读四个头字段与全部延时，再按行读每组四行路径。
// 支持头字段分行、合行及输入/输出延时合行；不支持文件列表模式。
func ParseCFG(data []byte) (*CFG, error) {
	r := &cfgReader{}
	if len(data) != 0 {
		// 末尾换行结束当前物理行，不额外制造一行。
		r.lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	c := &CFG{}
	for i, dest := range []*int{&c.SampleRate, &c.InputChannels, &c.OutputChannels} {
		token, line, err := r.token()
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(token)
		if err != nil || n <= 0 || (i > 0 && n > 8) {
			return nil, cfgError(line, "invalid sample rate or channel count")
		}
		*dest = n
	}
	token, line, err := r.token()
	if err != nil {
		return nil, err
	}
	mask, err := strconv.ParseUint(token, 16, 32)
	if err != nil {
		return nil, cfgError(line, "invalid hexadecimal channel mask")
	}
	c.OutputMask = uint32(mask)
	c.InDelayMS, err = r.delays(c.InputChannels)
	if err != nil {
		return nil, err
	}
	c.OutDelayMS, err = r.delays(c.OutputChannels)
	if err != nil {
		return nil, err
	}
	if len(r.fields) != 0 {
		return nil, cfgError(r.line, "path filename must start on a new line")
	}
	for {
		file, line, ok := r.nextLine()
		if !ok {
			break
		}
		if len(c.Paths) == 32 {
			return nil, cfgError(line, "too many paths")
		}
		p := CFGPath{File: file, Line: line}
		channel, number, ok := r.nextLine()
		if !ok {
			return nil, cfgError(number, "missing file channel")
		}
		p.Channel, err = strconv.Atoi(channel)
		if err != nil || p.Channel < 0 || p.Channel >= 8 {
			return nil, cfgError(number, "invalid file channel")
		}
		p.Inputs, err = r.weights(c.InputChannels)
		if err != nil {
			return nil, err
		}
		p.Outputs, err = r.weights(c.OutputChannels)
		if err != nil {
			return nil, err
		}
		c.Paths = append(c.Paths, p)
	}
	if len(c.Paths) == 0 {
		return nil, cfgError(len(r.lines)+1, "missing paths")
	}
	return c, nil
}

func (r *cfgReader) delays(channels int) ([]float64, error) {
	delays := make([]float64, channels)
	for i := range delays {
		token, line, err := r.token()
		if err != nil {
			return nil, err
		}
		v, err := strconv.ParseFloat(token, 64)
		if err != nil || !finite(v) || v < 0 {
			return nil, cfgError(line, "invalid delay")
		}
		delays[i] = v
	}
	return delays, nil
}

func (r *cfgReader) weights(channels int) ([]Weighted, error) {
	text, line, ok := r.nextLine()
	if !ok {
		return nil, cfgError(line, "missing routing weights")
	}
	var weights []Weighted
	for _, token := range strings.Fields(text) {
		sign := 1.0
		if strings.HasPrefix(token, "-") {
			sign, token = -1, token[1:]
		}
		channel, fraction, found := strings.Cut(token, ".")
		if !found || !digits(channel) || !digits(fraction) {
			return nil, cfgError(line, "invalid routing weight")
		}
		ch, err := strconv.Atoi(channel)
		if err != nil || ch >= channels {
			return nil, cfgError(line, "routing channel out of range")
		}
		weight, err := strconv.ParseFloat("0."+fraction, 64)
		if err != nil {
			return nil, cfgError(line, "invalid routing weight")
		}
		if strings.Trim(fraction, "0") == "" {
			weight = 1
		}
		weights = append(weights, Weighted{Channel: ch, Weight: sign * weight})
	}
	return weights, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// DelaySamples 将毫秒延时按目标流率向下取整，不使用 cfg 的源采样率。
func DelaySamples(milliseconds float64, sampleRate int) (int, error) {
	if sampleRate <= 0 || !finite(milliseconds) || milliseconds < 0 {
		return 0, fmt.Errorf("irload: invalid delay or sample rate")
	}
	n := math.Floor(milliseconds * float64(sampleRate) / 1000)
	if !finite(n) || n >= float64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("irload: delay exceeds sample count range")
	}
	return int(n), nil
}
