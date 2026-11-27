package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginruntime "github.com/qlqq/roomusic/backend/internal/plugins/runtime"
	"github.com/qlqq/roomusic/backend/internal/plugins/runtime/runtimetest"
)

func pluginTestEngine(t *testing.T) (*pluginruntime.Engine, string) {
	t.Helper()
	dir, err := filepath.Abs("../../../temp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err = os.MkdirTemp(dir, "plugin-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	engine, err := pluginruntime.New(filepath.Join(dir, ".cache"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return engine, dir
}

func buildPluginFixture(t *testing.T, dir, file string) []byte {
	t.Helper()
	return runtimetest.Build(t, dir, "../../plugins/examples/lyrics-demo", file)
}

func bundleFixture(t *testing.T, raw, wasm []byte, method uint16, extra bool) []byte {
	t.Helper()
	var b bytes.Buffer
	archive := zip.NewWriter(&b)
	add := func(name string, body []byte) {
		t.Helper()
		w, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("manifest.json", raw)
	add("plugin.wasm", wasm)
	if extra {
		add("notes.txt", []byte("synthetic"))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPluginPackagePreviewValidation(t *testing.T) {
	engine, dir := pluginTestEngine(t)
	good := buildPluginFixture(t, dir, "testdata/valid_describe.go")
	noDescribe := buildPluginFixture(t, dir, "testdata/no_describe.go")
	mismatch := buildPluginFixture(t, dir, "testdata/mismatched_describe.go")
	store := &Store{dir: dir, engine: engine, previews: make(map[string]pending)}
	if err := os.Mkdir(filepath.Join(dir, ".preview"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../plugins/examples/lyrics-demo/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		data   func(*testing.T) []byte
		reason string
	}{
		{"invalid_zip", func(*testing.T) []byte { return []byte("invalid zip") }, "zip"},
		{"invalid_json", func(t *testing.T) []byte { return bundleFixture(t, []byte("{"), good, zip.Deflate, false) }, "清单"},
		{"invalid_id", func(t *testing.T) []byte {
			return bundleFixture(t, bytes.Replace(manifest, []byte("lyrics-demo"), []byte("../Demo"), 1), good, zip.Deflate, false)
		}, "标识"},
		{"missing_describe", func(t *testing.T) []byte { return bundleFixture(t, manifest, noDescribe, zip.Deflate, false) }, "describe"},
		{"inconsistent_exports", func(t *testing.T) []byte { return bundleFixture(t, manifest, mismatch, zip.Deflate, false) }, "能力"},
		{"extra_file", func(t *testing.T) []byte { return bundleFixture(t, manifest, good, zip.Deflate, true) }, "zip"},
		{"oversized_package", func(t *testing.T) []byte {
			return bundleFixture(t, manifest, make([]byte, MaxPackage), zip.Store, false)
		}, "大小"},
		{"oversized_uncompressed", func(t *testing.T) []byte {
			return bundleFixture(t, manifest, make([]byte, MaxPackage+1), zip.Deflate, false)
		}, "大"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.Preview(t.Context(), tc.data(t))
			if !errors.Is(err, ErrInvalid) || got.Token != "" {
				t.Fatalf("预检应拒绝无效包，令牌存在 %t，错误 %v", got.Token != "", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("错误缺少拒绝原因 %q，实际 %v", tc.reason, err)
			}
		})
	}
	got, err := store.Preview(t.Context(), bundleFixture(t, manifest, good, zip.Deflate, false))
	if err != nil || got.Token == "" || got.Manifest.ID != "lyrics-demo" || len(got.Capabilities) != 1 || got.Capabilities[0] != "lyrics" {
		t.Fatalf("有效包未通过预检，能力 %v，错误 %v", got.Capabilities, err)
	}
}

func fixtureJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
