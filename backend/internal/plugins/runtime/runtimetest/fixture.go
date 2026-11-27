// Package runtimetest 构建合成 WASI 模块并准备测试专用编译缓存。
package runtimetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
)

func Build(t *testing.T, dir, moduleDir, source string) []byte {
	t.Helper()
	source, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strings.TrimSuffix(filepath.Base(source), ".go")+".wasm")
	cmd := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-buildmode=c-shared", "-o", path, source)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 WASI 夹具失败 %v\n%s", err, output)
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Go 来宾与 Extism 内核的冷编译属于夹具准备；实际调用仍走生产入口的配额与时限。
	cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(dir, ".cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close(t.Context())
	config := wazero.NewRuntimeConfig().WithCompilationCache(cache).WithCloseOnContextDone(true)
	compiled, err := extism.NewCompiledPlugin(t.Context(), extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: wasm}}}, extism.PluginConfig{RuntimeConfig: config}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return wasm
}
