package opra_test

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
)

func TestParseTextRecognizedAndUnrecognizedLines(t *testing.T) {
	// 合成 AutoEq 格式的十段参数；空白、注释和 OFF 不计入未识别行。
	const text = `Preamp: -6.2 dB
Filter 1: ON PK Fc 105 Hz Gain 6.5 dB Q 0.70
Filter 2: ON PK Fc 200 Hz Gain -2.0 dB Q 1.00
Filter 3: ON PK Fc 400 Hz Gain 1.5 dB Q 1.10
Filter 4: ON PK Fc 800 Hz Gain -3.0 dB Q 1.20
Filter 5: ON PK Fc 1000 Hz Gain 2.0 dB Q 1.30
Filter 6: ON PK Fc 2000 Hz Gain -1.0 dB Q 1.40
Filter 7: ON PK Fc 4000 Hz Gain 3.0 dB Q 1.50
Filter 8: ON PK Fc 6000 Hz Gain -4.0 dB Q 1.60
Filter 9: ON PK Fc 8000 Hz Gain 1.0 dB Q 1.70
filter 10: on pk Fc 10000 Hz Gain -2.5 dB Q 1.80

# 参数说明
Filter 11: OFF PK Fc 12000 Hz Gain 1 dB Q 1
Channel: L
Filter 12: ON PK Fc invalid Hz Gain 1 dB Q 1`
	result, err := opra.ParseText(text)
	if err != nil {
		t.Fatal(err)
	}
	wantBands := []opra.Band{
		{Type: "peaking", FreqHz: 105, GainDB: 6.5, Q: 0.7},
		{Type: "peaking", FreqHz: 200, GainDB: -2, Q: 1},
		{Type: "peaking", FreqHz: 400, GainDB: 1.5, Q: 1.1},
		{Type: "peaking", FreqHz: 800, GainDB: -3, Q: 1.2},
		{Type: "peaking", FreqHz: 1000, GainDB: 2, Q: 1.3},
		{Type: "peaking", FreqHz: 2000, GainDB: -1, Q: 1.4},
		{Type: "peaking", FreqHz: 4000, GainDB: 3, Q: 1.5},
		{Type: "peaking", FreqHz: 6000, GainDB: -4, Q: 1.6},
		{Type: "peaking", FreqHz: 8000, GainDB: 1, Q: 1.7},
		{Type: "peaking", FreqHz: 10000, GainDB: -2.5, Q: 1.8},
	}
	if result.PreampDB != -6.2 || !reflect.DeepEqual(result.Bands, wantBands) {
		t.Errorf("识别结果得到 preamp=%g、%d 段 %+v，期望 preamp=-6.2、10 段 %+v", result.PreampDB, len(result.Bands), result.Bands, wantBands)
	}
	if want := []int{15, 16}; !reflect.DeepEqual(result.UnrecognizedLines, want) {
		t.Errorf("未识别行得到 %v，期望 %v（共 2 行）", result.UnrecognizedLines, want)
	}
}
