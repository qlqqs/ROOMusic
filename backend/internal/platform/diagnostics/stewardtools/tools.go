// Package stewardtools 将安全诊断适配为管理员只读工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func StewardTools(service *diagnostics.Service) []tool.Definition {
	read := func(name, description, properties string, run tool.Handler) tool.Definition {
		return tool.Definition{Name: name, Description: description, MinRole: tool.RoleAdmin, Scope: tool.ScopeRead, Untrusted: true,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: run}
	}
	return []tool.Definition{
		read("get_system_status", "查看版本、启动时间、运行时长、数据库连通与连接池、迁移版本、曲库统计。", "", func(ctx context.Context, _ tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
			out, err := service.SystemStatus(ctx)
			if err != nil {
				return nil, err
			}
			return json.Marshal(out)
		}),
		read("get_disk_usage", "查看数据、备份及启用的音乐目录所在磁盘总量与可用量，不返回绝对路径。", "", func(ctx context.Context, _ tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
			out, err := service.DiskUsage(ctx)
			if err != nil {
				return nil, &tool.Error{Code: "diagnostics_unavailable", Message: "暂时无法读取磁盘信息"}
			}
			return json.Marshal(map[string]any{"items": out})
		}),
		read("list_recent_logs", "查看已脱敏的结构化日志（最新在前），不含消息正文。level 精确匹配；start/end 为 RFC3339，含边界。limit 默认 100、最多 200。", `"level":{"type":"string","enum":["DEBUG","INFO","WARN","ERROR"]},"module":{"type":"string","maxLength":128},"start":{"type":"string","maxLength":40},"end":{"type":"string","maxLength":40},"limit":{"type":"integer","minimum":1,"maximum":200}`, func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Level, Module, Start, End string
				Limit                     int
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			filter := diagnostics.LogFilter{Level: p.Level, Module: strings.TrimSpace(p.Module), Limit: p.Limit}
			for _, entry := range []struct {
				text   string
				target *time.Time
			}{{p.Start, &filter.Start}, {p.End, &filter.End}} {
				if entry.text != "" {
					t, err := time.Parse(time.RFC3339Nano, entry.text)
					if err != nil {
						return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "时间必须为 RFC3339"}
					}
					*entry.target = t
				}
			}
			if !filter.Start.IsZero() && !filter.End.IsZero() && filter.Start.After(filter.End) {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "起始时间不能晚于结束时间"}
			}
			return json.Marshal(map[string]any{"items": service.Logs.Recent(filter)})
		}),
	}
}
