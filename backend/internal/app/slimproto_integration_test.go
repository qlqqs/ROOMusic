package app

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/slimproto/slimtest"
	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

const slimProtoTestKey = "slim:02:00:00:00:00:01"

func slimProtoSettings(t *testing.T, f *upnpFixture, enabled bool) upnp.Settings {
	t.Helper()
	return zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings", map[string]bool{
		"enabled": true, "upnp_enabled": false, "cast_enabled": false, "airplay_enabled": false, "slimproto_enabled": enabled,
	}, &f.admin, nil), 200)
}

func slimProtoAdminDevices(t *testing.T, f *upnpFixture) []upnp.AdminDevice {
	t.Helper()
	return zonesSampleDecode[struct{ Items []upnp.AdminDevice }](t, f.request(t, "GET", upnpAdmin+"/devices", nil, &f.admin, nil), 200).Items
}

func slimProtoDevice(t *testing.T, f *upnpFixture, user *integrationTestUser) zones.Device {
	t.Helper()
	return slimProtoDeviceByKey(t, f, user, slimProtoTestKey)
}

func slimProtoDeviceByKey(t *testing.T, f *upnpFixture, user *integrationTestUser, key string) zones.Device {
	t.Helper()
	var d zones.Device
	f.wait(t, 3*time.Second, "SlimProto 设备上线", func() bool {
		d, _ = deviceFor(f.snapshot(t, user), key)
		return d.Online
	})
	return d
}

type slimProtoTestPlayer struct{ *slimtest.Player }

func startSlimProtoTestPlayer(t *testing.T, f *upnpFixture, rate int, codecs string) *slimProtoTestPlayer {
	t.Helper()
	return startSlimProtoTestPlayerWithMAC(t, f, rate, codecs, 1)
}

func startSlimProtoTestPlayerWithMAC(t *testing.T, f *upnpFixture, rate int, codecs string, lastMACByte byte) *slimProtoTestPlayer {
	t.Helper()
	host, _, err := net.SplitHostPort(f.server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p := &slimProtoTestPlayer{slimtest.Dial(t, net.JoinHostPort(host, "3483"))}
	helo := make([]byte, 36)
	copy(helo, []byte{12, 0, 2, 0, 0, 0, 0, lastMACByte})
	caps := fmt.Sprintf("Model=squeezelite,ModelName=SqueezeLite,Firmware=2.0.0,MaxSampleRate=%d,%s", rate, codecs)
	p.Send(t, "HELO", append(helo, caps...))
	p.expectControl(t, 'q')
	p.Send(t, "SETD", []byte("\x00书房\x00"))
	return p
}

func (p *slimProtoTestPlayer) readControl(t *testing.T, timeout time.Duration) []byte {
	t.Helper()
	if err := p.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	for {
		frame, err := p.ReadFrame()
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(frame[:4]) != "strm" {
			continue
		}
		if len(frame) < 28 {
			t.Fatalf("strm 缺少 24 字节头，帧长 %d", len(frame))
		}
		if frame[4] != 't' {
			return frame[4:]
		}
		p.Status(t, "STMt", 0)
	}
}

func (p *slimProtoTestPlayer) expectControl(t *testing.T, command byte) []byte {
	t.Helper()
	payload := p.readControl(t, 5*time.Second)
	if len(payload) == 0 || payload[0] != command {
		t.Fatalf("期望 strm %c，收到 %q", command, payload)
	}
	return payload
}

func (p *slimProtoTestPlayer) expectNoControl(t *testing.T, reason string) {
	t.Helper()
	// 等过一次 Output 的周期检查，确保接续条件不会在定时检查时被绕过。
	if payload := p.readControl(t, 1200*time.Millisecond); len(payload) != 0 {
		t.Fatalf("%s时收到意外的 strm %c", reason, payload[0])
	}
}

func readSlimProtoTestAudio(t *testing.T, stream []byte) {
	t.Helper()
	response := slimtest.OpenStream(t, stream)
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Proto != "HTTP/1.0" || !response.Close || response.ContentLength != -1 ||
		len(response.TransferEncoding) != 0 || response.Header.Get("Content-Type") != "audio/flac" {
		t.Fatalf("HTTP 取流响应 %s %s，关闭 %t，长度 %d，传输编码 %v，头 %v",
			response.Proto, response.Status, response.Close, response.ContentLength, response.TransferEncoding, response.Header)
	}
	var magic [4]byte
	if _, err := io.ReadFull(response.Body, magic[:]); err != nil || string(magic[:]) != "fLaC" {
		t.Fatalf("音频开头 %q，错误 %v", magic, err)
	}
	if n, err := io.Copy(io.Discard, response.Body); n == 0 || err != nil {
		t.Fatalf("读取至连接关闭，余下 %d 字节，错误 %v", n, err)
	}
}

func checkSlimProtoAPISamples(t *testing.T, f *upnpFixture) {
	t.Helper()
	if settings := slimProtoSettings(t, f, false); settings.SlimProtoEnabled || settings.SlimProtoUnavailable != nil {
		t.Fatalf("关闭 SlimProto 后的设置 = %+v", settings)
	}
	occupied, err := net.Listen("tcp4", "0.0.0.0:3483")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	assertUPnPSample(t, "slimproto-port-in-use", f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"slimproto_enabled": true, "shared": true}, &f.admin, nil))
	assertUPnPSample(t, "slimproto-port-in-use", f.request(t, "GET", upnpAdmin+"/settings", nil, &f.admin, nil))
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	f.wait(t, 36*time.Second, "3483 释放后自动恢复监听", func() bool {
		settings := zonesSampleDecode[upnp.Settings](t, f.request(t, "GET", upnpAdmin+"/settings", nil, &f.admin, nil), 200)
		return settings.SlimProtoEnabled && settings.SlimProtoUnavailable == nil
	})
	p := startSlimProtoTestPlayer(t, f, 192000, "flc,pcm")
	f.wait(t, 3*time.Second, "SETD 名称更新", func() bool {
		items := slimProtoAdminDevices(t, f)
		return len(items) == 1 && items[0].Name == "书房"
	})
	clientIP := p.LocalAddr().(*net.TCPAddr).AddrPort().Addr().Unmap().String()
	assertUPnPSample(t, "slimproto-list", f.request(t, "GET", upnpAdmin+"/devices", nil, &f.admin, nil), "192.168.44.2", clientIP)
	path := upnpAdmin + "/devices/" + url.PathEscape(slimProtoTestKey) + "/format"
	assertUPnPSample(t, "slimproto-format", f.request(t, "PUT", path,
		map[string]any{"codec": "flac", "max_sample_rate": 96000, "volume": "fixed"}, &f.admin, nil))
}

func TestPostgreSQLSlimProtoFormats(t *testing.T) {
	f := newUPnPFixture(t, false)
	if settings := slimProtoSettings(t, f, true); !settings.SlimProtoEnabled || settings.SlimProtoUnavailable != nil {
		t.Fatalf("SlimProto 设置 = %+v", settings)
	}
	requireAPIError(t, f.request(t, "POST", upnpAdmin+"/devices",
		map[string]string{"kind": "slimproto", "address": "192.168.44.2"}, &f.admin, nil), 400, "invalid_input")
	member := f.createUser(t, "user")
	p := startSlimProtoTestPlayer(t, f, 384000, "pcm,mp3")
	unsupported := slimProtoDevice(t, f, &member)
	if rows := slimProtoAdminDevices(t, f); len(rows) != 1 || rows[0].Capabilities.SlimProto == nil || !rows[0].Capabilities.SlimProto.Unsupported {
		t.Fatalf("无 FLAC 的设备行 = %+v", rows)
	}
	requireAPIError(t, f.activate(t, &member, unsupported, false), 409, "device_unsupported")
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	f.wait(t, 3*time.Second, "无 FLAC 的播放器下线", func() bool { return len(slimProtoAdminDevices(t, f)) == 0 })
	p = startSlimProtoTestPlayer(t, f, 800000, "flc")
	rows := slimProtoAdminDevices(t, f)
	if len(rows) != 1 || rows[0].Capabilities.MaxSampleRate != 768000 || rows[0].Capabilities.SlimProto == nil || rows[0].Capabilities.SlimProto.DeclaredMaxSampleRate != 800000 {
		t.Fatalf("超过封顶值的设备行 = %+v", rows)
	}
	path := upnpAdmin + "/devices/" + url.PathEscape(slimProtoTestKey) + "/format"
	for _, rate := range []int{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000} {
		got := zonesSampleDecode[upnp.DeviceFormat](t, f.request(t, "PUT", path,
			map[string]any{"codec": "flac", "max_sample_rate": rate, "volume": "fixed"}, &f.admin, nil), 200)
		if got.Codec != "flac" || got.MaxSampleRate != rate || got.Volume != "fixed" {
			t.Fatalf("%d Hz 格式响应 = %+v", rate, got)
		}
	}
	for _, setting := range []map[string]any{
		{"codec": "flac", "max_sample_rate": 0, "volume": "number"},
		{"codec": "flac", "max_sample_rate": 12345, "volume": "number"},
		{"codec": "flac", "max_sample_rate": 800000, "volume": "number"},
		{"codec": "pcm", "max_sample_rate": 96000, "volume": "number"},
		{"codec": "flac", "max_sample_rate": 96000, "volume": "invalid"},
	} {
		requireAPIError(t, f.request(t, "PUT", path, setting, &f.admin, nil), 400, "invalid_input")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	f.wait(t, 3*time.Second, "高采样率播放器下线", func() bool { return len(slimProtoAdminDevices(t, f)) == 0 })
	startSlimProtoTestPlayer(t, f, 96000, "flc")
	requireAPIError(t, f.request(t, "PUT", path,
		map[string]any{"codec": "flac", "max_sample_rate": 176400, "volume": "number"}, &f.admin, nil), 400, "invalid_input")
	response := f.request(t, "PUT", path, map[string]any{"codec": "flac", "max_sample_rate": nil, "volume": "number"}, &f.admin, nil)
	format := zonesSampleDecode[map[string]any](t, response, 200)
	if rate, ok := format["max_sample_rate"]; !ok || rate != nil {
		t.Fatalf("跟随设备的格式响应 = %+v", format)
	}
	if rows := slimProtoAdminDevices(t, f); len(rows) != 1 || rows[0].Capabilities.MaxSampleRate != 96000 || rows[0].Capabilities.Volume != "number" {
		t.Fatalf("跟随设备后的设备行 = %+v", rows)
	}
}

func TestPostgreSQLSlimProtoPlayback(t *testing.T) {
	f := newUPnPFixture(t, true)
	library := createPlaybackLibrary(t, f.integrationTestApplication)
	tracks := []string{
		f.addTrackAtRate(t, library, 0, 44100, 8),
		f.addTrackAtRate(t, library, 1, 96000, 8),
		f.addTrackAtRate(t, library, 2, 192000, 8),
	}
	if settings := slimProtoSettings(t, f, true); settings.SlimProtoUnavailable != nil {
		t.Fatalf("SlimProto 设置 = %+v", settings)
	}
	member := f.createUser(t, "user")
	p := startSlimProtoTestPlayer(t, f, 384000, "flc")
	d := slimProtoDevice(t, f, &member)
	f.play(t, &member, d, tracks)
	first := p.expectControl(t, 's')
	p.Status(t, "STMc", 0)
	readSlimProtoTestAudio(t, first)
	p.Status(t, "STMs", 0)
	var seq, nextSeq int64
	f.wait(t, 5*time.Second, "首流出声且接续流已准备", func() bool {
		z := f.zone(t, &member, d)
		if z.Status != "playing" || z.Stream == nil || z.NextStream == nil {
			return false
		}
		seq, nextSeq = z.Stream.Seq, z.NextStream.Seq
		return true
	})
	p.expectNoControl(t, "首流交付完但未回 STMd")
	f.command(t, &member, d, map[string]any{"command": "pause"})
	p.expectControl(t, 'p')
	p.Status(t, "STMp", 400)
	f.wait(t, 3*time.Second, "暂停回报", func() bool { return f.zone(t, &member, d).Status == "paused" })
	f.command(t, &member, d, map[string]any{"command": "play"})
	p.expectControl(t, 'u')
	p.Status(t, "STMr", 400)
	f.wait(t, 3*time.Second, "继续回报", func() bool { return f.zone(t, &member, d).Status == "playing" })
	p.Status(t, "STMd", 400)
	second := p.expectControl(t, 's')
	p.Status(t, "STMc", 400)
	if z := f.zone(t, &member, d); z.Stream == nil || z.Stream.Seq != seq {
		t.Fatalf("接续流还未出声却提前晋升 = %+v", z.Stream)
	}
	p.Status(t, "STMs", 0)
	f.wait(t, 5*time.Second, "第二条流晋升且第三条流已准备", func() bool {
		z := f.zone(t, &member, d)
		return z.Status == "playing" && z.Stream != nil && z.Stream.Seq == nextSeq && z.NextStream != nil
	})
	p.Status(t, "STMd", 400)
	p.expectNoControl(t, "第二条流已回 STMd 但还未交付")
	readSlimProtoTestAudio(t, second)
	last := p.expectControl(t, 's')
	p.Status(t, "STMc", 400)
	readSlimProtoTestAudio(t, last)
	p.Status(t, "STMs", 0)
	f.wait(t, 5*time.Second, "最后一条流出声", func() bool {
		z := f.zone(t, &member, d)
		return z.Status == "playing" && z.Current != nil && z.Current.Track.TrackID == tracks[2]
	})
	p.Status(t, "STMu", 8000)
	f.wait(t, 5*time.Second, "STMu 后播放区归零暂停", func() bool {
		z := f.zone(t, &member, d)
		return z.Intent == "pause" && z.Status == "paused" && z.Stream == nil && z.Position.Seconds == 0
	})
	p.expectControl(t, 'q')
	f.play(t, &member, d, tracks[:1])
	restarted := p.expectControl(t, 's')
	p.Status(t, "STMc", 0)
	readSlimProtoTestAudio(t, restarted)
	p.Status(t, "STMs", 0)
	f.wait(t, 3*time.Second, "断线前输出已占用", func() bool {
		device, _ := deviceFor(f.snapshot(t, &member), slimProtoTestKey)
		return device.OccupiedBy != nil && device.OccupiedBy.UserID == member.id && f.zone(t, &member, d).Status == "playing"
	})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	f.wait(t, 3*time.Second, "断线后移除在线设备", func() bool { return len(slimProtoAdminDevices(t, f)) == 0 })
	startSlimProtoTestPlayer(t, f, 384000, "flc")
	back := slimProtoDevice(t, f, &member)
	f.wait(t, 3*time.Second, "断线后释放占用并暂停原播放区", func() bool {
		device, _ := deviceFor(f.snapshot(t, &member), slimProtoTestKey)
		z := f.zone(t, &member, back)
		return back.DeviceID == d.DeviceID && device.OccupiedBy == nil &&
			z.Intent == "pause" && z.Status == "paused" && z.Stream == nil
	})
}
