package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/qlqq/roomusic/backend/internal/plugins/runtime/runtimetest"
)

func sandboxFixture(t *testing.T, name string) (*Engine, string, []byte) {
	t.Helper()
	dir, err := filepath.Abs("../../../../temp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err = os.MkdirTemp(dir, "plugin-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	engine, err := New(filepath.Join(dir, ".cache"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return engine, dir, buildFixture(t, dir, name)
}

func buildFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	return runtimetest.Build(t, dir, "../../../plugins/examples/lyrics-demo", filepath.Join("testdata", name+".go"))
}

func callFixture(ctx context.Context, e *Engine, wasm []byte, id, name string, input []byte, fetch Fetch) (Result, error) {
	return e.Call(ctx, id, func(context.Context) ([]byte, error) { return wasm, nil }, name, input, fetch)
}

func requireHealthyPlugin(t *testing.T, e *Engine, wasm []byte) {
	t.Helper()
	got, err := callFixture(t.Context(), e, wasm, "healthy", "describe", []byte("{}"), nil)
	if err != nil || got.Code != 0 || string(got.Output) != `{"capabilities":["lyrics"]}` {
		t.Fatalf("同进程的另一插件不可用，结果 %s，错误 %v", got.Output, err)
	}
}

func TestPluginSandbox(t *testing.T) {
	e, dir, wasm := sandboxFixture(t, "sandbox")
	t.Run("builtin_http", func(t *testing.T) {
		httpWasm := buildFixture(t, dir, "builtin_http")
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		input, _ := json.Marshal(server.URL)
		if got, err := callFixture(t.Context(), e, httpWasm, "sandbox", "builtin_http", input, nil); err == nil && got.Code == 0 {
			t.Error("生产运行时允许了内置 HTTP")
		}
		for _, hosts := range []string{"null", "[]"} {
			t.Run(hosts, func(t *testing.T) {
				var manifest extism.Manifest
				if err := json.Unmarshal([]byte(`{"allowed_hosts":`+hosts+`}`), &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Wasm = []extism.Wasm{extism.WasmData{Data: httpWasm}}
				plugin, err := extism.NewPlugin(t.Context(), manifest, extism.PluginConfig{EnableWasi: true, RuntimeConfig: e.config}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer plugin.Close(context.Background())
				if code, _, err := plugin.CallWithContext(t.Context(), "builtin_http", input); err == nil && code == 0 {
					t.Error("空白名单允许了内置 HTTP")
				}
			})
		}
		if n := requests.Load(); n != 0 {
			t.Errorf("测试服务器收到了 %d 个内置 HTTP 请求", n)
		}
		requireHealthyPlugin(t, e, wasm)
	})
	t.Run("WASI", func(t *testing.T) {
		const key = "ROOMUSIC_TEST_GUEST_SENTINEL"
		t.Setenv(key, "synthetic")
		path := filepath.Join(dir, "sentinel")
		if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		input := []byte(path + "\n" + key)
		got, err := callFixture(t.Context(), e, wasm, "sandbox", "check_wasi", input, nil)
		if err != nil || got.Code != 0 || string(got.Output) != `{"env_visible":false,"file_visible":false,"socket_visible":false}` {
			t.Errorf("WASI 隔离结果 %s，错误 %v", got.Output, err)
		}
		requireHealthyPlugin(t, e, wasm)
	})
	for _, tc := range []struct{ name, below, above string }{
		{"output_boundary", "262142", "262143"},
		{"memory_boundary", "4", "33"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := callFixture(t.Context(), e, wasm, "sandbox", tc.name, []byte(tc.below), nil); err != nil || got.Code != 0 {
				t.Fatalf("限制内的调用失败，退出码 %d，错误 %v", got.Code, err)
			}
			if got, err := callFixture(t.Context(), e, wasm, "sandbox", tc.name, []byte(tc.above), nil); err == nil && got.Code == 0 {
				t.Error("超出限制的调用成功")
			}
			requireHealthyPlugin(t, e, wasm)
		})
	}
}

func TestPluginCallDeadline(t *testing.T) {
	e, _, wasm := sandboxFixture(t, "sandbox")
	requireHealthyPlugin(t, e, wasm)
	start := time.Now()
	_, err := e.Call(t.Context(), "slow", func(ctx context.Context) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		const budget = 10 * time.Second
		if !ok || deadline.Before(start.Add(budget)) || deadline.After(time.Now().Add(budget)) {
			t.Fatalf("插件调用未设置 10 秒截止时间：%v", deadline)
		}
		return wasm, nil
	}, "spin", []byte("{}"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("死循环应由插件截止时间终止，错误 %v", err)
	}
	requireHealthyPlugin(t, e, wasm)
}

func TestPluginConcurrencyQuota(t *testing.T) {
	e, _, wasm := sandboxFixture(t, "upstream_fetch")
	requireHealthyPlugin(t, e, wasm)
	ctx, cancel := context.WithCancel(t.Context())
	reached := make(chan struct{}, 2)
	finished := make(chan error, 2)
	defer func() {
		cancel()
		for range 2 {
			<-finished
		}
	}()
	for range 2 {
		go func() {
			_, err := callFixture(ctx, e, wasm, "shared", "lyrics", []byte("{}"), func(ctx context.Context, _ []byte) []byte {
				reached <- struct{}{}
				<-ctx.Done()
				return []byte("{}")
			})
			finished <- err
		}()
	}
	for range 2 {
		select {
		case <-reached:
		case err := <-finished:
			finished <- err
			t.Fatalf("前两次调用应能同时进入上游，错误 %v", err)
		}
	}
	requireHealthyPlugin(t, e, wasm)
	third, stop := context.WithTimeout(t.Context(), 350*time.Millisecond)
	defer stop()
	var entered bool
	_, err := callFixture(third, e, wasm, "shared", "lyrics", []byte("{}"), func(context.Context, []byte) []byte {
		entered = true
		return []byte("{}")
	})
	if entered || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("第三次调用应在等待配额时超时，进入上游 %t，错误 %v", entered, err)
	}
}
