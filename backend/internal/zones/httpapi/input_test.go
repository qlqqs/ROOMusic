package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestMoveEntryInput(t *testing.T) {
	entryID := uuid.New()
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"缺少目标", fmt.Sprintf(`{"command":"move_entry","entry_id":%q}`, entryID), "invalid_input"},
		{"目标为null", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":null}`, entryID), "invalid_input"},
		{"缺少条目", `{"command":"move_entry","to_index":0}`, "invalid_input"},
		{"条目为null", `{"command":"move_entry","entry_id":null,"to_index":0}`, "invalid_input"},
		{"非法UUID", `{"command":"move_entry","entry_id":"bad-id","to_index":0}`, "invalid_input"},
		{"负下标", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":-1}`, entryID), "invalid_input"},
		{"旧delta", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"delta":1}`, entryID), "invalid_input"},
		{"新旧字段混用", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":0,"delta":-1}`, entryID), "invalid_input"},
		{"重复目标", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":0,"to_index":1}`, entryID), "invalid_input"},
		{"非整数", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":1.5}`, entryID), "invalid_json"},
		{"字符串下标", fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":"0"}`, entryID), "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := zones.NewService(nil, zones.Options{})
			defer s.Close()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.SetPathValue("zone_id", uuid.New())
			w := httptest.NewRecorder()
			(&Handlers{service: s}).command(w, r, uuid.New())
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusBadRequest || response.Error.Code != tc.code {
				t.Fatalf("HTTP 错误 = %d %s，期望 400 %s，解码错误=%v", w.Code, w.Body.String(), tc.code, err)
			}
		})
	}
	t.Run("零下标合法且不会当作缺失", func(t *testing.T) {
		body := fmt.Sprintf(`{"command":"move_entry","entry_id":%q,"to_index":0}`, entryID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var command zones.Command
		fields, ok := decodeInput(w, r, &command)
		if !ok || !validCommandFields(fields, command.Command) || command.EntryID != entryID || command.ToIndex != 0 {
			t.Fatalf("零下标被拒绝：%s，命令=%+v", w.Body.String(), command)
		}
	})
}

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
