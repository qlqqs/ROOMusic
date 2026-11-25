package upnp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type outputTestDevice struct {
	zones.ManagedOutputs
	device zones.ManagedDevice
}

func (d outputTestDevice) VisibleDevices(context.Context, string) ([]zones.ManagedDevice, error) {
	return []zones.ManagedDevice{d.device}, nil
}

func TestPostgreSQLUPnPResubscribesAfterHubOverflow(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_upnp_output_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const speakerName = "重订阅测试渲染器"
	userID := uuid.New()
	if _, err := db.ExecContext(ctx, "INSERT INTO users(id,username,password_hash,role) VALUES($1,'synthetic-listener','test-only','user')", userID); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan struct{}, 1)
	fakeDescriptionTransport(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.Body != nil {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				req.Body = io.NopCloser(bytes.NewReader(body))
				if bytes.Contains(body, []byte("<DesiredVolume>37</DesiredVolume>")) {
					select {
					case delivered <- struct{}{}:
					default:
					}
				}
			}
			next.ServeHTTP(w, req)
		})
	})
	device, err := ManualDescription(ctx, testDescription)
	if err != nil {
		t.Fatal(err)
	}
	caps := playbackCaps(device.Capabilities)
	volume := 0.5
	zoneService := zones.NewService(db, zones.Options{})
	defer zoneService.Close()
	zoneService.SetManagedOutputs(outputTestDevice{device: zones.ManagedDevice{
		Kind: "upnp", UDN: device.UDN, Name: speakerName, MaxSampleRate: device.Capabilities.MaxSampleRate,
		Capabilities: caps, InitialVolume: &volume,
	}})
	snapshot, err := zoneService.Snapshot(ctx, userID)
	if err != nil || len(snapshot.Devices) != 1 {
		t.Fatalf("托管设备快照 = %+v，错误 %v", snapshot, err)
	}
	speaker := snapshot.Devices[0]
	browser, err := zoneService.RegisterDevice(ctx, userID, zones.RegisterDeviceInput{Name: "事件来源", Kind: "browser", Volume: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	reporter, err := zoneService.ClaimOutput(ctx, userID, browser.Device.DeviceID, zones.ClaimOutputInput{Protocol: 1, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := zoneService.ClaimOutput(ctx, userID, speaker.DeviceID, zones.ClaimOutputInput{
		Managed: true, Protocol: 1, Capabilities: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := zoneService.SubscribeOutput(ctx, userID, claim.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	// 不启动输出循环，保证另一播放区的回报让 hub 主动关闭已满的订阅。
	for range 65 {
		if _, err := zoneService.ReportOutput(ctx, userID, reporter.SessionID, zones.ReportOutputInput{Kind: "status", Status: "paused"}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-sub.Done:
	default:
		t.Fatal("hub 未关闭积压的订阅")
	}

	observer := zoneService.Subscribe(userID)
	defer observer.Close()
	run, cancel := context.WithCancel(ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewRegistry()
	registry.Remember(device)
	output := &deviceOutput{
		ctx: run, cancel: cancel, done: make(chan struct{}), registry: registry,
		manager: &outputManager{zones: zoneService, logger: logger},
		actor:   identity.User{ID: userID}, device: device, udn: device.UDN, deviceID: speaker.DeviceID,
		zoneID: claim.Zone.ZoneID, sessionID: claim.SessionID, volume: -1,
	}
	go output.run(sub)
	defer func() { cancel(); <-output.done }()

	// 服务在重订阅和释放时都会广播设备，另查原会话的控制权限。
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case msg := <-observer.Events:
		var update struct{ Device zones.Device }
		if err := json.Unmarshal(msg.Data, &update); err != nil {
			t.Fatal(err)
		}
		if msg.Kind != "device" || update.Device.DeviceID != speaker.DeviceID {
			t.Fatalf("旧订阅关闭后的首条广播 = %s %s，期望同一设备", msg.Kind, msg.Data)
		}
	case <-timer.C:
		t.Fatal("hub 关闭订阅后，未收到重订阅广播")
	}
	if _, active := zoneService.ManagedOutputVolume(userID, claim.SessionID); !active {
		t.Error("hub 关闭订阅后，原输出会话被释放")
	}
	if _, err := zoneService.SetVolume(ctx, userID, speaker.DeviceID, 0.37, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("重订阅后的音量事件未送达渲染器")
	}
}
