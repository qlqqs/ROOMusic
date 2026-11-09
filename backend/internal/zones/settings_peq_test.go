package zones

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPEQSettingsSameValueDoesNotRebuild(t *testing.T) {
	const canonical = `{"enabled":true,"preamp_db":0,"bands":[{"type":"peaking","freq_hz":1000,"gain_db":0,"q":1,"channels":[0,1],"enabled":true},{"type":"high_pass","freq_hz":20,"slope_db":12,"channels":"all","enabled":true}]}`
	document, err := ParsePEQDocument([]byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	settings := applySettings(defaultSettings(), SettingsPatch{PEQ: &document})
	s := NewService(nil, Options{})
	defer s.Close()
	z := &zoneRuntime{persisted: PersistedZone{Settings: settings}, session: settingsPlanSession{}}
	for _, tc := range []struct {
		name  string
		patch string
		noop  bool
	}{
		{"同值", `{"peq":` + canonical + `}`, true},
		{"键序空白数值与声道集合规范化", ` { "peq": { "bands": [
			{"enabled":true,"channels":[1.0,0e0,1],"q":1e0,"gain_db":-0.0,"freq_hz":1e3,"type":"peaking"},
			{"channels":"all","enabled":true,"slope_db":12.0,"freq_hz":20.0,"type":"high_pass"}
		], "preamp_db":-0.0, "enabled":true } } `, true},
		{"省略", `{}`, true},
		{"实际变更", `{"peq":{"enabled":false,"preamp_db":0,"bands":[]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var patch SettingsPatch
			if err := json.Unmarshal([]byte(tc.patch), &patch); err != nil {
				t.Fatal(err)
			}
			if got := EqualSettings(settings, applySettings(settings, patch)); got != tc.noop {
				t.Fatalf("规范设置等值=%v，期望=%v", got, tc.noop)
			}
			command := Command{Command: "set_settings", Settings: patch}
			if err := validateCommand(command); err != nil {
				t.Fatal(err)
			}
			plan, err := s.plan(context.Background(), "user", z, command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if plan.noop != tc.noop || plan.debounce == tc.noop || plan.open {
				t.Fatalf("设置规划 noop=%v debounce=%v open=%v", plan.noop, plan.debounce, plan.open)
			}
		})
	}
}

func TestPEQSettingsDTOIndependentDecode(t *testing.T) {
	gain, q, slope := 6.0, 1.0, 12
	original := PEQSettings{
		Enabled: true, PreampDB: -3,
		Bands: []PEQBand{
			{Type: "peaking", FreqHz: 1000, GainDB: &gain, Q: &q, Channels: PEQChannels{Indices: []int{1, 0, 1}}, Enabled: true},
			{Type: "high_pass", FreqHz: 20, SlopeDB: &slope, Channels: PEQChannels{All: true}, Enabled: true},
		},
		Source: &PEQSource{Kind: "preset", PresetID: "00000000-0000-4000-8000-000000000001", Name: "试听均衡"},
	}
	document, err := NewPEQDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.Bands[0].Channels.Indices, []int{1, 0, 1}) {
		t.Fatal("规范化修改了调用方的声道切片")
	}
	original.Bands[0].FreqHz = 2000
	original.Bands[0].Channels.Indices[0] = 9
	gain, q, slope = -6, 2, 24
	original.Source.Name = "已修改"

	settings := applySettings(defaultSettings(), SettingsPatch{PEQ: &document})
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var restored PlaybackSettings
	if err := json.Unmarshal(encoded, &restored); err != nil || !EqualSettings(restored, settings) {
		t.Fatalf("DTO 往返改变设置：%v", err)
	}
	a, err := DecodeSettingsDocument[PEQSettings](restored.PEQ)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeSettingsDocument[PEQSettings](restored.PEQ)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal := func(got PEQSettings) {
		t.Helper()
		if len(got.Bands) != 2 || got.Source == nil || got.Source.Name != "试听均衡" {
			t.Fatalf("文档仍引用可变段或来源：%+v", got)
		}
		band := got.Bands[0]
		if band.FreqHz != 1000 || band.GainDB == nil || *band.GainDB != 6 || band.Q == nil || *band.Q != 1 ||
			!reflect.DeepEqual(band.Channels.Indices, []int{0, 1}) || got.Bands[1].SlopeDB == nil || *got.Bands[1].SlopeDB != 12 {
			t.Fatalf("段、参数指针或声道切片被共享：%+v", got.Bands)
		}
	}
	assertOriginal(a)
	assertOriginal(b)
	a.Bands[0].FreqHz = 3000
	*a.Bands[0].GainDB = 12
	*a.Bands[0].Q = 3
	a.Bands[0].Channels.Indices[0] = 8
	*a.Bands[1].SlopeDB = 36
	a.Source.Name = "解码后修改"
	assertOriginal(b)
	fresh, err := DecodeSettingsDocument[PEQSettings](settings.PEQ)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(fresh)
}
