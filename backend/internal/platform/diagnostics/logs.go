package diagnostics

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const LogCapacity = 1000

// LogRecord 只保存白名单字段和固定事件文本，不保存自由消息正文。
type LogRecord struct {
	Time   time.Time      `json:"time"`
	Level  string         `json:"level"`
	Fields map[string]any `json:"fields"`
}

type LogFilter struct {
	Level      string
	Module     string
	Start, End time.Time
	Limit      int
}

type LogRing struct {
	mu          sync.Mutex
	records     [LogCapacity]LogRecord
	next, count int
	redactor    atomic.Pointer[Redactor]
}

func NewLogRing(roots ...string) *LogRing {
	ring := &LogRing{}
	ring.SetRoots(roots...)
	return ring
}

func (r *LogRing) SetRoots(roots ...string) { r.redactor.Store(NewRedactor(roots...)) }

// Recent 返回最新在前的独立快照；写入遇到读取锁时丢弃该次环记录。
func (r *LogRing) Recent(filter LogFilter) []LogRecord {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	r.mu.Lock()
	result := make([]LogRecord, 0, limit)
	for i := 0; i < r.count && len(result) < limit; i++ {
		record := r.records[(r.next-1-i+LogCapacity)%LogCapacity]
		if filter.Level != "" && record.Level != filter.Level {
			continue
		}
		if filter.Module != "" && record.Fields["module"] != filter.Module {
			continue
		}
		if !filter.Start.IsZero() && record.Time.Before(filter.Start) {
			continue
		}
		if !filter.End.IsZero() && record.Time.After(filter.End) {
			continue
		}
		result = append(result, record)
	}
	r.mu.Unlock()
	// 入环后的字段不可变，在锁外复制可减少并发写入被丢弃的机会。
	for i := range result {
		result[i].Fields = cloneLogValue(result[i].Fields).(map[string]any)
	}
	return result
}

func cloneLogValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(value))
		for key, item := range value {
			copy[key] = cloneLogValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = cloneLogValue(item)
		}
		return copy
	default:
		return value
	}
}

type boundAttrs struct {
	groups []string
	attrs  []slog.Attr
}
type Handler struct {
	next   slog.Handler
	ring   *LogRing
	groups []string
	bound  []boundAttrs
}

func NewHandler(next slog.Handler, ring *LogRing) *Handler { return &Handler{next: next, ring: ring} }
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)
	h.capture(record)
	return err
}
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.next = h.next.WithAttrs(attrs)
	copy.bound = append(append([]boundAttrs(nil), h.bound...), boundAttrs{append([]string(nil), h.groups...), append([]slog.Attr(nil), attrs...)})
	return &copy
}
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copy := *h
	copy.next = h.next.WithGroup(name)
	copy.groups = append(append([]string(nil), h.groups...), name)
	return &copy
}
func (h *Handler) capture(record slog.Record) {
	// 诊断旁路的失败不得影响原日志与业务结果；不执行用户提供的 LogValuer/Stringer。
	defer func() { _ = recover() }()
	redactor := h.ring.redactor.Load()
	fields := map[string]any{}
	if _, fixed := fixedLogMessages[record.Message]; fixed {
		fields["msg"] = redactor.Text(record.Message)
	}
	budget := 64
	add := func(groups []string, attr slog.Attr) {
		// 命名组也是属性，未列入白名单的组整体丢弃，不能借容器绕过键过滤。
		if len(groups) != 0 {
			return
		}
		redactAttr(fields, attr, redactor, 0, &budget)
	}
	for _, bound := range h.bound {
		for _, attr := range bound.attrs {
			add(bound.groups, attr)
			if budget <= 0 {
				break
			}
		}
		if budget <= 0 {
			break
		}
	}
	record.Attrs(func(attr slog.Attr) bool { add(h.groups, attr); return budget > 0 })
	raw, err := json.Marshal(fields)
	if err != nil || len(raw) > 16*1024 {
		return
	}
	entry := LogRecord{Time: record.Time.UTC(), Level: record.Level.String(), Fields: fields}
	if !h.ring.mu.TryLock() {
		return
	}
	defer h.ring.mu.Unlock()
	h.ring.records[h.ring.next] = entry
	h.ring.next = (h.ring.next + 1) % LogCapacity
	if h.ring.count < LogCapacity {
		h.ring.count++
	}
}

// safeLogKey 的显式清单与 logging-guidelines.md 同步；数值键不接受字符串。
func safeLogKey(key string) (text, number bool) {
	if len(key) > 128 {
		return false, false
	}
	if strings.HasSuffix(key, "_id") {
		return true, false
	}
	switch key {
	case "event", "module", "level", "msg", "kind", "status", "reason", "tool", "mode", "source_kind", "outcome", "error_code", "error_kind", "route_template", "method", "mime_type", "category", "stage", "trigger", "direction", "capability", "phase", "yield_status", "operation":
		return true, key == "status"
	case "count", "total", "bytes", "duration_ms", "response_bytes", "status_code", "exit_code", "keep_days", "keep_count",
		"conversations", "analyzed", "completed", "written", "batch_size", "skipped_current", "batch_ms", "yield_ms", "elapsed_ms", "eta_ms", "live_updated",
		"target_generation", "from_version", "to_version", "version", "textnorm_version", "search_projection_version", "artist_projection_version",
		"track_budget", "name_budget", "estimated_tracks", "estimated_input_names", "files_parsed", "parse_cache_hits", "parse_cache_writes", "candidates_written", "candidates_skipped", "artwork_deferred_loads",
		"matched", "rebound", "ambiguous_keys", "content_replaced", "deferred_scopes", "fingerprints_borrowed", "fingerprints_computed", "fingerprints_reused", "fingerprint_failures", "scope_failures",
		"step", "steps", "input_tokens", "output_tokens", "usage_known_steps", "search_count", "failed_count", "result_count", "approved", "rejected", "needs_human_confirmation":
		return false, true
	default:
		return false, false
	}
}

func redactAttr(target map[string]any, attr slog.Attr, r *Redactor, depth int, budget *int) {
	if *budget <= 0 || depth > 8 {
		return
	}
	*budget--
	if attr.Key == "" && attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			redactAttr(target, child, r, depth+1, budget)
			if *budget <= 0 {
				break
			}
		}
		return
	}
	allowText, allowNumber := safeLogKey(attr.Key)
	if !allowText && !allowNumber {
		return
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		if allowText {
			value := r.text(attr.Value.String(), attr.Key == "route_template")
			if attr.Key == "msg" {
				if _, fixed := fixedLogMessages[attr.Value.String()]; !fixed {
					value = "[omitted]"
				}
			}
			target[attr.Key] = value
		}
	case slog.KindInt64:
		if allowNumber {
			target[attr.Key] = attr.Value.Int64()
		}
	case slog.KindUint64:
		if allowNumber {
			target[attr.Key] = attr.Value.Uint64()
		}
	case slog.KindFloat64:
		if allowNumber {
			target[attr.Key] = attr.Value.Float64()
		}
	case slog.KindDuration:
		if allowNumber {
			target[attr.Key] = attr.Value.Duration().Milliseconds()
		}
	case slog.KindAny:
		// 用量是可空整数指针；仅解引用明确的数值类型，不执行对象方法。
		if allowNumber {
			switch value := attr.Value.Any().(type) {
			case *int:
				if value != nil {
					target[attr.Key] = *value
				}
			case *int64:
				if value != nil {
					target[attr.Key] = *value
				}
			}
		}
	}
}
