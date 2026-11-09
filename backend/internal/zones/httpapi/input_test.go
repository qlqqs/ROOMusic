package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestSampleRateSettingsInput(t *testing.T) {
	for _, endpoint := range []string{"register", "command"} {
		for _, tc := range []struct {
			name     string
			settings string
			valid    bool
		}{
			{"默认", `{"sample_rate":{"strategy":"none","custom_rate":null,"filter":"linear_precise"}}`, true},
			{"完整对象", `{"sample_rate":{"strategy":"custom","custom_rate":96e3,"filter":"minimum_smooth"}}`, true},
			{"省略键", `{"quality":"lossless"}`, true},
			{"保留策略", `{"sample_rate":{"strategy":"dsd","custom_rate":null,"filter":"linear_precise"}}`, true},
			{"未知设置键", `{"future_setting":{}}`, false},
			{"对象空值", `{"sample_rate":null}`, false},
			{"不完整对象", `{"sample_rate":{"strategy":"none","filter":"linear_precise"}}`, false},
			{"未知字段", `{"sample_rate":{"strategy":"none","custom_rate":null,"filter":"linear_precise","extra":1}}`, false},
			{"错误策略", `{"sample_rate":{"strategy":"invalid","custom_rate":null,"filter":"linear_precise"}}`, false},
			{"错误滤波器", `{"sample_rate":{"strategy":"none","custom_rate":null,"filter":"minimum_2s"}}`, false},
			{"错误目标", `{"sample_rate":{"strategy":"custom","custom_rate":8000,"filter":"linear_precise"}}`, false},
			{"错误类型", `{"sample_rate":{"strategy":"custom","custom_rate":"48000","filter":"linear_precise"}}`, false},
			{"重复嵌套键", `{"sample_rate":{"strategy":"none","strategy":"custom","custom_rate":48000,"filter":"linear_precise"}}`, false},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				body := `{"settings":` + tc.settings + `}`
				var destination any
				if endpoint == "register" {
					destination = &zones.RegisterDeviceInput{}
				} else {
					body = `{"command":"set_settings","settings":` + tc.settings + `}`
					destination = &zones.Command{}
				}
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				w := httptest.NewRecorder()
				fields, ok := decodeInput(w, r, destination)
				if ok {
					ok = validSettings(fields, true)
					if !ok {
						invalidInput(w, r)
					}
				}
				if ok != tc.valid {
					t.Fatalf("输入校验=%v，期望=%v，响应=%s", ok, tc.valid, w.Body.String())
				}
				if tc.valid {
					var patch zones.SettingsPatch
					switch value := destination.(type) {
					case *zones.RegisterDeviceInput:
						patch = value.Settings
					case *zones.Command:
						patch = value.Settings
						if !validCommandFields(fields, value.Command) {
							t.Fatal("完整命令形状被拒绝")
						}
					}
					if err := zones.ValidateSettingsPatch(patch); err != nil {
						t.Fatal(err)
					}
					return
				}
				var response struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusBadRequest || response.Error.Code != "invalid_input" {
					t.Fatalf("设置错误未映射 invalid_input：%d %s，%v", w.Code, w.Body.String(), err)
				}
			})
		}
	}
}
