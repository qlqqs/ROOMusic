// Package runtime 封装插件引擎和宿主调用约定。
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const Timeout = 10 * time.Second

// 来宾模块与 Extism 的交换区各有一份线性内存，共享 64 MiB 预算。
const pagesPerMemory = 512

type Engine struct {
	config wazero.RuntimeConfig
	cache  wazero.CompilationCache
	logger *slog.Logger
	mu     sync.Mutex
	slots  map[string]*quota
}

type quota struct {
	active chan struct{}
	users  int
}

type Fetch func(context.Context, []byte) []byte
type Result struct {
	Code    uint32
	Output  []byte
	Exports map[string]bool
}

func New(dir string, logger *slog.Logger) (*Engine, error) {
	if _, ok := os.LookupEnv("EXTISM_ENABLE_WASI_OUTPUT"); ok {
		return nil, errors.New("插件沙箱要求取消 EXTISM_ENABLE_WASI_OUTPUT")
	}
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		return nil, err
	}
	config, mode := engineConfig()
	logger.Info("插件运行时已就绪", "mode", mode, "cache", "plugins/.cache")
	return &Engine{config: config.WithCompilationCache(cache).WithMemoryLimitPages(pagesPerMemory).WithCloseOnContextDone(true), cache: cache, logger: logger, slots: make(map[string]*quota)}, nil
}

// 显式探测当前系统的可执行内存权限，编译器不可用时选择解释器。
func engineConfig() (cfg wazero.RuntimeConfig, mode string) {
	defer func() {
		if recover() != nil {
			cfg, mode = wazero.NewRuntimeConfigInterpreter(), "interpreter"
		}
	}()
	cfg = wazero.NewRuntimeConfigCompiler()
	r := wazero.NewRuntimeWithConfig(context.Background(), cfg)
	_ = r.Close(context.Background())
	return cfg, "compiler"
}

func (e *Engine) Close(ctx context.Context) error { return e.cache.Close(ctx) }
func (e *Engine) acquire(ctx context.Context, id string) (func(), error) {
	e.mu.Lock()
	q := e.slots[id]
	if q == nil {
		q = &quota{active: make(chan struct{}, 2)}
		e.slots[id] = q
	}
	q.users++
	e.mu.Unlock()
	release := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		q.users--
		if q.users == 0 {
			delete(e.slots, id)
		}
	}
	select {
	case q.active <- struct{}{}:
		return func() { <-q.active; release() }, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

// SDK 只暴露导出名称，在预检时通过相同编译缓存检查 ABI 的真实签名。
func (e *Engine) exports(ctx context.Context, wasm []byte) (map[string]bool, error) {
	r := wazero.NewRuntimeWithConfig(ctx, e.config)
	defer r.Close(context.Background())
	mod, err := r.CompileModule(ctx, wasm)
	if err != nil {
		return nil, err
	}
	functions := mod.ExportedFunctions()
	result := map[string]bool{}
	for _, name := range []string{"describe", "cover_art", "lyrics", "artist_image", "artist_bio"} {
		def := functions[name]
		if def == nil {
			continue
		}
		if len(def.ParamTypes()) != 0 || len(def.ResultTypes()) != 1 || def.ResultTypes()[0] != api.ValueTypeI32 {
			return nil, errors.New("ABI 导出应为无参数并返回 i32 的函数")
		}
		result[name] = true
	}
	if !result["describe"] {
		return nil, errors.New("插件缺少 describe")
	}
	return result, nil
}

func (e *Engine) Call(ctx context.Context, id string, load func(context.Context) ([]byte, error), name string, input []byte, fetch Fetch) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	release, err := e.acquire(ctx, id)
	if err != nil {
		return Result{}, err
	}
	defer release()
	wasm, err := load(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{}
	if name == "describe" {
		res.Exports, err = e.exports(ctx, wasm)
		if err != nil {
			return Result{}, err
		}
	}
	logs := 0
	log := func(level slog.Level, msg string) {
		if logs < 50 {
			logs++
			e.logger.Log(ctx, level, "插件日志", "plugin", id, "message", strings.ToValidUTF8(msg[:min(len(msg), 1024)], ""))
		}
	}
	i64 := []extism.ValueType{extism.ValueTypeI64}
	read := func(ctx context.Context, p *extism.CurrentPlugin, ptr uint64, maxBytes uint64) ([]byte, bool) {
		n, err := p.LengthWithContext(ctx, ptr)
		if err != nil || n > maxBytes {
			return nil, false
		}
		b, err := p.ReadBytes(ptr)
		return b, err == nil
	}
	upstream := extism.NewHostFunctionWithStack("upstream_fetch", func(ctx context.Context, p *extism.CurrentPlugin, s []uint64) {
		out := []byte(`{"error":"invalid_request"}`)
		if b, ok := read(ctx, p, s[0], 64<<10); ok && fetch != nil {
			out = fetch(ctx, b)
		}
		s[0], _ = p.WriteBytes(out)
	}, i64, i64)
	logging := extism.NewHostFunctionWithStack("log", func(ctx context.Context, p *extism.CurrentPlugin, s []uint64) {
		if logs >= 50 {
			return
		}
		level, _ := read(ctx, p, s[0], 10)
		msg, ok := read(ctx, p, s[1], 1024)
		if ok {
			var l slog.Level
			_ = l.UnmarshalText(level)
			log(l, string(msg))
		}
	}, []extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64}, nil)
	manifest := extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: wasm}}, Timeout: 10000, Memory: &extism.ManifestMemory{MaxPages: pagesPerMemory}}
	compiled, err := extism.NewCompiledPlugin(ctx, manifest, extism.PluginConfig{EnableWasi: true, RuntimeConfig: e.config}, []extism.HostFunction{upstream, logging})
	if err != nil {
		return Result{}, err
	}
	defer compiled.Close(context.Background())
	p, err := compiled.Instance(ctx, extism.PluginInstanceConfig{ModuleConfig: wazero.NewModuleConfig().WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader)})
	if err != nil {
		return Result{}, err
	}
	defer p.Close(context.Background())
	p.SetLogger(func(l extism.LogLevel, msg string) {
		if l >= extism.LogLevelInfo {
			log(slog.LevelInfo, msg)
		}
	})
	res.Code, res.Output, err = p.CallWithContext(ctx, name, input)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		return Result{}, errors.New("插件执行失败")
	}
	if len(res.Output) > 256<<10 || res.Code == 0 && !json.Valid(res.Output) {
		return Result{}, errors.New("插件输出不是有效的限长 JSON")
	}
	return res, nil
}
