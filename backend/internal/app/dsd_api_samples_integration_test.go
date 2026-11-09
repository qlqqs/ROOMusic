package app

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// recordDSDFormatSamples 仅为已有 recorder 增加真实扫描所得的格式样本；
// 素材为临时生成的短静音，不依赖本机音乐或参考数据。
func recordDSDFormatSamples(t *testing.T, application *integrationTestApplication, samples *apiSampleRecorder, call apiSampleCall, admin *integrationTestUser) {
	t.Helper()
	root := createLibraryDirectory(t, application, "dsd-samples")
	for _, fixture := range []struct {
		codec string
		rate  int
	}{{"dsf", 2822400}, {"dff", 5644800}} {
		dir := filepath.Join(root, fixture.codec)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "01."+fixture.codec), dsdFormatSample(fixture.codec, fixture.rate), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	created := call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": root}, admin, map[string]string{"Idempotency-Key": "dsd-create-root"})
	if created.Code != http.StatusCreated {
		t.Fatalf("登记 DSD 样本目录失败：HTTP %d", created.Code)
	}
	rootID := decodeJSONMap(t, created)["id"].(string)
	// 其他样本会直接制造损坏来源和目录迁移；这里只扫描本样本拥有的根。
	rows, err := application.database.Query(`UPDATE library_roots SET status='disabled' WHERE id<>$1 AND status='active' RETURNING id::text`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	var restore []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		restore = append(restore, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	defer func() {
		for _, id := range restore {
			if _, err := application.database.Exec(`UPDATE library_roots SET status='active' WHERE id=$1`, id); err != nil {
				t.Error(err)
			}
		}
	}()
	started := call(http.MethodPost, "/api/v1/scans", nil, admin)
	if started.Code != http.StatusAccepted {
		t.Fatalf("启动 DSD 样本扫描失败：HTTP %d", started.Code)
	}
	if status := waitForScanToFinish(t, application, decodeJSONMap(t, started)["id"].(string)); status != "succeeded" {
		t.Fatalf("DSD 样本扫描失败：%s", status)
	}
	for _, codec := range []string{"dsf", "dff"} {
		var releaseID string
		if err := application.database.QueryRow(`SELECT m.release_id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1 AND t.codec=$2 AND t.source_status='present'`, rootID, codec).Scan(&releaseID); err != nil {
			t.Fatal(err)
		}
		samples.record("releases/get-"+codec, call(http.MethodGet, "/api/v1/releases/"+releaseID, nil, admin), http.StatusOK)
	}
}

func dsdFormatSample(codec string, rate int) []byte {
	const channels = 2
	// 半秒，末尾 DSF 块补齐不计入有效样本数。
	samples := rate / 2
	if codec == "dsf" {
		payload := ((samples/8 + 4095) / 4096) * 4096 * channels
		data := make([]byte, 92+payload)
		copy(data, "DSD ")
		binary.LittleEndian.PutUint64(data[4:], 28)
		binary.LittleEndian.PutUint64(data[12:], uint64(len(data)))
		copy(data[28:], "fmt ")
		binary.LittleEndian.PutUint64(data[32:], 52)
		binary.LittleEndian.PutUint32(data[40:], 1)
		binary.LittleEndian.PutUint32(data[48:], 2)
		binary.LittleEndian.PutUint32(data[52:], channels)
		binary.LittleEndian.PutUint32(data[56:], uint32(rate))
		binary.LittleEndian.PutUint32(data[60:], 8)
		binary.LittleEndian.PutUint64(data[64:], uint64(samples))
		binary.LittleEndian.PutUint32(data[72:], 4096)
		copy(data[80:], "data")
		binary.LittleEndian.PutUint64(data[84:], uint64(payload+12))
		copy(data[92:], bytes.Repeat([]byte{0x69}, payload))
		return data
	}
	chunk := func(id string, data []byte) []byte {
		out := make([]byte, 12+len(data)+len(data)%2)
		copy(out, id)
		binary.BigEndian.PutUint64(out[4:], uint64(len(data)))
		copy(out[12:], data)
		return out
	}
	fs := make([]byte, 4)
	binary.BigEndian.PutUint32(fs, uint32(rate))
	prop := append([]byte("SND "), chunk("FS  ", fs)...)
	prop = append(prop, chunk("CHNL", append([]byte{0, channels}, []byte("SLFTSRGT")...))...)
	prop = append(prop, chunk("CMPR", []byte("DSD \x00"))...)
	body := append([]byte("DSD "), chunk("FVER", []byte{1, 5, 0, 0})...)
	body = append(body, chunk("PROP", prop)...)
	body = append(body, chunk("DSD ", bytes.Repeat([]byte{0x69}, samples/8*channels))...)
	return chunk("FRM8", body)
}
