package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// 仅用一个裸浮点冲激和 cfg，避免此处重复验证音频解码器。
func convolutionUploadZIP(t *testing.T, delay string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	impulse := make([]byte, 4)
	binary.LittleEndian.PutUint32(impulse, math.Float32bits(1))
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"impulse.cfg", []byte("48000 1 1 1\n" + delay + "\n0\nimpulse.pcm\n0\n0.0\n0.0\n")},
		{"impulse.pcm", impulse},
	} {
		part, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func convolutionUpload(t *testing.T, app *integrationTestApplication, user integrationTestUser, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "测试卷积"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "impulse.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, app.origin+"/api/v1/me/dsp/ir-files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", app.origin)
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}

func TestPostgreSQLConvolutionRegisterRejectsOtherIR(t *testing.T) {
	app := newIntegrationTestApplication(t)
	owner, other := app.createUser(t, "user"), app.createUser(t, "user")
	ir := zonesSampleDecode[dspsettings.IRFile](t, convolutionUpload(t, app, owner, convolutionUploadZIP(t, "0")), http.StatusCreated)
	// 即使关闭卷积，保留的非空 ID 也必须属于本人。
	response := app.request(t, http.MethodPost, zonesSamplePrefix+"/devices", map[string]any{
		"name": "测试设备", "kind": "browser", "volume": 0.5, "muted": false,
		"settings": map[string]any{"convolution": map[string]any{"enabled": false, "ir_id": ir.ID, "before_peq": false}},
	}, &other, nil)
	requireAPIError(t, response, http.StatusNotFound, "not_found")
	snapshot := zonesSampleDecode[zones.Snapshot](t, app.request(t, http.MethodGet, zonesSamplePrefix, nil, &other, nil), http.StatusOK)
	if len(snapshot.Devices) != 0 || len(snapshot.Zones) != 0 {
		t.Fatal("被拒绝的登记留下了设备或播放区")
	}
}

func TestPostgreSQLConvolutionSettingsRejectOtherIR(t *testing.T) {
	app := newIntegrationTestApplication(t)
	owner, other := app.createUser(t, "user"), app.createUser(t, "user")
	ir := zonesSampleDecode[dspsettings.IRFile](t, convolutionUpload(t, app, owner, convolutionUploadZIP(t, "0")), http.StatusCreated)
	device := zonesSampleDecode[zones.RegisterDeviceResult](t, app.request(t, http.MethodPost, zonesSamplePrefix+"/devices", map[string]any{
		"name": "测试设备", "kind": "browser", "volume": 0.5, "muted": false,
	}, &other, nil), http.StatusCreated)
	response := app.request(t, http.MethodPost, zonesSamplePrefix+"/zones/"+device.Zone.ZoneID+"/commands", map[string]any{
		"command": "set_settings", "settings": map[string]any{"convolution": map[string]any{"enabled": true, "ir_id": ir.ID, "before_peq": false}},
	}, &other, nil)
	requireAPIError(t, response, http.StatusNotFound, "not_found")
	snapshot := zonesSampleDecode[zones.Snapshot](t, app.request(t, http.MethodGet, zonesSamplePrefix, nil, &other, nil), http.StatusOK)
	if len(snapshot.Zones) != 1 || snapshot.Zones[0].Settings.Convolution != device.Zone.Settings.Convolution || snapshot.Zones[0].Revision != device.Zone.Revision {
		t.Fatal("被拒绝的设置改变了播放区或修订号")
	}
}

func TestPostgreSQLConvolutionHugeDelayUploadCleansTemporaryFiles(t *testing.T) {
	app := newIntegrationTestApplication(t)
	owner := app.createUser(t, "user")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	// 文件只有几百字节，但延时需要约 384 MiB，超过 256 MiB 的预算。
	response := convolutionUpload(t, app, owner, convolutionUploadZIP(t, "1000000"))
	requireAPIError(t, response, http.StatusRequestEntityTooLarge, "ir_too_large")
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("失败上传遗留了 %d 个临时文件或目录", len(entries))
	}
	var count int
	if err := app.database.QueryRow(`SELECT count(*) FROM dsp_ir_files WHERE user_id=$1`, owner.id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("被拒绝的上传仍写入了 IR")
	}
}
