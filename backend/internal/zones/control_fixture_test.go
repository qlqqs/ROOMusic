package zones_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/httpapi"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

const controlPrefix = "/api/v1/me/playback"
const controlOrigin = "http://roomusic.test"

// 测试线程推进定时器；不启用假渲染器的实时时钟，也不依赖 sleep。
type controlClock struct {
	now    time.Time
	timers []*controlTimer
}
type controlTimer struct {
	at       time.Time
	callback func()
	stopped  bool
}

func (t *controlTimer) Stop() bool {
	active := !t.stopped
	t.stopped = true
	return active
}
func (c *controlClock) after(d time.Duration, callback func()) zones.Timer {
	timer := &controlTimer{at: c.now.Add(d), callback: callback}
	c.timers = append(c.timers, timer)
	return timer
}
func (c *controlClock) advance(d time.Duration) {
	until := c.now.Add(d)
	for {
		var next *controlTimer
		for _, timer := range c.timers {
			if !timer.stopped && !timer.at.After(until) && (next == nil || timer.at.Before(next.at)) {
				next = timer
			}
		}
		if next == nil {
			break
		}
		c.now = next.at
		next.stopped = true
		next.callback()
	}
	c.now = until
}

// 只给 retry 的前置条件注入一次开流失败，其余行为仍由已有假渲染器提供。
type controlRenderer struct {
	*zonestest.Renderer
	failNext bool
}

func (r *controlRenderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	if r.failNext {
		r.failNext = false
		return nil, errors.New("测试注入：渲染器暂忙")
	}
	return r.Renderer.Open(ctx, spec)
}

type controlUser struct{ id, token string }
type controlFixture struct {
	db       *sql.DB
	service  *zones.Service
	renderer *controlRenderer
	clock    *controlClock
	handler  http.Handler
	tracks   []string
}

func newControlFixture(t *testing.T) *controlFixture {
	t.Helper()
	db, err := postgres.Open(context.Background(), postgrestest.NewSchema(t, "roomusic_zones_control_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	clock := &controlClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	renderer := &controlRenderer{Renderer: zonestest.NewRenderer(zonestest.Options{})}
	service := zones.NewService(db, zones.Options{Renderer: renderer, Now: func() time.Time { return clock.now }, AfterFunc: clock.after})
	t.Cleanup(service.Close)
	mux := http.NewServeMux()
	httpapi.New(identity.NewGuard(db, false, controlOrigin), service).Register(mux)
	f := &controlFixture{db: db, service: service, renderer: renderer, clock: clock, handler: mux}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,'/synthetic/zones-control','active',1)`, root)
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1)`, group)
	f.exec(t, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'合成发行','测试艺人')`, release, group)
	f.exec(t, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	for i := 0; i < 4; i++ {
		id := uuid.New()
		f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at,duration_seconds)
   VALUES($1,$2,$3,'合成曲目','测试艺人',$4,$5,NOW(),180)`, id, medium, i+1, root, fmt.Sprintf("track-%d.flac", i))
		f.tracks = append(f.tracks, id)
	}
	return f
}
func (f *controlFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *controlFixture) user(t *testing.T, role string) controlUser {
	t.Helper()
	user := controlUser{id: uuid.New(), token: identity.GenerateSessionToken()}
	f.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'test-only',$3)`, user.id, "zones-"+user.id, role)
	f.exec(t, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+interval '1 hour')`, identity.HashSessionToken(user.token), user.id)
	return user
}
func (f *controlFixture) request(t *testing.T, user controlUser, method, path string, body any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, controlOrigin+controlPrefix+path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if user.token != "" {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}
func controlDecode[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP 状态 = %d，期望 %d：%s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("响应头不符合合同：%v", response.Header())
	}
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func controlError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	result := controlDecode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, response, status)
	if result.Error.Code != code {
		t.Fatalf("错误码 = %q，期望 %q", result.Error.Code, code)
	}
}
func (f *controlFixture) device(t *testing.T, user controlUser, online bool) zones.RegisterDeviceResult {
	t.Helper()
	result := controlDecode[zones.RegisterDeviceResult](t, f.request(t, user, "POST", "/devices", map[string]any{
		"name": "测试设备", "kind": "browser", "volume": 0.5, "muted": false,
	}, controlOrigin), http.StatusCreated)
	if online {
		f.output(t, user, result.Device.DeviceID, true, nil)
	}
	return result
}
func (f *controlFixture) output(t *testing.T, user controlUser, deviceID string, online bool, caps *zones.Capabilities) {
	t.Helper()
	if _, err := f.service.SetOutputState(context.Background(), user.id, deviceID, zones.OutputState{Online: online, Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
}
func (f *controlFixture) command(t *testing.T, user controlUser, zoneID string, body map[string]any) zones.CommandResult {
	t.Helper()
	return controlDecode[zones.CommandResult](t, f.request(t, user, "POST", "/zones/"+zoneID+"/commands", body, controlOrigin), http.StatusOK)
}
func (f *controlFixture) queue(t *testing.T, user controlUser, zoneID string) zones.QueueResult {
	t.Helper()
	return controlDecode[zones.QueueResult](t, f.request(t, user, "GET", "/zones/"+zoneID+"/queue", nil, ""), http.StatusOK)
}
func (f *controlFixture) snapshot(t *testing.T, user controlUser) zones.Snapshot {
	t.Helper()
	return controlDecode[zones.Snapshot](t, f.request(t, user, "GET", "", nil, ""), http.StatusOK)
}
