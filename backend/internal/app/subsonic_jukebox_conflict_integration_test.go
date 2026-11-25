package app

import (
	"context"
	"net/url"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

type jukeboxConflictOutputs struct {
	*zonestest.GroupOutputs
	beforeAcquire func()
	beforeList    func(context.Context, string) error
}

func (o jukeboxConflictOutputs) Acquire(ctx context.Context, userID, deviceID, udn string, caps zones.Capabilities, takeover bool) error {
	if o.beforeAcquire != nil {
		o.beforeAcquire()
	}
	return o.GroupOutputs.Acquire(ctx, userID, deviceID, udn, caps, takeover)
}

func (o jukeboxConflictOutputs) VisibleDevices(ctx context.Context, userID string) ([]zones.ManagedDevice, error) {
	if o.beforeList != nil {
		if err := o.beforeList(ctx, userID); err != nil {
			return nil, err
		}
	}
	return o.GroupOutputs.VisibleDevices(ctx, userID)
}

func TestPostgreSQLSubsonicJukeboxRetriesWithLatestIndex(t *testing.T) {
	f := newJukeboxPlaybackFixture(t)
	c := zonesSampleCalls{t: t, f: f}
	outputs := &zonestest.GroupOutputs{Service: f.service, Devices: []zones.ManagedDevice{{
		Kind: "slimproto", UDN: "slim:02:00:00:00:00:01", Name: "合成重试设备",
		MaxSampleRate: 48000, Capabilities: zonesSampleCapabilities(), FormatDeclared: true,
	}}}
	f.service.SetManagedOutputs(outputs)
	snapshot := jukeboxSnapshot(c)
	if len(snapshot.Devices) != 1 {
		t.Fatalf("网页快照中设备数为 %d，期望 1", len(snapshot.Devices))
	}
	device := snapshot.Devices[0]
	id := device.ZoneID
	c.call("", "POST", "/devices/"+device.DeviceID+"/activate", map[string]any{}, 200)
	selectJukeboxZone(t, f, id)
	initial := c.command("", id, map[string]any{"command": "enqueue", "track_ids": f.tracks[:3]})
	before := requireJukeboxQueue(t, c, id, f.tracks[:3])
	stream := f.events(t, "")
	stream.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })

	// skip 已读取版本和条目。认领输出的回调在提交前插入一次真实的网页队列移动。
	acquisitions := 0
	f.service.SetManagedOutputs(jukeboxConflictOutputs{GroupOutputs: outputs, beforeAcquire: func() {
		acquisitions++
		if acquisitions == 1 {
			c.command("", id, map[string]any{"command": "move_entry", "entry_id": before.Entries[0].EntryID, "to_index": 2})
		}
	}})
	response := jukeboxRequest(f, jukeboxMember, "json", "skip", url.Values{"index": {"1"}, "offset": {"30"}})
	requireJukeboxStatus(t, requireSubsonicOK(t, response), 1, true, device.Volume, 30)
	if acquisitions != 2 {
		t.Fatalf("一次冲突后的认领次数为 %d，期望首次及一次重试", acquisitions)
	}
	requireJukeboxQueueEvent(t, stream, id, "move", initial.Zone.QueueRevision+1)
	after := requireJukeboxQueue(t, c, id, []string{f.tracks[1], f.tracks[2], f.tracks[0]})
	current := jukeboxSnapshotZone(c, id)
	if len(f.renderer.Sessions()) != 1 || current.Current == nil || current.Current.EntryID != before.Entries[2].EntryID ||
		current.Revision != initial.Zone.Revision+2 || after.QueueRevision != initial.Zone.QueueRevision+1 {
		t.Fatalf("重试后的状态或版本异常：%+v，队列版本 %d，渲染会话数 %d", current, after.QueueRevision, len(f.renderer.Sessions()))
	}
	requireJukeboxRenderStart(t, f, id, f.tracks[2], 30)
}

func TestPostgreSQLSubsonicJukeboxRejectsPersistentRevisionConflicts(t *testing.T) {
	for _, tc := range []struct {
		action  string
		options func([]string) url.Values
	}{
		{"add", func(tracks []string) url.Values { return url.Values{"id": tracks[3:4]} }},
		{"remove", func([]string) url.Values { return url.Values{"index": {"1"}} }},
	} {
		t.Run(tc.action, func(t *testing.T) {
			f := newJukeboxPlaybackFixture(t)
			c := zonesSampleCalls{t: t, f: f}
			device := c.device("")
			zoneID := device.Zone.ZoneID
			selectJukeboxZone(t, f, zoneID)
			initial := c.command("", zoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[:3]})
			before := jukeboxQueue(c, zoneID)
			stream := f.events(t, "")
			stream.wait(t, func(frame zonesSampleFrame) bool { return frame.Event == "snapshot" })

			// 每次尝试先读快照和队列，再进入命令的设备同步。此时推进区版本，队列版本不变。
			listCalls, conflicts := 0, 0
			f.service.SetManagedOutputs(jukeboxConflictOutputs{
				GroupOutputs: &zonestest.GroupOutputs{Service: f.service},
				beforeList: func(ctx context.Context, userID string) error {
					listCalls++
					if listCalls%3 != 0 {
						return nil
					}
					conflicts++
					_, err := f.service.SetOutputState(ctx, userID, device.Device.DeviceID, zones.OutputState{Online: false})
					return err
				},
			})
			failed := jukeboxRequest(f, jukeboxMember, "json", tc.action, tc.options(f.tracks))
			f.service.SetManagedOutputs(nil)
			code := 0
			body := requireSubsonicJSON(t, failed, 200, &code)
			if body["error"].(map[string]any)["message"] != "Playback zone changed concurrently" || conflicts != 2 || listCalls != 6 {
				t.Fatalf("持续冲突返回 %v，注入冲突 %d 次，设备同步 %d 次", body["error"], conflicts, listCalls)
			}

			after := requireJukeboxQueue(t, c, zoneID, f.tracks[:3])
			web := jukeboxSnapshotZone(c, zoneID)
			if !reflect.DeepEqual(before, after) || web.Revision != initial.Zone.Revision+2 {
				t.Fatalf("拒绝的请求改变了队列或额外推进了区版本：前 %+v，后 %+v，区版本 %d", before, after, web.Revision)
			}

			// 随后的网页入队充当 SSE 顺序屏障，屏障前出现的任何队列事件都会使断言失败。
			barrier := c.command("", zoneID, map[string]any{"command": "enqueue", "track_ids": f.tracks[4:]})
			if barrier.Zone.QueueRevision != before.QueueRevision+1 {
				t.Fatalf("网页入队的队列版本为 %d，失败请求前为 %d", barrier.Zone.QueueRevision, before.QueueRevision)
			}
			requireJukeboxQueueEvent(t, stream, zoneID, "insert", barrier.Zone.QueueRevision)
			requireJukeboxQueue(t, c, zoneID, []string{f.tracks[0], f.tracks[1], f.tracks[2], f.tracks[4]})
		})
	}
}
