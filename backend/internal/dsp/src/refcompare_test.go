//go:build refcompare

package src

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/refdata"
)

func TestRefCompareKernels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(refdata.Dir(t), "roomusic-src-kernels.csv"))
	if os.IsNotExist(err) {
		t.Skip("本机参考核文件缺失，跳过对照")
	}
	if err != nil {
		t.Fatal("读取本机参考核文件失败")
	}
	const digest = "22fb34a3fdb781c5a71b1bc7ba5ce0207475f984e2f8b2f00c1af4b015612150"
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		t.Fatal("参考核文件 SHA-256 与 PRD C3a 不符")
	}
	reader := csv.NewReader(bytes.NewReader(data))
	header, err := reader.Read()
	if err != nil || !reflect.DeepEqual(header, []string{"name", "family", "up", "down", "n", "taps_per_phase", "sum", "group_delay_input_samples", "coefs_semicolon"}) {
		t.Fatal("参考 CSV 表头不符")
	}
	type familySpec struct {
		phase       Phase
		quality     Quality
		taps, count int
	}
	families := map[string]familySpec{
		"linear_phase_smooth":   {Linear, Smooth, 20, 16},
		"minimum_phase_smooth":  {Minimum, Smooth, 20, 16},
		"linear_phase_precise":  {Linear, Precise, 166, 16},
		"minimum_phase_precise": {Minimum, Precise, 166, 16},
		"linear_phase_2s":       {Linear, Stage2, 19, 6},
		"minimum_phase_2s":      {Minimum, Stage2, 19, 6},
	}
	// 用设计目录核对完整性，不从被测实现的 catalog 推导期望集合。
	ratios := [][2]int{{2, 1}, {3, 1}, {4, 1}, {6, 1}, {8, 1}, {16, 1}, {32, 1}, {64, 1}, {3, 2}, {160, 147}, {320, 147}, {640, 147}, {1280, 147}, {147, 20}, {147, 40}, {147, 80}}
	expected := make(map[string]bool)
	for family, spec := range families {
		for _, r := range ratios {
			if spec.quality == Stage2 && (r[1] != 1 || (r[0] != 2 && r[0] != 4 && r[0] != 8 && r[0] != 16 && r[0] != 32 && r[0] != 64)) {
				continue
			}
			expected[fmt.Sprintf("%s/%d_%d", family, r[0], r[1])] = true
		}
	}
	type summary struct {
		count, passed                     int
		passMargin, stopMargin, delayDiff float64
	}
	summaries := make(map[string]*summary)
	for family := range families {
		summaries[family] = &summary{passMargin: math.Inf(-1), stopMargin: math.Inf(1)}
	}
	total := 0
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("参考 CSV 行解析失败")
		}
		spec, ok := families[row[1]]
		if !ok {
			t.Fatal("参考 CSV 存在未知相位/质量族")
		}
		l, m := referenceInt(t, row[2]), referenceInt(t, row[3])
		key := fmt.Sprintf("%s/%d_%d", row[1], l, m)
		if !expected[key] {
			t.Fatalf("目录项重复或超出设计：%s", key)
		}
		delete(expected, key)
		total++
		summary := summaries[row[1]]
		summary.count++
		if t.Run(key, func(t *testing.T) {
			n := referenceInt(t, row[4])
			if n != l*spec.taps+1 || referenceInt(t, row[5]) != spec.taps {
				t.Fatal("参考核长度/每相抽头与设计不符")
			}
			fields := strings.Split(row[8], ";")
			if len(fields) != n {
				t.Fatal("参考核系数数量与元数据不符")
			}
			reference := make([]float64, n)
			for i, field := range fields {
				v, err := strconv.ParseFloat(field, 64)
				// 不回显解析错误，strconv 错误文本会带原始系数。
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("参考核抽头 %d 不是有限数", i)
				}
				reference[i] = v
			}
			ours := Kernel(spec.phase, spec.quality, l, m)
			if len(ours) != n {
				t.Fatalf("核长 %d，参考 %d", len(ours), n)
			}
			a, b := measureKernel(t, ours, spec.quality, l, m), measureKernel(t, reference, spec.quality, l, m)
			// 原始参考核允许存在微小 DC 偏移；§8.2 按各自 DC 归一后比较频响。
			if math.Abs(a.dc-1) > 1e-12 {
				t.Errorf("本实现直流和 = %.15g，期望 1", a.dc)
			}
			t.Logf("直流和：本实现 %.15g / 参考 %.15g", a.dc, b.dc)
			passMargin, stopMargin, delayDiff := a.passDB-b.passDB, a.stopDB-b.stopDB, math.Abs(a.delay-b.delay)
			summary.passMargin = max(summary.passMargin, passMargin)
			summary.stopMargin = min(summary.stopMargin, stopMargin)
			summary.delayDiff = max(summary.delayDiff, delayDiff)
			t.Logf("通带偏差：本实现 %.8f / 参考 %.8f dB，差 %+.8f，门限余量 %.8f dB；阻带衰减：本实现 %.4f / 参考 %.4f dB，差 %+.4f，门限余量 %.4f dB", a.passDB, b.passDB, passMargin, 1e-4-passMargin, a.stopDB, b.stopDB, stopMargin, stopMargin+0.5)
			t.Logf("低频群延迟：本实现 %.8f / 参考 %.8f 输入样本，差 %.8f；−6 dB 点 %.6f / %.6f；过渡带（−3 dB 至持续阻带起点）[%.6f, %.6f] / [%.6f, %.6f]", a.delay, b.delay, delayDiff, a.minus6, b.minus6, a.minus3, a.stopStart, b.minus3, b.stopStart)
			if passMargin > 1e-4 {
				t.Errorf("通带偏差劣于参考 %.8f dB，允许 0.0001 dB", passMargin)
			}
			if stopMargin < -0.5 {
				t.Errorf("阻带余量 %.4f dB < −0.5 dB", stopMargin)
			}
			if spec.phase == Linear {
				assertLinearKernel(t, ours, l, a.delay)
				assertLinearKernel(t, reference, l, b.delay)
			} else {
				if delayDiff > 0.5 {
					t.Errorf("最小相位群延迟差 %.8f > 0.5 输入样本", delayDiff)
				}
				if a.delay <= 0 || a.delay >= float64(n-1)/(2*float64(l)) {
					t.Errorf("最小相位低频群延迟未前移：%.8f", a.delay)
				}
			}
		}) {
			summary.passed++
		}
	}
	if total != 76 || len(expected) != 0 {
		t.Errorf("对照 %d 组，尚缺 %d 组，期望完整 76 组", total, len(expected))
	}
	for _, family := range []string{"linear_phase_smooth", "minimum_phase_smooth", "linear_phase_precise", "minimum_phase_precise", "linear_phase_2s", "minimum_phase_2s"} {
		s := summaries[family]
		if s.count != families[family].count {
			t.Errorf("%s 组数 %d，期望 %d", family, s.count, families[family].count)
		}
		t.Logf("族汇总 %s：通过 %d/%d，通带最差差值 %+.8f dB（门限余量 %.8f），阻带最差差值 %+.4f dB（门限余量 %.4f），最大群延迟差 %.8f 输入样本", family, s.passed, s.count, s.passMargin, 1e-4-s.passMargin, s.stopMargin, s.stopMargin+0.5, s.delayDiff)
	}
}

func referenceInt(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		t.Fatal("参考 CSV 整数元数据非法")
	}
	return v
}
