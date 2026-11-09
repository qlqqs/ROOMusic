package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func dspSamplePEQ() map[string]any {
	return map[string]any{"enabled": true, "preamp_db": -3, "bands": []any{
		map[string]any{"type": "peaking", "freq_hz": 1000, "gain_db": 6, "q": 1, "channels": "all", "enabled": true},
		map[string]any{"type": "high_pass", "freq_hz": 20, "slope_db": 12, "channels": []int{0, 1}, "enabled": true},
	}, "source": map[string]any{"kind": "import"}}
}

// 只采集本能力的请求/响应金样；行为测试由独立测试任务维护。
func TestPostgreSQLDSPAPIResponseSamples(t *testing.T) {
	application := newIntegrationTestApplication(t, withTestRenderer(t))
	user := application.createUser(t, "user")
	other := application.createUser(t, "user")
	samples := newAPISampleRecorder(t, application)
	samples.scope = "dsp"
	samples.forbid(user.token)
	samples.forbid(other.token)
	callAs := func(name, method, path string, body any, status int, actor *integrationTestUser) *httptest.ResponseRecorder {
		t.Helper()
		path = "/api/v1/me/dsp/" + path
		response := application.request(t, method, path, body, actor, map[string]string{"X-Request-ID": sampleRequestID})
		samples.recordPayload("dsp/requests/"+name, map[string]any{"method": method, "path": path, "body": body})
		if status == 204 {
			if response.Code != status || response.Body.Len() != 0 {
				t.Fatalf("%s: 期望空204，实际%d %s", name, response.Code, response.Body.String())
			}
			samples.recordPayload("dsp/responses/"+name, map[string]any{"status": status, "body": nil})
		} else {
			samples.record("dsp/responses/"+name, response, status)
		}
		return response
	}
	call := func(name, method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		return callAs(name, method, path, body, status, &user)
	}
	call("opra-search-empty", "GET", "opra/search?q=", nil, 200)
	search := call("opra-search", "GET", "opra/search?q=HD650", nil, 200)
	var products struct {
		Items []struct {
			ID string `json:"product_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(search.Body.Bytes(), &products); err != nil || len(products.Items) == 0 {
		t.Fatalf("OPRA样本产品缺失：%v", err)
	}
	call("opra-product", "GET", "opra/products/"+products.Items[0].ID, nil, 200)
	call("opra-not-found", "GET", "opra/products/"+uuid.New(), nil, 404)
	call("peq-import", "POST", "peq/import", map[string]any{"text": "Preamp: -3 dB\nFilter 1: ON PK Fc 1000 Hz Gain 6 dB Q 1\nChannel: L"}, 200)
	call("peq-import-empty", "POST", "peq/import", map[string]any{"text": "Preamp: -3 dB"}, 400)
	call("peq-response", "POST", "peq/response", map[string]any{"peq": dspSamplePEQ()}, 200)
	call("peq-response-invalid", "POST", "peq/response", map[string]any{"peq": dspSamplePEQ(), "sample_rate": 7999}, 400)
	call("presets-empty", "GET", "peq-presets", nil, 200)
	created := call("preset-create", "POST", "peq-presets", map[string]any{"name": "试听均衡", "peq": dspSamplePEQ()}, 201)
	var preset struct {
		ID string `json:"preset_id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &preset); err != nil || preset.ID == "" {
		t.Fatalf("预设样本无preset_id：%v", err)
	}
	call("presets-list", "GET", "peq-presets", nil, 200)
	call("preset-name-taken", "POST", "peq-presets", map[string]any{"name": "试听均衡", "peq": dspSamplePEQ()}, 409)
	call("preset-invalid", "POST", "peq-presets", map[string]any{"name": "", "peq": dspSamplePEQ()}, 400)
	call("preset-update", "PATCH", "peq-presets/"+preset.ID, map[string]any{"name": "客厅均衡", "peq": dspSamplePEQ()}, 200)
	callAs("preset-other-member", "PATCH", "peq-presets/"+preset.ID, map[string]any{"name": "他人预设"}, 404, &other)
	call("preset-delete", "DELETE", "peq-presets/"+preset.ID, nil, 204)
	call("preset-not-found", "DELETE", "peq-presets/"+preset.ID, nil, 404)
	callAs("unauthorized", "GET", "peq-presets", nil, 401, nil)
	mustExec(t, application, `INSERT INTO dsp_peq_presets(id,user_id,name,peq) SELECT md5('peq-sample-'||n)::uuid,$1,'样本预设'||n,'{"enabled":false,"preamp_db":0,"bands":[]}'::jsonb FROM generate_series(1,200) AS n`, user.id)
	call("preset-limit", "POST", "peq-presets", map[string]any{"name": "额外预设", "peq": dspSamplePEQ()}, 409)
	upload := func(name, fileName string, content []byte, status int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("name", "试听卷积"); err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		path := "/api/v1/me/dsp/ir-files"
		request := httptest.NewRequest("POST", application.origin+path, &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("Origin", application.origin)
		request.Header.Set("X-Request-ID", sampleRequestID)
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		samples.recordPayload("dsp/requests/"+name, map[string]any{"method": "POST", "path": path, "multipart": map[string]any{"name": "试听卷积", "file": map[string]any{"file_name": fileName, "size_bytes": len(content)}}})
		samples.record("dsp/responses/"+name, response, status)
		return response
	}
	irResponse := upload("ir-upload", "impulse.wav", dspSampleIRWAV(), 201)
	var ir struct {
		ID string `json:"ir_id"`
	}
	if err := json.Unmarshal(irResponse.Body.Bytes(), &ir); err != nil || ir.ID == "" {
		t.Fatalf("IR样本无ir_id：%v", err)
	}
	call("ir-list", "GET", "ir-files", nil, 200)
	call("ir-rename", "PATCH", "ir-files/"+ir.ID, map[string]any{"name": "客厅卷积"}, 200)
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	cfg, err := zipWriter.Create("invalid.cfg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Write([]byte("invalid\n")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	upload("ir-cfg-invalid", "invalid.zip", archive.Bytes(), 400)
	upload("ir-too-large", "oversized.wav", make([]byte, (32<<20)+1), 413)
	callAs("ir-not-found", "PATCH", "ir-files/"+ir.ID, map[string]any{"name": "他人卷积"}, 404, &other)
	samples.finish()
}

// 金样仅使用四抽头合成冲激，不读取本机参考数据。
func dspSampleIRWAV() []byte {
	data := make([]byte, 52)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 96000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 8)
	binary.LittleEndian.PutUint16(data[44:], 16384)
	return data
}
