package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const configID = `{"type":"string","minLength":36,"maxLength":36}`
const configRevision = `{"type":"integer","minimum":1,"maximum":9007199254740990}`
const configPath = `{"type":"string","minLength":1,"maxLength":4096}`

type configInput struct {
	RootID           string `json:"root_id"`
	ScanID           string `json:"scan_id"`
	Path             string `json:"path"`
	ExpectedRevision int64  `json:"expected_revision"`
	Enabled          bool   `json:"enabled"`
	Hour             int    `json:"hour"`
	Mode             string `json:"mode"`
}

type configAdapter struct {
	service *library.Service
	allowed []string
	logger  *slog.Logger
}

// ConfigTools 复用目录操作收据、扫描协调与计划服务；允许根仅由装配注入。
func ConfigTools(service *library.Service, allowedRoots []string, logger *slog.Logger) []tool.Definition {
	a := configAdapter{service, append([]string(nil), allowedRoots...), logger}
	definitions := []tool.Definition{
		definition("list_library_roots", "查看音乐目录及排除规则；仅返回目录名、状态、revision 与相对排除路径。", tool.RoleAdmin, "", "", func(ctx context.Context, _ tool.Caller, _ input) (any, error) {
			roots, err := service.ListRoots(ctx)
			return map[string]any{"items": roots}, configError(err)
		}),
		definition("get_scan_schedule", "查看每日定时扫描的开关与整点（实例时区）。", tool.RoleAdmin, "", "", func(ctx context.Context, _ tool.Caller, _ input) (any, error) {
			out, err := service.LoadScanSchedule(ctx)
			return out, configError(err)
		}),
	}
	root := `"root_id":` + configID
	definitions = append(definitions,
		a.write("add_library_root", "添加允许范围内的音乐目录，不自动扫描。不可撤销。", `"path":`+configPath, `"path"`),
		a.write("set_library_root_enabled", "停用或恢复音乐目录，expected_revision 来自目录列表。不可撤销。", root+`,"expected_revision":`+configRevision+`,"enabled":{"type":"boolean"}`, `"root_id","expected_revision","enabled"`),
		a.write("add_root_exclusion", "添加目录下不扫描的子文件夹。path 为允许范围内的子目录绝对路径，由目录服务校验。不可撤销。", root+`,"path":`+configPath, `"root_id","path"`),
		a.write("remove_root_exclusion", "移除目录排除规则，path 为列表中的相对路径。不可撤销。", root+`,"path":`+configPath, `"root_id","path"`),
		a.write("start_scan", "启动全库扫描，mode 默认 incremental，可选 full；已有扫描时返回已有任务。不可撤销。", `"mode":{"type":"string","enum":["incremental","full"]}`, ""),
		a.write("cancel_scan", "请求取消指定扫描，不等待扫描退出。不可撤销。", `"scan_id":`+configID, `"scan_id"`),
		a.write("set_scan_schedule", "保存每日定时扫描开关与整点（实例时区，0–23）。不可撤销。", `"enabled":{"type":"boolean"},"hour":{"type":"integer","minimum":0,"maximum":23}`, `"enabled","hour"`),
	)
	return definitions
}
func decodeConfig(raw json.RawMessage) (configInput, error) {
	p := configInput{Mode: "incremental"}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	for _, id := range []*string{&p.RootID, &p.ScanID} {
		if *id != "" {
			if !uuid.Valid(*id) {
				return p, &tool.Error{Code: tool.CodeInvalidInput, Message: "标识必须是 UUID"}
			}
			*id = strings.ToLower(*id)
		}
	}
	return p, nil
}
func (a configAdapter) write(name, description, properties, required string) tool.Definition {
	return tool.Definition{Name: name, Description: description, MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: false, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
		Preview: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (tool.Preview, error) {
			p, err := decodeConfig(raw)
			if err != nil {
				return tool.Preview{}, err
			}
			preview, err := a.preview(ctx, name, p)
			return preview, configError(err)
		},
		Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p, err := decodeConfig(raw)
			if err != nil {
				return nil, err
			}
			out, target, err := a.run(ctx, caller, name, p)
			if err != nil {
				var operationError *library.RootOperationError
				if errors.As(err, &operationError) && operationError.RecordFailure && a.logger != nil {
					a.logger.ErrorContext(ctx, "record root operation", "request_id", caller.RequestID, "error", operationError.Err)
				}
				return nil, configError(err)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				return nil, err
			}
			if target != "" {
				tool.LogConfigChanged(ctx, a.logger, caller, name, target)
			}
			return encoded, nil
		}}
}
func (a configAdapter) preview(ctx context.Context, name string, p configInput) (tool.Preview, error) {
	var selected library.RootInfo
	preview := tool.Preview{Changes: []tool.Change{}, Summary: "此配置操作不可撤销"}
	if p.RootID != "" {
		roots, err := a.service.ListRoots(ctx)
		if err != nil {
			return preview, err
		}
		for _, root := range roots {
			if root.ID == p.RootID {
				preview.Target = root.Name
				selected = root
				break
			}
		}
		if preview.Target == "" {
			return preview, &tool.Error{Code: tool.CodeTargetNotFound, Message: "目录不存在"}
		}
	}
	switch name {
	case "set_scan_schedule":
		before, err := a.service.LoadScanSchedule(ctx)
		if err != nil {
			return preview, err
		}
		preview.Target = "scan_schedule"
		preview.Changes = []tool.Change{{Field: "enabled", Before: before.Enabled, After: p.Enabled}, {Field: "hour", Before: before.Hour, After: p.Hour}}
	case "set_library_root_enabled":
		after := "disabled"
		if p.Enabled {
			after = "active"
		}
		preview.Changes = []tool.Change{{Field: "status", Before: selected.Status, After: after}}
	case "add_library_root":
		path, err := a.validateNewRoot(p.Path)
		if err != nil {
			return preview, err
		}
		preview.Target = a.relativeAllowedPath(path)
		preview.Summary = "添加音乐目录「" + preview.Target + "」，不可撤销"
	case "add_root_exclusion":
		preview.Summary = "在音乐目录「" + preview.Target + "」添加子目录「" + a.relativeAllowedPath(p.Path) + "」的排除规则，不可撤销"
	case "remove_root_exclusion":
		if err := library.ValidateExclusionRelativePath(p.Path); err != nil {
			return preview, &tool.Error{Code: "invalid_input", Message: "排除路径必须是根内相对路径"}
		}
		preview.Summary = "在音乐目录「" + preview.Target + "」移除子目录「" + p.Path + "」的排除规则，不可撤销"
	case "start_scan":
		roots, err := a.service.ListRoots(ctx)
		if err != nil {
			return preview, err
		}
		names := []string{}
		for _, root := range roots {
			if root.Status == "active" {
				names = append(names, root.Name)
			}
		}
		preview.Target = "全部启用音乐目录"
		preview.Summary = "启动全库扫描（" + p.Mode + "）：" + strings.Join(names, "、") + "；不可撤销"
	case "cancel_scan":
		preview.Target = p.ScanID
		preview.Summary = "请求取消扫描，不可撤销"
	}
	if len(preview.Changes) == 0 {
		preview.Changes = []tool.Change{{Field: "action", Before: nil, After: preview.Summary}}
	}
	return preview, nil
}
func (a configAdapter) run(ctx context.Context, caller tool.Caller, name string, p configInput) (any, string, error) {
	op := library.RootOperation{ActorID: caller.User.ID, IdempotencyKey: caller.IdempotencyKey, RequestID: caller.RequestID}
	var result library.RootOperationResult
	var err error
	target := p.RootID
	switch name {
	case "add_library_root":
		path, pathErr := a.validateNewRoot(p.Path)
		if pathErr != nil {
			return nil, target, pathErr
		}
		result, err = a.service.AddRoot(ctx, op, path, a.allowed)
	case "set_library_root_enabled":
		if p.Enabled {
			result, err = a.service.RestoreRoot(ctx, op, p.RootID, p.ExpectedRevision)
		} else {
			result, err = a.service.DisableRoot(ctx, op, p.RootID, p.ExpectedRevision)
		}
	case "add_root_exclusion":
		result, err = a.service.AddExclusion(ctx, op, p.RootID, p.Path, a.allowed)
	case "remove_root_exclusion":
		result, err = a.service.RemoveExclusion(ctx, op, p.RootID, p.Path)
	case "start_scan":
		out, err := a.service.StartScan(ctx, library.StartOptions{Mode: p.Mode, Trigger: library.ScanTriggerManual})
		if out.Started {
			target = out.Scan.ID
		}
		return map[string]any{"scan": presentScan(out.Scan, true), "started": out.Started}, target, err
	case "cancel_scan":
		out, err := a.service.CancelScan(ctx, p.ScanID, caller.User.ID)
		if out.Requested {
			target = p.ScanID
		}
		return map[string]any{"scan": presentScan(out.Scan, true), "requested": out.Requested}, target, err
	case "set_scan_schedule":
		out, err := a.service.SaveScanSchedule(ctx, library.ScanSchedule{Enabled: p.Enabled, Hour: p.Hour})
		return out, "scan_schedule", err
	}
	if err != nil {
		return nil, target, err
	}
	if target == "" {
		var receipt struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(result.Response, &receipt); err != nil {
			return nil, "", err
		}
		target = receipt.ID
	}
	return result.Response, target, nil
}
func configError(err error) error {
	if err == nil {
		return nil
	}
	var toolErr *tool.Error
	if errors.As(err, &toolErr) {
		return err
	}
	var rootErr *library.RootOperationError
	if errors.As(err, &rootErr) {
		return &tool.Error{Code: tool.ErrorCode(rootErr.Code), Message: rootErr.Message}
	}
	switch {
	case errors.Is(err, library.ErrScanNotFound):
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "扫描不存在"}
	case errors.Is(err, library.ErrInvalidScanSchedule), errors.Is(err, library.ErrInvalidScanMode):
		return &tool.Error{Code: tool.CodeInvalidInput, Message: "扫描参数无效"}
	case errors.Is(err, library.ErrCoordinationUnavailable):
		return &tool.Error{Code: "scan_coordination_unavailable", Message: "暂时无法启动扫描"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return &tool.Error{Code: "database_unavailable", Message: "服务暂不可用"}
	}
}

func (a configAdapter) validateNewRoot(path string) (string, error) {
	clean, err := library.ValidatePath(path, a.allowed)
	if err != nil || clean == string(filepath.Separator) {
		return "", &tool.Error{Code: "invalid_library_path", Message: "目录不在允许的音乐根目录内，或为文件系统根目录"}
	}
	return clean, nil
}

// relativeAllowedPath 与校验使用相同的真实允许根；保留根内层级，避免同名末级目录混淆。
func (a configAdapter) relativeAllowedPath(path string) string {
	bestRoot, display := "", filepath.Base(path)
	for _, allowed := range a.allowed {
		root, err := filepath.EvalSymlinks(allowed)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(root) > len(bestRoot) {
			bestRoot = root
			display = filepath.ToSlash(rel)
			if rel == "." {
				display = filepath.Base(root)
			}
		}
	}
	return display
}
