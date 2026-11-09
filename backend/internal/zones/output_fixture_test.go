package zones_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/httpapi"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

// 仍复用 B2 渲染器；脚本自行派发播放头事件，避免 Observe 自动生成事件干扰顺序断言。
type outputRenderer struct {
	*zonestest.Renderer
	mu                              sync.Mutex
	failOpens, emptyOpens, attempts int
}
type outputSession struct {
	zones.RenderSession
	empty bool
}

func (s *outputSession) Observe(zones.PlayheadSample) {}
func (s *outputSession) Format() zones.StreamFormat {
	if s.empty {
		return zones.StreamFormat{}
	}
	return s.RenderSession.Format()
}
func (s *outputSession) Body() io.Reader {
	if s.empty {
		return bytes.NewReader(nil)
	}
	return s.RenderSession.Body()
}
func (r *outputRenderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	r.mu.Lock()
	r.attempts++
	fail, empty := r.failOpens > 0, r.emptyOpens > 0
	if fail {
		r.failOpens--
	} else if empty {
		r.emptyOpens--
	}
	r.mu.Unlock()
	if fail {
		return nil, errors.New("测试注入：续流暂忙")
	}
	session, err := r.Renderer.Open(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &outputSession{RenderSession: session, empty: empty}, nil
}
func (r *outputRenderer) failNextOpens(n int) { r.mu.Lock(); defer r.mu.Unlock(); r.failOpens = n }
func (r *outputRenderer) openAttempts() int   { r.mu.Lock(); defer r.mu.Unlock(); return r.attempts }

func outputService(t *testing.T, f *controlFixture, renderer zones.Renderer) {
	t.Helper()
	f.service.Close()
	f.service = zones.NewService(f.db, zones.Options{Renderer: renderer, Now: f.clock.current, AfterFunc: f.clock.after})
	t.Cleanup(f.service.Close)
	mux := http.NewServeMux()
	httpapi.New(identity.NewGuard(f.db, false, controlOrigin), f.service).Register(mux)
	f.handler = mux
}
func newOutputFixture(t *testing.T, empty int) (*controlFixture, *outputRenderer) {
	t.Helper()
	f := newControlFixture(t)
	r := &outputRenderer{Renderer: zonestest.NewRenderer(zonestest.Options{Now: f.clock.current}), emptyOpens: empty}
	outputService(t, f, r)
	return f, r
}
func outputCapabilities() zones.Capabilities {
	return zones.Capabilities{StreamFormats: []zones.StreamFormatCapability{{Codec: "wav", MIMEType: "audio/wav", Lossless: true}}, MaxChannels: 2, DSD: "none", Volume: "number"}
}
func outputClaim(t *testing.T, f *controlFixture, user controlUser, deviceID string, takeover bool, playing *zones.PlayingOutput) zones.ClaimOutputResult {
	t.Helper()
	return controlDecode[zones.ClaimOutputResult](t, f.request(t, user, "POST", "/devices/"+deviceID+"/output", zones.ClaimOutputInput{Protocol: 1, Capabilities: outputCapabilities(), Takeover: takeover, Playing: playing}, controlOrigin), 200)
}
func outputReport(t *testing.T, f *controlFixture, user controlUser, session string, seq int64, kind, status string, seconds float64) zones.OutputResult {
	t.Helper()
	in := zones.ReportOutputInput{Kind: kind, Status: status, StreamSeq: &seq, StreamSeconds: seconds}
	if kind == "fault" {
		fault := "stream_lost"
		in.Fault = &fault
	}
	return controlDecode[zones.OutputResult](t, f.request(t, user, "POST", "/outputs/"+session+"/reports", in, controlOrigin), 200)
}
func outputRelease(t *testing.T, f *controlFixture, user controlUser, session string, seq int64, seconds float64) {
	t.Helper()
	response := f.request(t, user, "POST", "/outputs/"+session+"/release", zones.ReleaseOutputInput{StreamSeq: &seq, StreamSeconds: seconds}, controlOrigin)
	if response.Code != 204 || response.Body.Len() != 0 {
		t.Fatalf("释放响应 = %d %s", response.Code, response.Body.String())
	}
}
func outputStart(t *testing.T, f *controlFixture, user controlUser, tracks []string) (zones.ClaimOutputResult, zones.Zone) {
	t.Helper()
	d := f.device(t, user, false)
	claim := outputClaim(t, f, user, d.Device.DeviceID, false, nil)
	result := f.command(t, user, d.Zone.ZoneID, map[string]any{"command": "play_tracks", "track_ids": tracks, "start_index": 0})
	if result.Zone.Stream == nil {
		t.Fatal("起播未创建流")
	}
	return claim, result.Zone
}
func outputSubscribe(t *testing.T, f *controlFixture, user controlUser) *zones.Subscription {
	t.Helper()
	sub, err := f.service.SubscribeOutput(context.Background(), user.id, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	if msg := outputMessage(t, sub); msg.Kind != "snapshot" {
		t.Fatalf("首条事件 = %s", msg.Kind)
	}
	return sub
}

// 超时只作失败护栏；成功路径始终由事件通道同步，不靠时间等待推进业务。
func outputMessage(t *testing.T, sub *zones.Subscription) zones.Message {
	t.Helper()
	select {
	case message, ok := <-sub.Events:
		if !ok {
			t.Fatal("订阅提前关闭")
		}
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("等待运行时公告超时")
	}
	return zones.Message{}
}
func outputZone(t *testing.T, sub *zones.Subscription, match func(zones.Zone) bool) zones.Zone {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-sub.Events:
			if !ok {
				t.Fatal("等待状态时订阅提前关闭")
			}
			if msg.Kind != "zone" {
				continue
			}
			var data struct {
				Zone zones.Zone `json:"zone"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				t.Fatal(err)
			}
			if match(data.Zone) {
				return data.Zone
			}
		case <-deadline.C:
			t.Fatal("等待目标状态公告超时")
		}
	}
}
func outputEmit(t *testing.T, session *zonestest.Session, event zones.Event) {
	t.Helper()
	err := session.Emit(event)
	// 终止事件可在 Emit 的发送确认前被消费并关闭会话；最终状态由公告断言确认。
	terminal := event.Kind == "stream_ended" || event.Kind == "track_failed"
	if err != nil && !(terminal && errors.Is(err, zonestest.ErrClosed)) {
		t.Fatal(err)
	}
}
