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

// streamRecheckInterval 把复核间隔缩短到可以快速验证；生产值是 playback.DefaultRecheckInterval（10 秒）。
const streamRecheckInterval = 50 * time.Millisecond

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

// blockingResponseWriter 让测试在服务端写出第一块音频时把它挂起：此时处理请求的 goroutine 停在写出上，
// 测试可以先改变数据库状态，再放行写出，下一次读取就必然发生在复核间隔之后。
type blockingResponseWriter struct {
	header     http.Header
	status     int
	firstWrite chan struct{}
	release    chan struct{}
	once       sync.Once
	mutex      sync.Mutex
	written    int
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{header: http.Header{}, firstWrite: make(chan struct{}), release: make(chan struct{})}
}

func (writer *blockingResponseWriter) Header() http.Header { return writer.header }

func (writer *blockingResponseWriter) WriteHeader(status int) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *blockingResponseWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() { close(writer.firstWrite) })
	<-writer.release
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.written += len(data)
	return len(data), nil
}

func (writer *blockingResponseWriter) bytesWritten() int {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.written
}

// startStream 在后台处理一个音频请求，返回挂起的写出端和处理结束的信号。
func startStream(t *testing.T, application *integrationTestApplication, user *integrationTestUser, trackID string, requestContext context.Context) (*blockingResponseWriter, <-chan struct{}) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, audioPath(trackID), nil).WithContext(requestContext)
	request.Host = "roomusic.test"
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	writer := newBlockingResponseWriter()
	done := make(chan struct{})
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
	time.Sleep(4 * streamRecheckInterval) // 保证放行之后的第一次读取已经过了复核间隔
	close(writer.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream handler did not return after the writer was released")
	}
}

const streamFileSize = 1 << 20

func TestPostgreSQLPlaybackStreamIsCutWhenAccessIsWithdrawn(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		reason   string
		level    string
		withdraw func(t *testing.T, application *integrationTestApplication, library playbackLibrary, user integrationTestUser, trackID string)
	}{
		{"session revoked", "session_invalid", "INFO", func(t *testing.T, application *integrationTestApplication, _ playbackLibrary, user integrationTestUser, _ string) {
			mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, user.id)
		}},
		{"member disabled", "session_invalid", "INFO", func(t *testing.T, application *integrationTestApplication, _ playbackLibrary, user integrationTestUser, _ string) {
			mustExec(t, application, `UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid`, user.id)
		}},
		{"root disabled", "root_disabled", "INFO", func(t *testing.T, application *integrationTestApplication, library playbackLibrary, _ integrationTestUser, _ string) {
			mustExec(t, application, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, library.rootID)
		}},
		{"track no longer present", "track_unavailable", "INFO", func(t *testing.T, application *integrationTestApplication, _ playbackLibrary, _ integrationTestUser, trackID string) {
			mustExec(t, application, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, trackID)
		}},
		{"database fails while rechecking", "check_failed", "ERROR", func(t *testing.T, application *integrationTestApplication, _ playbackLibrary, _ integrationTestUser, _ string) {
			// 会话与成员仍然有效，只有目录表读不出来：复核必须失败即关闭，而不是放行。
			mustExec(t, application, `DROP TABLE library_roots CASCADE`)
		}},
		{"application shutting down", "shutdown", "INFO", func(t *testing.T, application *integrationTestApplication, _ playbackLibrary, _ integrationTestUser, _ string) {
			application.shutdown()
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logs := &lockedBuffer{}
			application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)), WithPlaybackRecheckInterval(streamRecheckInterval))
			library := createPlaybackLibrary(t, application)
			trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: flacBytes(streamFileSize)})
			user := application.createUser(t, "user")

			writer, done := startStream(t, application, &user, trackID, context.Background())
			testCase.withdraw(t, application, library, user, trackID)
			finishStream(t, writer, done)

			if written := writer.bytesWritten(); written == 0 || written >= streamFileSize {
				t.Fatalf("delivered %d of %d bytes; want some audio, then a cut before the end", written, streamFileSize)
			}
			if writer.status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (the headers were already sent when the stream was cut)", writer.status)
			}
			aborted := logEvents(t, logs, "playback.stream.aborted")
			if len(aborted) != 1 {
				t.Fatalf("playback.stream.aborted events = %d, want 1: %s", len(aborted), logs.String())
			}
			event := aborted[0]
			if event["reason"] != testCase.reason || event["track_id"] != trackID || event["level"] != testCase.level {
				t.Fatalf("abort event = %v, want reason %s, track %s, level %s", event, testCase.reason, trackID, testCase.level)
			}
			if request, _ := event["request_id"].(string); request == "" {
				t.Fatalf("abort event has no request_id: %v", event)
			}
			for _, secret := range []string{application.allowedRoot, "playback-library", "long.flac", user.token} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("logs leak %q: %s", secret, logs.String())
				}
			}
		})
	}
}

func TestPostgreSQLPlaybackStreamContinuesWhileEveryRecheckPasses(t *testing.T) {
	logs := &lockedBuffer{}
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)), WithPlaybackRecheckInterval(streamRecheckInterval))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: flacBytes(streamFileSize)})
	user := application.createUser(t, "user")

	writer, done := startStream(t, application, &user, trackID, context.Background())
	finishStream(t, writer, done)

	if written := writer.bytesWritten(); written != streamFileSize {
		t.Fatalf("delivered %d of %d bytes although nothing was withdrawn", written, streamFileSize)
	}
	if aborted := logEvents(t, logs, "playback.stream.aborted"); len(aborted) != 0 {
		t.Fatalf("unexpected abort events: %v", aborted)
	}
}

func TestPostgreSQLPlaybackStreamDoesNotLogAClientDisconnectAsAServerAbort(t *testing.T) {
	logs := &lockedBuffer{}
	application := newIntegrationTestApplicationWithLogger(t, slog.New(slog.NewJSONHandler(logs, nil)), WithPlaybackRecheckInterval(streamRecheckInterval))
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "long.flac", content: flacBytes(streamFileSize)})
	user := application.createUser(t, "user")

	requestContext, disconnect := context.WithCancel(context.Background())
	writer, done := startStream(t, application, &user, trackID, requestContext)
	disconnect()
	finishStream(t, writer, done)

	if written := writer.bytesWritten(); written >= streamFileSize {
		t.Fatalf("delivered the whole file (%d bytes) to a disconnected client", written)
	}
	if aborted := logEvents(t, logs, "playback.stream.aborted"); len(aborted) != 0 {
		t.Fatalf("a client disconnect was logged as a server abort: %v", aborted)
	}
}

// 真实连接：net/http 在响应长度不足时关闭连接，客户端读到的是提前结束的响应，而不是一个“完整”的文件。
func TestPostgreSQLPlaybackRealConnectionIsClosedAfterRevocation(t *testing.T) {
	application := newIntegrationTestApplication(t, WithPlaybackRecheckInterval(streamRecheckInterval))
	library := createPlaybackLibrary(t, application)
	// 文件要大于回环连接两端的缓冲，服务端才会在客户端停止读取时停在写出上。
	const size = 24 << 20
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "very-long.flac", content: flacBytes(size)})
	user := application.createUser(t, "user")
	server := httptest.NewServer(application.handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodGet, server.URL+audioPath(trackID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request audio: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != size {
		t.Fatalf("response: HTTP %d, Content-Length %d", response.StatusCode, response.ContentLength)
	}
	received, err := io.ReadFull(response.Body, make([]byte, 64<<10))
	if err != nil {
		t.Fatalf("read the first chunk: %v", err)
	}
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, user.id)
	time.Sleep(4 * streamRecheckInterval)
	rest, err := io.Copy(io.Discard, response.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reading after the revocation = %d bytes, %v; want the connection to be cut (unexpected EOF)", rest, err)
	}
	if total := int64(received) + rest; total >= size {
		t.Fatalf("received %d of %d bytes after the session was revoked", total, size)
	}
}
