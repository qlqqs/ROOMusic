package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func c9Record(h slog.Handler, at time.Time, level slog.Level, attrs ...slog.Attr) error {
	r := slog.NewRecord(at, level, "合成消息正文不得进入日志环", 0)
	r.AddAttrs(attrs...)
	return h.Handle(context.Background(), r)
}

func TestC9LogRingCapacityFiltersAndSnapshot(t *testing.T) {
	ring := NewLogRing()
	h := NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)).Handler(), ring)
	start := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 1100; i++ {
		level := slog.LevelInfo
		if i%2 == 0 {
			level = slog.LevelError
		}
		if err := c9Record(h, start.Add(time.Duration(i)*time.Second), level, slog.String("module", "library"), slog.Int("count", i), slog.String("reason", "original")); err != nil {
			t.Fatal(err)
		}
	}
	if ring.count != 1000 {
		t.Fatalf("容量=%d", ring.count)
	}
	if got := ring.Recent(LogFilter{End: start.Add(99 * time.Second)}); len(got) != 0 {
		t.Fatalf("未淘汰旧记录: %+v", got)
	}
	got := ring.Recent(LogFilter{})
	if len(got) != 100 || got[0].Fields["count"] != int64(1099) {
		t.Fatalf("默认条数/排序错误: %d %+v", len(got), got[0])
	}
	if len(ring.Recent(LogFilter{Limit: 1000})) != 200 {
		t.Fatal("未限制为 200")
	}
	got = ring.Recent(LogFilter{Level: "ERROR", Module: "library", Start: start.Add(100 * time.Second), End: start.Add(104 * time.Second)})
	if len(got) != 3 || got[0].Fields["count"] != int64(104) || got[2].Fields["count"] != int64(100) {
		t.Fatalf("过滤边界: %+v", got)
	}
	if len(ring.Recent(LogFilter{Module: "other"})) != 0 || len(ring.Recent(LogFilter{Level: "WARN"})) != 0 {
		t.Fatal("模块或级别未精确匹配")
	}
	got[0].Fields["reason"] = "modified"
	if ring.Recent(LogFilter{Level: "ERROR", End: start.Add(104 * time.Second), Limit: 1})[0].Fields["reason"] != "original" {
		t.Fatal("快照修改影响环")
	}
	if len(NewLogRing().Recent(LogFilter{})) != 0 {
		t.Fatal("新环未清空")
	}
}

func TestC9LogRingRedaction(t *testing.T) {
	ring := NewLogRing("/synthetic/music", "/synthetic/music/nested", "/synthetic/data")
	h := NewHandler(slog.NewJSONHandler(io.Discard, nil), ring)
	attrs := []slog.Attr{
		slog.String("module", "library"), slog.String("password", "password-sentinel"), slog.String("API_KEY", "api-sentinel"), slog.String("Authorization", "Bearer bearer-sentinel"), slog.String("COOKIE", "cookie-sentinel"), slog.String("DATABASE_URL", "postgres://user:dsn-sentinel@db/app"), slog.String("Message", "body-sentinel"),
		slog.Group("group", slog.String("MixedCaseToKeN", "nested-token-sentinel"), slog.String("safe", "/synthetic/music/nested/album/01.flac")),
		slog.Any("container", map[string]any{"PaSsWoRd": "container-sentinel", "safe": []any{"Bearer value-sentinel", "postgres://user:value-secret@db/app", "/outside/private/02.flac"}}),
		slog.String("detail", "host=private-db user=synthetic password=detail-sentinel"),
		slog.String("quoted", `failed "/outside/private/with space/03.flac"`), slog.String("windows", `C:\private\04.flac`),
		slog.Any("error", errors.New("error-sentinel /outside/error/private.flac Bearer error-token")),
	}
	if err := c9Record(h, time.Now(), slog.LevelInfo, attrs...); err != nil {
		t.Fatal(err)
	}
	bound := h.WithAttrs([]slog.Attr{slog.String("module", "bound")}).WithGroup("nested").WithAttrs([]slog.Attr{slog.String("SeCrEt", "bound-sentinel")})
	_ = c9Record(bound, time.Now(), slog.LevelInfo, slog.String("reason", "/synthetic/music-neighbor/private/05.flac"))
	_ = c9Record(h.WithGroup("Message"), time.Now(), slog.LevelInfo, slog.String("reason", "group-body-sentinel"))
	raw, _ := json.Marshal(ring.Recent(LogFilter{}))
	for _, bad := range []string{"sentinel", "/synthetic/", "/outside/", `C:`, "postgres://", "Bearer ", "合成消息正文"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("泄露 %q: %s", bad, raw)
		}
	}
	for _, record := range ring.Recent(LogFilter{}) {
		for _, key := range []string{"password", "API_KEY", "Authorization", "COOKIE", "DATABASE_URL", "Message", "group", "container", "detail", "quoted", "windows", "error", "safe", "nested"} {
			if _, exists := record.Fields[key]; exists {
				t.Errorf("非白名单键仍存在 %s: %+v", key, record)
			}
		}
	}
}

type c9PanicValue struct{}

func (c9PanicValue) LogValue() slog.Value { panic("不得求值") }
func (c9PanicValue) String() string       { panic("不得格式化") }

type c9Handler struct {
	records []slog.Record
	err     error
}

func (h *c9Handler) Enabled(context.Context, slog.Level) bool { return true }
func (h *c9Handler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return h.err
}
func (h *c9Handler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *c9Handler) WithGroup(string) slog.Handler      { return h }

func TestC9LogHandlerPreservesOriginalOnFailureAndContention(t *testing.T) {
	want := errors.New("synthetic-output-error")
	original := &c9Handler{err: want}
	ring := NewLogRing()
	h := NewHandler(original, ring)
	if err := c9Record(h, time.Now(), slog.LevelInfo, slog.Any("unknown", c9PanicValue{})); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if len(original.records) != 1 || original.records[0].Message != "合成消息正文不得进入日志环" {
		t.Fatal("原输出被改写")
	}
	ring.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- c9Record(h, time.Now(), slog.LevelInfo, slog.String("safe", "contended")) }()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("环锁竞争阻塞原 Handler")
	}
	ring.mu.Unlock()
	if len(original.records) != 2 {
		t.Fatal("争用时丢失原输出")
	}
	// 未初始化的脱敏器会令旁路 panic，原处理器仍完成并保留原返回值。
	broken := NewHandler(original, &LogRing{})
	if err := c9Record(broken, time.Now(), slog.LevelInfo, slog.String("safe", "x")); !errors.Is(err, want) {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		_ = c9Record(h, time.Now(), slog.LevelInfo, slog.String("safe", fmt.Sprint(i)))
	}
	if len(original.records) != 1004 {
		t.Fatal("环满影响原输出")
	}
}

func TestC9LogRingConcurrent(t *testing.T) {
	ring := NewLogRing("/synthetic")
	h := NewHandler(slog.NewJSONHandler(io.Discard, nil), ring)
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				ring.SetRoots("/synthetic")
				_ = c9Record(h, time.Now(), slog.LevelInfo, slog.String("reason", "/synthetic/album with space/01.flac"), slog.String("request_id", "api_key=c9-synthetic"))
				_ = ring.Recent(LogFilter{Limit: 5})
			}
		}()
	}
	wg.Wait()
	records := ring.Recent(LogFilter{Limit: 200})
	if len(records) == 0 || len(records) > 200 {
		t.Fatalf("并发结果数=%d", len(records))
	}
	for _, record := range records {
		if record.Fields["reason"] != "[redacted]" || record.Fields["request_id"] != "[redacted]" {
			t.Fatalf("并发脱敏结果=%+v", record)
		}
		record.Fields["reason"] = "c9-modified-snapshot"
	}
	for _, record := range ring.Recent(LogFilter{Limit: 200}) {
		if record.Fields["reason"] != "[redacted]" {
			t.Fatal("并发读取快照影响环")
		}
	}
}

func TestC9LogWhitelistFieldsMessagesAndPaths(t *testing.T) {
	ring := NewLogRing()
	original := &c9Handler{}
	h := NewHandler(original, ring)
	for _, event := range []string{"identity.login.failed", "identity.login.throttled", "identity.origin.rejected"} {
		attrs := []slog.Attr{slog.String("event", event), slog.String("request_id", "c9-request"), slog.Int("count", 2), slog.Group("", slog.String("kind", "anonymous"), slog.String("username", "c9-private"))}
		for _, key := range []string{"err", "error", "detail", "path", "username", "source", "host", "sql", "env", "message", "key", "model", "domain", "sequence"} {
			attrs = append(attrs, slog.Int(key, 7))
		}
		attrs = append(attrs, slog.Group("reason", slog.String("kind", "private")), slog.Any("status", map[string]any{"count": 2}), slog.String("total", "3"), slog.Any("bytes", c9PanicValue{}))
		if err := c9Record(h, time.Now(), slog.LevelWarn, attrs...); err != nil {
			t.Fatal(err)
		}
		fields := ring.Recent(LogFilter{Limit: 1})[0].Fields
		if len(fields) != 4 || fields["event"] != event || fields["request_id"] != "c9-request" || fields["kind"] != "anonymous" || fields["count"] != int64(2) {
			t.Errorf("白名单输出=%+v", fields)
		}
	}
	for _, tc := range []struct{ key, value, want string }{
		{"reason", `/synthetic/album with space/track.flac`, "[redacted]"},
		{"reason", `failed "/synthetic/album with space/track.flac"`, `failed [redacted]"`},
		{"reason", `path:/synthetic/private album/track.flac`, "path[redacted]"},
		{"reason", `目录：/synthetic/private album/track.flac`, "目录[redacted]"},
		{"reason", `failed (/synthetic/private album/track.flac)`, "failed [redacted])"},
		{"reason", `C:\private album\track.flac`, "[redacted]"},
		{"route_template", "GET /api/v1/releases/{id}", "GET /api/v1/releases/{id}"},
		{"route_template", "/readyz", "/readyz"},
		{"route_template", "<unmatched>", "<unmatched>"},
		{"route_template", "/private/path", "[redacted]"},
		{"mime_type", "audio/flac", "audio/flac"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_ = c9Record(h, time.Now(), slog.LevelInfo, slog.String(tc.key, tc.value))
			if got := ring.Recent(LogFilter{Limit: 1})[0].Fields[tc.key]; got != tc.want {
				t.Fatalf("got=%v want=%s", got, tc.want)
			}
		})
	}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "http request completed", 0)
	_ = h.Handle(context.Background(), r)
	if ring.Recent(LogFilter{Limit: 1})[0].Fields["msg"] != "http request completed" {
		t.Fatal("固定消息丢失")
	}
	_ = c9Record(h, time.Now(), slog.LevelInfo, slog.String("msg", "合成自由正文"))
	if ring.Recent(LogFilter{Limit: 1})[0].Fields["msg"] != "[omitted]" {
		t.Fatal("自由正文未省略")
	}
	if len(original.records) != 16 {
		t.Fatalf("原 Handler 丢失记录: %d", len(original.records))
	}
}

func TestC9LogWhitelistDropsUnknownTextFields(t *testing.T) {
	ring := NewLogRing()
	original := &c9Handler{}
	h := NewHandler(original, ring)
	attrs := []slog.Attr{slog.String("event", "identity.login.failed"), slog.String("request_id", "c9-request")}
	for _, key := range []string{"err", "error", "detail", "path", "username", "source", "host", "sql", "env", "message", "key", "model", "domain", "sequence"} {
		attrs = append(attrs, slog.String(key, "c9-private-value"))
	}
	for _, event := range []string{"identity.login.failed", "identity.login.throttled"} {
		attrs[0] = slog.String("event", event)
		_ = c9Record(h, time.Now(), slog.LevelWarn, attrs...)
		fields := ring.Recent(LogFilter{Limit: 1})[0].Fields
		if len(fields) != 2 || fields["event"] != event || fields["request_id"] != "c9-request" {
			t.Fatalf("未知文本字段未整项丢弃: %+v", fields)
		}
	}
	if len(original.records) != 2 {
		t.Fatal("原日志未保留")
	}
	for _, record := range original.records {
		values := map[string]string{}
		record.Attrs(func(attr slog.Attr) bool { values[attr.Key] = attr.Value.String(); return true })
		for _, key := range []string{"username", "source", "host"} {
			if values[key] != "c9-private-value" {
				t.Errorf("原日志字段改变: %s", key)
			}
		}
	}
}
