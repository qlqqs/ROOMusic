package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/fakerenderer"
	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

const upnpAdmin = "/api/v1/admin/upnp"
const upnpTestDeviceName = "客厅音响"

type upnpFixture struct {
	*integrationTestApplication
	admin  integrationTestUser
	server *httptest.Server
}

func newUPnPFixture(t *testing.T, realAudio bool) *upnpFixture {
	t.Helper()
	listener := lanTestListener(t)
	server := httptest.NewUnstartedServer(nil)
	_ = server.Listener.Close()
	server.Listener = listener
	t.Cleanup(server.Close)
	var opts []Option
	if realAudio {
		opts = append(opts, withTestRenderer(t))
	}
	a := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.Server{LANURL: "http://" + server.Listener.Addr().String()}, opts...)
	t.Cleanup(func() {
		a.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.app.WaitForZones(ctx); err != nil {
			t.Error(err)
		}
	})
	server.Config.Handler = a.handler
	server.Start()
	return &upnpFixture{integrationTestApplication: a, admin: a.createUser(t, "admin"), server: server}
}

type upnpTestRenderer struct {
	*fakerenderer.Renderer
	address        string
	udn            string
	getVolumeFails atomic.Bool
	pollFails      atomic.Bool
	pollsFailed    atomic.Int32
	holdAudio      atomic.Bool
	heldPlaying    atomic.Bool
	mu             sync.Mutex
	actions        []string
}

func lanTestListener(t *testing.T) net.Listener {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err == nil && upnp.IsLANAddress(prefix.Addr()) {
			l, err := net.Listen("tcp4", net.JoinHostPort(prefix.Addr().String(), "0"))
			if err == nil {
				return l
			}
		}
	}
	t.Skip("测试需要本机局域网 IPv4 地址")
	return nil
}

func newUPnPTestRenderer(t *testing.T, relTime string) *upnpTestRenderer {
	t.Helper()
	return newUPnPTestRendererWithActionHook(t, relTime, nil)
}

func newUPnPTestRendererWithActionHook(t *testing.T, relTime string, before func(string, *http.Request)) *upnpTestRenderer {
	t.Helper()
	l := lanTestListener(t)
	udn := "uuid:" + uuid.New()
	r, err := fakerenderer.New(fakerenderer.Options{UDN: udn, Name: upnpTestDeviceName, RelTime: relTime})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	f := &upnpTestRenderer{Renderer: r, udn: udn, address: "http://" + l.Addr().String() + "/description.xml"}
	next := r.Handler()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, action, _ := strings.Cut(strings.Trim(req.Header.Get("SOAPACTION"), "\""), "#")
		switch action {
		case "SetAVTransportURI", "Play", "Stop", "Pause", "SetVolume":
			f.mu.Lock()
			f.actions = append(f.actions, action)
			f.mu.Unlock()
		}
		if before != nil {
			before(action, req)
		}
		if action == "GetVolume" && f.getVolumeFails.Load() {
			http.Error(w, "合成音量读取失败", http.StatusServiceUnavailable)
			return
		}
		if action == "GetTransportInfo" && f.pollFails.Load() {
			f.pollsFailed.Add(1)
			http.Error(w, "合成设备离线", http.StatusServiceUnavailable)
			return
		}
		if f.holdAudio.Load() {
			// 令牌用例由测试客户端取流，设备状态仍随控制动作变化。
			switch action {
			case "Play":
				f.heldPlaying.Store(true)
				w.Header().Set("Content-Type", "text/xml")
				_, _ = io.WriteString(w, "<Envelope><Body><PlayResponse/></Body></Envelope>")
				return
			case "Stop", "SetAVTransportURI":
				f.heldPlaying.Store(false)
			case "GetTransportInfo":
				if f.heldPlaying.Load() {
					w.Header().Set("Content-Type", "text/xml")
					_, _ = io.WriteString(w, "<Envelope><Body><GetTransportInfoResponse><CurrentTransportState>PLAYING</CurrentTransportState></GetTransportInfoResponse></Body></Envelope>")
					return
				}
			}
		}
		next.ServeHTTP(w, req)
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		if err := server.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()
	return f
}

func (r *upnpTestRenderer) transportActions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(r.actions), func(action string) bool { return action == "SetVolume" })
}

func (r *upnpTestRenderer) remote(t *testing.T, action string) {
	t.Helper()
	device, err := upnp.ManualDescription(t.Context(), r.address)
	if err != nil {
		t.Fatal(err)
	}
	switch action {
	case "pause":
		err = device.Pause(t.Context())
	case "play":
		err = device.Play(t.Context())
	case "stop":
		err = device.Stop(t.Context())
	default:
		t.Fatalf("未知测试操作 %q", action)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (f *upnpFixture) enable(t *testing.T) {
	t.Helper()
	if got := zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings", map[string]bool{"enabled": true}, &f.admin, nil), 200); !got.Enabled || !got.Shared {
		t.Fatalf("局域网开关 = %+v", got)
	}
}

func (f *upnpFixture) add(t *testing.T, r *upnpTestRenderer) *httptest.ResponseRecorder {
	t.Helper()
	resp := f.request(t, "POST", upnpAdmin+"/devices", map[string]string{"address": r.address}, &f.admin, nil)
	if got := zonesSampleDecode[upnp.AdminDevice](t, resp, 200); got.UDN != r.udn || !got.Online || !got.Manual {
		t.Fatalf("手动登记结果 = %+v", got)
	}
	return resp
}

func (f *upnpFixture) snapshot(t *testing.T, user *integrationTestUser) zones.Snapshot {
	t.Helper()
	return zonesSampleDecode[zones.Snapshot](t, f.request(t, "GET", zonesSamplePrefix, nil, user, nil), 200)
}

func deviceFor(snapshot zones.Snapshot, udn string) (zones.Device, bool) {
	for _, device := range snapshot.Devices {
		if device.ExternalID == udn {
			return device, true
		}
	}
	return zones.Device{}, false
}

func (f *upnpFixture) device(t *testing.T, user *integrationTestUser, r *upnpTestRenderer) zones.Device {
	t.Helper()
	device, ok := deviceFor(f.snapshot(t, user), r.udn)
	if !ok {
		t.Fatalf("账号快照中没有设备 %s", r.udn)
	}
	return device
}

func (f *upnpFixture) zone(t *testing.T, user *integrationTestUser, device zones.Device) zones.Zone {
	t.Helper()
	for _, zone := range f.snapshot(t, user).Zones {
		if zone.ZoneID == device.ZoneID {
			return zone
		}
	}
	t.Fatalf("设备 %s 缺少播放区", device.DeviceID)
	return zones.Zone{}
}

func (f *upnpFixture) command(t *testing.T, user *integrationTestUser, device zones.Device, body map[string]any) zones.Zone {
	t.Helper()
	return zonesSampleDecode[zones.CommandResult](t, f.request(t, "POST", zonesSamplePrefix+"/zones/"+device.ZoneID+"/commands", body, user, nil), 200).Zone
}

func (f *upnpFixture) activate(t *testing.T, user *integrationTestUser, device zones.Device, takeover bool) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, "POST", zonesSamplePrefix+"/devices/"+device.DeviceID+"/activate", map[string]bool{"takeover": takeover}, user, nil)
}

func (f *upnpFixture) tracks(t *testing.T, seconds ...int) []string {
	t.Helper()
	return f.tracksAtRate(t, 44100, seconds...)
}

func (f *upnpFixture) tracksAtRate(t *testing.T, rate int, seconds ...int) []string {
	t.Helper()
	library := createPlaybackLibrary(t, f.integrationTestApplication)
	var tracks []string
	for i, duration := range seconds {
		name := fmt.Sprintf("upnp-%d.flac", i)
		generator := exec.CommandContext(t.Context(), requireTestFFmpeg(t), "-v", "error", "-f", "lavfi",
			"-i", fmt.Sprintf("sine=frequency=%d:sample_rate=%d:duration=%d", 440+i*100, rate, duration), "-ac", "2", "-c:a", "flac", "-f", "flac", "pipe:1")
		var stderr bytes.Buffer
		generator.Stderr = &stderr
		audio, err := generator.Output()
		if err != nil {
			t.Fatalf("生成合成音频: %v %s", err, &stderr)
		}
		id := library.addTrack(t, f.integrationTestApplication, playbackTrackSpec{relative: name, content: audio, sampleRate: rate, bitDepth: 16, channels: 2})
		mustExec(t, f.integrationTestApplication, "UPDATE tracks SET duration_seconds=$1 WHERE id=$2", duration, id)
		if i == 0 {
			mustExec(t, f.integrationTestApplication, "UPDATE tracks SET title='海 & 浪' WHERE id=$1", id)
		}
		tracks = append(tracks, id)
	}
	return tracks
}

func (f *upnpFixture) wait(t *testing.T, timeout time.Duration, why string, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-timer.C:
			t.Fatalf("等待%s超时", why)
		case <-ticker.C:
		}
	}
}

func (f *upnpFixture) play(t *testing.T, member *integrationTestUser, device zones.Device, tracks []string) zones.Zone {
	t.Helper()
	return f.command(t, member, device, map[string]any{"command": "play_tracks", "track_ids": tracks, "start_index": 0})
}
