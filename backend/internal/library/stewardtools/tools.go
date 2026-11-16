// Package stewardtools 将扫描的公开读取能力适配为管家只读工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const pageProperties = `"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50}`
const pageDescription = "page 从 1 开始，默认 1；page_size 默认 20、最大 50；根据 pagination 翻页。"

type input struct {
	ID       string `json:"id"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

func definition(name, description string, role tool.Role, properties, required string, read func(context.Context, tool.Caller, input) (any, error)) tool.Definition {
	return tool.Definition{
		Name: name, Description: description, MinRole: role, Scope: tool.ScopeRead, Untrusted: true,
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`),
		Output: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			if p.ID != "" && !uuid.Valid(p.ID) {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "扫描标识必须是 UUID"}
			}
			p.ID = strings.ToLower(p.ID)
			value, err := read(ctx, caller, p)
			if errors.Is(err, library.ErrScanNotFound) {
				return nil, &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
			}
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		},
	}
}

// StewardTools 提供扫描记录、报告与当前状态工具；复用 app 的扫描服务，不创建扫描任务。
func StewardTools(service *library.Service) []tool.Definition {
	return []tool.Definition{
		definition("list_scans", "管理员查看历史扫描记录时使用。按开始时间倒序返回扫描状态、时间、模式、进度与诊断计数；不含根目录名称或路径。"+pageDescription,
			tool.RoleAdmin, pageProperties, "",
			func(ctx context.Context, caller tool.Caller, p input) (any, error) {
				result, err := service.ListScans(ctx, p.Page, p.PageSize)
				if err != nil {
					return nil, err
				}
				items := make([]any, 0, len(result.Items))
				for _, scan := range result.Items {
					items = append(items, presentScan(scan, caller.User.Role == "admin"))
				}
				return map[string]any{"items": items, "pagination": map[string]any{"page": p.Page, "page_size": p.PageSize, "total": result.Total}}, nil
			}),
		definition("get_scan_report", "管理员查看一次扫描结果时使用，id 是扫描 UUID。返回冻结的文件、专辑、曲目与问题统计和新增专辑；分页只作用于 added_albums.items，分页信息在 added_albums.pagination。"+pageDescription,
			tool.RoleAdmin, `"id":{"type":"string","minLength":36,"maxLength":36},`+pageProperties, `"id"`,
			func(ctx context.Context, _ tool.Caller, p input) (any, error) {
				return service.GetScanReport(ctx, p.ID, p.Page, p.PageSize)
			}),
		definition("get_scan_status", "查看当前正在执行的扫描时使用，无参数、不分页。返回 scan（无活动扫描时为 null），含状态、时间、模式、进度和诊断计数；成员不能读取根目录身份，所有人均不获得根目录名称或路径。",
			tool.RoleMember, "", "",
			func(ctx context.Context, caller tool.Caller, _ input) (any, error) {
				scan, err := service.ActiveScan(ctx)
				if err != nil {
					return nil, err
				}
				var value any
				if scan != nil {
					value = presentScan(*scan, caller.User.Role == "admin")
				}
				return map[string]any{"scan": value}, nil
			}),
	}
}

func presentScan(scan library.Scan, admin bool) any {
	var root any
	if r := scan.RootProgress; r != nil {
		value := map[string]any{"index": r.Index, "total": r.Total}
		if admin {
			value["id"] = r.ID
		}
		// 不透传 RootProgress.Name（可能包含目录位置）；只返回进度与管理员可见的根 ID。
		root = value
	}
	return struct {
		library.Scan
		Root any `json:"root_progress"`
	}{scan, root}
}
