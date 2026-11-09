package zones

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestSampleRateDocumentCanonicalAndShape(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raws      []string
		canonical string
	}{
		{"键序空白数值", []string{`{"strategy":"custom","custom_rate":48000,"filter":"linear_precise"}`, ` { "filter":"linear_precise", "custom_rate":48000.0, "strategy":"custom" } `, `{"custom_rate":48e3,"strategy":"custom","filter":"linear_precise"}`}, `{"strategy":"custom","custom_rate":48000,"filter":"linear_precise"}`},
		{"空目标规范化", []string{`{"strategy":"custom","custom_rate":null,"filter":"linear_precise"}`, `{"strategy":"none","custom_rate":null,"filter":"linear_precise"}`}, `{"strategy":"none","custom_rate":null,"filter":"linear_precise"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first SettingsDocument
			for i, raw := range tc.raws {
				d, err := ParseSampleRateDocument([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = d
				} else if d != first {
					t.Fatal("等价文档不相等")
				}
				got, err := json.Marshal(d)
				if err != nil || string(got) != tc.canonical {
					t.Fatalf("规范 JSON=%s，错误=%v", got, err)
				}
				var patch SettingsPatch
				if err := json.Unmarshal([]byte(`{"sample_rate":`+raw+`}`), &patch); err != nil || patch.SampleRate == nil || *patch.SampleRate != d {
					t.Fatalf("设置补丁=%+v，错误=%v", patch, err)
				}
			}
		})
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"strategy":"none","filter":"linear_precise"}`,
		`{"strategy":null,"custom_rate":null,"filter":"linear_precise"}`,
		`{"strategy":"none","custom_rate":null,"filter":null}`,
		`{"strategy":"none","custom_rate":null,"filter":"linear_precise","extra":1}`,
	} {
		if _, err := ParseSampleRateDocument([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("未拒绝不完整对象 %s：%v", raw, err)
		}
		if err := ValidateSettingsShape([]byte(`{"sample_rate":` + raw + `}`)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("HTTP 形状未拒绝 %s：%v", raw, err)
		}
	}
	if err := ValidateSettingsShape([]byte(`{"sample_rate":{"strategy":"none","custom_rate":null,"filter":"linear_precise"}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestSampleRateDocumentValues(t *testing.T) {
	rate := 48000
	for _, strategy := range []string{"none", "max_sample_rate", "power_of_2", "dsd", "custom"} {
		if _, err := NewSampleRateDocument(SampleRateSettings{Strategy: strategy, CustomRate: &rate, Filter: "linear_precise"}); err != nil {
			t.Fatalf("合法策略 %s：%v", strategy, err)
		}
	}
	for _, filter := range []string{"linear_precise", "minimum_precise", "linear_smooth", "minimum_smooth"} {
		if _, err := NewSampleRateDocument(SampleRateSettings{Strategy: "custom", CustomRate: &rate, Filter: filter}); err != nil {
			t.Fatalf("合法滤波器 %s：%v", filter, err)
		}
	}
	for _, rate := range []int{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000} {
		if _, err := NewSampleRateDocument(SampleRateSettings{Strategy: "custom", CustomRate: &rate, Filter: "linear_precise"}); err != nil {
			t.Fatalf("合法采样率 %d：%v", rate, err)
		}
	}
	for _, raw := range []string{
		`{"strategy":"unknown","custom_rate":null,"filter":"linear_precise"}`,
		`{"strategy":"none","custom_rate":null,"filter":"linear_2s"}`,
		`{"strategy":true,"custom_rate":null,"filter":"linear_precise"}`,
		`{"strategy":"none","custom_rate":null,"filter":3}`,
	} {
		if _, err := ParseSampleRateDocument([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("未拒绝非法枚举或类型：%s，%v", raw, err)
		}
	}
	for _, rate := range []string{`8000`, `48000.1`, `"48000"`} {
		raw := fmt.Sprintf(`{"strategy":"custom","custom_rate":%s,"filter":"linear_precise"}`, rate)
		if _, err := ParseSampleRateDocument([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("未拒绝非法目标率：%s，%v", rate, err)
		}
	}
	if err := ValidateSettingsPatch(SettingsPatch{SampleRate: &SettingsDocument{}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("补丁接受未构造文档")
	}
	headroom, err := NewHeadroomDocument(defaultHeadroom())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSettingsPatch(SettingsPatch{SampleRate: &headroom}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("补丁接受其他设置文档")
	}
}

// 命令规划只检查会话是否存在，不调用渲染方法。
type settingsPlanSession struct{ RenderSession }

func TestSampleRateSettingsSameValueDoesNotRebuild(t *testing.T) {
	rateA, rateB := 96000, 96000
	a, err := NewSampleRateDocument(SampleRateSettings{Strategy: "custom", CustomRate: &rateA, Filter: "minimum_precise"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSampleRateDocument(SampleRateSettings{Strategy: "custom", CustomRate: &rateB, Filter: "minimum_precise"})
	if err != nil {
		t.Fatal(err)
	}
	rateA = 48000
	decodedA, err := DecodeSettingsDocument[SampleRateSettings](a)
	if err != nil {
		t.Fatal(err)
	}
	decodedB, err := DecodeSettingsDocument[SampleRateSettings](a)
	if err != nil || *decodedA.CustomRate != 96000 || *decodedB.CustomRate != 96000 {
		t.Fatalf("文档仍引用构造参数：%+v，%v", decodedA, err)
	}
	*decodedA.CustomRate = 44100
	if *decodedB.CustomRate != 96000 {
		t.Fatal("两次解码共享指针")
	}
	settings := applySettings(defaultSettings(), SettingsPatch{SampleRate: &a})
	if !EqualSettings(settings, applySettings(settings, SettingsPatch{SampleRate: &b})) || !EqualSettings(settings, applySettings(settings, SettingsPatch{})) {
		t.Fatal("同值补丁或省略键改变了设置")
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var restored PlaybackSettings
	if err := json.Unmarshal(encoded, &restored); err != nil || !EqualSettings(restored, settings) {
		t.Fatalf("DTO 往返改变设置：%v", err)
	}
	s := NewService(nil, Options{})
	defer s.Close()
	z := &zoneRuntime{persisted: PersistedZone{Settings: settings}, session: settingsPlanSession{}}
	for _, tc := range []struct {
		name string
		doc  SettingsDocument
		noop bool
	}{{"同值", b, true}, {"实际变更", DefaultSampleRateDocument(), false}} {
		t.Run(tc.name, func(t *testing.T) {
			command := Command{Command: "set_settings", Settings: SettingsPatch{SampleRate: &tc.doc}}
			if err := validateCommand(command); err != nil {
				t.Fatal(err)
			}
			plan, err := s.plan(context.Background(), "user", z, command, nil)
			if err != nil || plan.noop != tc.noop || plan.debounce == tc.noop || plan.open {
				t.Fatalf("设置规划 noop=%v debounce=%v open=%v，错误=%v", plan.noop, plan.debounce, plan.open, err)
			}
		})
	}
}

func TestSampleRateSettingsServiceValidation(t *testing.T) {
	s := NewService(nil, Options{})
	defer s.Close()
	invalid := SettingsDocument{}
	patch := SettingsPatch{SampleRate: &invalid}
	// 校验在用户加载和事务前发生，因此不需要数据库。
	if _, err := s.RegisterDevice(context.Background(), "user", RegisterDeviceInput{Name: "设备", Kind: "browser", Volume: 1, Settings: patch}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("注册未复用文档校验：%v", err)
	}
	if err := validateCommand(Command{Command: "set_settings", Settings: patch}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("设置命令未复用文档校验：%v", err)
	}
}
