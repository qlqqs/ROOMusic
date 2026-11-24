package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/devices/fakecast"
	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/insights/schema"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func startCastTestReceiver(t *testing.T, fixed bool) (*fakecast.Receiver, string, string, func()) {
	t.Helper()
	control, state := lanTestListener(t), lanTestListener(t)
	id := strings.ReplaceAll(uuid.New(), "-", "")
	r := fakecast.New(fakecast.Options{ID: id, Name: "合成 Cast 音箱", FixedVolume: fixed})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.Serve(ctx, control, state, nil) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	return r, "cast:" + id, control.Addr().String(), stop
}

func enableCast(t *testing.T, f *upnpFixture) {
	t.Helper()
	settings := zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"enabled": true, "upnp_enabled": false, "airplay_enabled": false}, &f.admin, nil), 200)
	if !settings.Enabled || !settings.CastEnabled || !settings.Shared || settings.UPnPEnabled {
		t.Fatalf("Cast 发现设置 = %+v", settings)
	}
}

func castSnapshotDevice(t *testing.T, f *upnpFixture, user *integrationTestUser, key string) zones.Device {
	t.Helper()
	var d zones.Device
	f.wait(t, 10*time.Second, "Cast 设备发现", func() bool {
		d, _ = deviceFor(f.snapshot(t, user), key)
		return d.Online
	})
	if d.Kind != "cast" || d.ExternalID != key {
		t.Fatalf("Cast 成员设备 = %+v", d)
	}
	return d
}

func castReceiverAction(t *testing.T, r *fakecast.Receiver, action string) {
	t.Helper()
	resp := httptest.NewRecorder()
	r.Handler().ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/_fake/"+action, nil))
	if resp.Code != http.StatusNoContent {
		t.Fatalf("接收端执行 %s 返回 %d，状态 %+v", action, resp.Code, r.State())
	}
}

func waitCastPlayback(t *testing.T, f *upnpFixture, r *fakecast.Receiver, user *integrationTestUser, d zones.Device, track, previousURI string) zones.Zone {
	t.Helper()
	var z zones.Zone
	f.wait(t, 12*time.Second, "Cast 播放曲目并拉取音频", func() bool {
		z = f.zone(t, user, d)
		s := r.State()
		return z.Status == "playing" && z.Current != nil && z.Current.Track.TrackID == track &&
			z.Stream != nil && z.Stream.Format != nil && s.PlayerState == "PLAYING" && s.URI != previousURI && s.BytesRead > 0
	})
	return z
}

func assertCastLoadedMedia(t *testing.T, address string, loaded fakecast.State) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c, err := castv2.Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reply, err := c.Request(ctx, castv2.ReceiverID, castv2.Receiver, map[string]any{"type": "GET_STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	var receiver castv2.ReceiverStatus
	if err := reply.DecodeStatus("RECEIVER_STATUS", &receiver); err != nil || len(receiver.Applications) != 1 {
		t.Fatalf("播放时的 RECEIVER_STATUS = %+v，错误 %v", receiver, err)
	}
	transport := receiver.Applications[0].TransportID
	if err := c.Connect(transport); err != nil {
		t.Fatal(err)
	}
	reply, err = c.Request(ctx, transport, castv2.Media, map[string]any{"type": "GET_STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		MediaSessionID int `json:"mediaSessionId"`
		Media          struct {
			ContentID   string `json:"contentId"`
			ContentType string `json:"contentType"`
			StreamType  string `json:"streamType"`
			Duration    float64
		}
	}
	if err := reply.DecodeStatus("MEDIA_STATUS", &rows); err != nil || len(rows) != 1 {
		t.Fatalf("播放时的 MEDIA_STATUS 有 %d 条记录，错误 %v", len(rows), err)
	}
	row := rows[0]
	if row.MediaSessionID != loaded.MediaSessionID || row.Media.ContentID != loaded.URI ||
		row.Media.ContentType != "audio/mpeg" || row.Media.StreamType != "BUFFERED" || row.Media.Duration != 7 {
		t.Fatalf("LOAD 的媒体会话、格式、缓冲模式或时长错误：会话 %d，MIME %q，模式 %q，时长 %g",
			row.MediaSessionID, row.Media.ContentType, row.Media.StreamType, row.Media.Duration)
	}
}

func TestPostgreSQLCastPlayback(t *testing.T) {
	f := newUPnPFixture(t, true)
	enableCast(t, f)
	r, key, address, stop := startCastTestReceiver(t, false)
	member := f.createUser(t, "user")
	d := castSnapshotDevice(t, f, &member, key)
	if d.MaxSampleRate != 48000 || d.Capabilities.Volume != "number" {
		t.Fatalf("默认 Cast 能力 = %+v", d)
	}
	tracks := f.tracks(t, 7, 30, 30)
	var releaseID string
	if err := f.database.QueryRow("SELECT media.release_id FROM tracks JOIN media ON tracks.medium_id=media.id WHERE tracks.id=$1", tracks[0]).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	var cover bytes.Buffer
	if err := png.Encode(&cover, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	if err := artwork.NewStore(f.dataDirectory).Save(t.Context(), f.database, releaseID, cover.Bytes(), "embedded"); err != nil {
		t.Fatal(err)
	}

	f.play(t, &member, d, tracks)
	firstZone := waitCastPlayback(t, f, r, &member, d, tracks[0], "")
	first := r.State()
	var metadata struct {
		MetadataType int    `json:"metadataType"`
		Title        string `json:"title"`
		Artist       string `json:"artist"`
		Album        string `json:"albumName"`
		Images       []struct{ URL string }
	}
	if err := json.Unmarshal(first.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if first.AppID != castv2.DefaultApp || first.LoadCount != 1 || firstZone.Stream.MimeType != "audio/mpeg" ||
		firstZone.Stream.Format.Codec != "mp3" || len(firstZone.Stream.Segments) != 1 ||
		metadata.MetadataType != 3 || metadata.Title != "海 & 浪" || metadata.Artist != firstZone.Current.Track.Artist ||
		metadata.Album != firstZone.Current.Track.Release.Title || len(metadata.Images) != 1 || metadata.Images[0].URL != first.URI+"/artwork" {
		t.Fatalf("初次 Cast LOAD = %+v，元数据 %+v，流 %+v", first, metadata, firstZone.Stream)
	}
	assertCastLoadedMedia(t, address, first)
	path := strings.TrimPrefix(first.URI, f.server.URL)
	if !strings.HasPrefix(path, "/upnp/streams/") {
		t.Fatal("LOAD 未使用局域网托管流")
	}
	requireAPIError(t, upnpRouteRequest(t, f, "HEAD", path, "192.0.2.44:9000"), 404, "not_found")
	castCover := upnpRouteRequest(t, f, "GET", path+"/artwork", "192.168.10.2:9000")
	if castCover.Code != 200 || !bytes.Equal(castCover.Body.Bytes(), cover.Bytes()) {
		t.Fatalf("Cast 封面令牌请求 = %d，响应 %d 字节", castCover.Code, castCover.Body.Len())
	}

	secondZone := waitCastPlayback(t, f, r, &member, d, tracks[1], first.URI)
	second := r.State()
	if second.LoadCount != 2 || secondZone.Stream.Seq == firstZone.Stream.Seq ||
		len(secondZone.Stream.Segments) != 1 || secondZone.Stream.Segments[0].StreamStartSeconds != 0 ||
		secondZone.Event == nil || secondZone.Event.Kind != "track_started" || secondZone.Event.EntryID != secondZone.Current.EntryID {
		t.Fatalf("自然播完后的下一首 = %+v，流 %+v", second, secondZone.Stream)
	}

	f.command(t, &member, d, map[string]any{"command": "pause"})
	f.wait(t, 6*time.Second, "服务端暂停 Cast", func() bool {
		return r.State().PlayerState == "PAUSED" && f.zone(t, &member, d).Status == "paused"
	})
	f.command(t, &member, d, map[string]any{"command": "play"})
	waitCastPlayback(t, f, r, &member, d, tracks[1], first.URI)
	if got := r.State(); got.URI != second.URI || got.LoadCount != second.LoadCount {
		t.Fatalf("暂停继续后重新加载了媒体，LOAD 次数 %d", got.LoadCount)
	}

	for _, action := range []string{"pause", "play"} {
		castReceiverAction(t, r, action)
		want := map[string]string{"pause": "paused", "play": "playing"}[action]
		f.wait(t, 6*time.Second, "Cast 设备端 "+action+" 事件", func() bool {
			z := f.zone(t, &member, d)
			return z.Status == want && z.Intent == action && z.Event != nil && z.Event.Kind == action && z.Event.Source == "device"
		})
	}
	zonesSampleDecode[zones.DeviceResult](t, f.request(t, "PUT", zonesSamplePrefix+"/devices/"+d.DeviceID+"/volume",
		map[string]any{"volume": 0.27, "muted": false}, &member, nil), 200)
	f.wait(t, 6*time.Second, "Cast 执行 SET_VOLUME", func() bool { return r.State().Volume == 0.27 })

	f.command(t, &member, d, map[string]any{"command": "next"})
	waitCastPlayback(t, f, r, &member, d, tracks[2], second.URI)
	castReceiverAction(t, r, "stop")
	f.wait(t, 6*time.Second, "Cast CANCELLED 暂停并释放占用", func() bool {
		z := f.zone(t, &member, d)
		current, _ := deviceFor(f.snapshot(t, &member), key)
		return z.Intent == "pause" && z.Status == "paused" && z.Stream == nil && current.OccupiedBy == nil &&
			z.Event != nil && z.Event.Kind == "pause" && z.Event.Source == "device"
	})
	stopped := r.State()
	f.command(t, &member, d, map[string]any{"command": "play"})
	beforeTakeover := waitCastPlayback(t, f, r, &member, d, tracks[2], stopped.URI)
	castReceiverAction(t, r, "takeover")
	f.wait(t, 6*time.Second, "别的 Cast 应用接管后释放输出", func() bool {
		z := f.zone(t, &member, d)
		current, _ := deviceFor(f.snapshot(t, &member), key)
		return z.Intent == "pause" && z.Status == "paused" && z.Stream == nil && current.OccupiedBy == nil &&
			r.State().AppID == "FAKEOTHER"
	})

	takeover := f.zone(t, &member, d)
	if e := takeover.Event; e == nil || e.Kind != "pause" || e.Source != "device" || e.Seq <= beforeTakeover.Revision {
		t.Errorf("接管释放后缺少新的设备端 pause 事件：之前 revision=%d，当前 revision=%d，事件=%+v",
			beforeTakeover.Revision, takeover.Revision, e)
	}

	f.command(t, &member, d, map[string]any{"command": "play"})
	waitCastPlayback(t, f, r, &member, d, tracks[2], "")
	lost := r.State()
	stop()
	f.wait(t, 8*time.Second, "Cast 断开后离线并撤销流令牌", func() bool {
		current, visible := deviceFor(f.snapshot(t, &member), key)
		path := strings.TrimPrefix(lost.URI, f.server.URL)
		return (!visible || !current.Online) && upnpRouteRequest(t, f, "HEAD", path, "192.168.10.2:9000").Code == 404
	})
}

func TestPostgreSQLCastSharingAndFormats(t *testing.T) {
	f := newUPnPFixture(t, true)
	enableCast(t, f)
	r, key, address, _ := startCastTestReceiver(t, true)
	ip := netip.MustParseAddrPort(address).Addr().String()
	a, b := f.createUser(t, "user"), f.createUser(t, "user")
	path := upnpAdmin + "/devices/" + url.PathEscape(key)
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", upnpAdmin + "/settings", nil},
		{"GET", upnpAdmin + "/devices", nil},
		{"POST", upnpAdmin + "/devices", map[string]string{"kind": "cast", "address": ip}},
		{"PUT", path + "/format", upnp.DeviceFormat{Codec: "aac", MaxSampleRate: 44100}},
	} {
		requireAPIError(t, f.request(t, req.method, req.path, req.body, &a, nil), 403, "forbidden")
	}
	da, db := castSnapshotDevice(t, f, &a, key), castSnapshotDevice(t, f, &b, key)
	if da.DeviceID == db.DeviceID || da.ZoneID == db.ZoneID || da.Capabilities.Volume != "fixed" {
		t.Fatalf("固定音量 Cast 的两个成员设备 = %+v / %+v", da, db)
	}
	requireAPIError(t, f.request(t, "PUT", zonesSamplePrefix+"/devices/"+da.DeviceID+"/volume",
		map[string]any{"volume": 0.27, "muted": false}, &a, nil), 409, "volume_fixed")

	tracks := f.tracksAtRate(t, 96000, 40, 40)
	f.play(t, &a, da, tracks[:1])
	first := waitCastPlayback(t, f, r, &a, da, tracks[0], "")
	owner := castSnapshotDevice(t, f, &b, key).OccupiedBy
	if owner == nil || owner.UserID != a.id || r.State().Volume != 0.5 {
		t.Fatalf("占用者 = %+v，固定音量 = %g", owner, r.State().Volume)
	}
	requireAPIError(t, f.activate(t, &b, db, false), 409, "device_busy")

	zonesSampleDecode[upnp.Settings](t, f.request(t, "PUT", upnpAdmin+"/settings",
		map[string]bool{"shared": false}, &f.admin, nil), 200)
	if _, visible := deviceFor(f.snapshot(t, &b), key); visible {
		t.Fatal("关闭共享后未分配的成员仍看见 Cast 设备")
	}
	requireAPIError(t, f.activate(t, &b, db, true), 404, "not_found")
	f.wait(t, 6*time.Second, "关闭共享后释放 Cast 播放", func() bool { return r.State().PlayerState == "IDLE" })
	zonesSampleDecode[map[string][]string](t, f.request(t, "PUT", path+"/grants",
		map[string][]string{"user_ids": {a.id, b.id}}, &f.admin, nil), 200)
	if got := castSnapshotDevice(t, f, &a, key); got.DeviceID != da.DeviceID {
		t.Fatalf("重新分配后 Cast 播放区身份改变：%+v", got)
	}
	f.play(t, &a, da, tracks[:1])
	waitCastPlayback(t, f, r, &a, da, tracks[0], "")
	previous := r.State().URI
	f.command(t, &b, db, map[string]any{"command": "play_tracks", "track_ids": tracks[1:], "start_index": 0, "takeover": true})
	z := waitCastPlayback(t, f, r, &b, db, tracks[1], previous)
	if z.Stream.Format.Codec != "mp3" || first.Stream.Format.Codec != "mp3" {
		t.Fatal("默认效果未使用 MP3 渲染")
	}
	if old := f.zone(t, &a, da); old.Intent != "pause" || old.Status != "paused" || old.Stream != nil || old.QueueLength != 1 {
		t.Fatalf("成员 B 接管后的原播放区 = %+v", old)
	}
	for _, tc := range []struct {
		format upnp.DeviceFormat
		mime   string
	}{
		{upnp.DeviceFormat{Codec: "aac", MaxSampleRate: 44100}, "audio/aac"},
		{upnp.DeviceFormat{Codec: "flac", MaxSampleRate: 96000}, "audio/flac"},
		{upnp.DeviceFormat{Codec: "mp3", MaxSampleRate: 48000}, "audio/mpeg"},
	} {
		previous = r.State().URI
		if got := zonesSampleDecode[upnp.DeviceFormat](t, f.request(t, "PUT", path+"/format", tc.format, &f.admin, nil), 200); got != tc.format {
			t.Fatalf("格式 PUT = %+v，期望 %+v", got, tc.format)
		}
		if got := castSnapshotDevice(t, f, &b, key); got.MaxSampleRate != tc.format.MaxSampleRate {
			t.Fatalf("格式采样率上限未传给播放区：%+v", got)
		}
		f.play(t, &b, db, tracks[1:])
		z := waitCastPlayback(t, f, r, &b, db, tracks[1], previous)
		if z.Stream.Format.Codec != tc.format.Codec || z.Stream.Format.SampleRate != tc.format.MaxSampleRate ||
			z.Stream.MimeType != tc.mime {
			t.Fatalf("选择 %+v 后新播放的流 MIME=%q，音频格式=%+v", tc.format, z.Stream.MimeType, z.Stream.Format)
		}
	}

	status := zonesSampleDecode[struct{ Payload json.RawMessage }](t, f.request(t, "GET", insightsAdminPath, nil, &f.admin, nil), 200)
	payload, err := schema.Decode(bytes.NewReader(status.Payload))
	if err != nil || payload.Schema != schema.Version || payload.Devices30d.Cast != "1" {
		t.Fatalf("两位成员共用同一 Cast 的统计 = %+v，错误 %v", payload, err)
	}
}
