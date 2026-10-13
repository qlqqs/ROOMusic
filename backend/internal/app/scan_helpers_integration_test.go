package app

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

// writeSyntheticTrack 在 path 写一个合成的 FLAC（临时目录里的素材，不读任何真实音乐）：md5 与音频帧由 md5 决定，
// 同一个路径同一个 md5 重写标签，扫描认为是同一首曲目的标签变了。
func writeSyntheticTrack(t *testing.T, path string, md5 byte, comments ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	content := parsertest.FLACWithAudio(t, [16]byte{md5}, bytes.Repeat([]byte{md5}, 64), comments...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runScan 启动一次扫描并等它成功结束。
func runScan(t *testing.T, application *integrationTestApplication, admin *integrationTestUser) {
	t.Helper()
	started := application.request(t, http.MethodPost, "/api/v1/scans", map[string]any{}, admin, nil)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start scan: HTTP %d: %s", started.Code, started.Body.String())
	}
	if status := waitForScanToFinish(t, application, decodeJSONMap(t, started)["id"].(string)); status != "succeeded" {
		t.Fatalf("scan finished as %s", status)
	}
}
