//go:build refcompare

package dsd

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/refdata"
)

func TestRefCompareFIR(t *testing.T) {
	file, err := os.Open(filepath.Join(refdata.Dir(t), "roomusic-dsd2pcm-fir48.csv"))
	if os.IsNotExist(err) {
		t.Skip("本机 DSD FIR 参考文件缺失，跳过对照")
	}
	if err != nil {
		t.Fatal("读取本机 DSD FIR 参考文件失败")
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = 2
	rows, err := reader.ReadAll()
	// 不打印解析错误或 CSV 行，以免把参考系数带入测试输出。
	if err != nil || len(rows) != 49 || rows[0][0] != "index" || rows[0][1] != "coefficient" {
		t.Fatal("参考 FIR CSV 格式不符")
	}
	var referenceHalf [48]float64
	var seen [48]bool
	for _, row := range rows[1:] {
		index, err := strconv.Atoi(row[0])
		if err != nil || index < 0 || index >= len(referenceHalf) || seen[index] {
			t.Fatal("参考 FIR 索引不符")
		}
		coefficient, err := strconv.ParseFloat(row[1], 64)
		if err != nil || math.IsNaN(coefficient) || math.IsInf(coefficient, 0) {
			t.Fatal("参考 FIR 系数格式不符")
		}
		referenceHalf[index], seen[index] = coefficient, true
	}

	// CSV 从中心向外；自主 firHalf 是 h[0:48]，从外向中心，先统一方向。
	var ourCenterFirst [48]float64
	for i := range firHalf {
		ourCenterFirst[i] = firHalf[len(firHalf)-1-i]
	}
	ours := measureFIR(refCompareNormalizedFIR(t, ourCenterFirst))
	reference := measureFIR(refCompareNormalizedFIR(t, referenceHalf))
	for _, metric := range []struct {
		name                    string
		ours, reference, margin float64
	}{
		{"0–20 kHz 波动", ours.ripple20, reference.ripple20, reference.ripple20 - ours.ripple20},
		{"≥176.4 kHz 最差衰减", ours.stop176, reference.stop176, ours.stop176 - reference.stop176},
		{"≥328.8 kHz 最差衰减", ours.stop328, reference.stop328, ours.stop328 - reference.stop328},
	} {
		t.Logf("%s：自主 %.9f dB，参考 %.9f dB，余量 %.9f dB", metric.name, metric.ours, metric.reference, metric.margin)
		if !(metric.margin >= 0) || math.IsInf(metric.margin, 0) {
			t.Errorf("%s：余量 %.9f dB", metric.name, metric.margin)
		}
	}
}

// 输入均以中心抽头开头，完整核为 reverse(half) 接 half，再按直流归一。
func refCompareNormalizedFIR(t *testing.T, half [48]float64) []float64 {
	t.Helper()
	h := make([]float64, 96)
	var sum float64
	for i, v := range half {
		h[47-i], h[48+i] = v, v
	}
	for _, v := range h {
		sum += v
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		t.Fatal("FIR 直流无法归一化")
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}
