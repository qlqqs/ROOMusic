package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/listening"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
	zonesapi "github.com/qlqq/roomusic/backend/internal/zones/httpapi"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

const zonesSamplePrefix = "/api/v1/me/playback"

// 时钟只在测试线程推进，回调不持时钟锁，允许事件消费者安装定时器。
type zonesSampleClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*zonesSampleTimer
}
type zonesSampleTimer struct {
	clock   *zonesSampleClock
	at      time.Time
	fn      func()
	stopped bool
}

func (c *zonesSampleClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *zonesSampleClock) After(d time.Duration, fn func()) zones.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &zonesSampleTimer{clock: c, at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}
func (t *zonesSampleTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.stopped
	t.stopped = true
	return active
}
func (c *zonesSampleClock) Advance(d time.Duration) {
	until := c.Now().Add(d)
	for {
		c.mu.Lock()
		var next *zonesSampleTimer
		for _, timer := range c.timers {
			if !timer.stopped && !timer.at.After(until) && (next == nil || timer.at.Before(next.at)) {
				next = timer
			}
		}
		if next == nil {
			c.now = until
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.stopped = true
		c.mu.Unlock()
		next.fn()
	}
}

// 仍使用 zonestest 会话；脚本控制播放头，避免 Observe 另行自动生成 time 与脚本事件竞争。
type zonesSampleSession struct {
	*zonestest.Session
	empty bool
}

func (s *zonesSampleSession) Observe(zones.PlayheadSample) {}
func (s *zonesSampleSession) Format() zones.StreamFormat {
	if s.empty {
		return zones.StreamFormat{}
	}
	return s.Session.Format()
}
func (s *zonesSampleSession) Body() io.Reader {
	if s.empty {
		return bytes.NewReader(nil)
	}
	return s.Session.Body()
}

type zonesSampleRenderer struct {
	*zonestest.Renderer
	empty                 bool
	openErr, negotiateErr error
}

func (r *zonesSampleRenderer) Negotiate(c zones.Capabilities, q string) (zones.StreamFormat, error) {
	if r.negotiateErr != nil {
		return zones.StreamFormat{}, r.negotiateErr
	}
	return r.Renderer.Negotiate(c, q)
}
func (r *zonesSampleRenderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	session, err := r.Renderer.Open(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &zonesSampleSession{Session: session.(*zonestest.Session), empty: r.empty}, nil
}

type zonesSampleFixture struct {
	*integrationTestApplication
	service  *zones.Service
	renderer *zonesSampleRenderer
	clock    *zonesSampleClock
	tracks   []string
	user     integrationTestUser
}

func newZonesSampleFixture(t *testing.T) *zonesSampleFixture {
	t.Helper()
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "roomusic_zones_samples_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	clock := &zonesSampleClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	renderer := &zonesSampleRenderer{Renderer: zonestest.NewRenderer(zonestest.Options{Now: clock.Now})}
	id := 0
	service := zones.NewService(db, zones.Options{Renderer: renderer, IRReferences: zoneIRReferences{service: dspsettings.NewService(db)}, Now: clock.Now, AfterFunc: clock.After, Random: func() float64 { return 0 }, NewID: func() string { id++; return fmt.Sprintf("20000000-0000-4000-8000-%012d", id) }, PlayRecorder: zonePlayRecorder{service: listening.NewService(db)}})
	t.Cleanup(service.Close)
	mux := http.NewServeMux()
	zonesapi.New(identity.NewGuard(db, false, "http://roomusic.test"), service).Register(mux)
	application := &integrationTestApplication{database: db, origin: "http://roomusic.test", allowedRoot: t.TempDir(), handler: httpserver.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), mux)}
	f := &zonesSampleFixture{integrationTestApplication: application, service: service, renderer: renderer, clock: clock, user: application.createUser(t, "user")}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,'/synthetic/zones-samples','active',1)`, root)
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1)`, group)
	f.exec(t, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'合成发行','测试艺人')`, release, group)
	f.exec(t, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	for i := 0; i < 5; i++ {
		id := uuid.New()
		f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at,duration_seconds) VALUES($1,$2,$3,$4,'测试艺人',$5,$6,NOW(),20)`, id, medium, i+1, fmt.Sprintf("合成曲目 %d", i+1), root, fmt.Sprintf("track-%d.flac", i))
		f.tracks = append(f.tracks, id)
	}
	return f
}
func (f *zonesSampleFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.database.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func zonesSampleDecode[T any](t *testing.T, r *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if r.Code != status {
		t.Fatalf("状态 %d，期望 %d：%s", r.Code, status, r.Body.String())
	}
	var v T
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func zonesSampleCapabilities() zones.Capabilities {
	return zones.Capabilities{StreamFormats: []zones.StreamFormatCapability{{Codec: "wav", MIMEType: "audio/wav", Lossless: true}}, MaxChannels: 2, DSD: "none", Volume: "number"}
}

// 真实 SSE 经 HTTP 客户端逐帧解析；通道只承接本测试有限的脚本事件。
type zonesSampleFrame struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}
type zonesSampleEvents struct {
	request map[string]any
	frames  <-chan zonesSampleFrame
	errors  <-chan error
	close   func()
}

func (f *zonesSampleFixture) events(t *testing.T, session string) *zonesSampleEvents {
	t.Helper()
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	req, _ := http.NewRequest("GET", server.URL+zonesSamplePrefix+"/events?output_session="+session, nil)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.user.token})
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != 200 {
		t.Fatalf("SSE 状态 %d", response.StatusCode)
	}
	frames := make(chan zonesSampleFrame, 512)
	errors := make(chan error, 1)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		var frame zonesSampleFrame
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if frame.Event != "" {
					frames <- frame
				}
				frame = zonesSampleFrame{}
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				frame.Event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				frame.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			}
		}
		errors <- scanner.Err()
	}()
	return &zonesSampleEvents{request: map[string]any{"method": req.Method, "path": req.URL.RequestURI(), "body": nil}, frames: frames, errors: errors, close: func() { _ = response.Body.Close() }}
}
func (e *zonesSampleEvents) wait(t *testing.T, predicate func(zonesSampleFrame) bool) zonesSampleFrame {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case frame, ok := <-e.frames:
			if !ok {
				t.Fatalf("SSE 提前结束：%v", <-e.errors)
			}
			if predicate(frame) {
				return frame
			}
		case <-timeout.C:
			t.Fatal("等待 SSE 脚本事件超时")
		}
	}
}
func (e *zonesSampleEvents) zone(t *testing.T, id string, predicate func(zones.Zone) bool) zonesSampleFrame {
	t.Helper()
	return e.wait(t, func(frame zonesSampleFrame) bool {
		if frame.Event != "zone" {
			return false
		}
		var data struct {
			Zone zones.Zone `json:"zone"`
		}
		if err := json.Unmarshal(frame.Data, &data); err != nil {
			t.Fatal(err)
		}
		return data.Zone.ZoneID == id && predicate(data.Zone)
	})
}
func (f *zonesSampleFixture) emit(t *testing.T, s *zonestest.Session, event zones.Event) {
	t.Helper()
	if err := s.Emit(event); err != nil {
		t.Fatal(err)
	}
}
