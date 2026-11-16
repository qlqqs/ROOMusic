// Package stewardtools 将本人收藏与歌单服务适配为管家工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const idSchema = `{"type":"string","minLength":36,"maxLength":36}`
const pageProperties = `"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50}`
const playlistProperty = `"playlist_id":` + idSchema
const revisionProperty = `"expected_revision":{"type":"integer","minimum":1,"maximum":9007199254740991}`
const tracksProperty = `"track_ids":{"type":"array","items":` + idSchema + `}`
const nameProperty = `"name":{"type":"string","minLength":1,"maxLength":100}`
const kindProperty = `"kind":{"type":"string","enum":["track","album","artist"]}`

type input struct {
	personal.Command
	Kind      string `json:"kind"`
	TargetID  string `json:"target_id"`
	Favorited bool   `json:"favorited"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
}

func definition(name, description, properties, required string, scope tool.Scope, run func(context.Context, string, input) (any, error)) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleMember, Scope: scope, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			for _, id := range []*string{&p.PlaylistID, &p.ItemID, &p.TargetID, p.BeforeItemID} {
				if id != nil && *id != "" {
					if !uuid.Valid(*id) {
						return nil, toolError(tool.CodeInvalidInput, "标识必须是 UUID")
					}
					*id = strings.ToLower(*id)
				}
			}
			result, err := run(ctx, caller.User.ID, p)
			if err != nil {
				return nil, mapError(err)
			}
			return json.Marshal(result)
		}}
}
func toolError(code tool.ErrorCode, message string) error {
	return &tool.Error{Code: code, Message: message}
}
func mapError(err error) error {
	switch {
	case errors.Is(err, personal.ErrInvalidInput):
		return toolError(tool.CodeInvalidInput, "个人库参数无效")
	case errors.Is(err, personal.ErrNotFound):
		return toolError(tool.CodeTargetNotFound, "目标不存在")
	case errors.Is(err, personal.ErrRevisionConflict):
		return toolError("revision_conflict", "歌单版本已变化，请重新读取歌单后再操作")
	case errors.Is(err, personal.ErrPlaylistLimit):
		return toolError("playlist_limit_reached", "歌单数量已达上限")
	case errors.Is(err, personal.ErrTrackLimit):
		return toolError("playlist_track_limit_reached", "歌单曲目数量已达上限")
	case errors.Is(err, personal.ErrArtistCatalogPreparing):
		return toolError("artist_catalog_preparing", "艺人目录正在准备")
	case errors.Is(err, personal.ErrArtistCatalogNotReady):
		return toolError("artist_catalog_not_ready", "艺人目录暂不可用")
	default:
		return err
	}
}
func write(service *personal.Service, kind string) func(context.Context, string, input) (any, error) {
	return func(ctx context.Context, user string, p input) (any, error) {
		c := p.Command
		c.Kind = kind
		result, err := service.Execute(ctx, user, uuid.New(), c)
		if err != nil {
			return nil, err
		}
		if result.Status < 200 || result.Status >= 300 {
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(result.Body, &body); err != nil {
				return nil, err
			}
			if body.Error.Code == "" {
				return nil, toolError("tool_execution_failed", "歌单操作失败")
			}
			return nil, toolError(tool.ErrorCode(body.Error.Code), body.Error.Message)
		}
		return result.Body, nil
	}
}

// StewardTools 统一按调用者取数；歌单写入由 Execute 管理版本与幂等收据。
func StewardTools(service *personal.Service) []tool.Definition {
	return []tool.Definition{
		definition("list_favorites", "列出本人的收藏。kind 为 track、album 或 artist；page 默认 1，page_size 默认 20、最大 50。返回 items、pagination、revision。", kindProperty+`,`+pageProperties, `"kind"`, tool.ScopeRead,
			func(ctx context.Context, user string, p input) (any, error) {
				page := personal.Page{Number: p.Page, Size: p.PageSize}
				switch p.Kind {
				case "track":
					return service.FavoriteTracks(ctx, user, page)
				case "album":
					return service.FavoriteAlbums(ctx, user, page)
				default:
					return service.FavoriteArtists(ctx, user, page)
				}
			}),
		definition("list_playlists", "列出本人的歌单，返回 items 与 pagination。page 默认 1，page_size 默认及最大 20。", strings.Replace(pageProperties, `"maximum":50`, `"maximum":20`, 1), "", tool.ScopeRead,
			func(ctx context.Context, user string, p input) (any, error) {
				return service.Playlists(ctx, user, personal.Page{Number: p.Page, Size: p.PageSize})
			}),
		definition("get_playlist", "读取本人歌单及分页曲目。返回 playlist（含 revision）、items、pagination、page_after_item_id；写入时使用此 revision。page 默认 1，page_size 默认 20、最大 50。", playlistProperty+`,`+pageProperties, `"playlist_id"`, tool.ScopeRead,
			func(ctx context.Context, user string, p input) (any, error) {
				return service.PlaylistItems(ctx, user, p.PlaylistID, personal.Page{Number: p.Page, Size: p.PageSize})
			}),
		definition("set_favorite", "收藏或取消收藏。kind 为 track、album、artist，target_id 分别是曲目、专辑组、艺人 UUID，favorited 指定目标状态。返回收藏状态与 revision。", kindProperty+`,"target_id":`+idSchema+`,"favorited":{"type":"boolean"}`, `"kind","target_id","favorited"`, tool.ScopePersonal,
			func(ctx context.Context, user string, p input) (any, error) {
				return service.SetFavorite(ctx, user, p.Kind, p.TargetID, p.Favorited)
			}),
		definition("create_playlist", "创建本人歌单，name 必填，可附 track_ids（最多 10000）。返回 playlist 与加入、已有、重复数量。", nameProperty+`,`+tracksProperty, `"name"`, tool.ScopePersonal, write(service, "create")),
		definition("rename_playlist", "修改本人歌单名称。expected_revision 来自 get_playlist；返回 playlist 与 changed，版本冲突时重读歌单。", playlistProperty+`,`+revisionProperty+`,`+nameProperty, `"playlist_id","expected_revision","name"`, tool.ScopePersonal, write(service, "rename")),
		definition("delete_playlist", "删除本人歌单及其条目。expected_revision 来自 get_playlist；返回 playlist_id 与 deleted。", playlistProperty+`,`+revisionProperty, `"playlist_id","expected_revision"`, tool.ScopePersonal, write(service, "delete")),
		definition("add_playlist_tracks", "向本人歌单加入曲目，track_ids 为 1 至 10000 个 UUID。expected_revision 来自 get_playlist；返回 playlist 与加入、已有、重复数量。", playlistProperty+`,`+revisionProperty+`,`+tracksProperty, `"playlist_id","expected_revision","track_ids"`, tool.ScopePersonal, write(service, "add_items")),
		definition("remove_playlist_track", "移除本人歌单条目，item_id 来自 get_playlist，区别于曲目 ID。返回 playlist、removed 与 undo。", playlistProperty+`,`+revisionProperty+`,"item_id":`+idSchema, `"playlist_id","expected_revision","item_id"`, tool.ScopePersonal, write(service, "remove_item")),
		definition("move_playlist_track", "移动本人歌单条目。action 为 up、down、first、last 或 before；before 时 before_item_id 是锚点，省略表示末尾。返回 playlist、moved 与 changed。", playlistProperty+`,`+revisionProperty+`,"item_id":`+idSchema+`,"action":{"type":"string","enum":["up","down","first","last","before"]},"before_item_id":`+idSchema, `"playlist_id","expected_revision","item_id","action"`, tool.ScopePersonal, write(service, "move_item")),
	}
}
