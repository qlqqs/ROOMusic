package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInsightsCollectorProcessPrivacy(t *testing.T) {
	const addressEnv = "ROOMUSIC_INSIGHTS_TEST_LISTEN_ADDR"
	const dataEnv = "ROOMUSIC_INSIGHTS_TEST_PROCESS_DATA"
	if addr := os.Getenv(addressEnv); addr != "" {
		os.Args = []string{os.Args[0], "-addr", addr, "-data", os.Getenv(dataEnv), "-trusted-proxy", "127.0.0.1"}
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		main()
		return
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInsightsCollectorProcessPrivacy$")
	cmd.Env = append(os.Environ(), addressEnv+"="+addr, dataEnv+"="+dir)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	baseURL := "http://" + addr
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	for {
		status := 0
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/summary", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err == nil {
			status = response.StatusCode
			response.Body.Close()
		}
		if status == http.StatusOK {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			_ = cmd.Wait()
			t.Fatalf("统计进程未启动：HTTP %d，错误 %v，日志 %s", status, err, logs.String())
		}
	}

	const ipv4 = "198.51.100.174"
	const ipv6 = "2001:db8:abcd:1234::d"
	const failurePeer = "203.0.113.199"
	const valid = `{"schema":1,"instance_id":"a1000000-0000-4000-8000-000000000001","version":"dev","os":"linux","arch":"amd64","container":true,"library":{"tracks":"0","releases":"0"},"members":"1","features":{"auto_curation":false,"steward_model":false,"mcp_token":false,"lan_devices":false,"dsp":false,"scheduled_scan":false,"plugins":"0"},"devices_30d":{"browser":"0","upnp":"0"}}`
	post := func(ip, body string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/heartbeat", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Real-IP", ip)
		req.Header.Set("Content-Type", "application/json")
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("心跳返回 %d，期望 %d", r.StatusCode, want)
		}
		if body, err := io.ReadAll(r.Body); err != nil || len(body) != 0 {
			t.Fatalf("心跳应返回空正文，收到 %q，错误 %v", body, err)
		}
	}
	for range 30 {
		post(ipv4, valid, http.StatusNoContent)
	}
	post(ipv4, valid, http.StatusTooManyRequests)
	post(ipv6, valid, http.StatusNoContent)
	post(ipv6, `{"client_ip":"2001:db8:abcd:1234::d"}`, http.StatusBadRequest)

	// 挡住文件替换位置，核对持久化失败路径的日志。
	path := filepath.Join(dir, "instances.json")
	saved := filepath.Join(dir, "snapshot.json")
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	post(failurePeer, valid, http.StatusInternalServerError)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("统计进程退出失败：%v\n%s", err, logs.String())
	}
	assertNoAddress := func(where, content string) {
		t.Helper()
		for _, ip := range []string{"127.0.0.1", ipv4, ipv6, "2001:db8:abcd:1234", failurePeer} {
			if strings.Contains(content, ip) {
				t.Errorf("%s 暴露了客户端地址 %q", where, ip)
			}
		}
	}
	assertNoAddress("进程日志", logs.String())
	files := 0
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		assertNoAddress("文件名", entry.Name())
		assertNoAddress(entry.Name(), string(data))
		if !bytes.Contains(data, []byte("a1000000-0000-4000-8000-000000000001")) {
			t.Errorf("%s 中缺少成功接收的实例", entry.Name())
		}
		return nil
	})
	if err != nil || files != 1 {
		t.Fatalf("统计目录有 %d 个文件，遍历错误 %v", files, err)
	}
}
