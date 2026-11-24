package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "run-mock-command" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		main()
		return
	}
	os.Exit(m.Run())
}

func TestMockCommandDataScriptAndInitialScenario(t *testing.T) {
	fixtures := filepath.Join("..", "..", "internal", "upstreammock", "testdata")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "run-mock-command", "-addr", "127.0.0.1:0",
		"-data", filepath.Join(fixtures, "override-test"),
		"-script", filepath.Join(fixtures, "conversation-test.json"),
		"-scenario", "lrclib=unavailable",
		"-scenario", "tavily=rate_limited",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		if err := cmd.Wait(); err != nil {
			t.Errorf("mock 命令退出: %v", err)
		}
	}()
	scanner := bufio.NewScanner(stderr)
	if !scanner.Scan() {
		t.Fatalf("mock 未报告监听地址: %v", scanner.Err())
	}
	_, addr, ok := strings.Cut(scanner.Text(), "已监听 ")
	if !ok {
		t.Fatalf("mock 启动日志 = %q", scanner.Text())
	}
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()

	check := func(method, path, body string, wantStatus int, data any) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s HTTP %d，期望 %d", path, resp.StatusCode, wantStatus)
		}
		if data != nil {
			if err := json.NewDecoder(resp.Body).Decode(data); err != nil {
				t.Fatal(err)
			}
		}
	}
	var release struct{ ID, Title string }
	const releaseID = "30000000-0000-4000-8000-000000000001"
	check(http.MethodGet, "/musicbrainz/ws/2/release/"+releaseID, "", 200, &release)
	if release.ID != releaseID || release.Title != "测试目录覆盖的发行" {
		t.Errorf("-data 未覆盖发行: %+v", release)
	}
	check(http.MethodGet, "/musicbrainz/ws/2/release/30000000-0000-4000-8000-000000000002", "", 404, nil)
	var models struct{ Data []struct{ ID string } }
	check(http.MethodGet, "/openai/v1/models", "", 200, &models)
	if len(models.Data) != 2 || models.Data[0].ID != "fixture-model" || models.Data[1].ID != "fixture-reviewer" {
		t.Errorf("-script 模型列表 = %+v", models.Data)
	}
	check(http.MethodGet, "/lrclib/api/get?track_name=ignored", "", 503, nil)
	check(http.MethodPost, "/tavily/search", `{"query":"合成查询"}`, 429, nil)
	check(http.MethodPost, "/_mock/reset", "", 204, nil)
	var search struct{ Results []struct{ Title, URL string } }
	check(http.MethodPost, "/tavily/search", `{"query":"合成查询"}`, 200, &search)
	if len(search.Results) != 1 || search.Results[0].Title != "测试目录覆盖的搜索结果" ||
		search.Results[0].URL != "https://override.example.invalid/release" {
		t.Errorf("重置情景后的 -data 搜索结果 = %+v", search)
	}
}
