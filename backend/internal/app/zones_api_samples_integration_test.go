package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

// 请求与 SSE 保留自己的信封，不伪装为 JSON HTTP 响应；共用已有规范化和敏感值检测。
func (r *apiSampleRecorder) recordZonesPayload(name string, payload any) {
	r.t.Helper()
	name = "playback-zones/" + name
	if _, exists := r.samples[name]; exists {
		r.t.Fatalf("重复样本 %s", name)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, secret := range r.forbidden {
		if apiSampleContainsSecret(string(data), secret) {
			r.t.Fatalf("样本 %s 含敏感值", name)
		}
	}
	normalized := encodeAPISample(r.t, name, 0, data)
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		r.t.Fatal(err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, envelope.Body, "", "  "); err != nil {
		r.t.Fatal(err)
	}
	out.WriteByte('\n')
	r.samples[name] = out.Bytes()
}

type zonesSampleCalls struct {
	t       *testing.T
	f       *zonesSampleFixture
	samples *apiSampleRecorder
}

func (c zonesSampleCalls) call(name, method, path string, body any, status int) *httptest.ResponseRecorder {
	return c.as(name, method, path, body, status, &c.f.user, nil)
}
func (c zonesSampleCalls) as(name, method, path string, body any, status int, user *integrationTestUser, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, c.f.origin+zonesSamplePrefix+path, bytes.NewReader(data))
	request.Header.Set("X-Request-ID", sampleRequestID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && method != "HEAD" {
		request.Header.Set("Origin", c.f.origin)
	}
	if user != nil {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	c.f.handler.ServeHTTP(response, request)
	if response.Code != status {
		c.t.Fatalf("%s: 状态 %d，期望 %d：%s", name, response.Code, status, response.Body.String())
	}
	if c.samples != nil && name != "" {
		var sent any
		if len(data) > 0 {
			if err := json.Unmarshal(data, &sent); err != nil {
				c.t.Fatal(err)
			}
		}
		c.samples.recordZonesPayload("requests/"+name, map[string]any{"method": method, "path": zonesSamplePrefix + path, "body": sent})
		if status == 204 {
			if response.Body.Len() != 0 {
				c.t.Fatal("204 不应有响应体")
			}
			c.samples.recordZonesPayload("responses/"+name, map[string]any{"status": status, "body": nil})
		} else {
			c.samples.record("playback-zones/responses/"+name, response, status)
		}
	}
	return response
}
func (c zonesSampleCalls) command(name, id string, body map[string]any) zones.CommandResult {
	return zonesSampleDecode[zones.CommandResult](c.t, c.call(name, "POST", "/zones/"+id+"/commands", body, 200), 200)
}
func (c zonesSampleCalls) claim(name, device string, takeover bool, playing *zones.PlayingOutput) zones.ClaimOutputResult {
	return zonesSampleDecode[zones.ClaimOutputResult](c.t, c.call(name, "POST", "/devices/"+device+"/output", map[string]any{"protocol": 1, "capabilities": zonesSampleCapabilities(), "takeover": takeover, "playing": playing}, 200), 200)
}
func (c zonesSampleCalls) report(name, session, kind, status string, seq *int64, seconds float64, fault *string) zones.OutputResult {
	return zonesSampleDecode[zones.OutputResult](c.t, c.call(name, "POST", "/outputs/"+session+"/reports", zones.ReportOutputInput{Kind: kind, Status: status, StreamSeq: seq, StreamSeconds: seconds, Fault: fault}, 200), 200)
}
func (c zonesSampleCalls) device(name string) zones.RegisterDeviceResult {
	return zonesSampleDecode[zones.RegisterDeviceResult](c.t, c.call(name, "POST", "/devices", map[string]any{"name": "样本设备", "kind": "browser", "volume": 0.5, "muted": false, "settings": map[string]any{}}, 201), 201)
}

func TestPostgreSQLZonesAPIResponseSamples(t *testing.T) {
	f := newZonesSampleFixture(t)
	samples := newAPISampleRecorder(t, f.integrationTestApplication)
	samples.scope = "playback-zones"
	samples.forbid(f.user.token)
	c := zonesSampleCalls{t: t, f: f, samples: samples}
	c.call("snapshot-empty", "GET", "", nil, 200)
	events := f.events(t, "")
	samples.recordZonesPayload("requests/events", events.request)
	samples.recordZonesPayload("events/snapshot", events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" }))
	device := c.device("register-device")
	id := device.Zone.ZoneID
	did := device.Device.DeviceID
	samples.recordZonesPayload("events/device", events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "device" }))
	c.call("rename-device", "PATCH", "/devices/"+did, map[string]any{"name": "客厅"}, 200)
	c.call("volume", "PUT", "/devices/"+did+"/volume", map[string]any{"volume": 0.8, "muted": true}, 200)
	output := c.claim("claim", did, false, nil)
	c.call("snapshot", "GET", "", nil, 200)
	c.call("queue-empty", "GET", "/zones/"+id+"/queue", nil, 200)
	started := c.command("play-tracks", id, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:3], "start_index": 0})
	samples.recordZonesPayload("events/queue", events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "queue" }))
	samples.recordZonesPayload("events/zone-track-started", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "track_started" }))
	queue := zonesSampleDecode[zones.QueueResult](t, c.call("queue", "GET", "/zones/"+id+"/queue", nil, 200), 200)
	for _, status := range []string{"loading", "playing", "buffering", "paused"} {
		c.report("status-"+status, output.SessionID, "status", status, &started.Zone.Stream.Seq, 0, nil)
	}
	c.report("", output.SessionID, "status", "playing", &started.Zone.Stream.Seq, 0, nil)
	f.clock.Advance(11 * time.Second)
	session := f.renderer.Sessions()[0]
	f.emit(t, session, zones.Event{Kind: "time", EntryID: queue.Entries[0].EntryID, Seconds: 11, At: f.clock.Now()})
	samples.recordZonesPayload("events/zone-play-recorded", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "play_recorded" }))
	f.emit(t, session, zones.Event{Kind: "format_boundary", NextEntryID: queue.Entries[1].EntryID, Reason: "format"})
	boundary := events.zone(t, id, func(z zones.Zone) bool { return z.NextStream != nil })
	samples.recordZonesPayload("events/zone-next-stream", boundary)
	var boundaryData struct {
		Zone zones.Zone `json:"zone"`
	}
	if err := json.Unmarshal(boundary.Data, &boundaryData); err != nil {
		t.Fatal(err)
	}
	c.call("snapshot-next-stream", "GET", "", nil, 200)
	c.report("next-stream-playing", output.SessionID, "status", "playing", &boundaryData.Zone.NextStream.Seq, 0, nil)
	current := c.command("pause", id, map[string]any{"command": "pause"})
	c.command("play", id, map[string]any{"command": "play"})
	fault := "autoplay_blocked"
	blocked := c.report("fault-autoplay-blocked", output.SessionID, "fault", "paused", &current.Zone.Stream.Seq, 0, &fault)
	samples.recordZonesPayload("events/zone-autoplay-blocked", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "autoplay_blocked" }))
	c.command("", id, map[string]any{"command": "play"})
	fault = "stream_lost"
	recovered := c.report("fault-stream-lost", output.SessionID, "fault", "buffering", &blocked.Zone.Stream.Seq, 2, &fault)
	c.report("ended", output.SessionID, "ended", "paused", &recovered.Zone.Stream.Seq, 0, nil)
	c.command("enqueue", id, map[string]any{"command": "enqueue", "track_ids": f.tracks[3:]})
	c.command("play-next", id, map[string]any{"command": "play_next", "track_ids": f.tracks[3:4]})
	c.command("move-entry", id, map[string]any{"command": "move_entry", "entry_id": queue.Entries[2].EntryID, "delta": -1})
	c.command("remove-entry", id, map[string]any{"command": "remove_entry", "entry_id": queue.Entries[2].EntryID})
	c.command("play-entry", id, map[string]any{"command": "play_entry", "entry_id": queue.Entries[0].EntryID})
	c.command("set-mode", id, map[string]any{"command": "set_mode", "mode": "repeat_all"})
	c.command("next", id, map[string]any{"command": "next"})
	c.command("previous", id, map[string]any{"command": "previous"})
	c.command("seek", id, map[string]any{"command": "seek", "position_seconds": 3})
	f.clock.Advance(300 * time.Millisecond)
	c.command("set-settings", id, map[string]any{"command": "set_settings", "settings": map[string]any{"leveling": "album", "crossfade": true, "crossfade_seconds": 8}})
	f.clock.Advance(300 * time.Millisecond)
	c.command("stop", id, map[string]any{"command": "stop"})
	f.renderer.openErr = errors.New("测试注入：渲染器暂忙")
	c.command("play-transient", id, map[string]any{"command": "play"})
	samples.recordZonesPayload("events/zone-transient-failure", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "transient_failure" }))
	f.renderer.openErr = nil
	c.command("retry", id, map[string]any{"command": "retry"})
	c.command("", id, map[string]any{"command": "stop"})
	f.renderer.negotiateErr = errors.New("测试注入：无可用格式")
	c.command("play-no-stream-format", id, map[string]any{"command": "play"})
	samples.recordZonesPayload("events/zone-no-stream-format", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "no_stream_format" }))
	f.renderer.negotiateErr = nil
	c.command("clear-queue", id, map[string]any{"command": "clear_queue"})
	imported := c.command("import", id, map[string]any{"command": "import", "entries": []map[string]string{{"entry_id": "legacy-1", "track_id": f.tracks[0]}}, "current_entry_id": "legacy-1", "mode": "sequential", "position_seconds": 2, "history": []string{}})
	samples.recordZonesPayload("events/zone-queue-restored", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "queue_restored" }))
	c.command("import-unchanged", id, map[string]any{"command": "import", "entries": []any{}, "current_entry_id": nil, "mode": "sequential", "position_seconds": 0, "history": []string{}})
	other := c.device("register-target")
	target := c.claim("claim-target", other.Device.DeviceID, false, nil)
	transfer := c.command("transfer", id, map[string]any{"command": "transfer", "target_zone_id": other.Zone.ZoneID})
	c.call("release", "POST", "/outputs/"+target.SessionID+"/release", zones.ReleaseOutputInput{StreamSeq: &transfer.TargetZone.Stream.Seq, StreamSeconds: 2}, 204)
	resumed := c.claim("claim-takeover", other.Device.DeviceID, true, nil)
	c.call("release-before-playing", "POST", "/outputs/"+resumed.SessionID+"/release", zones.ReleaseOutputInput{StreamSeq: &resumed.Zone.Stream.Seq, StreamSeconds: 3}, 204)
	c.claim("claim-playing", other.Device.DeviceID, false, &zones.PlayingOutput{EntryID: imported.Zone.Current.EntryID, PositionSeconds: 3})

	// 空首流通过真实 handler 投影 null 格式，并记录真实音频响应头，不把音频伪装成 JSON。
	f.renderer.empty = true
	empty := c.command("play-empty-format", id, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:1], "start_index": 0})
	if empty.Zone.Stream == nil || empty.Zone.Stream.Format != nil {
		t.Fatal("空首流必须具有 format:null")
	}
	raw := c.call("", "GET", fmt.Sprintf("/zones/%s/streams/%d", id, empty.Zone.Stream.Seq), nil, 200)
	samples.recordZonesPayload("responses/stream-headers", map[string]any{"status": raw.Code, "headers": map[string]string{"Content-Type": raw.Header().Get("Content-Type"), "Cache-Control": raw.Header().Get("Cache-Control"), "Accept-Ranges": raw.Header().Get("Accept-Ranges"), "X-Content-Type-Options": raw.Header().Get("X-Content-Type-Options"), "X-Accel-Buffering": raw.Header().Get("X-Accel-Buffering"), "Content-Length": raw.Header().Get("Content-Length")}})
	samples.recordZonesPayload("requests/stream", map[string]any{"method": "GET", "path": empty.Zone.Stream.URL, "body": nil})
	failedSession := f.renderer.Sessions()[len(f.renderer.Sessions())-1]
	f.emit(t, failedSession, zones.Event{Kind: "track_failed", EntryID: empty.Zone.Current.EntryID, Reason: "decode_failed"})
	samples.recordZonesPayload("events/zone-track-failed", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "track_failed" }))
	// 终结事件可能先被消费并关闭会话，再完成 Emit 确认；后续 SSE 才是完成证据。
	if err := failedSession.Emit(zones.Event{Kind: "stream_ended"}); err != nil && !errors.Is(err, zonestest.ErrClosed) {
		t.Fatal(err)
	}
	samples.recordZonesPayload("events/zone-all-failed", events.zone(t, id, func(z zones.Zone) bool { return z.Event != nil && z.Event.Kind == "all_failed" }))
	f.renderer.empty = false
	c.command("play-failures", id, map[string]any{"command": "play_tracks", "track_ids": f.tracks, "start_index": 0})
	failures := zonesSampleDecode[zones.QueueResult](t, c.call("", "GET", "/zones/"+id+"/queue", nil, 200), 200)
	failedSession = f.renderer.Sessions()[len(f.renderer.Sessions())-1]
	for i, e := range failures.Entries {
		if err := failedSession.Emit(zones.Event{Kind: "track_failed", EntryID: e.EntryID, Reason: "source_unavailable"}); err != nil && !(i == 4 && errors.Is(err, zonestest.ErrClosed)) {
			t.Fatal(err)
		}
		frame := events.zone(t, id, func(z zones.Zone) bool {
			return len(z.Failures) == i+1 && (i != 4 || (z.Event != nil && z.Event.Kind == "failures_stopped"))
		})
		if i == 4 {
			samples.recordZonesPayload("events/zone-failures-stopped", frame)
		}
	}

	// 错误矩阵每个状态/错误码取一条真实边界请求。
	c.as("errors/unauthorized", "GET", "", nil, 401, nil, nil)
	c.as("errors/origin-required", "PATCH", "/devices/"+did, map[string]any{"name": "拒绝"}, 403, &f.user, map[string]string{"Origin": ""})
	c.as("errors/origin-forbidden", "PATCH", "/devices/"+did, map[string]any{"name": "拒绝"}, 403, &f.user, map[string]string{"Origin": "http://foreign.test"})
	c.call("errors/invalid-id", "GET", "/zones/not-a-uuid/queue", nil, 400)
	c.call("errors/invalid-json", "POST", "/devices", []string{"not-an-object"}, 400)
	c.commandError("invalid-input", id, map[string]any{"command": "unknown"}, 400)
	c.call("errors/unsupported-protocol", "POST", "/devices/"+did+"/output", map[string]any{"protocol": 2, "capabilities": zonesSampleCapabilities(), "takeover": false, "playing": nil}, 400)
	c.call("errors/not-found", "GET", "/zones/"+uuid.New()+"/queue", nil, 404)
	c.commandError("track-not-found", id, map[string]any{"command": "enqueue", "track_ids": []string{uuid.New()}}, 404)
	c.call("errors/output-unknown", "POST", "/outputs/"+uuid.New()+"/reports", zones.ReportOutputInput{Kind: "status", Status: "paused"}, 404)
	newer := c.claim("claim-replacement", did, false, nil)
	c.call("errors/output-superseded", "POST", "/outputs/"+output.SessionID+"/reports", zones.ReportOutputInput{Kind: "status", Status: "paused"}, 409)
	c.call("errors/stream-gone", "GET", fmt.Sprintf("/zones/%s/streams/%d", id, empty.Zone.Stream.Seq), nil, 410)
	c.call("errors/device-online", "DELETE", "/devices/"+did, nil, 409)
	fixed := zonesSampleCapabilities()
	fixed.Volume = "fixed"
	fixedOutput := zonesSampleDecode[zones.ClaimOutputResult](t, c.call("claim-fixed", "POST", "/devices/"+did+"/output", map[string]any{"protocol": 1, "capabilities": fixed, "takeover": false, "playing": nil}, 200), 200)
	c.call("errors/volume-fixed", "PUT", "/devices/"+did+"/volume", map[string]any{"volume": 0.4, "muted": false}, 409)
	c.call("release-fixed", "POST", "/outputs/"+fixedOutput.SessionID+"/release", zones.ReleaseOutputInput{}, 204)
	c.commandError("zone-offline", id, map[string]any{"command": "play"}, 409)
	c.command("", id, map[string]any{"command": "clear_queue"})
	// 容量错误的准备不属于事件样本；避免把一万条 insert 的大帧纳入采集器。
	events.close()
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = f.tracks[0]
	}
	c.command("", id, map[string]any{"command": "enqueue", "track_ids": ids})
	c.commandError("queue-full", id, map[string]any{"command": "enqueue", "track_ids": f.tracks[:1]}, 409)
	c.command("", id, map[string]any{"command": "clear_queue"})
	events = f.events(t, "")
	events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })
	f.exec(t, `CREATE FUNCTION zones_sample_write_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '测试注入'; END $$`)
	f.exec(t, `CREATE TRIGGER zones_sample_write_failure BEFORE UPDATE ON playback_devices FOR EACH ROW EXECUTE FUNCTION zones_sample_write_failure()`)
	c.call("errors/database-unavailable", "PATCH", "/devices/"+did, map[string]any{"name": "数据库失败"}, 503)
	f.exec(t, `DROP TRIGGER zones_sample_write_failure ON playback_devices`)
	c.call("delete-device", "DELETE", "/devices/"+did, nil, 204)
	samples.recordZonesPayload("events/device-removed", events.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "device_removed" }))
	c.call("release-unknown", "POST", "/outputs/"+newer.SessionID+"/release", zones.ReleaseOutputInput{}, 204)
	// 新合同使用末尾独立播放区，不改变既有样本的设置、事件顺序与 revision。
	{
		device := c.device("")
		id := device.Zone.ZoneID
		c.claim("", device.Device.DeviceID, false, nil)
		headroom := zones.Headroom{Enabled: true, DB: -3.1, ClipIndicator: true}
		settings := c.command("set-settings-headroom", id, map[string]any{"command": "set_settings", "settings": map[string]any{"headroom": headroom}})
		if settings.Zone.Settings.Headroom != headroom {
			t.Fatalf("设置样本未保留非默认 Headroom：%+v", settings.Zone.Settings.Headroom)
		}
		c.command("", id, map[string]any{"command": "play_tracks", "track_ids": f.tracks[:1], "start_index": 0})
		// 经渲染端口与真实 SSE 投影生成非空路径，而非直接拼出响应样本。
		signal := map[string]any{
			"quality": "enhanced", "clipping": true, "processing_speed": 85.3,
			"steps": []map[string]any{
				{"kind": "source", "quality": "lossless", "lossy": false, "compat": false, "params": map[string]any{"codec": "wav", "sample_rate": 48000, "bit_depth": 16, "channels": 2}},
				{"kind": "headroom", "quality": "enhanced", "lossy": false, "compat": false, "params": map[string]any{"gain_db": -3.1}},
				{"kind": "dither", "quality": "enhanced", "lossy": false, "compat": false, "params": map[string]any{"bits": 16, "kind": "tpdf"}},
				{"kind": "encode", "quality": "lossless", "lossy": false, "compat": false, "params": map[string]any{"codec": "wav", "sample_rate": 48000, "bit_depth": 16}},
			},
		}
		wantSignal, err := json.Marshal(signal)
		if err != nil {
			t.Fatal(err)
		}
		sessions := f.renderer.Sessions()
		f.emit(t, sessions[len(sessions)-1], zones.Event{Kind: "signal", Signal: signal})
		samples.recordZonesPayload("events/zone-signal", events.zone(t, id, func(z zones.Zone) bool {
			if z.Signal == nil {
				return false
			}
			got, err := json.Marshal(z.Signal)
			if err != nil || !bytes.Equal(got, wantSignal) {
				t.Fatalf("信号事件未原样投影：signal=%s err=%v", got, err)
			}
			return true
		}))
	}
	samples.finish()
}
func (c zonesSampleCalls) commandError(name, id string, body map[string]any, status int) {
	c.call("errors/"+name, "POST", "/zones/"+id+"/commands", body, status)
}
