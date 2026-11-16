package stewardtools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func (f *c5Fixture) device(t *testing.T, user string, online bool) zones.RegisterDeviceResult {
	t.Helper()
	ctx := context.Background()
	d, err := f.service.RegisterDevice(ctx, user, zones.RegisterDeviceInput{Name: "合成输出", Kind: "browser", Volume: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if online {
		if _, err := f.service.SetOutputState(ctx, user, d.Device.DeviceID, zones.OutputState{Online: true}); err != nil {
			t.Fatal(err)
		}
	}
	return d
}
func (f *c5Fixture) seedQueue(t *testing.T, user, zone string) {
	t.Helper()
	if _, err := f.service.Command(context.Background(), user, zone, zones.Command{Command: "play_tracks", TrackIDs: f.tracks}); err != nil {
		t.Fatal(err)
	}
}
func TestPostgreSQLStewardC5ZoneTools(t *testing.T) {
	f := newC5Fixture(t)
	names := []string{"get_playback", "get_queue", "control_playback", "play_tracks", "edit_queue", "set_play_mode", "transfer_playback", "set_device_volume"}
	if len(names) != len(StewardTools(f.service, nil)) {
		t.Fatal("工具覆盖清单漂移")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"success", "other_user", "missing", "offline"} {
				if scenario == "offline" && (name == "get_playback" || name == "get_queue") {
					continue
				}
				t.Run(scenario, func(t *testing.T) {
					owner := f.a
					if scenario == "other_user" {
						owner = f.b
					}
					d := f.device(t, owner, true)
					target := f.device(t, owner, true)
					zone := d.Zone.ZoneID
					f.seedQueue(t, owner, zone)
					q, err := f.service.ReadQueue(context.Background(), owner, zone)
					if err != nil {
						t.Fatal(err)
					}
					in := map[string]any{"zone_id": zone}
					switch name {
					case "control_playback":
						in["action"] = "pause"
					case "play_tracks":
						in["track_ids"] = f.tracks
						in["placement"] = "end"
					case "edit_queue":
						in["action"] = "remove"
						in["entry_id"] = q.Entries[0].EntryID
					case "set_play_mode":
						in["mode"] = "repeat_all"
					case "transfer_playback":
						in["target_zone_id"] = target.Zone.ZoneID
					case "set_device_volume":
						in = map[string]any{"device_id": d.Device.DeviceID, "volume": 0.25, "muted": true}
					}
					want := ""
					switch scenario {
					case "other_user":
						want = "not_found"
					case "missing":
						want = "not_found"
						if name == "set_device_volume" {
							in["device_id"] = uuid.New()
						} else {
							in["zone_id"] = uuid.New()
						}
					case "offline":
						if _, err := f.service.SetOutputState(context.Background(), owner, d.Device.DeviceID, zones.OutputState{Online: false}); err != nil {
							t.Fatal(err)
						}
					}
					before, err := f.service.Snapshot(context.Background(), owner)
					if err != nil {
						t.Fatal(err)
					}
					f.invoke(t, f.a, name, in, want)
					after, err := f.service.Snapshot(context.Background(), owner)
					if err != nil {
						t.Fatal(err)
					}
					if want != "" {
						if !reflect.DeepEqual(before.Zones, after.Zones) || !reflect.DeepEqual(before.Devices, after.Devices) {
							t.Fatal("被拒绝调用改变分区")
						}
						return
					}
					updated, err := f.service.ReadQueue(context.Background(), owner, zone)
					if err != nil {
						t.Fatal(err)
					}
					var current zones.Zone
					var device zones.Device
					for _, z := range after.Zones {
						if z.ZoneID == zone {
							current = z
						}
					}
					for _, v := range after.Devices {
						if v.DeviceID == d.Device.DeviceID {
							device = v
						}
					}
					switch name {
					case "control_playback":
						if current.Intent != "pause" {
							t.Fatal(current)
						}
					case "play_tracks":
						if len(updated.Entries) != 4 {
							t.Fatal(updated)
						}
					case "edit_queue":
						if len(updated.Entries) != 1 {
							t.Fatal(updated)
						}
					case "set_play_mode":
						if current.Mode != queue.Mode("repeat_all") {
							t.Fatal(current)
						}
					case "transfer_playback":
						to, err := f.service.ReadQueue(context.Background(), owner, target.Zone.ZoneID)
						if err != nil || len(to.Entries) != 2 || len(updated.Entries) != 0 {
							t.Fatalf("transfer=%+v from=%+v err=%v", to, updated, err)
						}
					case "set_device_volume":
						if device.Volume != 0.25 || !device.Muted {
							t.Fatal(device)
						}
					}
				})
			}
		})
	}
}

func TestPostgreSQLStewardC5ZoneActionsAndMissingTargets(t *testing.T) {
	f := newC5Fixture(t)
	ctx := context.Background()
	for _, action := range []string{"play", "pause", "stop", "next", "previous", "seek"} {
		t.Run("control/"+action, func(t *testing.T) {
			d := f.device(t, f.a, true)
			f.seedQueue(t, f.a, d.Zone.ZoneID)
			in := map[string]any{"zone_id": d.Zone.ZoneID, "action": action}
			if action == "seek" {
				in["position_seconds"] = 12
			}
			f.invoke(t, f.a, "control_playback", in, "")
		})
	}
	for _, placement := range []string{"replace", "next", "end"} {
		t.Run("release/"+placement, func(t *testing.T) {
			d := f.device(t, f.a, true)
			f.invoke(t, f.a, "play_tracks", map[string]any{"zone_id": d.Zone.ZoneID, "release_id": f.release, "placement": placement}, "")
			q, err := f.service.ReadQueue(ctx, f.a, d.Zone.ZoneID)
			if err != nil || len(q.Entries) != 2 || q.Entries[0].Track.TrackID != f.tracks[0] || q.Entries[1].Track.TrackID != f.tracks[1] {
				t.Fatalf("发行顺序错误：%+v %v", q, err)
			}
		})
	}
	for _, action := range []string{"move", "clear", "play_entry"} {
		t.Run("queue/"+action, func(t *testing.T) {
			d := f.device(t, f.a, true)
			f.seedQueue(t, f.a, d.Zone.ZoneID)
			q, err := f.service.ReadQueue(ctx, f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			in := map[string]any{"zone_id": d.Zone.ZoneID, "action": action}
			if action != "clear" {
				in["entry_id"] = q.Entries[1].EntryID
			}
			if action == "move" {
				in["to_index"] = 0
			}
			f.invoke(t, f.a, "edit_queue", in, "")
		})
	}

	t.Run("nested_missing", func(t *testing.T) {
		d := f.device(t, f.a, true)
		f.seedQueue(t, f.a, d.Zone.ZoneID)
		foreign := f.device(t, f.b, true)
		for _, tc := range []struct {
			name, tool string
			input      map[string]any
		}{
			{"release", "play_tracks", map[string]any{"release_id": uuid.New(), "placement": "end"}},
			{"track", "play_tracks", map[string]any{"track_ids": []string{uuid.New()}, "placement": "end"}},
			{"target_zone", "transfer_playback", map[string]any{"target_zone_id": uuid.New()}},
			{"foreign_target_zone", "transfer_playback", map[string]any{"target_zone_id": foreign.Zone.ZoneID}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tc.input["zone_id"] = d.Zone.ZoneID
				f.invoke(t, f.a, tc.tool, tc.input, "not_found")
			})
		}
	})

}

func TestPostgreSQLStewardC5ZoneRendererUnavailable(t *testing.T) {
	f := newC5Fixture(t)
	f.service.Close()
	f.service = zones.NewService(f.db, zones.Options{})
	t.Cleanup(f.service.Close)
	// 新服务从隔离数据库读取，不使用任何真实输出或媒体文件。
	d := f.device(t, f.a, true)
	r := StewardTools(f.service, nil)
	f.registry = tool.NewRegistry()
	for _, d := range r {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	f.invoke(t, f.a, "play_tracks", map[string]any{"zone_id": d.Zone.ZoneID, "track_ids": f.tracks, "placement": "replace"}, "renderer_unavailable")
	q, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
	if err != nil || len(q.Entries) != 0 {
		t.Fatalf("渲染器缺失改变队列：%+v %v", q, err)
	}
}

func TestPostgreSQLStewardC5QueueMissingEntryReturnsNotFound(t *testing.T) {
	f := newC5Fixture(t)
	d := f.device(t, f.a, true)
	f.seedQueue(t, f.a, d.Zone.ZoneID)
	foreign := f.device(t, f.b, true)
	f.seedQueue(t, f.b, foreign.Zone.ZoneID)
	foreignQueue, err := f.service.ReadQueue(context.Background(), f.b, foreign.Zone.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"remove", "move", "play_entry"} {
		for _, target := range []struct{ name, id string }{{"missing", uuid.New()}, {"other_user", foreignQueue.Entries[0].EntryID}} {
			t.Run(action+"/"+target.name, func(t *testing.T) {
				in := map[string]any{"zone_id": d.Zone.ZoneID, "action": action, "entry_id": target.id}
				if action == "move" {
					in["to_index"] = 0
				}
				f.invoke(t, f.a, "edit_queue", in, "not_found")
			})
		}
	}
}

func TestPostgreSQLStewardC5OfflineActionsFollowZoneService(t *testing.T) {
	f := newC5Fixture(t)
	for _, tc := range []struct {
		name, tool, action, placement string
		want                          string
	}{
		{"pause", "control_playback", "pause", "", ""},
		{"stop", "control_playback", "stop", "", ""},
		{"play", "control_playback", "play", "", "zone_offline"},
		{"enqueue", "play_tracks", "", "end", ""},
		{"play_next", "play_tracks", "", "next", ""},
		{"replace", "play_tracks", "", "replace", "zone_offline"},
		{"remove", "edit_queue", "remove", "", ""},
		{"move", "edit_queue", "move", "", ""},
		{"clear", "edit_queue", "clear", "", ""},
		{"play_entry", "edit_queue", "play_entry", "", "zone_offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := f.device(t, f.a, true)
			f.seedQueue(t, f.a, d.Zone.ZoneID)
			q, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.SetOutputState(context.Background(), f.a, d.Device.DeviceID, zones.OutputState{Online: false}); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"zone_id": d.Zone.ZoneID}
			switch tc.tool {
			case "control_playback":
				input["action"] = tc.action
			case "play_tracks":
				input["placement"], input["track_ids"] = tc.placement, f.tracks
			case "edit_queue":
				input["action"] = tc.action
				if tc.action != "clear" {
					input["entry_id"] = q.Entries[1].EntryID
				}
				if tc.action == "move" {
					input["to_index"] = 0
				}
			}
			before, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			f.invoke(t, f.a, tc.tool, input, tc.want)
			after, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("离线拒绝改变队列")
				}
				return
			}
			switch tc.name {
			case "enqueue", "play_next":
				if len(after.Entries) != 4 {
					t.Fatal(after)
				}
			case "remove":
				if len(after.Entries) != 1 || after.Entries[0].EntryID != q.Entries[0].EntryID {
					t.Fatal(after)
				}
			case "move":
				if after.Entries[0].EntryID != q.Entries[1].EntryID {
					t.Fatal(after)
				}
			case "clear":
				if len(after.Entries) != 0 {
					t.Fatal(after)
				}
			case "pause", "stop":
				if !reflect.DeepEqual(before.Entries, after.Entries) {
					t.Fatal("暂停/停止改变队列条目")
				}
			}
		})
	}
}

func TestPostgreSQLStewardC5ReleaseWithoutPlayableTracks(t *testing.T) {
	f := newC5Fixture(t)
	d := f.device(t, f.a, true)
	f.seedQueue(t, f.a, d.Zone.ZoneID)
	before, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	// 同时覆盖没有曲目的专辑，以及全部曲目已缺失的专辑。
	empty := uuid.New()
	if _, err := f.db.Exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
	 SELECT $1,group_id,'合成空专辑',artist,source_root_id,'empty-album','empty-c5','ordinary_directory' FROM releases WHERE id=$2`, empty, f.release); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE tracks SET source_status='missing' WHERE id=ANY($1::uuid[])`, f.tracks); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, release, code string }{
		{"empty", empty, "no_playable_tracks"},
		{"all_missing", f.release, "no_playable_tracks"},
		{"not_found", uuid.New(), "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.invoke(t, f.a, "play_tracks", map[string]any{"zone_id": d.Zone.ZoneID, "release_id": tc.release, "placement": "end"}, tc.code)
			after, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			// 缺失状态来自合成曲库更新，队列本身不得被失败调用改动。
			if after.QueueRevision != before.QueueRevision || len(after.Entries) != len(before.Entries) {
				t.Fatal(after)
			}
			for i, entry := range after.Entries {
				if entry.EntryID != before.Entries[i].EntryID {
					t.Fatal(after)
				}
			}
		})
	}
}

func c5AssertCommandSummary(t *testing.T, raw json.RawMessage, affected, queueLength int, kind string) map[string]any {
	t.Helper()
	if len(raw) > 64<<10 {
		t.Fatalf("写工具结果超出 64 KiB：%d", len(raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["affected_entries"] != float64(affected) || out["server_time"] == nil {
		t.Fatalf("受影响条目数或时间错误：%s", raw)
	}
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				switch key {
				case "entries", "failures", "source", "relative_path", "stream", "next_stream", "signal_path", "release_title":
					t.Errorf("写工具输出含非摘要字段 %s", key)
				}
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(out)
	zone, ok := out["zone"].(map[string]any)
	if !ok || zone["zone_id"] == nil || zone["status"] == nil || zone["queue_length"] != float64(queueLength) || zone["failure_count"] == nil {
		t.Fatalf("分区摘要错误：%s", raw)
	}
	if current, ok := zone["current"].(map[string]any); ok {
		track, ok := current["track"].(map[string]any)
		if !ok || current["entry_id"] == nil || track["track_id"] == nil || track["present"] != true || track["duration_seconds"] == nil {
			t.Fatalf("当前曲目摘要错误：%s", raw)
		}
		for _, key := range []string{"title", "artist"} {
			text, ok := track[key].(string)
			if !ok || len([]rune(text)) > 300 {
				t.Fatalf("当前曲目 %s 没有精简", key)
			}
		}
	}
	if kind == "" {
		if out["queue_change"] != nil {
			t.Fatalf("没有队列变动仍返回摘要：%s", raw)
		}
	} else {
		change, ok := out["queue_change"].(map[string]any)
		if !ok || change["kind"] != kind || change["queue_revision"] != zone["queue_revision"] || len(change) != 2 {
			t.Fatalf("队列变动摘要错误：%s", raw)
		}
	}
	return out
}

func TestPostgreSQLStewardC5WriteToolsReturnSummaries(t *testing.T) {
	f := newC5Fixture(t)
	for _, tc := range []struct {
		name, tool, action, placement, kind string
		affected, length                    int
	}{
		{"replace", "play_tracks", "", "replace", "replace", 3, 3},
		{"next", "play_tracks", "", "next", "insert", 3, 5},
		{"end", "play_tracks", "", "end", "insert", 3, 5},
		{"remove", "edit_queue", "remove", "", "remove", 1, 1},
		{"move", "edit_queue", "move", "", "move", 1, 2},
		{"clear", "edit_queue", "clear", "", "clear", 2, 0},
		{"play_entry", "edit_queue", "play_entry", "", "", 0, 2},
		{"pause", "control_playback", "pause", "", "", 0, 2},
		{"mode", "set_play_mode", "", "", "", 0, 2},
		{"transfer", "transfer_playback", "", "", "clear", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := f.device(t, f.a, true)
			f.seedQueue(t, f.a, d.Zone.ZoneID)
			q, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"zone_id": d.Zone.ZoneID}
			switch tc.tool {
			case "play_tracks":
				input["track_ids"], input["placement"] = []string{f.tracks[0], f.tracks[1], f.tracks[0]}, tc.placement
			case "edit_queue":
				input["action"] = tc.action
				if tc.action != "clear" {
					input["entry_id"] = q.Entries[1].EntryID
				}
				if tc.action == "move" {
					input["to_index"] = 0
				}
			case "control_playback":
				input["action"] = tc.action
			case "set_play_mode":
				input["mode"] = "repeat_all"
			case "transfer_playback":
				input["target_zone_id"] = f.device(t, f.a, true).Zone.ZoneID
			}
			raw := f.invoke(t, f.a, tc.tool, input, "")
			out := c5AssertCommandSummary(t, raw, tc.affected, tc.length, tc.kind)
			if tc.length > 0 && out["zone"].(map[string]any)["current"] == nil {
				t.Fatal("当前曲目摘要丢失")
			}
			if tc.tool == "transfer_playback" {
				target, ok := out["target_zone"].(map[string]any)
				if !ok || target["zone_id"] != input["target_zone_id"] || target["queue_length"] != float64(2) || target["current"] == nil {
					t.Fatalf("目标摘要错误：%s", raw)
				}
			}
		})
	}
}

func TestPostgreSQLStewardC5BulkEnqueueResultUnder64KiB(t *testing.T) {
	f := newC5Fixture(t)
	// 合成超长标签和满队列，证明写入不会因完整条目或当前曲目标签触发结果截断。
	if _, err := f.db.Exec(`UPDATE tracks SET title=$1,artist=$2 WHERE id=ANY($3::uuid[])`, strings.Repeat("曲", 600), strings.Repeat("人", 600), f.tracks); err != nil {
		t.Fatal(err)
	}
	for _, placement := range []string{"replace", "next", "end"} {
		t.Run(placement, func(t *testing.T) {
			d := f.device(t, f.a, true)
			tracks := make([]string, queue.MaxQueueEntries)
			for i := range tracks {
				tracks[i] = f.tracks[i%len(f.tracks)]
			}
			kind := "insert"
			if placement == "replace" {
				kind = "reload"
			}
			raw := f.invoke(t, f.a, "play_tracks", map[string]any{"zone_id": d.Zone.ZoneID, "track_ids": tracks, "placement": placement}, "")
			c5AssertCommandSummary(t, raw, len(tracks), len(tracks), kind)
			stored, err := f.service.ReadQueue(context.Background(), f.a, d.Zone.ZoneID)
			if err != nil || len(stored.Entries) != len(tracks) {
				t.Fatalf("批量入队未完成：entries=%d err=%v", len(stored.Entries), err)
			}
		})
	}
}
