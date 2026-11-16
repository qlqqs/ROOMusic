package stewardtools

import "github.com/qlqq/roomusic/backend/internal/zones"

// 输出只选择播放所需事实，不包含流地址、输出会话标识和信号链内部数据。
func zoneView(z zones.Zone) map[string]any {
	return map[string]any{
		"zone_id": z.ZoneID, "device_id": z.DeviceID, "revision": z.Revision, "queue_revision": z.QueueRevision,
		"queue_length": z.QueueLength, "mode": z.Mode, "current": z.Current, "has_next": z.HasNext,
		"intent": z.Intent, "status": z.Status, "fault": z.Fault, "failures": z.Failures, "position": z.Position,
	}
}

// 命令结果只保留当前曲目的必要字段，避免队列与失败条目让写入结果超限。
func commandZoneView(z zones.Zone) map[string]any {
	out := zoneView(z)
	delete(out, "failures")
	out["failure_count"] = len(z.Failures)
	out["current"] = nil
	if current := z.Current; current != nil {
		out["current"] = map[string]any{"entry_id": current.EntryID, "track": map[string]any{
			"track_id": current.Track.TrackID, "title": summaryText(current.Track.Title),
			"artist": summaryText(current.Track.Artist), "present": current.Track.Present,
			"duration_seconds": current.Track.DurationSeconds,
		}}
	}
	return out
}

func summaryText(text string) string {
	runes := []rune(text)
	return string(runes[:min(len(runes), 300)])
}

func deviceView(d zones.Device) map[string]any {
	return map[string]any{"device_id": d.DeviceID, "zone_id": d.ZoneID, "name": d.Name, "kind": d.Kind, "online": d.Online, "volume": d.Volume, "muted": d.Muted}
}
