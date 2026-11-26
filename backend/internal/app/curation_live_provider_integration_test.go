package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// 管家使用自己的凭据存储与 Responses 适配器；回环端点只转发到配置的 HTTPS 目标并观察返回用量。
func liveRealProvider(t *testing.T, app *integrationTestApplication, admin *integrationTestUser, search string, meter *liveResponseMeter) string {
	t.Helper()
	endpoint, err := url.Parse(os.Getenv("ROOMUSIC_LIVE_ENDPOINT"))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		t.Fatal("真实模型需要 HTTPS 接口地址")
	}
	model := os.Getenv("ROOMUSIC_LIVE_MODEL")
	file, field := os.Getenv("ROOMUSIC_LIVE_KEY_FILE"), os.Getenv("ROOMUSIC_LIVE_KEY_FIELD")
	if model == "" || file == "" || field == "" {
		t.Fatal("真实模型需要配置模型名、密钥文件路径和字段名")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("读取真实模型密钥文件失败")
	}
	var fields map[string]json.RawMessage
	var key string
	if json.Unmarshal(data, &fields) != nil || json.Unmarshal(fields[field], &key) != nil || strings.TrimSpace(key) == "" {
		t.Fatal("真实模型密钥字段缺失或内容无效")
	}
	base := strings.TrimRight(endpoint.Path, "/")
	if base == "" {
		base = "/v1"
	}
	endpoint.Path = base + "/responses"
	endpoint.RawPath = ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "无效的模型请求", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "读取模型请求失败", http.StatusBadRequest)
			return
		}
		up, err := http.NewRequestWithContext(req.Context(), http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, "构建模型请求失败", http.StatusBadRequest)
			return
		}
		for _, name := range []string{"Authorization", "Content-Type", "Accept"} {
			up.Header.Set(name, req.Header.Get(name))
		}
		record := meter.begin(body)
		// 单次传输沿用环境代理，重定向不会携带凭据到下一个地址。
		resp, err := http.DefaultTransport.RoundTrip(up)
		if err != nil {
			record.fail()
			http.Error(w, "模型连接失败", http.StatusBadGateway)
			return
		}
		source := record.observe(resp.StatusCode, resp.Body)
		defer source.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			http.Error(w, "模型端点返回错误状态", resp.StatusCode)
			return
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(liveStreamWriter{w}, source)
	}))
	t.Cleanup(server.Close)
	resp := app.request(t, http.MethodPut, stewardBase+"/settings", map[string]string{
		"endpoint": server.URL + "/v1", "protocol": "responses", "model": model, "review_model": model, "key": key,
		"search_source": "searxng", "searxng_url": search + "/searxng",
	}, admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("保存真实模型设置失败（HTTP %d）", resp.Code)
	}
	resp = app.request(t, http.MethodPut, curationBase+"/settings", map[string]any{
		"model": model, "daily_token_limit": 3_000_000,
	}, admin, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("保存整理模型和每日上限失败（HTTP %d）", resp.Code)
	}
	return key
}

type liveStreamWriter struct{ http.ResponseWriter }

func (w liveStreamWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil {
		err = http.NewResponseController(w.ResponseWriter).Flush()
	}
	return n, err
}
