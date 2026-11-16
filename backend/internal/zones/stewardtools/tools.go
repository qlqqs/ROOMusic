// Package stewardtools 将本人的播放分区适配为管家工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

const idSchema = `{"type":"string","minLength":36,"maxLength":36}`
const zoneProperty = `"zone_id":` + idSchema
const pageProperties = `"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50}`

type input struct {
	ZoneID          string     `json:"zone_id"`
	DeviceID        string     `json:"device_id"`
	Action          string     `json:"action"`
	TrackIDs        []string   `json:"track_ids"`
	ReleaseID       string     `json:"release_id"`
	Placement       string     `json:"placement"`
	EntryID         string     `json:"entry_id"`
	ToIndex         *int       `json:"to_index"`
	PositionSeconds *float64   `json:"position_seconds"`
	Mode            queue.Mode `json:"mode"`
	TargetZoneID    string     `json:"target_zone_id"`
	Volume          *float64   `json:"volume"`
	Muted           *bool      `json:"muted"`
	Page            int        `json:"page"`
	PageSize        int        `json:"page_size"`
}

func invalid(message string) error { return &tool.Error{Code: tool.CodeInvalidInput, Message: message} }
func mapError(err error) error {
	var code tool.ErrorCode
	message := ""
	switch {
	case errors.Is(err, zones.ErrNotFound), errors.Is(err, zones.ErrTrackNotFound), errors.Is(err, catalog.ErrReleaseNotFound):
		code, message = tool.CodeTargetNotFound, "目标不存在"
	case errors.Is(err, zones.ErrInvalidInput):
		code, message = tool.CodeInvalidInput, "播放参数无效"
	case errors.Is(err, zones.ErrZoneOffline):
		code, message = "zone_offline", "分区离线，请先连接输出设备"
	case errors.Is(err, zones.ErrRendererUnavailable):
		code, message = "renderer_unavailable", "没有可用的音频渲染器"
	case errors.Is(err, zones.ErrQueueFull):
		code, message = "queue_full", "播放队列已满"
	case errors.Is(err, zones.ErrVolumeFixed):
		code, message = "volume_fixed", "此设备不支持调整音量"
	default:
		return err
	}
	return &tool.Error{Code: code, Message: message}
}
func definition(name, description, properties, required string, scope tool.Scope, run func(context.Context, string, input) (any, error)) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleMember, Scope: scope, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			for _, id := range []*string{&p.ZoneID, &p.DeviceID, &p.ReleaseID, &p.EntryID, &p.TargetZoneID} {
				if *id != "" {
					if !uuid.Valid(*id) {
						return nil, invalid("标识必须是 UUID")
					}
					*id = strings.ToLower(*id)
				}
			}
			if len(p.TrackIDs) > queue.MaxQueueEntries {
				return nil, invalid("曲目最多 10000 首")
			}
			for i, id := range p.TrackIDs {
				if !uuid.Valid(id) {
					return nil, invalid("曲目标识必须是 UUID")
				}
				p.TrackIDs[i] = strings.ToLower(id)
			}
			value, err := run(ctx, caller.User.ID, p)
			if err != nil {
				return nil, mapError(err)
			}
			return json.Marshal(value)
		}}
}

// StewardTools 注入已装配的分区服务与目录读取服务，命令不自动重试。
func StewardTools(service *zones.Service, catalogStore *catalog.Store) []tool.Definition {
	command := func(ctx context.Context, user string, p input, c zones.Command) (any, error) {
		result, err := service.Command(ctx, user, p.ZoneID, c)
		if err != nil {
			return nil, err
		}
		var change any
		if q := result.QueueChange; q != nil {
			change = map[string]any{"kind": q.Kind, "queue_revision": q.QueueRevision}
		}
		out := map[string]any{"server_time": result.ServerTime, "zone": commandZoneView(result.Zone), "queue_change": change, "affected_entries": result.AffectedEntries}
		if result.TargetZone != nil {
			out["target_zone"] = commandZoneView(*result.TargetZone)
		}
		return out, nil
	}
	return []tool.Definition{
		definition("get_playback", "查看本人的播放状态和设备。省略 zone_id 返回全部分区；提供时只返回该分区及设备。没有当前分区上下文时先调用本工具。返回 zones、devices、server_time。", zoneProperty, "", tool.ScopeRead,
			func(ctx context.Context, user string, p input) (any, error) {
				snap, err := service.Snapshot(ctx, user)
				if err != nil {
					return nil, err
				}
				zs := []any{}
				ds := []any{}
				for _, z := range snap.Zones {
					if p.ZoneID == "" || z.ZoneID == p.ZoneID {
						zs = append(zs, zoneView(z))
					}
				}
				if p.ZoneID != "" && len(zs) == 0 {
					return nil, zones.ErrNotFound
				}
				for _, d := range snap.Devices {
					if p.ZoneID == "" || d.ZoneID == p.ZoneID {
						ds = append(ds, deviceView(d))
					}
				}
				return map[string]any{"server_time": snap.ServerTime, "zones": zs, "devices": ds}, nil
			}),
		definition("get_queue", "读取本人分区队列。返回 zone_id、queue_revision、entries 和 pagination；page 默认 1，page_size 默认 20、最大 50。", zoneProperty+`,`+pageProperties, `"zone_id"`, tool.ScopeRead,
			func(ctx context.Context, user string, p input) (any, error) {
				result, err := service.ReadQueue(ctx, user, p.ZoneID)
				if err != nil {
					return nil, err
				}
				start := min((p.Page-1)*p.PageSize, len(result.Entries))
				end := min(start+p.PageSize, len(result.Entries))
				return map[string]any{"zone_id": result.ZoneID, "queue_revision": result.QueueRevision, "entries": result.Entries[start:end], "pagination": map[string]any{"page": p.Page, "page_size": p.PageSize, "total": len(result.Entries)}}, nil
			}),
		definition("control_playback", "控制本人分区播放。action 为 play、pause、stop、next、previous、seek；seek 必须带 position_seconds。返回更新后的 zone、queue_change 摘要与 affected_entries。", zoneProperty+`,"action":{"type":"string","enum":["play","pause","stop","next","previous","seek"]},"position_seconds":{"type":"number","minimum":0}`, `"zone_id","action"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				c := zones.Command{Command: p.Action}
				if p.Action == "seek" {
					if p.PositionSeconds == nil {
						return nil, invalid("跳转需要 position_seconds")
					}
					c.PositionSeconds = *p.PositionSeconds
				} else if p.PositionSeconds != nil {
					return nil, invalid("只有跳转接受 position_seconds")
				}
				return command(ctx, user, p, c)
			}),
		definition("play_tracks", "将曲目或发行版本放入本人分区。track_ids 与 release_id 必须二选一；placement 为 replace（替换并播放）、next（下一首）、end（队尾）。发行按碟序和曲序加入；返回 zone、queue_change 摘要与 affected_entries（受影响条目数）。", zoneProperty+`,"track_ids":{"type":"array","items":`+idSchema+`},"release_id":`+idSchema+`,"placement":{"type":"string","enum":["replace","next","end"]}`, `"zone_id","placement"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				if (p.TrackIDs != nil) == (p.ReleaseID != "") {
					return nil, invalid("track_ids 与 release_id 必须二选一")
				}
				if p.ReleaseID != "" {
					media, err := catalogStore.LoadReleaseMedia(ctx, p.ReleaseID)
					if err != nil {
						return nil, err
					}
					for _, medium := range media {
						for _, track := range medium.Tracks {
							p.TrackIDs = append(p.TrackIDs, track.ID)
						}
					}
					if len(p.TrackIDs) == 0 {
						exists, err := catalogStore.ReleaseExists(ctx, p.ReleaseID)
						if err != nil {
							return nil, err
						}
						if !exists {
							return nil, catalog.ErrReleaseNotFound
						}
						return nil, &tool.Error{Code: "no_playable_tracks", Message: "这张专辑没有可播放的曲目"}
					}
				}
				if len(p.TrackIDs) == 0 {
					return nil, invalid("曲目列表不能为空")
				}
				name := map[string]string{"replace": "play_tracks", "next": "play_next", "end": "enqueue"}[p.Placement]
				return command(ctx, user, p, zones.Command{Command: name, TrackIDs: p.TrackIDs})
			}),
		definition("edit_queue", "编辑本人分区队列。action 为 remove、move、clear、play_entry；除 clear 外需要 entry_id；move 还需要从 0 开始的 to_index。返回 zone、queue_change 摘要与 affected_entries（受影响条目数）。", zoneProperty+`,"action":{"type":"string","enum":["remove","move","clear","play_entry"]},"entry_id":`+idSchema+`,"to_index":{"type":"integer","minimum":0,"maximum":9999}`, `"zone_id","action"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				if p.Action != "clear" && p.EntryID == "" {
					return nil, invalid("此动作需要 entry_id")
				}
				if p.Action == "clear" && p.EntryID != "" {
					return nil, invalid("清空不接受 entry_id")
				}
				c := zones.Command{Command: map[string]string{"remove": "remove_entry", "move": "move_entry", "clear": "clear_queue", "play_entry": "play_entry"}[p.Action], EntryID: p.EntryID}
				if p.Action == "move" {
					if p.ToIndex == nil {
						return nil, invalid("移动需要 to_index")
					}
					c.ToIndex = *p.ToIndex
				} else if p.ToIndex != nil {
					return nil, invalid("只有移动接受 to_index")
				}
				if p.Action != "clear" {
					result, err := service.ReadQueue(ctx, user, p.ZoneID)
					if err != nil {
						return nil, err
					}
					for _, entry := range result.Entries {
						if entry.EntryID == p.EntryID {
							return command(ctx, user, p, c)
						}
					}
					return nil, zones.ErrNotFound
				}
				return command(ctx, user, p, c)
			}),
		definition("set_play_mode", "设置本人分区模式：sequential 顺序、repeat_all 列表循环、repeat_one 单曲循环、shuffle 随机。返回 zone、queue_change 摘要与 affected_entries（受影响条目数）。", zoneProperty+`,"mode":{"type":"string","enum":["sequential","repeat_all","repeat_one","shuffle"]}`, `"zone_id","mode"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				return command(ctx, user, p, zones.Command{Command: "set_mode", Mode: p.Mode})
			}),
		definition("transfer_playback", "将本人源分区的队列和播放位置转移到本人 target_zone_id 并播放，源队列清空。返回 zone、target_zone、queue_change 摘要与 affected_entries（转移条目数）。", zoneProperty+`,"target_zone_id":`+idSchema, `"zone_id","target_zone_id"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				return command(ctx, user, p, zones.Command{Command: "transfer", TargetZoneID: p.TargetZoneID})
			}),
		definition("set_device_volume", "调整本人设备音量或静音，volume 为 0 至 1，muted 为布尔值，至少提供一项，省略项保留当前值。返回 device 与 server_time。", `"device_id":`+idSchema+`,"volume":{"type":"number","minimum":0,"maximum":1},"muted":{"type":"boolean"}`, `"device_id"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				if p.Volume == nil && p.Muted == nil {
					return nil, invalid("至少提供 volume 或 muted")
				}
				snap, err := service.Snapshot(ctx, user)
				if err != nil {
					return nil, err
				}
				for _, d := range snap.Devices {
					if d.DeviceID == p.DeviceID {
						volume, muted := d.Volume, d.Muted
						if p.Volume != nil {
							volume = *p.Volume
						}
						if p.Muted != nil {
							muted = *p.Muted
						}
						result, err := service.SetVolume(ctx, user, p.DeviceID, volume, muted)
						return map[string]any{"server_time": result.ServerTime, "device": deviceView(result.Device)}, err
					}
				}
				return nil, zones.ErrNotFound
			}),
	}
}
