// Package stewardtools 将备份概况、启动与保留规则适配为管家工具。
package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func retention(raw json.RawMessage) (backup.Retention, error) {
	var rule backup.Retention
	if err := json.Unmarshal(raw, &rule); err != nil || (rule.KeepDays != nil && (*rule.KeepDays < 1 || *rule.KeepDays > 3650)) || (rule.KeepCount != nil && (*rule.KeepCount < 1 || *rule.KeepCount > 1000)) {
		return rule, &tool.Error{Code: tool.CodeInvalidInput, Message: "keep_days 需为 1–3650 的整数或 null，keep_count 需为 1–1000 的整数或 null"}
	}
	return rule, nil
}
func failure(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, backup.ErrBusy):
		return &tool.Error{Code: "backup_busy", Message: "另一份备份正在进行"}
	case errors.Is(err, backup.ErrNotWritable):
		return &tool.Error{Code: "backup_directory_not_writable", Message: "备份目录不可写"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return &tool.Error{Code: "backup_unavailable", Message: "备份服务暂不可用"}
	}
}
func StewardTools(service *backup.Service, db *sql.DB, logger *slog.Logger) []tool.Definition {
	makeDefinition := func(name, description string, scope tool.Scope, properties, required string, handler tool.Handler) tool.Definition {
		return tool.Definition{Name: name, Description: description, Scope: scope, MinRole: tool.RoleAdmin, Revertible: false, Untrusted: true,
			Available: func(context.Context, tool.Caller) bool { return service != nil },
			Input:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`), Handler: handler}
	}
	overview := makeDefinition("get_backup_overview", "查看备份运行状态、保留规则与最近最多 500 份备份的名字、时间和大小。", tool.ScopeRead, "", "", func(ctx context.Context, _ tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
		out, err := service.Overview(ctx)
		if err != nil {
			return nil, failure(err)
		}
		return json.Marshal(out)
	})
	start := makeDefinition("start_backup", "发起手动备份，返回是否已启动与状态；后台完成后按现有保留规则清理。不可撤销。", tool.ScopeShared, "", "", func(ctx context.Context, caller tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
		status, started, err := service.StartManual(ctx, db)
		if err != nil {
			return nil, failure(err)
		}
		if started {
			tool.LogConfigChanged(ctx, logger, caller, "start_backup", "backup")
		}
		return json.Marshal(map[string]any{"status": status, "started": started})
	})
	start.Preview = func(context.Context, tool.Caller, json.RawMessage) (tool.Preview, error) {
		return tool.Preview{Target: "backup", Changes: []tool.Change{{Field: "action", Before: nil, After: "启动手动备份，完成后按现有保留规则清理；不可撤销"}}, Summary: "启动手动备份，完成后按现有保留规则清理；不可撤销"}, nil
	}
	save := makeDefinition("set_backup_retention", "保存不会清理现有备份的保留规则；需要清理时请到设置页操作。keep_days 为 1–3650，keep_count 为 1–1000；两项必填，null 表示不限。不可撤销。", tool.ScopeShared, `"keep_days":{},"keep_count":{}`, `"keep_days","keep_count"`, func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
		rule, err := retention(raw)
		if err != nil {
			return nil, err
		}
		out, err := service.SaveRetention(ctx, rule, false, caller.User.ID)
		if err != nil {
			return nil, failure(err)
		}
		// SaveRetention 在同一临界区内重新计算；不能只依赖预览时的备份清单。
		if out.ConfirmationRequired {
			return nil, retentionWouldDelete()
		}
		tool.LogConfigChanged(ctx, logger, caller, "set_backup_retention", "backup_retention")
		return json.Marshal(map[string]any{"retention": rule, "deleted": out.Deleted, "failed": out.Failed})
	})
	save.Preview = func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (tool.Preview, error) {
		rule, err := retention(raw)
		if err != nil {
			return tool.Preview{}, err
		}
		before, err := service.Overview(ctx)
		if err != nil {
			return tool.Preview{}, failure(err)
		}
		count, err := service.PreviewRetention(ctx, rule)
		if err != nil {
			return tool.Preview{}, failure(err)
		}
		if count > 0 {
			return tool.Preview{}, retentionWouldDelete()
		}
		return tool.Preview{Target: "backup_retention", Changes: []tool.Change{{Field: "retention", Before: before.Retention, After: rule}}, Summary: "保存保留规则，不清理现有备份；不可撤销"}, nil
	}
	return []tool.Definition{overview, start, save}
}

func retentionWouldDelete() error {
	return &tool.Error{Code: "retention_would_delete_backups", Message: "新策略会清理现有备份，请到设置页操作"}
}
