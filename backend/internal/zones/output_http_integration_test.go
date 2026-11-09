package zones_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func outputHTTPServer(t *testing.T, f *controlFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(httpserver.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), f.handler))
	t.Cleanup(server.Close)
	return server
}
func outputHTTPGet(t *testing.T, server *httptest.Server, user controlUser, path string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+controlPrefix+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func outputSSEFrame(t *testing.T, reader *bufio.Reader) zones.Message {
	t.Helper()
	var msg zones.Message
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			break
		}
		if strings.HasPrefix(line, "event: ") {
			msg.Kind = strings.TrimSuffix(strings.TrimPrefix(line, "event: "), "\n")
		} else if strings.HasPrefix(line, "data: ") {
			msg.Data = json.RawMessage(strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n"))
		} else {
			t.Fatalf("非合同 SSE 行：%q", line)
		}
	}
	if msg.Kind == "" || !json.Valid(msg.Data) {
		t.Fatalf("无效 SSE 帧：%+v", msg)
	}
	return msg
}

func TestPostgreSQLZonesOutputHTTPSnapshotDeltaIsolation(t *testing.T) {
	f, _ := newOutputFixture(t, 0)
	user := f.user(t, "user")
	other := f.user(t, "admin")
	own := f.device(t, user, false)
	foreign := f.device(t, other, false)
	server := outputHTTPServer(t, f)
	response := outputHTTPGet(t, server, user, "/events")
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("事件流响应头：%d %v", response.StatusCode, response.Header)
	}
	reader := bufio.NewReader(response.Body)
	first := outputSSEFrame(t, reader)
	var snapshot zones.Snapshot
	if err := json.Unmarshal(first.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if first.Kind != "snapshot" || snapshot.Protocol != 1 || len(snapshot.Zones) != 1 || snapshot.Zones[0].ZoneID != own.Zone.ZoneID || len(snapshot.Devices) != 1 || snapshot.Devices[0].DeviceID != own.Device.DeviceID {
		t.Fatalf("首帧非本人完整快照：%s %+v", first.Kind, snapshot)
	}
	// 他人的命令已经同步发布完成，再发本人命令作观察屏障；收到的首个增量仍必须属于本人。
	f.command(t, other, foreign.Zone.ZoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[1:2]})
	result := f.command(t, user, own.Zone.ZoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[:1]})
	queueMessage := outputSSEFrame(t, reader)
	zoneMessage := outputSSEFrame(t, reader)
	var queueData struct {
		ZoneID string            `json:"zone_id"`
		Change zones.QueueChange `json:"change"`
	}
	var zoneData struct {
		Zone zones.Zone `json:"zone"`
	}
	if err := json.Unmarshal(queueMessage.Data, &queueData); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(zoneMessage.Data, &zoneData); err != nil {
		t.Fatal(err)
	}
	if queueMessage.Kind != "queue" || zoneMessage.Kind != "zone" || queueData.ZoneID != own.Zone.ZoneID || zoneData.Zone.ZoneID != own.Zone.ZoneID || zoneData.Zone.Revision != result.Zone.Revision || len(queueData.Change.Entries) != 1 || queueData.Change.Entries[0].Track.TrackID != f.tracks[0] {
		t.Fatalf("命令增量泄漏、乱序或未对齐：%s %+v / %s %+v", queueMessage.Kind, queueData, zoneMessage.Kind, zoneData)
	}
}

func TestPostgreSQLZonesOutputHTTPStreamsAndHEAD(t *testing.T) {
	f, r := newOutputFixture(t, 0)
	user := f.user(t, "user")
	claim, z := outputStart(t, f, user, f.tracks[:2])
	seq := z.Stream.Seq
	streamPath := func(seq int64) string { return "/zones/" + z.ZoneID + "/streams/" + strconv.FormatInt(seq, 10) }
	// HEAD 必须在鉴权后拒绝，不能消耗随后唯一一次 GET。
	head := f.request(t, user, "HEAD", streamPath(seq), nil, "")
	if head.Code != 405 || head.Header().Get("Allow") != "GET" {
		t.Fatalf("HEAD = %d %v", head.Code, head.Header())
	}
	server := outputHTTPServer(t, f)
	readStream := func(seq int64) {
		t.Helper()
		response := outputHTTPGet(t, server, user, streamPath(seq))
		if response.StatusCode != 200 || response.Header.Get("Content-Type") != "audio/wav" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Accept-Ranges") != "none" || response.Header.Get("Content-Length") != "" || response.Header.Get("X-Accel-Buffering") != "no" {
			t.Fatalf("渲染流响应头：%d %v", response.StatusCode, response.Header)
		}
		// 流尚未结束时已能读取 WAV 头，证明中间件下的 Flush 没有等 EOF。
		header := make([]byte, 44)
		if _, err := io.ReadFull(response.Body, header); err != nil {
			t.Fatal(err)
		}
		if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
			t.Fatalf("Body 不是原始 WAV：%x", header)
		}
		response.Body.Close()
	}
	readStream(seq)
	if r.Sessions()[0].Closed() {
		t.Fatal("HTTP 客户端断开不应关闭渲染会话")
	}
	controlError(t, f.request(t, user, "GET", streamPath(seq), nil, ""), 410, "stream_gone")
	entries := f.queue(t, user, z.ZoneID).Entries
	sub := outputSubscribe(t, f, user)
	outputEmit(t, r.Sessions()[0], zones.Event{Kind: "format_boundary", NextEntryID: entries[1].EntryID, Reason: "format"})
	next := outputZone(t, sub, func(z zones.Zone) bool { return z.NextStream != nil })
	readStream(next.NextStream.Seq)
	if r.Sessions()[1].Closed() {
		t.Fatal("接续流 GET 不应接管会话生命周期")
	}
	controlError(t, f.request(t, user, "GET", streamPath(next.NextStream.Seq), nil, ""), 410, "stream_gone")
	renewed := outputReport(t, f, user, claim.SessionID, seq, "fault", "loading", 3)
	if renewed.Zone.Stream == nil || renewed.Zone.Stream.Seq == seq {
		t.Fatal("续流没有更换 seq")
	}
	controlError(t, f.request(t, user, "GET", streamPath(seq), nil, ""), 410, "stream_gone")
	controlError(t, f.request(t, user, "GET", streamPath(next.NextStream.Seq), nil, ""), 410, "stream_gone")
	// 这条旧流从未 GET，410 必须来自 seq 失效，而不是单次取用标记。
	untaken := renewed.Zone.Stream.Seq
	latest := outputReport(t, f, user, claim.SessionID, untaken, "fault", "loading", 0)
	if latest.Zone.Stream == nil || latest.Zone.Stream.Seq == untaken {
		t.Fatal("再次续流没有更换 seq")
	}
	controlError(t, f.request(t, user, "GET", streamPath(untaken), nil, ""), 410, "stream_gone")
}
