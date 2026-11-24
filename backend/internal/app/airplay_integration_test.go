package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/hap"
	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/insights/schema"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/ptp"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func advertiseAirPlayFixture(t *testing.T, entry mdns.Entry) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- mdns.Advertise(ctx, entry) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	return stop
}

func TestPostgreSQLAirPlayAPISamplesAndAccess(t *testing.T) {
	var collision atomic.Bool
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := ptp.Status{Available: true, Role: "master", ClockID: 42, GrandmasterID: 42}
		if collision.Load() {
			status = ptp.Status{ClockID: 42, Reason: "port_in_use"}
		}
		json.NewEncoder(w).Encode(status)
	}))
	t.Cleanup(clock.Close)
	app := newConfiguredIntegrationTestApplication(t, slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.Server{PTPAddress: strings.TrimPrefix(clock.URL, "http://")})
	t.Cleanup(func() {
		app.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := app.app.WaitForZones(ctx); err != nil {
			t.Error(err)
		}
	})
	f := &upnpFixture{integrationTestApplication: app, admin: app.createUser(t, "admin")}
	member, other := f.createUser(t, "user"), f.createUser(t, "user")
	const id = "02:3F:BA:15:D9:79"
	const key = "airplay:" + id
	const name = "ROOMusic-AirPlay2"
	base := upnpAdmin + "/devices"
	path := base + "/" + url.PathEscape(key)
	headers := map[string]string{"X-Request-ID": "sample-request"}
	addBody := map[string]string{"kind": "airplay", "address": "8.8.8.8"}
	requireAPIError(t, f.request(t, "POST", base, addBody, &f.admin, nil), 409, "airplay_disabled")
	settings := zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"enabled": true, "upnp_enabled": false, "cast_enabled": false}, &f.admin, nil), 200)
	if !settings.Enabled || !settings.Shared || !settings.AirPlayEnabled || settings.UPnPEnabled || settings.CastEnabled {
		t.Fatalf("启用局域网后的协议设置 = %+v", settings)
	}
	requireAPIError(t, f.request(t, "POST", base, addBody, &f.admin, nil), 400, "invalid_address")

	recv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/pair-pin-start" {
			w.WriteHeader(200)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || r.URL.Path != "/pair-setup" ||
			!bytes.Equal(body, []byte{6, 1, 1, 0, 1, 0}) {
			t.Errorf("意外的配对请求 %s %s，错误 %v", r.Method, r.URL.Path, err)
			w.WriteHeader(400)
			return
		}
		// 仅提供 M2 挑战，完整配对由独立 HAP 用例和真实接收端脚本验证。
		w.Write([]byte{6, 1, 2, 2, 2, 1, 2, 3, 1, 5})
	}))
	recv.Listener.Close()
	recv.Listener = lanTestListener(t)
	recv.Start()
	t.Cleanup(recv.Close)
	address := netip.MustParseAddrPort(recv.Listener.Addr().String())
	entry := mdns.Entry{
		Service: "_airplay._tcp.local.", Instance: name, IP: address.Addr(), Port: address.Port(),
		TXT: map[string]string{"deviceid": id, "model": "ShairportSync", "features": "0x401c4a00,0x5c340", "flags": "0x4",
			"pk": "e953ee348102f56c50026bec32bd2de1c38715844384c1f7b825b5aa796fb900"},
	}
	stopNative := advertiseAirPlayFixture(t, entry)
	classic := entry
	classic.Service, classic.Instance = "_raop._tcp.local.", "023FBA15D979@"+name
	classic.TXT = map[string]string{"am": "ShairportSync", "sf": "0x4", "et": "0,1"}
	stopClassic := advertiseAirPlayFixture(t, classic)
	list := func() *httptest.ResponseRecorder {
		return f.request(t, "GET", base, nil, &f.admin, nil)
	}
	f.wait(t, 8*time.Second, "两种 AirPlay 服务公告已合并", func() bool {
		for _, d := range zonesSampleDecode[struct{ Items []upnp.AdminDevice }](t, list(), 200).Items {
			if d.UDN == key && d.Online && d.Capabilities.AirPlay != nil &&
				d.Capabilities.AirPlay.AirPlayAddress != "" && d.Capabilities.AirPlay.RAOPAddress != "" {
				return true
			}
		}
		return false
	})
	assertUPnPSample(t, "airplay-list", list(), "192.168.144.2:7000", address.String())
	addBody["address"] = address.Addr().String()
	assertUPnPSample(t, "airplay-manual-added", f.request(t, "POST", base, addBody, &f.admin, nil),
		"192.168.144.2:7000", address.String())

	pin := map[string]string{"pin": "0123"}
	defaultFormat := upnp.DeviceFormat{Codec: "alac", MaxSampleRate: 44100, MaxBitDepth: 16}
	highFormat := upnp.DeviceFormat{Codec: "alac", MaxSampleRate: 48000, MaxBitDepth: 24}
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", upnpAdmin + "/settings", nil}, {"GET", base, nil}, {"POST", base, addBody},
		{"POST", path + "/pair/start", nil}, {"POST", path + "/pair/finish", pin}, {"PUT", path + "/format", highFormat},
	} {
		requireAPIError(t, f.request(t, req.method, req.path, req.body, &member, nil), 403, "forbidden")
	}
	requireAPIError(t, f.request(t, "POST", path+"/pair/finish", map[string]string{"pin": "1x23"}, &f.admin, nil), 400, "invalid_input")
	assertUPnPSample(t, "airplay-pair-expired", f.request(t, "POST", path+"/pair/finish", pin, &f.admin, headers))
	assertUPnPSample(t, "airplay-pair-start", f.request(t, "POST", path+"/pair/start", nil, &f.admin, headers))
	requireAPIError(t, f.request(t, "POST", path+"/pair/start", nil, &f.admin, nil), 409, "pairing_busy")

	assertUPnPSample(t, "airplay-format", f.request(t, "PUT", path+"/format", defaultFormat, &f.admin, nil))
	for _, invalid := range []upnp.DeviceFormat{
		{Codec: "mp3", MaxSampleRate: 44100, MaxBitDepth: 16},
		{Codec: "alac", MaxSampleRate: 48000, MaxBitDepth: 16},
		{Codec: "alac", MaxSampleRate: 44100, MaxBitDepth: 24},
	} {
		requireAPIError(t, f.request(t, "PUT", path+"/format", invalid, &f.admin, nil), 400, "invalid_input")
	}
	assertUPnPSample(t, "airplay-high-resolution-format", f.request(t, "PUT", path+"/format", highFormat, &f.admin, nil))

	memberDevice, ok := deviceFor(f.snapshot(t, &member), key)
	otherDevice, otherOK := deviceFor(f.snapshot(t, &other), key)
	if !ok || !otherOK || memberDevice.DeviceID == otherDevice.DeviceID || memberDevice.ZoneID == otherDevice.ZoneID ||
		memberDevice.Kind != "airplay" || memberDevice.MaxSampleRate != 48000 {
		t.Fatalf("共享成员设备 = %+v / %+v", memberDevice, otherDevice)
	}
	zonesSampleDecode[zones.DeviceResult](t, f.activate(t, &member, memberDevice, false), 200)
	occupied, _ := deviceFor(f.snapshot(t, &other), key)
	if occupied.OccupiedBy == nil || occupied.OccupiedBy.UserID != member.id {
		t.Fatalf("共享设备占用者 = %+v", occupied.OccupiedBy)
	}
	requireAPIError(t, f.activate(t, &other, otherDevice, false), 409, "device_busy")
	zonesSampleDecode[zones.DeviceResult](t, f.activate(t, &other, otherDevice, true), 200)
	zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"shared": false}, &f.admin, nil), 200)
	requireAPIError(t, f.activate(t, &other, otherDevice, true), 404, "not_found")
	if _, visible := deviceFor(f.snapshot(t, &member), key); visible {
		t.Fatal("关闭共享后未分配的账号仍看到设备")
	}
	zonesSampleDecode[map[string][]string](t, f.request(t, "PUT", path+"/grants",
		map[string][]string{"user_ids": {member.id}}, &f.admin, nil), 200)
	reassigned, ok := deviceFor(f.snapshot(t, &member), key)
	if !ok || reassigned.DeviceID != memberDevice.DeviceID || reassigned.OccupiedBy != nil {
		t.Fatalf("重新分配后应恢复原设备且释放占用，实际 %+v", reassigned)
	}
	if _, visible := deviceFor(f.snapshot(t, &other), key); visible {
		t.Fatal("未获分配的账号仍看到设备")
	}
	status := zonesSampleDecode[struct{ Payload json.RawMessage }](t, f.request(t, "GET", insightsAdminPath, nil, &f.admin, nil), 200)
	payload, err := schema.Decode(bytes.NewReader(status.Payload))
	if err != nil || payload.Schema != 3 || payload.Devices30d.AirPlay != "1" {
		t.Fatalf("两位用户共享同一物理设备后的统计 = %+v，错误 %v", payload, err)
	}

	collision.Store(true)
	for _, model := range []struct{ id, name string }{{"02:00:00:00:00:51", "AudioAccessory5,1"}, {"02:00:00:00:00:53", "Sonos Era 300"}} {
		e := entry
		e.Instance = "ROOMusic Fixture " + model.name
		e.TXT = map[string]string{"deviceid": model.id, "model": model.name}
		advertiseAirPlayFixture(t, e)
		var blocked zones.Device
		f.wait(t, 8*time.Second, model.name+"设备公告", func() bool {
			var visible bool
			blocked, visible = deviceFor(f.snapshot(t, &f.admin), "airplay:"+model.id)
			return visible
		})
		if blocked.UnavailableReason != airplay.PrecisionUnavailable {
			t.Fatalf("%s 不可用原因 = %q", model.name, blocked.UnavailableReason)
		}
		requireAPIError(t, f.activate(t, &f.admin, blocked, false), 409, "device_unsupported")
		for _, row := range zonesSampleDecode[struct{ Items []upnp.AdminDevice }](t, list(), 200).Items {
			if row.UDN == "airplay:"+model.id && (!row.Capabilities.AirPlay.PTPRequired ||
				!row.Capabilities.AirPlay.Unsupported || row.Capabilities.AirPlay.TimingReason != "port_in_use") {
				t.Fatalf("%s 端口冲突时的管理员设备信息 = %+v", model.name, row.Capabilities.AirPlay)
			}
		}
	}

	stopNative()
	stopClassic()
	f.wait(t, 8*time.Second, "接收端离线", func() bool {
		_, online := deviceFor(f.snapshot(t, &member), key)
		return !online
	})
	settings = zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"airplay_enabled": false}, &f.admin, nil), 200)
	if !settings.Enabled || settings.AirPlayEnabled {
		t.Fatalf("单独关闭 AirPlay 后的设置 = %+v", settings)
	}
	requireAPIError(t, f.request(t, "POST", base, addBody, &f.admin, nil), 409, "airplay_disabled")
	assertUPnPSample(t, "airplay-pair-expired", f.request(t, "POST", path+"/pair/finish", pin, &f.admin, headers))
}

func TestPostgreSQLAirPlayCredentialsEncryptedAndRestored(t *testing.T) {
	app := newIntegrationTestApplication(t)
	keyStore := func() *airplay.CredentialStore {
		return airplay.NewCredentialStore(app.database, newInstanceSecrets(app.dataDirectory))
	}
	const key = "airplay:02:00:00:00:00:01"
	want := hap.Credentials{
		ControllerID: "synthetic-controller", AccessoryID: "synthetic-accessory",
		ControllerSeed: bytes.Repeat([]byte{0x52}, 32), AccessoryPublicKey: bytes.Repeat([]byte{0x27}, 32),
	}
	ctx := t.Context()
	if err := keyStore().Save(ctx, key, want); err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		t.Helper()
		var sealed []byte
		if err := app.database.QueryRowContext(ctx, "SELECT credentials_ciphertext FROM airplay_pairings WHERE udn=$1", key).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	stored := read()
	if len(stored) == 0 || json.Valid(stored) || bytes.Contains(stored, []byte(want.ControllerID)) ||
		bytes.Contains(stored, []byte(want.AccessoryID)) || bytes.Contains(stored, want.ControllerSeed) ||
		bytes.Contains(stored, []byte(base64.StdEncoding.EncodeToString(want.ControllerSeed))) {
		t.Fatal("数据库中的配对凭据含有明文或可逆编码")
	}
	if got, err := keyStore().Load(ctx, key); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("重新打开同一实例密钥文件后恢复凭据失败，错误 %v", err)
	}
	if err := keyStore().Save(ctx, key, want); err != nil || bytes.Equal(stored, read()) {
		t.Fatalf("以相同配对身份更新时没有重新加密，错误 %v", err)
	}
	const differentKey = "airplay:02:00:00:00:00:02"
	mustExec(t, app, "INSERT INTO airplay_pairings(udn, credentials_ciphertext) VALUES($1, $2)", differentKey, stored)
	if _, err := keyStore().Load(ctx, differentKey); !errors.Is(err, secret.ErrCannotOpen) {
		t.Fatalf("将密文复制给其他设备后错误 = %v", err)
	}
	stored[len(stored)-1] ^= 1
	mustExec(t, app, "UPDATE airplay_pairings SET credentials_ciphertext=$2 WHERE udn=$1", key, stored)
	if _, err := keyStore().Load(ctx, key); !errors.Is(err, secret.ErrCannotOpen) {
		t.Fatalf("损坏密文后错误 = %v", err)
	}
}
