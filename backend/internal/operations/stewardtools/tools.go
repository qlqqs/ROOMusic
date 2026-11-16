// Package stewardtools 将操作记录的公开读取能力适配为管家只读工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type input struct {
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
	ReleaseID string `json:"release_id"`
	GroupID   string `json:"group_id"`
	ArtistID  string `json:"artist_id"`
}

type operationItem struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	CreatedAt  time.Time               `json:"created_at"`
	Release    *operations.ReleaseInfo `json:"release"`
	Album      *operations.AlbumInfo   `json:"album"`
	Artist     *operations.ArtistInfo  `json:"artist"`
	Changes    any                     `json:"changes"`
	Reverts    *string                 `json:"reverts"`
	RevertedBy *string                 `json:"reverted_by"`
	Revertible bool                    `json:"revertible"`
}

// StewardTools 提供管理员操作记录查询，使用与 HTTP 相同的操作类型展示器，不返回操作者或内部状态。
func StewardTools(database operations.Queryer, kinds ...operations.Kind) []tool.Definition {
	registered := make(map[string]operations.Kind, len(kinds))
	for _, kind := range kinds {
		registered[kind.Type()] = kind
	}
	return []tool.Definition{{
		Name:        "list_operations",
		Description: "管理员追溯曲库修改时使用。按时间倒序返回目标当前摘要、改动摘要和撤销关系，不含操作者信息。release_id、group_id、artist_id 为可选 UUID，多个筛选取并集；按版本筛选也含以它为来源的认领。page 从 1 开始、默认 1；page_size 默认 20、最大 50；根据 pagination 翻页。",
		MinRole:     tool.RoleAdmin, Scope: tool.ScopeRead, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{
			"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50},
			"release_id":{"type":"string","minLength":36,"maxLength":36},"group_id":{"type":"string","minLength":36,"maxLength":36},"artist_id":{"type":"string","minLength":36,"maxLength":36}}}`),
		Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			params := operations.ListParams{Page: p.Page, PageSize: p.PageSize}
			for _, target := range []operations.Target{
				{Type: operations.TargetRelease, ID: p.ReleaseID},
				{Type: operations.TargetReleaseGroup, ID: p.GroupID},
				{Type: operations.TargetArtist, ID: p.ArtistID},
			} {
				if target.ID == "" {
					continue
				}
				if !uuid.Valid(target.ID) {
					return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "目标标识必须是 UUID"}
				}
				target.ID = strings.ToLower(target.ID)
				params.Targets = append(params.Targets, target)
				if target.Type == operations.TargetRelease {
					params.Sources = append(params.Sources, target.ID)
				}
			}
			listed, err := operations.List(ctx, database, params)
			if err != nil {
				return nil, err
			}
			descriptions, err := describe(ctx, database, registered, listed.Items)
			if err != nil {
				return nil, err
			}
			items := make([]operationItem, 0, len(listed.Items))
			for _, record := range listed.Items {
				d := descriptions[record.ID]
				items = append(items, operationItem{
					ID: record.ID, Type: record.Type, CreatedAt: record.CreatedAt,
					Release: d.Release, Album: d.Album, Artist: d.Artist, Changes: d.Changes,
					Reverts: optionalID(record.RevertsID), RevertedBy: optionalID(record.RevertedByID),
					Revertible: d.Revertible && record.Type != operations.TypeRevert && record.RevertedByID == "",
				})
			}
			return json.Marshal(map[string]any{"items": items, "pagination": map[string]any{"page": p.Page, "page_size": p.PageSize, "total": listed.Total}})
		},
	}}
}

func describe(ctx context.Context, database operations.Queryer, kinds map[string]operations.Kind, records []operations.Record) (map[string]operations.Description, error) {
	byType := map[string][]operations.Record{}
	for _, record := range records {
		byType[record.ChangeType()] = append(byType[record.ChangeType()], record)
	}
	result := map[string]operations.Description{}
	for changeType, batch := range byType {
		kind, ok := kinds[changeType]
		if !ok {
			continue
		}
		descriptions, err := kind.Describe(ctx, database, batch)
		if err != nil {
			return nil, err
		}
		for id, description := range descriptions {
			result[id] = description
		}
	}
	return result, nil
}

func optionalID(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
