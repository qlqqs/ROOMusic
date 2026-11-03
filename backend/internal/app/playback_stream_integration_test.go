package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

// lockedBuffer 是并发安全的日志缓冲：处理请求的 goroutine 写入，测试在它结束之后读取。
type lockedBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(data []byte) (int, error) {
	locked.mutex.Lock()
	defer locked.mutex.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedBuffer) String() string {
	locked.mutex.Lock()
	defer locked.mutex.Unlock()
	return locked.buffer.String()
}

// logEvents 返回日志里所有 event 字段等于 name 的记录。
func logEvents(t *testing.T, logs *lockedBuffer, name string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		if record["event"] == name {
			events = append(events, record)
		}
	}
	return events
}

// blockingResponseWriter 在第一块音频写出后挂起，测试通过通道确定传输已建立再取消生命周期。
// forward 非空时交给真实连接并刷新；ctx 模拟真实客户端断开时 Write 返回错误。
type blockingResponseWriter struct {
	header     http.Header
	status     int
	firstWrite chan struct{}
	release    chan struct{}
	once       sync.Once
	released   sync.Once
	body       bytes.Buffer
	forward    http.ResponseWriter
	ctx        context.Context
}

func newBlockingResponseWriter(ctx context.Context) *blockingResponseWriter {
	return &blockingResponseWriter{header: http.Header{}, firstWrite: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
}

func (writer *blockingResponseWriter) Header() http.Header {
	if writer.forward != nil {
		return writer.forward.Header()
	}
	return writer.header
}

func (writer *blockingResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
		if writer.forward != nil {
			writer.forward.WriteHeader(status)
		}
	}
}

func (writer *blockingResponseWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := writer.body.Write(data)
	if writer.forward != nil {
		n, err = writer.forward.Write(data)
		if err == nil {
			err = http.NewResponseController(writer.forward).Flush()
		}
	}
	writer.once.Do(func() { close(writer.firstWrite) })
	<-writer.release
	if disconnected := writer.ctx.Err(); disconnected != nil {
		return n, disconnected
	}
	return n, err
}

func (writer *blockingResponseWriter) unblock() {
	writer.released.Do(func() { close(writer.release) })
}

// bytesWritten 仅在 handler 结束后读取，避免与写出并发。
func (writer *blockingResponseWriter) bytesWritten() int { return writer.body.Len() }

// startStream 在后台处理一个音频请求，返回挂起的写出端和处理结束的信号。
func startStream(t *testing.T, application *integrationTestApplication, user *integrationTestUser, trackID string, requestContext context.Context) (*blockingResponseWriter, <-chan struct{}) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, audioPath(trackID), nil).WithContext(requestContext)
	request.Host = "roomusic.test"
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	writer := newBlockingResponseWriter(requestContext)
	done := make(chan struct{})
	t.Cleanup(func() { finishStream(t, writer, done) })
	go func() {
		defer close(done)
		application.handler.ServeHTTP(writer, request)
	}()
	select {
	case <-writer.firstWrite:
	case <-time.After(10 * time.Second):
		t.Fatal("the server never started writing audio")
	}
	return writer, done
}

func finishStream(t *testing.T, writer *blockingResponseWriter, done <-chan struct{}) {
	t.Helper()
	writer.unblock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream handler did not return after the writer was released")
	}
}

const streamFileSize = 1 << 20

func TestPostgreSQLPlaybackStreamIsAbortedOnShutdown(t *testing.T) {
	logs := &lockedBuffer{}
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)))
	library := createPlaybackLibrary(t, application)
	content := flacBytes(streamFileSize)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: content})
	user := application.createUser(t, "user")

	writer, done := startStream(t, application, &user, trackID, context.Background())
	application.shutdown()
	finishStream(t, writer, done)

	if written := writer.bytesWritten(); written == 0 || written >= streamFileSize {
		t.Fatalf("delivered %d of %d bytes; want some audio, then a cut before the end", written, streamFileSize)
	}
	if writer.status != http.StatusOK || !bytes.Equal(writer.body.Bytes(), content[:writer.bytesWritten()]) {
		t.Fatalf("shutdown changed the established response or appended a non-audio body: HTTP %d", writer.status)
	}
	requireShutdownEvent(t, logs, trackID)
	for _, secret := range []string{application.allowedRoot, "playback-library", "long.flac", user.token} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leak %q: %s", secret, logs.String())
		}
	}
}

func requireShutdownEvent(t *testing.T, logs *lockedBuffer, trackID string) {
	t.Helper()
	aborted := logEvents(t, logs, "playback.stream.aborted")
	if len(aborted) != 1 {
		t.Fatalf("playback.stream.aborted events = %d, want 1: %s", len(aborted), logs.String())
	}
	event := aborted[0]
	if event["reason"] != "shutdown" || event["track_id"] != trackID || event["level"] != "INFO" {
		t.Fatalf("abort event = %v, want shutdown, track %s, INFO", event, trackID)
	}
	if request, _ := event["request_id"].(string); request == "" {
		t.Fatalf("abort event has no request_id: %v", event)
	}
}

func TestPostgreSQLPlaybackStreamCompletesWithoutShutdown(t *testing.T) {
	logs := &lockedBuffer{}
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: flacBytes(streamFileSize)})
	user := application.createUser(t, "user")

	writer, done := startStream(t, application, &user, trackID, context.Background())
	finishStream(t, writer, done)

	if written := writer.bytesWritten(); written != streamFileSize {
		t.Fatalf("delivered %d of %d bytes without shutdown", written, streamFileSize)
	}
	if aborted := logEvents(t, logs, "playback.stream.aborted"); len(aborted) != 0 {
		t.Fatalf("unexpected abort events: %v", aborted)
	}
}

func TestPostgreSQLPlaybackStreamDoesNotLogAClientDisconnectAsAServerAbort(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "client disconnect"
		if shutdown {
			name = "client disconnect before shutdown"
		}
		t.Run(name, func(t *testing.T) {
			logs := &lockedBuffer{}
			application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)))
			library := createPlaybackLibrary(t, application)
			trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: flacBytes(streamFileSize)})
			user := application.createUser(t, "user")

			requestContext, disconnect := context.WithCancel(context.Background())
			defer disconnect()
			writer, done := startStream(t, application, &user, trackID, requestContext)
			disconnect()
			if shutdown {
				application.shutdown()
			}
			finishStream(t, writer, done)

			if written := writer.bytesWritten(); written == 0 || written >= streamFileSize {
				t.Fatalf("delivered %d bytes; want an established stream cut before completion", written)
			}
			if aborted := logEvents(t, logs, "playback.stream.aborted"); len(aborted) != 0 {
				t.Fatalf("a client disconnect was logged as a server abort: %v", aborted)
			}
		})
	}
}

// 真实连接不依赖网络缓冲大小：第一块响应已刷新时服务端仍挂起，停机后放行，客户端必须收到提前结束而非 JSON。
func TestPostgreSQLPlaybackRealConnectionIsClosedOnShutdown(t *testing.T) {
	logs := &lockedBuffer{}
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)))
	library := createPlaybackLibrary(t, application)
	content := flacBytes(streamFileSize)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: content})
	user := application.createUser(t, "user")
	writer := newBlockingResponseWriter(context.Background())
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		defer close(done)
		writer.forward = responseWriter
		writer.ctx = request.Context()
		application.handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(writer.unblock)

	request, err := http.NewRequest(http.MethodGet, server.URL+audioPath(trackID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request audio: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != streamFileSize {
		t.Fatalf("response: HTTP %d, Content-Length %d", response.StatusCode, response.ContentLength)
	}
	select {
	case <-writer.firstWrite:
	case <-time.After(10 * time.Second):
		t.Fatal("the established response did not reach the write barrier")
	}
	application.shutdown()
	writer.unblock()
	got, err := io.ReadAll(response.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reading after shutdown = %d bytes, %v; want unexpected EOF", len(got), err)
	}
	if len(got) == 0 || len(got) >= len(content) || !bytes.Equal(got, content[:len(got)]) {
		t.Fatalf("received %d bytes; want only an incomplete audio prefix", len(got))
	}
	finishStream(t, writer, done)
	requireShutdownEvent(t, logs, trackID)
}
