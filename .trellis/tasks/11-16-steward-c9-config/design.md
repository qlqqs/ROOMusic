# 管家 C9 设计

## 服务层抽取

- `library`：`AddRoot`、`DisableRoot`、`RestoreRoot`、`AddExclusion`、`RemoveExclusion` 成为 `library.Service` 的方法
  （事务、幂等键、`library_root_operations` 记录原样搬过去），`library/httpapi` 改为调用它们；`relocate` 一并抽取以保持一致，
  但不登记工具。
- `identity`：`SetUserDisabled`（含最后一个管理员的保护）、`RevokeUserSessions`、`ListUsers` 成为包级服务函数，
  处理函数改为调用。
- 抽取是纯移动，不改行为；由现有的接口集成用例把关。

## 工具

| 包 | 工具 | 范围 / 身份 |
| --- | --- | --- |
| `platform/diagnostics/stewardtools`（新包 `platform/diagnostics`） | `get_system_status`、`get_disk_usage`、`list_recent_logs` | read / 管理员 |
| `backup/stewardtools` | `get_backup_overview` | read / 管理员 |
| | `start_backup`、`set_backup_retention` | shared / 管理员 |
| `library/stewardtools`（已有） | `list_library_roots`、`get_scan_schedule` | read / 管理员 |
| | `add_library_root`、`set_library_root_enabled`、`add_root_exclusion`、`remove_root_exclusion`、`start_scan`、`cancel_scan`、`set_scan_schedule` | shared / 管理员 |
| `identity/stewardtools` | `list_members` | read / 管理员 |
| | `set_member_disabled`、`revoke_member_sessions` | shared / 管理员 |
| `playback/stewardtools` | `get_track_audio`、`get_analysis_progress` | read / 成员（进度仅管理员） |
| `zones/stewardtools`（C5 已有） | `get_zone_settings` read；`set_zone_settings` personal | 成员 |
| `dspsettings/stewardtools` | `list_peq_presets` read；`apply_peq_preset` personal | 成员 |

- 配置工具 `Revertible` 为假；`Preview` 给出旧值与新值（扫描计划、保留策略、成员状态、目录状态）或一句动作说明（启动扫描、备份）。
- 配置工具执行后记日志事件 `steward.config.changed`（工具名、调用者、来源、目标 ID、会话或令牌 ID；不记输入正文）。
- `list_members` 输出：ID、用户名、角色、是否停用、创建时间。

## 诊断

- `platform/diagnostics`：
  - 日志环：一个 `slog.Handler` 包装器，把经过的记录（已经是日志规范允许的字段）再过一遍脱敏后存进容量 1000 的环形缓冲。
    脱敏：按键名丢弃（`password`、`secret`、`token`、`key`、`authorization`、`cookie`、`dsn`、`database_url` 等），
    值里的绝对路径替换为相对音乐目录或数据目录的部分，无法归到已知根的路径只留文件名。`main.go` 装配。
  - 磁盘：对数据目录、备份目录、各启用的音乐目录做 `statfs`；输出目录的标识（目录名或 root ID）与总量、可用量，不输出绝对路径。
  - 状态：`buildinfo`、启动时间、`db.PingContext`（2 秒超时）、`sql.DBStats`、当前迁移版本、曲库统计（现有 `catalog` 统计服务）。
- `get_track_audio` 组合曲目的音频事实、`playback.Service.Prepare` 的格式信息与分析结果；不含流地址与路径。

## 前端

工具标签与确认卡片里“不能撤销”的标注（C6 已有位置）；没有新页面。

## 测试

- 抽取：现有 `library/httpapi`、`identity/httpapi` 的集成用例（属于受影响范围）。
- 新增：每个工具的权限、成功、拒绝；脱敏（含密钥、连接串、绝对路径的合成记录）；环形缓冲的容量与并发；
  最后一个管理员；路径校验；确认流程里配置工具的不可撤销标注与 `steward` 模式转人工。
