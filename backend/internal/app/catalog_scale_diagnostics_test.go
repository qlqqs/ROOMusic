package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// 此取证器仅用于隔离合成库；参数只记录类型和数组长度，不记录值、Cookie或请求体。
// 逐语句与100ms采样仅在显式诊断模式的在线回填阶段开启；正式p95之前停止。
type scaleDiagnosticKey struct{}
type scaleDiagnosticRequestKey struct{}
type scaleDiagnosticActorKey struct{}
type scaleDiagnosticQuery struct {
	ID               uint64                    `json:"query_id"`
	Actor            string                    `json:"actor"`
	Arguments        []scaleDiagnosticArgument `json:"arguments"`
	ElapsedMS        float64                   `json:"elapsed_ms,omitempty"`
	PID              uint32                    `json:"pid"`
	SQLID            string                    `json:"sql_id"`
	TxID             uint64                    `json:"transaction_id,omitempty"`
	RequestID        string                    `json:"request_id,omitempty"`
	Started          time.Time                 `json:"started_at"`
	Command          string                    `json:"command"`
	CATP             bool                      `json:"catp"`
	ApplicationPhase string                    `json:"application_phase,omitempty"`
}
type scaleDiagnosticTx struct {
	ID               uint64     `json:"transaction_id"`
	Actor            string     `json:"actor"`
	RequestID        string     `json:"request_id,omitempty"`
	ElapsedMS        float64    `json:"elapsed_ms"`
	CATPWaitMS       float64    `json:"catp_wait_ms"`
	CATPHoldMS       float64    `json:"catp_hold_ms"`
	PID              uint32     `json:"pid"`
	Started          time.Time  `json:"started_at"`
	Phase            string     `json:"phase"`
	Inferred         bool       `json:"inferred_without_begin"`
	CATPRequested    *time.Time `json:"catp_requested_at,omitempty"`
	CATPAcquired     *time.Time `json:"catp_acquired_at,omitempty"`
	CATPWaiting      bool       `json:"catp_waiting"`
	ApplicationPhase string     `json:"application_phase,omitempty"`
}
type scaleDiagnosticStat struct {
	Count   int64   `json:"count"`
	TotalMS float64 `json:"total_ms"`
	MaxMS   float64 `json:"max_ms"`
	Errors  int64   `json:"errors"`
}
type scaleDiagnosticHTTP struct {
	RequestID    string  `json:"request_id"`
	Method       string  `json:"method"`
	Kind         string  `json:"kind"`
	Route        string  `json:"route,omitempty"`
	Status       int     `json:"status"`
	ElapsedMS    float64 `json:"elapsed_ms"`
	DatabaseMS   float64 `json:"database_ms"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	ContextError string  `json:"context_error,omitempty"`
}
type scaleDiagnosticSnapshot struct {
	UnconfirmedApplicationBatches []scaleCommittedGate           `json:"unconfirmed_application_batches"`
	AcceptanceFailure             string                         `json:"acceptance_failure,omitempty"`
	Recording                     bool                           `json:"recording"`
	At                            time.Time                      `json:"at"`
	QueriesStarted                uint64                         `json:"queries_started"`
	QueriesEnded                  uint64                         `json:"queries_ended"`
	ActiveQueries                 []scaleDiagnosticQuery         `json:"active_queries"`
	ActiveTransactions            []scaleDiagnosticTx            `json:"active_transactions"`
	Commands                      map[string]scaleDiagnosticStat `json:"command_latency"`
	CATPWait                      scaleDiagnosticStat            `json:"catp_acquire_wait"`
	CATPHold                      scaleDiagnosticStat            `json:"catp_hold_until_transaction_end"`
	TransactionLifetime           scaleDiagnosticStat            `json:"transaction_lifetime"`
	HTTP                          []scaleDiagnosticHTTP          `json:"http"`
	Samples                       int64                          `json:"samples"`
	SampleErrors                  int64                          `json:"sample_errors"`
	LastSampleAt                  *time.Time                     `json:"last_sample_at,omitempty"`
	WriteError                    string                         `json:"write_error,omitempty"`
}
type scaleDiagnosticStream struct {
	file    *os.File
	buffer  *bufio.Writer
	encoder *json.Encoder
}
type scaleDiagnostics struct {
	mu                    sync.Mutex
	recording             atomic.Bool
	detailed              bool
	pendingBatches        map[uint64]scaleCommittedGate
	acceptanceFailure     string
	onAcceptanceFailure   func(string)
	batchHolds            []float64
	maxBatch              *scaleCommittedGate
	batchHoldsByPhase     map[string][]float64
	stopOnce              sync.Once
	closeOnce             sync.Once
	closeErr              error
	stopErr               error
	closed                bool
	output                string
	streams               map[string]*scaleDiagnosticStream
	sqlSeen               map[string]bool
	active                map[uint64]scaleDiagnosticQuery
	transactions          map[uint32]*scaleDiagnosticTx
	commands              map[string]scaleDiagnosticStat
	http                  []scaleDiagnosticHTTP
	next, ended           uint64
	nextTx                uint64
	wait, hold, lifetime  scaleDiagnosticStat
	samples, sampleErrors int64
	lastSample            *time.Time
	writeErr              error
	monitor               *sql.DB
	cancel                context.CancelFunc
	done                  chan struct{}
}

func newScaleDiagnostics(output string) (*scaleDiagnostics, error) {
	d := &scaleDiagnostics{pendingBatches: map[uint64]scaleCommittedGate{}, detailed: true, batchHoldsByPhase: map[string][]float64{}, output: output, streams: map[string]*scaleDiagnosticStream{}, sqlSeen: map[string]bool{}, active: map[uint64]scaleDiagnosticQuery{}, transactions: map[uint32]*scaleDiagnosticTx{}, commands: map[string]scaleDiagnosticStat{}, http: []scaleDiagnosticHTTP{}}
	d.recording.Store(true)
	for _, name := range []string{"queries", "sql", "transactions", "activity", "http"} {
		file, err := os.OpenFile(filepath.Join(output, "diagnostic-"+name+".jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			_ = d.Close()
			return nil, err
		}
		buffer := bufio.NewWriterSize(file, 256*1024)
		d.streams[name] = &scaleDiagnosticStream{file: file, buffer: buffer, encoder: json.NewEncoder(buffer)}
	}
	return d, nil
}
func scaleSQLID(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}
func scaleDiagnosticCommand(query string) string {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		return "unknown"
	}
	if fields[0] == "rollback" && len(fields) > 1 && fields[1] == "to" {
		return "rollback_to_savepoint"
	}
	return fields[0]
}

// pgx执行选项不是绑定参数；字符串、字节串、数组元素一律不展开。
type scaleDiagnosticArgument struct {
	Position int    `json:"position"`
	Type     string `json:"type"`
	Elements *int   `json:"elements,omitempty"`
}

func scaleDiagnosticArguments(args []any) []scaleDiagnosticArgument {
	result := make([]scaleDiagnosticArgument, 0, len(args))
	for _, arg := range args {
		switch arg.(type) {
		case pgx.QueryResultFormatsByOID, pgx.QueryResultFormats, pgx.QueryExecMode:
			continue
		}
		item := scaleDiagnosticArgument{Position: len(result) + 1, Type: fmt.Sprintf("%T", arg)}
		value := reflect.ValueOf(arg)
		if value.IsValid() && (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) {
			count := value.Len()
			item.Elements = &count
		}
		result = append(result, item)
	}
	return result
}
func scaleDiagnosticContext(ctx context.Context, actor, requestID string) context.Context {
	ctx = context.WithValue(ctx, scaleDiagnosticActorKey{}, actor)
	return context.WithValue(ctx, scaleDiagnosticRequestKey{}, requestID)
}
func (d *scaleDiagnostics) enabled() bool { return d != nil && d.recording.Load() }

func scaleCATPQuery(query string, args []any) bool {
	if !strings.Contains(strings.ToLower(query), "pg_advisory_xact_lock") {
		return false
	}
	for _, arg := range args {
		switch value := arg.(type) {
		case int:
			if value == 0x43415450 {
				return true
			}
		case int64:
			if value == 0x43415450 {
				return true
			}
		case int32:
			if value == 0x43415450 {
				return true
			}
		case uint32:
			if value == 0x43415450 {
				return true
			}
		}
	}
	return false
}

var scaleDiagnosticURL = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s]+`)

func scaleDiagnosticError(err error) string {
	if err == nil {
		return ""
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return fmt.Sprintf("SQLSTATE %s: %s (constraint=%s)", pgerr.Code, pgerr.Message, pgerr.ConstraintName)
	}
	return scaleDiagnosticURL.ReplaceAllString(err.Error(), "[redacted database URL]")
}
func (d *scaleDiagnostics) emitLocked(stream string, value any) {
	if d.writeErr != nil || d.closed || (!d.detailed && stream != "transactions") {
		return
	}
	if err := d.streams[stream].encoder.Encode(value); err != nil {
		d.writeErr = err
	}
}
func (d *scaleDiagnostics) flushLocked() {
	for _, stream := range d.streams {
		if err := stream.buffer.Flush(); err != nil && d.writeErr == nil {
			d.writeErr = err
		}
	}
}
func scaleDiagnosticAdd(stat *scaleDiagnosticStat, elapsed time.Duration, err error) {
	ms := float64(elapsed) / float64(time.Millisecond)
	stat.Count++
	stat.TotalMS += ms
	stat.MaxMS = max(stat.MaxMS, ms)
	if err != nil {
		stat.Errors++
	}
}
func (d *scaleDiagnostics) beginQuery(ctx context.Context, pid uint32, status byte, query string, args []any) context.Context {
	if !d.enabled() {
		return ctx
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.recording.Load() || d.closed {
		return ctx
	}
	now := time.Now().UTC()
	actor, _ := ctx.Value(scaleDiagnosticActorKey{}).(string)
	requestID, _ := ctx.Value(scaleDiagnosticRequestKey{}).(string)
	id := ""
	if d.detailed {
		id = scaleSQLID(query)
	}
	command := scaleDiagnosticCommand(query)
	if d.detailed && !d.sqlSeen[id] {
		d.sqlSeen[id] = true
		d.emitLocked("sql", map[string]any{"sql_id": id, "sql": query})
	}
	tx := d.transactions[pid]
	if tx == nil && (command == "begin" || status == 'T' || status == 'E') {
		d.nextTx++
		tx = &scaleDiagnosticTx{ID: d.nextTx, PID: pid, Started: now, Phase: "active", Inferred: command != "begin", Actor: actor, RequestID: requestID}
		d.transactions[pid] = tx
		if command == "begin" {
			tx.Phase = "beginning"
		}
		d.emitLocked("transactions", map[string]any{"event": "transaction_start", "at": now, "transaction": tx})
	}
	d.next++
	if tx != nil {
		if actor == "" {
			actor = tx.Actor
		}
		if requestID == "" {
			requestID = tx.RequestID
		}
	}
	applicationPhase := ""
	if command == "update" {
		applicationPhase = scaleApplicationPhase(query, args)
	}
	q := scaleDiagnosticQuery{ID: d.next, PID: pid, SQLID: id, RequestID: requestID, Started: now, Command: command, CATP: scaleCATPQuery(query, args), Actor: actor, Arguments: nil, ApplicationPhase: applicationPhase}
	if d.detailed {
		q.Arguments = scaleDiagnosticArguments(args)
	}
	if tx != nil {
		q.TxID = tx.ID
		if command == "commit" {
			tx.Phase = "committing"
		} else if command == "rollback" {
			tx.Phase = "rolling_back"
		}
		if q.CATP {
			at := now
			tx.CATPRequested = &at
			tx.CATPWaiting = true
		}
	}
	d.active[q.ID] = q
	d.emitLocked("queries", map[string]any{"event": "query_start", "at": now, "query": q, "pg_tx_status": string(status)})
	return context.WithValue(ctx, scaleDiagnosticKey{}, q)
}
func (d *scaleDiagnostics) endQuery(ctx context.Context, status byte, tag string, err error) {
	if d == nil {
		return
	}
	q, ok := ctx.Value(scaleDiagnosticKey{}).(scaleDiagnosticQuery)
	if !ok {
		return
	}
	d.mu.Lock()
	var acceptanceErr error
	defer func() {
		if acceptanceErr != nil && d.acceptanceFailure == "" {
			d.acceptanceFailure = acceptanceErr.Error()
		}
		callback := d.onAcceptanceFailure
		d.mu.Unlock()
		// 回调会保存快照，必须先释放diagnostics锁。
		if acceptanceErr != nil && callback != nil {
			callback(acceptanceErr.Error())
		}
	}()
	now := time.Now().UTC()
	if d.closed {
		return
	}
	elapsed := now.Sub(q.Started)
	delete(d.active, q.ID)
	d.ended++
	command := d.commands[q.Command]
	scaleDiagnosticAdd(&command, elapsed, err)
	d.commands[q.Command] = command
	d.emitLocked("queries", map[string]any{"event": "query_end", "at": now, "query": q, "elapsed_ms": float64(elapsed) / float64(time.Millisecond), "error": scaleDiagnosticError(err), "command_tag": tag, "pg_tx_status": string(status)})
	tx := d.transactions[q.PID]
	if q.CATP {
		scaleDiagnosticAdd(&d.wait, elapsed, err)
		if tx != nil {
			tx.CATPWaiting = false
			if err == nil && tx.CATPAcquired == nil {
				at := now
				tx.CATPAcquired = &at
			}
		}
		d.emitLocked("transactions", map[string]any{"event": "catp_acquire_end", "at": now, "pid": q.PID, "transaction_id": q.TxID, "query_id": q.ID, "wait_ms": float64(elapsed) / float64(time.Millisecond), "acquired": err == nil, "error": scaleDiagnosticError(err)})
	}
	if tx != nil {
		if q.ApplicationPhase != "" && err == nil && pgconn.NewCommandTag(tag).RowsAffected() > 0 {
			if tx.ApplicationPhase != "" && tx.ApplicationPhase != q.ApplicationPhase {
				acceptanceErr = fmt.Errorf("同一应用批的phase写入不一致")
			}
			tx.ApplicationPhase = q.ApplicationPhase
		}
		if q.Command == "begin" && err == nil {
			tx.Phase = "active"
		}
		// 错误且仍处于T/E状态不能假称事务结束；未结束者留在周期快照与失败前快照中。
		if status == 'I' || ((q.Command == "commit" || q.Command == "rollback") && err == nil) {
			lifetime := now.Sub(tx.Started)
			scaleDiagnosticAdd(&d.lifetime, lifetime, err)
			var heldMS float64
			if tx.CATPAcquired != nil {
				held := now.Sub(*tx.CATPAcquired)
				heldMS = float64(held) / float64(time.Millisecond)
				scaleDiagnosticAdd(&d.hold, held, err)
			}
			// 仅明确COMMIT且实际推进了work/phase的maintenance事务计入；
			// 发布、markFailed、清单发现以及回滚不能删除未确认应用提交。
			if tx.Actor == "maintenance" && tx.ApplicationPhase != "" && q.Command == "commit" && tag == "COMMIT" && err == nil {
				if tx.CATPAcquired == nil {
					acceptanceErr = fmt.Errorf("应用提交transaction=%d缺少CATP证据", tx.ID)
				} else {
					if commitErr := d.recordApplicationCommitLocked(scaleCommittedGate{TransactionID: tx.ID, HoldMS: heldMS, Phase: tx.ApplicationPhase, PID: tx.PID, AcquiredAt: *tx.CATPAcquired, CommittedAt: now, CommitMS: float64(elapsed) / float64(time.Millisecond), BeforeCommitMS: max(0, heldMS-float64(elapsed)/float64(time.Millisecond))}); commitErr != nil {
						acceptanceErr = commitErr
					}
				}
			}
			d.emitLocked("transactions", map[string]any{"event": "transaction_end", "at": now, "transaction": tx, "command": q.Command, "command_ms": float64(elapsed) / float64(time.Millisecond), "lifetime_ms": float64(lifetime) / float64(time.Millisecond), "catp_hold_ms": heldMS, "error": scaleDiagnosticError(err)})
			delete(d.transactions, q.PID)
		}
	}
	if err != nil {
		d.flushLocked()
	}
}
func (d *scaleDiagnostics) snapshotLocked() scaleDiagnosticSnapshot {
	result := scaleDiagnosticSnapshot{UnconfirmedApplicationBatches: d.unconfirmedLocked(), AcceptanceFailure: d.acceptanceFailure, Recording: d.recording.Load(), At: time.Now().UTC(), QueriesStarted: d.next, QueriesEnded: d.ended, ActiveQueries: []scaleDiagnosticQuery{}, ActiveTransactions: []scaleDiagnosticTx{}, Commands: map[string]scaleDiagnosticStat{}, CATPWait: d.wait, CATPHold: d.hold, TransactionLifetime: d.lifetime, HTTP: append([]scaleDiagnosticHTTP{}, d.http...), Samples: d.samples, SampleErrors: d.sampleErrors, LastSampleAt: d.lastSample, WriteError: scaleDiagnosticError(d.writeErr)}
	for _, q := range d.active {
		q.ElapsedMS = float64(result.At.Sub(q.Started)) / float64(time.Millisecond)
		result.ActiveQueries = append(result.ActiveQueries, q)
	}
	for _, tx := range d.transactions {
		item := *tx
		item.ElapsedMS = float64(result.At.Sub(tx.Started)) / float64(time.Millisecond)
		if tx.CATPWaiting && tx.CATPRequested != nil {
			item.CATPWaitMS = float64(result.At.Sub(*tx.CATPRequested)) / float64(time.Millisecond)
		}
		if tx.CATPAcquired != nil {
			item.CATPHoldMS = float64(result.At.Sub(*tx.CATPAcquired)) / float64(time.Millisecond)
		}
		result.ActiveTransactions = append(result.ActiveTransactions, item)
	}
	for name, stat := range d.commands {
		result.Commands[name] = stat
	}
	return result
}
func (d *scaleDiagnostics) checkpoint(name string) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.flushLocked()
	data, err := json.MarshalIndent(d.snapshotLocked(), "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(d.output, name+".json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return d.writeErr
}

const scaleActivitySQL = `WITH scoped AS MATERIALIZED (
 SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1
), participants AS MATERIALIZED (
 SELECT pid FROM scoped UNION SELECT unnest(pg_blocking_pids(pid)) FROM scoped
) SELECT jsonb_build_object('sampled_at',clock_timestamp(),
 'activity',COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'pid',a.pid,'application_name',a.application_name,'backend_type',a.backend_type,'state',a.state,
 'wait_event_type',a.wait_event_type,'wait_event',a.wait_event,'query',a.query,
 'xact_start',a.xact_start,'query_start',a.query_start,'state_change',a.state_change,'backend_start',a.backend_start,
 'backend_xid',a.backend_xid::text,'backend_xmin',a.backend_xmin::text,'blocking_pids',pg_blocking_pids(a.pid)))
 FROM pg_stat_activity a WHERE a.pid IN(SELECT pid FROM participants)),'[]'::jsonb),
 'locks',COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'pid',l.pid,'locktype',l.locktype,'mode',l.mode,'granted',l.granted,'waitstart',l.waitstart,
 'virtualtransaction',l.virtualtransaction,'database',l.database,'relation_oid',l.relation,'page',l.page,'tuple',l.tuple,
 'virtualxid',l.virtualxid,'transactionid',l.transactionid::text,'classid',l.classid,'objid',l.objid,'objsubid',l.objsubid,'fastpath',l.fastpath))
 FROM pg_locks l WHERE l.pid IN(SELECT pid FROM participants)),'[]'::jsonb))`

func (d *scaleDiagnostics) startSampler(ctx context.Context, scopedURL, applicationName string) error {
	if d == nil {
		return nil
	}
	config, err := pgx.ParseConfig(scopedURL)
	if err != nil {
		return err
	}
	config.RuntimeParams["application_name"] = applicationName + "-monitor"
	// 独立pool和scoped search_path；不占用被测12连接池，不给自己套tracer，不改任何PG设置。
	d.monitor = stdlib.OpenDB(*config)
	d.monitor.SetMaxOpenConns(1)
	d.monitor.SetMaxIdleConns(1)
	sampleCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	go func() {
		defer close(d.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		count := 0
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
			}
			var raw json.RawMessage
			started := time.Now()
			err := d.monitor.QueryRowContext(sampleCtx, scaleActivitySQL, applicationName).Scan(&raw)
			if sampleCtx.Err() != nil {
				return
			}
			d.mu.Lock()
			if err != nil {
				d.sampleErrors++
				d.emitLocked("activity", map[string]any{"event": "sample_error", "at": time.Now().UTC(), "error": scaleDiagnosticError(err)})
			} else {
				now := time.Now().UTC()
				d.lastSample = &now
				d.samples++
				d.emitLocked("activity", map[string]any{"event": "sample", "elapsed_ms": float64(time.Since(started)) / float64(time.Millisecond), "database": raw, "in_flight": d.snapshotLocked().ActiveQueries})
			}
			d.flushLocked()
			d.mu.Unlock()
			count++
			if count%10 == 0 {
				if err := d.checkpoint("diagnostics-summary"); err != nil {
					d.mu.Lock()
					if d.writeErr == nil {
						d.writeErr = err
					}
					d.mu.Unlock()
				}
			}
		}
	}()
	return nil
}
func scaleHTTPKind(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	switch {
	case method == "POST" && path == "/api/v1/scans":
		return "scan_start"
	case method == "PATCH" && strings.HasSuffix(path, "/corrections"):
		return "correction"
	case strings.Contains(path, "/audio"):
		return "audio"
	case path == "/readyz":
		return "ready"
	case path == "/api/v1/search/suggestions":
		return "suggestions"
	case path == "/api/v1/search":
		return "search"
	default:
		return "other"
	}
}

// 仅使用已知模板，不输出原始URL、查询词或资源ID。
func scaleDiagnosticRoute(method, path string) string {
	switch scaleHTTPKind(method, path) {
	case "scan_start":
		return "POST /api/v1/scans"
	case "correction":
		return "PATCH /api/v1/releases/{id}/corrections"
	case "audio":
		return "GET /api/v1/tracks/{id}/audio"
	case "ready":
		return "GET /readyz"
	case "suggestions":
		return "GET /api/v1/search/suggestions"
	case "search":
		return "GET /api/v1/search"
	default:
		return "<other>"
	}
}
func (d *scaleDiagnostics) httpStart(id, method, path string) {
	if !d.enabled() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.emitLocked("http", map[string]any{"event": "http_start", "at": time.Now().UTC(), "request_id": id, "method": method, "kind": scaleHTTPKind(method, path)})
}
func (d *scaleDiagnostics) httpEnd(ctx context.Context, id, method, path, route string, response *httptest.ResponseRecorder, elapsed, dbMS float64) {
	if !d.enabled() {
		return
	}
	item := scaleDiagnosticHTTP{RequestID: id, Method: method, Kind: scaleHTTPKind(method, path), Route: route, Status: response.Code, ElapsedMS: elapsed, DatabaseMS: dbMS, ContextError: scaleDiagnosticError(ctx.Err())}
	if response.Code >= 400 {
		var envelope struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(response.Body.Bytes(), &envelope) == nil {
			item.ErrorCode, item.ErrorMessage = envelope.Error.Code, envelope.Error.Message
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.http = append(d.http, item)
	d.emitLocked("http", map[string]any{"event": "http_end", "at": time.Now().UTC(), "request": item})
	d.flushLocked()
}

// 必须先等采样goroutine退出并关闭独立连接，再进入ANALYZE/正式计时。
// 失败路径先checkpoint，再取消应用，避免只剩清理后的回滚信息。
func (d *scaleDiagnostics) stopOnline() error {
	if d == nil {
		return nil
	}
	d.stopOnce.Do(func() {
		if d.cancel != nil {
			d.cancel()
			<-d.done
		}
		if d.monitor != nil {
			if err := d.monitor.Close(); err != nil {
				d.mu.Lock()
				if d.writeErr == nil {
					d.writeErr = err
				}
				d.mu.Unlock()
			}
		}
		d.recording.Store(false)
		d.stopErr = d.checkpoint("diagnostics-online-end")
	})
	return d.stopErr
}
func (d *scaleDiagnostics) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		d.closeErr = d.stopOnline()
		if err := d.checkpoint("diagnostics-summary"); err != nil && d.closeErr == nil {
			d.closeErr = err
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.closed = true
		for _, stream := range d.streams {
			if err := stream.buffer.Flush(); err != nil && d.writeErr == nil {
				d.writeErr = err
			}
			if err := stream.file.Close(); err != nil && d.writeErr == nil {
				d.writeErr = err
			}
		}
		if d.closeErr == nil {
			d.closeErr = d.writeErr
		}
	})
	return d.closeErr
}

func TestScaleDiagnosticUnfinishedTransactions(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	start := func(ctx context.Context, pid uint32, status byte, sql string, args ...any) context.Context {
		return d.beginQuery(ctx, pid, status, sql, args)
	}
	scan := scaleDiagnosticContext(context.Background(), "scan_background", "scale-scan-probe")
	worker := scaleDiagnosticContext(context.Background(), "maintenance", "")
	correction := scaleDiagnosticContext(context.Background(), "http:correction", "scale-http-m3")
	d.endQuery(start(scan, 11, 'I', "begin"), 'T', "BEGIN", nil)
	d.endQuery(start(scan, 11, 'T', "SELECT pg_advisory_xact_lock($1)", int64(0x43415450)), 'T', "SELECT 1", nil)
	ids := make([]string, 20000)
	for i := range ids {
		ids[i] = "private-artist-name"
	}
	profiles := start(scan, 11, 'T', "SELECT * FROM profiles WHERE id=ANY($1)", ids)
	d.endQuery(start(worker, 22, 'I', "begin"), 'T', "BEGIN", nil)
	_ = start(worker, 22, 'T', "SELECT pg_advisory_xact_lock($1)", int64(0x43415450))
	d.endQuery(start(correction, 33, 'I', "begin"), 'T', "BEGIN", nil)
	waitingM3 := start(correction, 33, 'T', "SELECT pg_advisory_xact_lock($1)", int64(0x43415450))
	d.endQuery(waitingM3, 'E', "", context.DeadlineExceeded)
	if err := d.checkpoint("before-cleanup"); err != nil {
		t.Fatal(err)
	}
	var snapshot scaleDiagnosticSnapshot
	raw, err := os.ReadFile(filepath.Join(d.output, "before-cleanup.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.QueriesStarted != 7 || snapshot.QueriesEnded != 5 || len(snapshot.ActiveQueries) != 2 || len(snapshot.ActiveTransactions) != 3 {
		t.Fatalf("未结束现场错误: %+v", snapshot)
	}
	byPID := map[uint32]scaleDiagnosticTx{}
	for _, tx := range snapshot.ActiveTransactions {
		byPID[tx.PID] = tx
	}
	if tx := byPID[11]; tx.Actor != "scan_background" || tx.RequestID != "scale-scan-probe" || tx.CATPAcquired == nil || tx.CATPHoldMS <= 0 || tx.ElapsedMS <= 0 {
		t.Fatalf("scan持有态丢失: %+v", tx)
	}
	if tx := byPID[22]; tx.Actor != "maintenance" || !tx.CATPWaiting || tx.CATPWaitMS <= 0 || tx.CATPAcquired != nil {
		t.Fatalf("worker等待态丢失: %+v", tx)
	}
	if tx := byPID[33]; tx.Actor != "http:correction" || tx.CATPWaiting || tx.CATPAcquired != nil {
		t.Fatalf("M3失败误判持有: %+v", tx)
	}
	if snapshot.CATPWait.Count != 2 || snapshot.CATPWait.Errors != 1 {
		t.Fatalf("等待统计错误: %+v", snapshot.CATPWait)
	}
	found := false
	for _, q := range snapshot.ActiveQueries {
		if q.PID == 11 {
			found = len(q.Arguments) == 1 && q.Arguments[0].Elements != nil && *q.Arguments[0].Elements == 20000
		}
	}
	if !found || bytesContain(raw, "private-artist-name") {
		t.Fatal("profiles参数基数或脱敏失败")
	}
	// 在COMMIT开始、尚未结束时再次取证；没有普通请求计时ctx也必须记录它。
	d.endQuery(profiles, 'T', "SELECT 20000", nil)
	commit := start(context.Background(), 11, 'T', "commit")
	if err := d.checkpoint("pending-commit"); err != nil {
		t.Fatal(err)
	}
	pending, err := os.ReadFile(filepath.Join(d.output, "pending-commit.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContain(pending, "committing") || !bytesContain(pending, "scan_background") {
		t.Fatal("未结束COMMIT现场丢失")
	}
	d.endQuery(commit, 'I', "COMMIT", nil)
	d.mu.Lock()
	after := d.snapshotLocked()
	d.mu.Unlock()
	if after.CATPHold.Count != 1 || after.TransactionLifetime.Count != 1 || after.Commands["commit"].Count != 1 {
		t.Fatalf("commit/持有时长未记录: %+v", after)
	}
	// 后续清理不应覆盖先前的失败快照。
	saved, err := os.ReadFile(filepath.Join(d.output, "before-cleanup.json"))
	if err != nil || string(saved) != string(raw) {
		t.Fatal("清理覆盖取证")
	}
}

func bytesContain(data []byte, value string) bool { return strings.Contains(string(data), value) }

func TestScaleDiagnosticArgumentsAndErrors(t *testing.T) {
	args := scaleDiagnosticArguments([]any{pgx.QueryExecModeExec, pgx.QueryResultFormats{0}, "secret-session-token", []string{"secret-a", "secret-b"}, []byte("secret-hash"), nil, []string(nil), [3]int{1, 2, 3}})
	if len(args) != 6 || args[1].Position != 2 || *args[1].Elements != 2 || *args[4].Elements != 0 || *args[5].Elements != 3 {
		t.Fatalf("参数元信息错误: %+v", args)
	}
	encoded, err := json.Marshal(args)
	if err != nil || bytesContain(encoded, "secret-") {
		t.Fatal("参数值泄漏")
	}
	if !scaleCATPQuery("SELECT pg_advisory_xact_lock($1)", []any{int32(0x43415450)}) || scaleCATPQuery("SELECT pg_advisory_xact_lock($1)", []any{int32(0x4341544d)}) {
		t.Fatal("CATP与CATM混淆")
	}
	if scaleDiagnosticCommand("ROLLBACK TO SAVEPOINT probe") != "rollback_to_savepoint" {
		t.Fatal("savepoint误判事务结束")
	}
	errText := scaleDiagnosticError(fmt.Errorf("connection failed postgres://user:secret@host/db"))
	if strings.Contains(errText, "secret") {
		t.Fatal("数据库URL泄漏")
	}
	errText = scaleDiagnosticError(&pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout", Detail: "secret-token"})
	if !strings.Contains(errText, "57014") || strings.Contains(errText, "secret-token") {
		t.Fatalf("SQL错误脱敏错误: %s", errText)
	}
}

func TestScaleDiagnosticStopBeforeMeasurement(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := scaleDiagnosticContext(context.Background(), "http:correction", "scale-http-m3")
	pending := d.beginQuery(ctx, 44, 'I', "BEGIN", nil)
	// 用受控goroutine证明stopOnline等待采样退出，而不是只发取消信号。
	sampleCtx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan struct{})
	go func() { <-sampleCtx.Done(); close(d.done) }()
	if err := d.stopOnline(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.done:
	default:
		t.Fatal("采样仍在运行")
	}
	if d.enabled() {
		t.Fatal("正式测量仍开启详细trace")
	}
	fresh := context.Background()
	if got := d.beginQuery(fresh, 55, 'I', "SELECT $1", []any{"secret"}); got != fresh {
		t.Fatal("正式测量新增详细trace")
	}
	// 原有在途语句的结束仍可记入最终summary，在线边界快照保持原样。
	d.endQuery(pending, 'T', "BEGIN", nil)
	original, err := os.ReadFile(filepath.Join(d.output, "diagnostics-online-end.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.stopOnline(); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(d.output, "diagnostics-online-end.json"))
	if err != nil || string(original) != string(again) {
		t.Fatal("重复停止覆盖了在线结束现场")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal("关闭不幂等:", err)
	}
}

func TestScaleDiagnosticHTTPFailure(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := "/api/v1/releases/private-id/corrections"
	d.httpStart("m3", "PATCH", path)
	response := httptest.NewRecorder()
	response.WriteHeader(503)
	_, _ = response.WriteString(`{"error":{"code":"database_unavailable","message":"Database unavailable"}}`)
	d.httpEnd(ctx, "m3", "PATCH", path, scaleDiagnosticRoute("PATCH", path), response, 45000, 44900)
	if err := d.checkpoint("http-failure"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(d.output, "diagnostic-http.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"http_start", "http_end", "database_unavailable", "45000", "context canceled", "/api/v1/releases/{id}/corrections"} {
		if !bytesContain(raw, expected) {
			t.Fatalf("缺少HTTP取证字段%s: %s", expected, raw)
		}
	}
	if bytesContain(raw, "private-id") {
		t.Fatal("泄漏原始路径")
	}
	if scaleHTTPKind("GET", "/api/v1/search/suggestions?q=secret") != "suggestions" || strings.Contains(scaleDiagnosticRoute("GET", "/api/v1/search/suggestions?q=secret"), "secret") {
		t.Fatal("查询词泄漏")
	}
}

func (d *scaleDiagnostics) onlineAcceptance(load scaleOnlineLoadReport, complete bool) scaleOnlineAcceptanceReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	return scaleEvaluateOnline(d.http, load.Cycles, complete)
}
