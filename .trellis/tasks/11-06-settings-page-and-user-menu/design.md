# 设计：设置页、侧边栏用户菜单与删除备份

> 依据：同目录 `prd.md`（D1–D3、R1–R3、A1–A10），`implement.jsonl` 所列 spec，`frontend/DESIGN.md` 第 3–4 节，`frontend/DESIGN-components.md` 第 5 节。
> 本文只定合同与结构，执行顺序与命令写在 `implement.md`。

## 0. 结论速览

| 项 | 决定 |
| --- | --- |
| 删除接口 | `DELETE /api/v1/system/backups/{name}`，同源 → 管理员；成功 **200，响应体就是最新的 `Overview`**（与 `GET /system/backups` 同形） |
| 新错误码 | 只新增 `503 backup_directory_not_writable`；其余复用 `backup_not_found`、`forbidden`、`origin_required`、`origin_forbidden`、`unauthorized`、`database_unavailable` |
| 新样本 | `system/backup-deleted.json`（200）；新错误码不录样本（理由见 §1.5） |
| Service | `files.go` 新增 `DeleteBackup(ctx, name, actorID string) error` 与 `DeleteBackups(ctx, names, actorID)`（共用单份删除步骤），`types.go` 新增 `ErrNotWritable`；`retain()` 移到新文件 `retention.go` 并按保存的规则清理，遇到“已不存在”静默跳过 |
| 日志 | `backup.deleted`（INFO，`file`、`actor_id`）、`backup.delete_failed`（WARN，`file`、`reason`）；规则 `backup.retention.saved`、`backup.retention_unreadable`，清理沿用 `backup.retention.deleted`（加 `trigger`） |
| 批量删除（§1b） | 后端单独接口 `POST /api/v1/system/backups/deletions`，体 `{"names": [...]}`（1–500 个）；200 返回 `deleted` / `missing` / `failed` 三个名单 + 最新 Overview；已不存在算成功 |
| 保留规则存储（§1c） | **备份目录里单独的规则文件** `.roomusic-backup-retention.json`，不建表、不加迁移；文件不存在时用 `ROOMUSIC_BACKUP_KEEP_DAYS`（份数不限） |
| 保留规则接口（§1c） | 读：并入 Overview 的 `retention` 字段（**改动 §1.3**：Overview 多一个字段，现有 system 样本要重录）；写：`PUT /api/v1/system/backups/retention`，`{keep_days, keep_count, confirm}`，`null` 表示不限；不带确认且会删文件时 200 `{"confirmation_required": true, "deletions": n}`、什么都不改；带确认时保存并立即清理，返回实际删除数 |
| 清理算法（§1c） | 按新到旧排序；最新一份永远保留；其余“超过 N 天”或“排在第 M 份之后”任一成立就删；天数、份数都按所有来源计 |
| 前端新增（§3.7–3.8） | 备份页加“自动清理”区段（每项数字框 + “不限”勾选）；文件表加勾选列，表格上方一行“全选”勾选框 + “删除所选（n）”；部分失败留在确认框里列出没删掉的时间 |
| 路由 | `/settings` 布局 + `account / library / library/scans/$scanId / members / operations / backups`；`/settings` 替换跳转到 `/settings/account` |
| 旧地址 | 挂在根路由下、在登录守卫之前跳转：保留原查询串与 hash，`replace: true` |
| 设置页布局 | 视口 ≥1024px：左侧分栏（176px）+ 右侧内容；<1024px：内容上方一行横向分栏链接（与“我的音乐”页签同一套样式） |
| 页面复用 | `LibraryPage`、`MembersPage`、`OperationsPage`、`BackupsPage`、`ScanDetail` 原样放进设置布局，各自仍是唯一 h1；新增 `AccountPage` |
| 账号栏 | 用户名、角色（描述列表）+ 复用 `ChangePasswordButton`（对话框），不做内联表单 |
| 用户菜单 | 新 `app/shell/user-menu.tsx` 替换 `identity-panel.tsx`；`shared/ui/menu-button.tsx` 抽出 `useMenu`，增加向上弹出、菜单项图标、在 `<dialog>` 内就地渲染，`MenuButton` 对外 API 不变 |

## 1. 删除备份接口合同

### 1.1 请求

```
DELETE /api/v1/system/backups/{name}
```

- 无请求体、无查询参数、无幂等键。重复删除同一个名字第二次得到 404，本身就是幂等结果。
- `{name}` 是一段路径（ServeMux 单段通配，已解码）。`%2F` 解码后含 `/`、反斜杠、NUL、`..` 等都不可能匹配 `FileNamePattern`，一律 404。
- 与下载共用同一个模式路径；注册后 ServeMux 的 405 `Allow` 自动包含 `DELETE, GET, HEAD`，无需额外处理。

### 1.2 鉴权顺序（照抄 `start` 的写法）

```go
func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, ok := h.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	err := h.service.DeleteBackup(r.Context(), r.PathValue("name"), user.ID)
	switch {
	case errors.Is(err, backup.ErrNotFound):
		httpserver.WriteError(w, r, 404, "backup_not_found", "备份不存在")
		return
	case errors.Is(err, backup.ErrNotWritable):
		httpserver.WriteError(w, r, 503, "backup_directory_not_writable", "备份目录不能写入")
		return
	case err != nil:
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	httpserver.WriteJSON(w, 200, overview)
}
```

注册：`mux.HandleFunc("DELETE /api/v1/system/backups/{name}", h.remove)`（放在 `download` 之后）。
来源检查在身份检查之前，所以跨源的匿名请求得到 403 `origin_*` 而不是 401，与现有 `POST` 一致。

### 1.3 成功响应：200 + 最新 Overview

```json
{
  "status": { "running": null, "directory": "ready", "last_success_at": "…", "last_failure": null, "next_scheduled_at": "…" },
  "retention": { "keep_days": 14, "keep_count": null },
  "backups": [ … 删除后的列表，最多 500 份 … ],
  "total": 3
}
```

> **改动（第二轮，因 R1c）**：Overview 新增 `retention` 字段（定义见 §1c.4），`GET /system/backups`、本接口、批量删除与保存规则的响应都带它。

取舍（200 + Overview vs. 204 / `{deleted}`）：

- 本项目所有 `DELETE` 都返回 200 JSON（`/me/plays` 返回 `{deleted}`、歌单删除返回 JSON），样本录制器也只接受 `application/json`，204 无法进样本体系。
- 删除最新一份会同时改变 `status.last_success_at` 与 `next_scheduled_at`（见 §1.6），只回 `{deleted}` 时前端还得再读一次；回整份 Overview，前端直接写进同一个查询缓存，对话框关闭时列表已经是新的，不会出现“行还在、对话框已关”的闪烁。
- 前端解码复用 `decodeBackupOverview`，不新增 DTO。

### 1.4 错误码表

| 情形 | HTTP | code | message | 是否新增 | 样本 |
| --- | ---: | --- | --- | --- | --- |
| 未登录 | 401 | `unauthorized` | 现有 | 否 | 现有 `errors/unauthorized.json` |
| 无 Origin | 403 | `origin_required` | 现有 | 否 | 现有 |
| 跨源 | 403 | `origin_forbidden` | 现有 | 否 | 现有 `errors/origin-forbidden.json` |
| 非管理员 | 403 | `forbidden` | “需要管理员权限” | 否 | 现有 `errors/forbidden.json` |
| 名字不合规 / 文件不存在 / 已被自动清理或重复删除 / 符号链接 / 目录 / FIFO 等非普通文件 / 备份目录不存在 | 404 | `backup_not_found` | “备份不存在” | 否 | 现有 `errors/backup-not-found.json`（由 GET 录制；DELETE 产生的 body 完全相同，不重复录） |
| 目录不可写、只读文件系统、`EPERM`/`EACCES`/`EBUSY`/`EIO` 等删除失败 | 503 | `backup_directory_not_writable` | “备份目录不能写入” | **是** | **不录**，见 §1.5 |
| 删除成功但读 Overview 失败（只在请求已取消时出现） | 503 | `database_unavailable` | “服务暂不可用” | 否 | 现有 |
| 其他方法（PUT/PATCH/POST 到 `{name}`） | 405 | `method_not_allowed` | 现有 | 否 | 现有 |

### 1.5 样本

- 新增 `backend/internal/app/testdata/api-samples/system/backup-deleted.json`：在 `TestPostgreSQLSystemAPIResponseSamples` 中，以现有固定时钟与合成文件，删除一份非最新的备份后录制 200 响应（`isSystemAPISample` 已按 `system/` 前缀归属，不改所有权逻辑）。
- `backup_directory_not_writable` 不录样本：要让 `os.Root.Remove` 稳定失败只能靠 `chmod 0500`，在 root 身份（容器/CI）下不生效；录制器对“多余/缺少样本”是硬失败，带条件的样本会让 root 环境红。改由 Go 测试覆盖（非 root 时断言状态码与 code，root 时 `t.Skip`，与 `playback/open_test.go:195` 的做法一致），前端在 Vitest 替身里覆盖这一 code 的显示。
- 前端：`frontend/src/test/api-samples` 读取 `system/backup-deleted.json` 验证 `decodeBackupOverview` 能解码。
- **第二轮补充**：Overview 加了 `retention`，现有 `system/backups.json`、`backups-empty.json`、`backups-directory-missing.json`、`backups-running.json` 都要重录（`backup-started.json`、`backup-already-running.json` 只有 `status`，不变）；新样本见 §1b.5、§1c.7。

### 1.6 并发与副作用

| 并发对象 | 行为 | 理由 |
| --- | --- | --- |
| 正在进行的备份 | 不互斥，不拿 `mu`、不拿 advisory lock | 进行中的是 `.roomusic-*.partial.*`，不匹配 `FileNamePattern`，删不到；发布用 `os.Link` 到带当前秒的新名字，删除请求的名字要么已存在（删的是它），要么还不存在（404） |
| `retain()` 自动清理与保存规则后的立即清理 | 两边都可能 `Remove` 同一文件，后到的一方得到 `ENOENT`：删除接口映射为 404；清理（§1c.5）在 `errors.Is(err, os.ErrNotExist)` 时静默 `continue`，不再打 `backup.retention_failed` 误报 | 结果一致（文件不在了），避免假告警 |
| 正在下载同一文件 | 允许删除；Linux 上已打开的句柄继续可读，下载完整结束 | `OpenBackup` 持有 fd |
| CLI 恢复 | 恢复要求 app 已停止（连接数检查），实际不会并发；即使并发，`RestoreSession` 持有已校验的 fd，删除不影响恢复输入 | `restore.go` 的会话设计 |
| 定时计划 | **删除最新一份会让“上次成功”退回到剩下最新的那份**。若已过当天的备份时刻且剩下的都不是今天的，调度器下一轮（≤10 分钟）会补做一份定时备份；删光所有备份同理 | `schedule()` 每轮按现存文件取最新时间，`Overview` 同样如此；这与现在在服务器上手工删文件的行为一致，且保证总有一份近期备份。不另存“上次成功时间”，避免状态文件与目录不一致 |

### 1.7 Service 方法（`backend/internal/backup/files.go`，紧跟 `OpenBackup`）

```go
// types.go
var ErrNotWritable = errors.New("备份目录不能写入")

// DeleteBackup 删除备份目录里的一份已发布备份。名字不合规、不存在、不是普通文件（符号链接、目录、FIFO）都按 ErrNotFound；
// 删除本身失败按 ErrNotWritable。actorID 只进日志。
func (s *Service) DeleteBackup(ctx context.Context, name, actorID string) error
```

步骤（第二轮：抽成内部函数 `deleteIn(root *os.Root, name string) error`，单删与批量删共用第 2、4、5 步）：

1. `ctx.Err() != nil` → 返回 `ctx.Err()`（handler 落到 503 `database_unavailable`；客户端已离开，无人看到）。
2. `!fileNamePattern.MatchString(name)` → `ErrNotFound`（先于任何文件系统调用）。
3. `root, err := os.OpenRoot(s.settings.Directory)`：`ErrNotExist` → `ErrNotFound`；其他错误 → `ErrNotWritable`；`defer root.Close()`。
4. `info, err := root.Lstat(name)`：`ErrNotExist` → `ErrNotFound`；其他错误 → `ErrNotWritable`；`!info.Mode().IsRegular()` → `ErrNotFound`。
5. `root.Remove(name)`：`ErrNotExist` → `ErrNotFound`；其他错误 → 记 `backup.delete_failed` 后返回 `ErrNotWritable`。
6. `syncDirectory(s.settings.Directory)`，失败忽略（文件已删除；不因 fsync 失败报错误给用户）。
7. 记 `backup.deleted`，返回 nil。

文件安全说明：

- 与 `OpenBackup` 一样经 `os.OpenRoot` 限定在备份目录内，名字先过白名单正则，所以不可能穿越目录。
- `Lstat` 不跟随链接，符号链接（指向目录内或目录外）都按“不是普通文件”拒绝，链接和它的目标都不动。
- `Lstat` 与 `Remove` 之间被换成符号链接或空目录的竞争：`unlinkat` 不跟随最后一段，删掉的只是备份目录里这一个条目本身，不会影响目录外的任何文件；备份目录只有应用自己的 uid 可写，不再加二次核对。
- 不删除状态文件 `.roomusic-backup-state.json` 与 `.partial` 文件（名字不匹配）。

### 1.8 日志

| event | level | 字段 | 说明 |
| --- | --- | --- | --- |
| `backup.deleted` | INFO | `file`、`actor_id` | message “已删除备份”。删除备份是有安全意义的管理动作，按日志规范可以记稳定的用户 ID；不记用户名、路径 |
| `backup.delete_failed` | WARN | `file`、`reason`（`not_writable`） | message “无法删除备份”。不记 `err.Error()`（可能含绝对路径） |

404 不另记事件，HTTP 完成日志（`http.request.completed`，带 `request_id`、`actor_id`、`route_template`）已足够。不进“操作记录”。

## 1b. 批量删除接口（第二轮新增，R1b）

### 1b.1 选型：后端批量接口

| 方案 | 问题 |
| --- | --- |
| 前端逐个调用单删接口 | 全选最多 500 份 → 500 个请求，每个都回一整份 Overview（500 行，约 60 KB），最坏约 30 MB；中途关页面会删一半；部分失败要前端自己拼 |
| **后端 `POST /api/v1/system/backups/deletions`（采用）** | 一个请求、一份 Overview；逐份结果由服务端分好类，前端只负责展示 |

单删接口（§1）保留：行内“删除”用它，A1 的 404 语义也靠它。

### 1b.2 请求

```
POST /api/v1/system/backups/deletions
Content-Type: application/json

{ "names": ["roomusic-20261101-030000.dump", "roomusic-20261102-030000-manual.dump"] }
```

- 鉴权顺序同单删：`RequireSameOrigin` → `RequireAdmin`，再解码。
- `names`：必填，字符串数组，1–500 个（与列表上限一致，全选时正好 ≤500）；重复的名字服务端去重，不报错。
- 缺 `names`、不是数组、空数组、超过 500、含非字符串 → 400 `invalid_input`，message “要删除的备份无效”。JSON 不合法 → 现有 400 `invalid_json`。
- 用 `POST` 加子资源名而不是“带请求体的 `DELETE /backups`”：带体的 DELETE 在代理与客户端里支持不一致。`deletions` 不可能是备份文件名（不匹配 `FileNamePattern`），与 `GET /backups/{name}` 不冲突（`GET /backups/deletions` 落到下载，404）。

### 1b.3 响应：200

```json
{
  "deleted": ["roomusic-20261101-030000.dump"],
  "missing": ["roomusic-20261102-030000-manual.dump"],
  "failed": [],
  "overview": { "status": { … }, "retention": { … }, "backups": [ … ], "total": 7 }
}
```

- 每个名字恰好进一个名单，名单内保持请求里的顺序（去重后）。
- `missing`：名字不合规、文件不存在、不是普通文件（同单删的 404 情形）。按 PRD 算成功——前端不把它们当失败展示。
- `failed`：删除时文件系统报错（同单删的 503 情形）。
- 部分或全部失败也是 200：批量操作的结果在名单里，不用状态码表达；只有鉴权、输入错误走错误信封。
- 备份目录打不开：`ENOENT` → 全部 `missing`；其他错误 → 全部 `failed`。
- 读 Overview 失败（请求已取消）→ 503 `database_unavailable`（文件可能已删，前端出错时总会刷新列表）。

### 1b.4 Service

```go
type DeleteResult struct{ Deleted, Missing, Failed []string }

// DeleteBackups 逐份删除；只打开一次备份目录，每份用 deleteIn，结果按名单分类。
func (s *Service) DeleteBackups(ctx context.Context, names []string, actorID string) (DeleteResult, error)
```

- 每份成功记一条 `backup.deleted`（`file`、`actor_id`），失败记 `backup.delete_failed`，与单删相同；不另加汇总事件。
- 循环中途 `ctx` 取消：停止删除，返回 `ctx.Err()`（handler 503）；已删的已经删了，前端刷新列表即可看到。
- 不拿 `mu` / advisory lock，并发行为同 §1.6。

### 1b.5 样本

- `system/backups-deleted.json`（200）：一份成功、一份不存在、一个非法名，`failed` 为空（`failed` 非空需要目录不可写，理由同 §1.5，不录，由 Go 测试覆盖）。
- `errors/backup-deletions-invalid.json`（400 `invalid_input`，空数组）。

## 1c. 保留规则（第二轮新增，R1c）

### 1c.1 存储：备份目录里的规则文件（不建表）

| 考量 | 数据库表（需迁移 0031） | **备份目录里的规则文件（采用）** |
| --- | --- | --- |
| 恢复数据库后 | 规则跟着回到那份备份里的值（或表还不存在 → 默认值）；用旧规则清理现在目录里的文件，可能删掉管理员想留的备份（包括刚做的“恢复前”备份） | 恢复只改数据库，规则不变——规则描述的是这个目录里的文件，和文件放在一起最自然 |
| 迁移前备份（启动时，迁移之前） | 那时新表可能还没建，`retain()` 读不到规则，只能退回环境变量——若管理员设过“不限”，升级当次就会按 14 天删文件 | 不依赖数据库，迁移前备份后的清理照样按保存的规则 |
| `roomusic backup` / `restore` CLI | 都要多一次查询，`restore` 时数据库正被重建 | 同一容器、同一目录，直接读文件 |
| 备份目录不可写 | 规则能存，但备份、删除本来都做不了，规则没有意义 | 存不了，503 `backup_directory_not_writable`——和“目录不能写入”的状态一致 |
| 降级到 0.1.0 | 回退迁移删表，规则丢失 | 旧版本不认识这个文件、不会碰它，按环境变量清理；再升级回来规则还在 |
| 代价 | 第一个带回退文件的迁移、往返测试 | 原子写文件（临时文件 → fsync → rename → fsync 目录）；换备份卷时规则随之丢失（新目录用默认值，可接受） |

**不放进现有 `.roomusic-backup-state.json`**：0.1.0 的 `readState` 只认 `version: 1`，`writeState` 每次整文件重写；若在同一文件加字段或升版本，降级后的旧程序第一次写状态就会把规则抹掉。单独文件两边互不干扰。

### 1c.2 规则文件

路径：`<备份目录>/.roomusic-backup-retention.json`，权限 `0600`。

```json
{ "version": 1, "keep_days": 14, "keep_count": null, "saved_at": "2026-11-06T08:00:00Z" }
```

- `keep_days`：`null`（不限）或 1–3650 的整数（与 `ROOMUSIC_BACKUP_KEEP_DAYS` 的范围一致）。
- `keep_count`：`null`（不限）或 1–1000 的整数。
- 读取：限 4 KiB；`version` 必须为 1，取值越界、类型不对、多出第二个 JSON 值都算“读不到”。
- 名字以 `.roomusic-` 开头、不以 `.dump` 结尾：`FileNamePattern` 与 `partialPattern` 都不匹配，不会被列出、删除或当作残留清理。写入用的临时文件 `.roomusic-backup-retention.json.tmp-*` 同样不匹配；写失败时 `defer os.Remove` 清掉。

**有效规则（D7）**：

| 文件状态 | 有效规则 | 自动清理 |
| --- | --- | --- |
| 不存在（从未保存） | `keep_days = settings.KeepDays`（环境变量，默认 14），`keep_count = null` | 照常——与现在的行为完全一致（A1d） |
| 存在且有效 | 文件里的值（环境变量不再起作用） | 照常 |
| 存在但读不到（损坏、权限） | 无 | **跳过本次清理**，WARN `backup.retention_unreadable`。不退回环境变量：若管理员存的是“不限”，按 14 天清理会删掉想留的备份 |

### 1c.3 清理算法（D5）

纯函数，保存预览、保存后的清理与备份后的清理共用：

```go
type Retention struct {
	KeepDays  *int `json:"keep_days"`  // nil 表示不限
	KeepCount *int `json:"keep_count"` // nil 表示不限
}

// retentionPlan 返回要删除的备份。files 必须是 listFiles 的结果（新到旧，时间相同按名字倒序）。
// 第一份（最新）永远保留；其余只要“早于 now-KeepDays”或“序号 ≥ KeepCount”之一成立就删除。
func retentionPlan(files []File, rule Retention, now time.Time) []File
```

- 份数按**所有来源**计（定时、手动、迁移前、恢复前，D7），序号从 0 开始，`KeepCount = 3` 时保留序号 0、1、2。
- 天数与份数同时生效，是两个独立上限（D5）；都为 `nil` 时计划为空。
- “最新一份”指本次列出的最新一份。删除只针对列出的文件，所以列出之后才发布的新备份更新、不会被删；真正的最新一份在任何时刻都不会被自动删除。
- 时间用文件 mtime（与列表的“时间”列一致）。
- 列表接口只返回 500 份，但计划基于 `listFiles()` 的全部文件。

### 1c.4 读规则：并入 Overview

`Overview` 增加 `Retention *Retention json:"retention"`：

- 有效规则见 §1c.2 的表；读不到时为 `null`。
- 不单独开 `GET` 接口：备份页本来就读 Overview（一个查询供状态、清理、文件三个区段），保存、删除的响应也都带 Overview，前端只写一处缓存。
- 不返回“是否保存过”：界面不需要区分（D7 的优先级只在服务端起作用）。

### 1c.5 `retain()` 的改动（移到 `backend/internal/backup/retention.go`）

```go
func (s *Service) retain() {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	rule, err := s.effectiveRetention()
	if err != nil {
		s.logger.Warn("读不到备份清理规则，这次不清理", "event", "backup.retention_unreadable")
		return
	}
	files, err := s.listFiles()
	if err != nil { /* 同现在：backup.retention_failed */ return }
	for _, file := range retentionPlan(files, rule, s.now()) {
		// Remove：ENOENT 静默跳过；其他错误 backup.retention_failed 后继续
		// 成功：backup.retention.deleted，file、trigger="backup"
	}
}
```

- 调用时机不变：每次备份成功（`run()` 末尾，含定时、手动、迁移前、恢复前，以及 CLI 进程里的备份）。
- `Service` 新增 `retentionMu sync.Mutex`：同一进程里“备份后的清理”与“保存规则 + 立即清理”串行，避免两边各按新旧规则交错删除。
- 跨进程（CLI `roomusic backup` 与运行中的 app）不加锁：规则文件原子替换，对方读到的要么是旧规则要么是新规则；最坏情况是保存规则的那一瞬间 CLI 还按旧规则清理了一次。窗口极小，且旧规则是管理员自己设过的，接受。
- 与手动删除、批量删除并发：都只 `Remove` 单个条目，`ENOENT` 互相容忍（§1.6）。
- 与正在进行的备份：清理只针对已发布的 `.dump`，`.partial` 不受影响。

### 1c.6 保存规则：`PUT /api/v1/system/backups/retention`

**D6 的实现选型**：

| 方案 | 取舍 |
| --- | --- |
| 先 `POST …/retention/preview` 得到将删数量，再 `PUT` 保存 | 两次请求之间来了一份新备份时，预览说“0 份”，保存时却会删一份——用户没确认过就删了文件 |
| 未确认时返回 409 + `details.deletions` | 现有错误信封 `APIErrorDetail` 没有 `details`，要改共享的 `httpserver` 与前端 `ApiError` 解析；而且“需要确认”不是错误 |
| **同一个 `PUT`，`confirm: false` 时服务端只在“不会删文件”时保存（采用）** | 不确认就绝不删文件，这一点由服务端在保存那一刻判断；不会删时一次请求完成；会删时第二次带 `confirm: true` 提交，按那一刻的实际计划清理（PRD：以保存时实际清理结果为准） |

请求：

```json
{ "keep_days": 30, "keep_count": 3, "confirm": false }
```

- 鉴权：`RequireSameOrigin` → `RequireAdmin`，再解码。
- `keep_days`、`keep_count` 两个键**必须出现**，值为 `null` 或范围内整数（解码到 `map[string]json.RawMessage` 判断键是否存在，再逐个解析，`1.5`、`"3"`、`0`、`3651` 都拒绝）；`confirm` 可省略，默认 `false`，必须是布尔。多余的键忽略。
- 输入不合法 → 400 `invalid_input`，message “清理设置无效”；JSON 不合法 → 400 `invalid_json`。

服务端（`SaveRetention`，持 `retentionMu`）：

1. `files := listFiles()`，`plan := retentionPlan(files, rule, now)`。
2. `len(plan) > 0 && !confirm` → 返回“需要确认”，**不写文件、不删任何东西**。
3. 写规则文件（开发环境 `CreateDirectory` 时先 `MkdirAll`）；失败 → `ErrNotWritable`（503，什么都没删）。记 `backup.retention.saved`。
4. 逐个删除 `plan`：成功记 `backup.retention.deleted`（`trigger="saved"`），`ENOENT` 跳过不计数，其他错误记 `backup.retention_failed` 并计入 `failed`。
5. 返回实际 `deleted`、`failed`。

响应（都是 200，用 `confirmation_required` 区分）：

```json
{ "confirmation_required": true, "deletions": 3 }
```

```json
{ "confirmation_required": false, "deleted": 3, "failed": 0, "overview": { … 含新的 retention … } }
```

- `confirm: true` 而那一刻计划为空：照常保存，`deleted: 0`。
- 确认后实际删除数可能与确认框里的数不同（期间有新备份或文件被删），以响应为准，前端刷新列表。

错误码：

| 情形 | HTTP | code |
| --- | ---: | --- |
| 未登录 / 无 Origin / 跨源 / 非管理员 | 401 / 403 | `unauthorized` / `origin_required` / `origin_forbidden` / `forbidden`（现有） |
| 请求体不合法 | 400 | `invalid_json`（现有）、`invalid_input`（现有 code，message “清理设置无效”） |
| 规则文件写不进（目录不存在、不可写） | 503 | `backup_directory_not_writable`（§1.4 新增的同一个） |
| 读 Overview 失败（请求已取消） | 503 | `database_unavailable`（现有） |

### 1c.7 日志与样本

| event | level | 字段 |
| --- | --- | --- |
| `backup.retention.saved` | INFO | `keep_days`、`keep_count`（整数，或字符串 `"unlimited"`——不要直接传 `*int`，文本日志会打出指针地址）、`actor_id` |
| `backup.retention.deleted` | INFO | `file`、`trigger`（`"backup"` / `"saved"`）——沿用现有事件，加字段 |
| `backup.retention_failed` | WARN | 现有 |
| `backup.retention_unreadable` | WARN | 无额外字段 |

样本（system 作用域）：`system/retention-confirmation-required.json`、`system/retention-saved.json`（确认后删 2 份）、`system/retention-saved-unlimited.json`（两项不限，不弹确认）、`errors/backup-retention-invalid.json`（400）。

### 1c.8 文件位置

| 文件 | 内容 |
| --- | --- |
| `backend/internal/backup/retention.go`（新） | `Retention`、规则文件读写、`effectiveRetention`、`retentionPlan`、`SaveRetention`、从 `files.go` 移来的 `retain()` |
| `backend/internal/backup/types.go` | `Overview.Retention`、`RetentionResult{ConfirmationRequired bool; Deletions, Deleted, Failed int}`、`DeleteResult` |
| `backend/internal/backup/service.go` | `retentionMu`；`Overview` 填 `Retention` |
| `backend/internal/backup/httpapi/handlers.go` | `PUT …/retention`、`POST …/deletions` 两个 handler 与输入解析 |
| `backend/internal/platform/config/backup.go` | 不变（`KeepDays` 仍是“从未保存”时的默认值）；字段注释写明这一点 |

## 2. 前端路由

### 2.1 新路由树（`frontend/src/app/router.tsx`）

```
authenticatedRoute
└── settingsRoute               path "/settings"                      component SettingsLayout
    ├── settingsIndexRoute      path "/"        beforeLoad → redirect({ to: "/settings/account", replace: true })
    ├── settingsAccountRoute    path "/account"                       AccountPage
    ├── settingsLibraryRoute    path "/library"   validateSearch scan_page   <AdminGate><LibraryPage scanPage/></AdminGate>
    ├── settingsScanDetailRoute path "/library/scans/$scanId"         <AdminGate><ScanDetail scanId/></AdminGate>
    ├── settingsMembersRoute    path "/members"                       <AdminGate><MembersPage/></AdminGate>
    ├── settingsOperationsRoute path "/operations" validateSearch parseOperationsSearch  <AdminGate><OperationsPage …/></AdminGate>
    └── settingsBackupsRoute    path "/backups"                       <AdminGate><BackupsPage/></AdminGate>
```

- `/settings` 默认进“账号”：这是所有角色都有的唯一一栏，成员和管理员行为一致；`replace` 让返回键不回到 `/settings`（同 `/me`）。
- 扫描详情是 `settingsRoute` 的直接子路由（与 `library` 平级），`LibraryPage` 不需要 `<Outlet />`。
- `AdminGate` 照旧包在每个管理员栏的路由组件里，`SettingsLayout` 自己不判断权限（只决定分栏显示哪些项）。成员直接打开 `/settings/members` 时，布局照常显示（分栏只有“账号”），内容区是 `ForbiddenPage`，不调用任何管理接口。
- 原有 `LibraryRoute`、`MembersRoute`、`OperationsRoute`、`ScanDetailRoute`、`BackupsRoute` 组件与 `validateSearch` 原样迁到新路由，只改 `getParentRoute` 与 `path`。删掉“‘系统’组目前只有这一页”注释。

### 2.2 旧地址重定向

挂在 **`rootRoute`** 下（不在 `authenticatedRoute` 下），在会话守卫之前跳转：匿名访问旧书签时，登录页记住的 `redirect` 已经是新地址。

```ts
// 旧书签与外部链接：换成设置下的新地址，查询串与 hash 原样保留，替换历史项（返回键不会回到旧地址再跳一次）。
function legacyRedirectRoute(path: string, from: string, to: string) {
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: ({ location }) => {
      throw redirect({ href: to + location.pathname.slice(from.length) + location.searchStr + (location.hash === "" ? "" : `#${location.hash}`), replace: true });
    },
  });
}

const legacyRoutes = [
  legacyRedirectRoute("/library", "/library", "/settings/library"),
  legacyRedirectRoute("/library/scans/$scanId", "/library", "/settings/library"),
  legacyRedirectRoute("/members", "/members", "/settings/members"),
  legacyRedirectRoute("/operations", "/operations", "/settings/operations"),
  legacyRedirectRoute("/system/backups", "/system/backups", "/settings/backups"),
];
```

- 用 `href` 而不是 `to + search`：不需要为旧路由写 `validateSearch`，也不会丢掉未知参数（例如 `auth.spec.ts` 用的 `/members?tab=1`）；`$scanId` 随路径前缀替换自然带过去。
- 非法参数（`scan_page=abc`）照样带过去，由新路由的 `validateSearch` 丢弃，和现在直接访问的结果相同。
- 旧路由永久保留（代价是几行代码）。注意：旧路径仍在类型化路由树里，`<Link to="/library">` 依然能通过类型检查，所以“站内链接全部改完”靠 grep 门槛（§2.3）而不是 `tsc`。

### 2.3 要改链接的文件（已 grep `frontend/src`、`frontend/e2e`、`frontend/e2e/real/`、`scripts/`）

产品代码（`to` / `navigate` 目标改成 `/settings/...`，`search` 回调形状不变）：

| 文件 | 位置 |
| --- | --- |
| `features/members/components/member-summary.tsx:10` | `/members` → `/settings/members` |
| `features/library/components/root-summary.tsx:10` | `/library` |
| `features/library/components/scan-summary.tsx:13` | `/library` |
| `features/library/components/scan-detail.tsx:19` | “返回扫描目录” `/library` |
| `features/library/components/scan-history-section.tsx:28,51,73` | 详情链接、越界 `navigate`、分页链接 |
| `features/library/components/scan-section.tsx:25,58` | 开始扫描后 `navigate` 与链接 |
| `features/library/components/active-scan.tsx:26` | 详情链接 |
| `features/governance/components/operations-view.tsx:51,76,104` | 清除筛选、越界 `navigate`、分页 |
| `features/governance/components/release-history.tsx:30` | `/operations?release&group` |
| `features/governance/components/artist-admin-actions.tsx:41` | `/operations?artist` |
| `features/catalog/components/album-list-view.tsx:75` | 空库提示里的“前往扫描目录” |
| `app/shell/nav-items.ts` | 见 §5 |
| `app/router.tsx` | 见 §2.1–2.2 |

只改注释/文案的：`app/routes/backups-page.tsx:4`、`features/system/index.ts:1`、`src/test/system-data.ts:5`、`src/test/fake-system.ts:6`、`e2e/support/api-mock.ts:74`（“系统 › 备份”→“设置 › 备份”）、`app/shell/app-shell.tsx:27`（“当前用户区”描述）。

`scripts/playback-e2e.sh` 里的 `library` 只是临时目录名，**不需要改**；它运行的 `e2e/real/*.real.ts` 需要改（见 §6.3）。

用户文档（PRD 未列，建议并入本任务）：`README.md:14`（“下载备份”→加“删除”）、`deploy/production/README.md:99`（“系统 › 备份”→“设置 › 备份”，加“删除”）。

验收门槛：`rg -n '"/(library|members|operations|system/backups)\b|`/(library|members|operations)\b' frontend/src --glob '!*.test.*'` 只剩 `router.tsx` 里的旧路由定义。

## 3. 设置页布局

### 3.1 组件与文件

| 文件 | 内容 |
| --- | --- |
| `app/routes/settings-sections.ts` | `settingsSections` 常量与 `visibleSettingsSections(role)`：账号（`UserRound`）、扫描目录（`FolderSearch`）、成员（`Users`）、操作记录（`History`）、备份（`DatabaseBackup`），后四项 `adminOnly` |
| `app/routes/settings-sections.test.ts` | 成员只得到“账号”；管理员得到五项且顺序固定 |
| `app/routes/settings-layout.tsx` + `.module.css` | 分栏 `nav` + `<Outlet />` |
| `app/routes/account-page.tsx` | 账号栏 |
| `styles/tokens.css` | 新增 `--layout-settings-nav-width: 176px`（DESIGN 3.4 要求布局数值集中定义） |

### 3.2 结构

```tsx
<div className={styles.settings}>
  <nav aria-label="设置" className={styles.nav}>
    <p className={styles.navTitle}>设置</p>          {/* 宽屏可见；窄屏隐藏，nav 的可访问名称仍是“设置” */}
    <ul className={styles.list}>
      {visibleSettingsSections(viewer.role).map((section) => (
        <li key={section.to}>
          <Link to={section.to} search={{}} activeOptions={{ includeSearch: false }} className={styles.link}>
            <section.icon size={18} aria-hidden="true" />
            <span>{section.label}</span>
          </Link>
        </li>
      ))}
    </ul>
  </nav>
  <div className={styles.content}><Outlet /></div>
</div>
```

- 当前栏由 TanStack `Link` 自动加的 `aria-current="page"` 表达（与主导航、“我的音乐”页签一致）；默认的前缀匹配让扫描详情时“扫描目录”也是当前项；`includeSearch: false` 让带 `page`、`artist` 等参数时仍高亮。
- 链接 `search={{}}`：从分栏切换栏目时不带上一栏的查询参数。
- **不另加 h1**：每栏的内容组件自带 `Page`，仍是该页唯一的 h1 与文档标题（“扫描目录 · ROOMusic”）。若布局也用 `Page`，会出现两个 h1，且子页面的 `usePageTitle` 先于父级执行，标题会被父级覆盖。
- “设置”二字用 `p` 加样式（同主导航分组小标题），不用 h2，避免标题层级出现在 h1 之前。

### 3.3 宽度与形态

| 视口 | 形态 |
| --- | --- |
| ≥1024px | 两列网格：`var(--layout-settings-nav-width) minmax(0, 1fr)`；分栏是竖排链接（图标 + 文字，当前项整行金色轻底 + 加粗，同 `primary-nav` 的选中样式），顶部内边距与 `Page` 的 `--layout-page-inset-y` 对齐；分栏随页面滚动 |
| <1024px | 单列：内容上方一行横向链接（复用 `me-layout.module.css` 的下边线页签样式，不显示图标），`flex-wrap: nowrap` + `overflow-x: auto` 兜底；左右内边距用 `--layout-page-inset-x`，与下方 `Page` 对齐 |

为什么断点取 1024 而不是 PRD 写的 768：外壳在 ≥768px 已经有 216px 的主侧栏，768px 视口再放 176px 分栏，内容区只剩约 320px，备份表、成员表都会挤成堆叠卡片且标题 48px 很局促；1024px 时内容区约 576px，可以接受。横向分栏在 375px 下五个短标签（约 290px）一行放得下，不需要滚动；比“列表页 → 点进去 → 返回”的两级结构少一次点击，且 `/settings` 的默认跳转对所有宽度相同，也与“我的音乐”的页签一致。

### 3.4 账号栏（`AccountPage`）

```tsx
<Page title="账号">
  <StatList items={[{ label: "用户名", value: viewer.username }, { label: "角色", value: roleLabels[viewer.role] }]} />
  <div><ChangePasswordButton /></div>
</Page>
```

- 复用 `ChangePasswordButton` + `ChangePasswordDialog`，不做内联表单：对话框已经有提交中锁定、字段旁错误、成功后“完成”、隐藏 `username` 字段配合密码管理器、窄屏嵌套对话框的 Esc 处理，以及 Vitest / Playwright / 真实服务三层测试；内联表单要把这些重写一遍。
- `ChangePasswordButton` 迁出侧栏后只剩这一处使用：把按钮从 `variant="text"` 改为默认的 `secondary`（页面里的独立命令，描边按钮更清楚）。
- 不加任何说明段落（不写“修改后其他设备会退出登录”之类；对话框里已有的文案保持不变）。
- 用户名长时 `StatList` 的值允许换行（`overflow-wrap: anywhere`），完整显示。

### 3.5 其他栏

- 扫描目录：`LibraryPage`（含扫描详情 `ScanDetail`）原样。
- 成员：`MembersPage` 原样。
- 操作记录：`OperationsPage` 原样（保留现有 `description`）。
- 备份：`BackupsPage` 增加区段（第二轮改动）：`BackupStatusSection` → `BackupRetentionSection`（新，§3.7）→ `BackupFilesSection`（删除见 §3.6，勾选与批量删除见 §3.8）。

### 3.6 备份删除的界面

- 文件表最后一列由“下载”改为“操作”（`hideLabelWhenStacked` 保持）：`下载` 链接 + `删除` 文本按钮（`Button variant="text"`，`Trash2` 图标），两者同一行；可访问名称都带时间：“删除 2026-11-06 03:00 的备份”（`VisuallyHidden`，同下载链接）。
- 确认框用 `ConfirmDialog`：
  - 标题：`删除 {formatDateTime(created_at)} 的备份？`
  - 正文：`删除后不能恢复。`（只这一句）
  - 确认按钮：`删除`；提交中 `pending`（确认按钮忙碌，取消与 Esc 暂时无效）。
  - 失败：`error={<FailureNotice error=… context="write" messages={BACKUP_DELETE_MESSAGES} />}`，留在对话框里，可再点“删除”重试或“取消”。`BACKUP_DELETE_MESSAGES = { backup_directory_not_writable: "备份目录不能写入，没有删除。" }` 放在 `backup-messages.ts`。
- `404 backup_not_found`：不当作失败。关闭对话框并刷新列表（文件已经不在了，正是用户要的结果），不显示错误。
- 成功：对话框关闭，列表已是响应里的新列表；焦点回到“备份文件”区段标题（`Panel headingRef`，因为打开对话框的那一行已经不在了），并用 `role="status"` 的读屏文字说“已删除。”（`VisuallyHidden`，不在页面上常驻显示）——做法同 `history-view.tsx` 的移除。
- 正在备份时也可以删除，不禁用按钮。
- 新 hook `features/system/hooks/use-delete-backup.ts`：
  ```ts
  mutationFn: (name: string) => deleteBackup(name),
  onSuccess: async (response) => { if (!scope.isCurrent()) return; await queryClient.cancelQueries({ queryKey }); queryClient.setQueryData(queryKey, response); },
  onError: (error) => { if (scope.isCurrent() && isBackupNotFound(error)) void queryClient.invalidateQueries({ queryKey }); },
  ```
  先 `cancelQueries` 再写缓存：避免正在进行的轮询（备份进行中每 2 秒一次）用删除前的列表把新列表盖回去。
- `features/system/api/endpoints.ts` 新增 `deleteBackup(name)`：`apiRequest({ method: "DELETE", path: backupDownloadPath(name) }, decodeBackupOverview)`（路径与下载相同，已 `encodeURIComponent`）。

### 3.7 “自动清理”区段（第二轮新增，R1c）

`features/system/components/backup-retention-section.tsx`，`Panel title="自动清理"`，数据来自同一个 `useBackupOverview()`。

```
自动清理
  保留天数   [ 14 ] 天   ☐ 不限
  保留份数   [ 10 ] 份   ☑ 不限
  [保存]   已保存。
```

- 每项一个 `<fieldset>`，`<legend>` 是“保留天数”/“保留份数”；里面一个数字框（`type="number"`、`inputMode="numeric"`、`min`/`max`，可访问名称同 legend）、单位文字“天”/“份”（`aria-hidden`）、一个“不限”勾选框。勾选“不限”时数字框 `disabled` 但保留原值；取消勾选时数字框获得焦点。数字框为空时按原值显示（从未有过值时天数填 14、份数填 10）。
- 选“勾选框 + 数字框”而不是“单选：不限 / 最多 N”：一行就能看清当前值，切换只需一下；单选在 375 宽下要占两行。
- 初值来自 `overview.retention`；表单没改动时，服务端值变化（别的标签页保存了）跟着更新；改动过就不覆盖。
- `overview.retention === null`（服务端读不到规则文件）：区段顶部一句 `Notice`“没能读取当前设置。”，表单用默认值（14 天 / 份数不限），保存即覆盖修复。
- 前端校验：整数，天数 1–3650、份数 1–1000；不合规时字段旁错误“填 1 到 3650 之间的整数。”/“填 1 到 1000 之间的整数。”，不发请求。
- “保存”（`Button variant="primary"`），提交中 `busy`、文字“正在保存…”。流程：
  1. `PUT` 带 `confirm: false`。
  2. 响应 `confirmation_required: false` → 写缓存（先 `cancelQueries` 再 `setQueryData(overview)`），按钮旁 `role="status"`：`deleted > 0` 时“已保存，删除了 3 份旧备份。”，否则“已保存。”
  3. 响应 `confirmation_required: true` → 打开 `ConfirmDialog`：标题“保存清理设置？”，正文“将删除 3 份旧备份。删除后不能恢复。”，确认按钮“保存并删除”；确认后再 `PUT` 带 `confirm: true`，成功同第 2 步并关闭对话框；取消则什么都没改，表单保持改动。
  4. 失败：第 1 步的错误显示在保存按钮下方（`FailureNotice`，context `write`，`backup_directory_not_writable` 文案“备份目录不能写入，没有保存。”）；第 3 步的错误留在确认框里，可重试或取消。任何失败后都 `invalidateQueries` 刷新列表。
  5. `failed > 0`（规则已保存但个别文件删不掉）：状态行之外加一句 `Notice`“有 2 份旧备份没能删除。”
- 不加说明段落（不解释“最新一份不会删”“两项同时生效”等规则）。
- 375 宽：两个 fieldset 纵向排列，每个一行（legend 一行 + 数字框约 8em + 单位 + 勾选框），保存按钮单独一行；不横向溢出。
- hook：`features/system/hooks/use-save-retention.ts`；端点 `saveRetention(rule, confirm)`；解码 `decodeRetentionSave`（按 `confirmation_required` 判别联合）；`decodeBackupOverview` 增加 `retention: { keep_days: number | null; keep_count: number | null } | null`。

### 3.8 勾选与批量删除（第二轮新增，R1b）

文件表结构（`BackupFilesSection`）：

```
备份文件
  [☐ 全选]   [删除所选（3）]
  ┌───┬──────────────────┬──────┬────────┬──────────────┐
  │ ☐ │ 2026-11-06 03:00 │ 自动 │ 12 MB  │ 下载   删除  │
```

- **勾选列**：普通 `Column`（`id: "select"`、`header: "选择"`、`hideLabelWhenStacked`），放第一列；每行一个原生勾选框，可访问名称“选择 2026-11-06 03:00 的备份”（`VisuallyHidden`）。`DataTable` **不需要改**：勾选框就是单元格内容。
- **全选放在表格上方的工具行**，不放进表头：`DataTable` 在容器窄于 640px（375 宽，以及带侧栏的中等视口）时把表头隐藏，表头里的全选在窄屏上看不见；工具行宽窄屏都可用，只有一个控件，读屏也不重复。`header` 仍是字符串，不扩展 `DataTable` 的表头渲染。
  - 全选勾选框：全部选中时勾上；部分选中时 `indeterminate`（经 ref 设置）；点击时部分或全不选 → 全选当前列出的全部（最多 500），全选 → 全部取消。
  - “删除所选（n）”（`Button`，`Trash2` 图标）：有选中时出现在全选右边；没选中时不渲染。
- **选中状态**：`useState<ReadonlySet<string>>`（按文件名）。渲染时用“当前列表里还在的名字”求交集；列表数据变化（轮询、单删、批量删、保存规则后的清理）后用 effect 把集合裁成交集，已经不在的勾选自动去掉。
- **确认框**（`ConfirmDialog`）：标题“删除 3 份备份？”，正文“删除后不能恢复。”，确认按钮“删除”；提交中 `pending`。
- **结果**：
  - 先 `cancelQueries` 再 `setQueryData(response.overview)`；`deleted` 与 `missing` 从选中集合里移除。
  - `failed` 为空 → 关闭对话框，焦点到“备份文件”标题，`role="status"`（`VisuallyHidden`）“已删除 3 份。”
  - `failed` 非空 → 对话框不关，标题随选中数更新为“删除 1 份备份？”，`error` 位置显示 `Notice tone="error"`，标题“有 1 份没能删除”，内容是这几份的时间列表；再点“删除”只重试剩下的，“取消”关闭并保留这些勾选。
  - 请求本身失败（401/403/网络）→ 对话框里 `FailureNotice`，可重试；同时 `invalidateQueries`。
- 行内“删除”与批量删除互不影响：单删成功后那一行的勾选也随交集去掉。
- 375 宽：工具行一行放得下（勾选框 + “全选”约 60px，“删除所选（500）”约 140px）；表格堆叠后每张卡片第一行是勾选框，接着是时间标题。
- hook：`features/system/hooks/use-delete-backups.ts`；端点 `deleteBackups(names)`；解码 `decodeBackupDeletions`。

## 4. 侧边栏用户菜单

### 4.1 `MenuButton` 扩展（`frontend/src/shared/ui/menu-button.tsx`）

现状：触发器固定是 `Button`，菜单固定在触发器下方，菜单项只有文字，浮层 `createPortal` 到 `document.body`。

**问题**：窄屏菜单是原生模态 `<dialog>`，模态打开时对话框之外的内容全部惰性（inert）且在顶层之下；portal 到 `body` 的菜单在窄屏对话框里既看不见也点不了。

扩展方式（不破坏现有调用）：

```ts
export type MenuAction = { id: string; label: string; onSelect: () => void; disabled?: boolean; icon?: ReactNode };

type MenuOptions = {
  label: string;                      // 菜单的可访问名称
  actions: readonly MenuAction[];
  placement?: "below" | "above";      // 默认 below
  matchTriggerWidth?: boolean;        // 默认 false；true 时菜单宽度 = max(触发器宽度, 200px)
};

/** 菜单的状态、键盘与浮层；触发器由调用方渲染。 */
export function useMenu(options: MenuOptions): {
  triggerProps: { ref; "aria-haspopup": "menu"; "aria-expanded": boolean; "aria-controls": string | undefined; onClick; onKeyDown };
  menu: ReactNode;     // 打开时的浮层，未打开为 null
  open: boolean;
};

export function MenuButton({ label, icon, actions, disabled, placement }: …) {
  const menu = useMenu({ label, actions, placement });
  return <><Button {...menu.triggerProps} icon={icon} disabled={disabled}>{label}</Button>{menu.menu}</>;
}
```

- `useMenu` 就是现在 `MenuButton` 里的状态、`open/close`、`onMenuKey`、外部点击/滚动/缩放关闭，原样搬进去；`MenuButton` 变成薄包装，现有两处调用（`track-actions.tsx`、`player-controls.tsx`）与 `menu-button.test.tsx` 不变。
- `placement: "above"`：打开时用 `{ left, bottom: window.innerHeight - rect.top + 4 }` 定位（CSS `bottom`，不需要先量菜单高度）；`max-height: calc(rect.top - 8px)` 防止顶出窗口。`below` 维持现有逻辑（含底部越界上移）。
- 浮层容器：打开时取 `trigger.closest("dialog") ?? document.body` 作为 portal 目标。在对话框里时菜单渲染在对话框内（`position: fixed` 仍相对视口，对话框没有 transform，不会被 `overflow: auto` 裁掉），不受惰性影响。对现有调用无影响（它们不在对话框里时目标仍是 `body`）。
- 菜单项有 `icon` 时渲染为 `<span aria-hidden="true">{icon}</span>{label}`，按钮改 `display: flex; gap: var(--space-2); align-items: center`；没有 `icon` 的现有菜单外观不变。
- 菜单项仍是按钮。“设置”是导航，但 `shared/ui` 不依赖路由，且 ARIA 菜单项本身就是命令；调用方在 `onSelect` 里 `navigate`。这是“导航用链接”规则的一个例外，写进 component-guidelines。

### 4.2 `UserMenu`（`frontend/src/app/shell/user-menu.tsx` + `.module.css`，删除 `identity-panel.*`）

```tsx
export function UserMenu({ onNavigate }: { onNavigate?: () => void }) {
  const { viewer } = useViewerScope();
  const logout = useLogout();
  const navigate = useNavigate();
  const inSettings = useRouterState({ select: (state) => state.location.pathname.startsWith("/settings") });
  const menu = useMenu({
    label: "账号",
    placement: "above",
    matchTriggerWidth: true,
    actions: [
      { id: "settings", label: "设置", icon: <Settings size={16} />, onSelect: () => { onNavigate?.(); void navigate({ to: "/settings" }); } },
      { id: "logout", label: "退出登录", icon: <LogOut size={16} />, onSelect: () => logout.mutate(), disabled: logout.isPending },
    ],
  });
  return (
    <section className={styles.panel} aria-label="当前用户">
      {logout.isError ? <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} /> : null}
      <button type="button" className={styles.trigger} data-current={inSettings ? "" : undefined} aria-busy={logout.isPending || undefined} {...menu.triggerProps}>
        <span className={styles.avatar} aria-hidden="true">{initialOf(viewer.username)}</span>
        <span className={styles.who}>
          <span className={styles.name}>{viewer.username}</span>
          <span className={styles.role}>{logout.isPending ? "正在退出…" : roleLabels[viewer.role]}</span>
        </span>
        <ChevronsUpDown size={16} aria-hidden="true" />
      </button>
      {menu.menu}
    </section>
  );
}
```

- **保留 `<section aria-label="当前用户">`**：把失败提示与按钮归为一组，现有测试里 `getByRole("region", { name: "当前用户" })` 的选择器继续可用。
- 一整行是一个按钮：圆形头像（28px，`border-radius: 50%`，`--color-accent-100` 底、`--color-accent-800` 字、1px `--color-accent` 描边，DESIGN 3.3 允许头像用圆形）+ 用户名（单行，超长省略号）+ 角色小字（`--fs-12`，`--text-secondary`）+ 上下箭头图标。行高不低于 `--control-height`，窄屏 44px。按钮的可访问名称是“curator 管理员”这样的完整文字（省略号只是视觉截断，DOM 里是全名）。
- 头像首字：`initialOf(username)` 用 `Intl.Segmenter` 取第一个字素（没有时退回 `Array.from(username)[0]`），拉丁字母转大写；中日文原样。
- **在设置页时**：`data-current` → 整行金色轻底（同主导航当前项的 `color-mix(in srgb, var(--color-accent) 11%, transparent)`）+ 用户名加粗。不加 `aria-current`（这个按钮不是指向当前页的链接）；读屏用户由设置分栏的 `aria-current` 与 h1 得知位置。
- 菜单：在按钮上方弹出、与按钮同宽，两项“设置”（`Settings` 图标）、“退出登录”（`LogOut` 图标）。键盘：Enter / Space / ↓ 打开并聚焦首项，↑ 打开并聚焦末项，↑↓ 循环、Home/End、Esc 关闭并回到按钮、Tab 关闭并离开——全部继承 `useMenu`。
- **退出**：选“退出登录”后菜单关闭（焦点回到按钮），按钮 `aria-busy`、角色那一行换成“正在退出…”；成功由会话控制器清理并离开已登录界面。**失败**：`FailureNotice`（标题“退出失败”，带“重试”）显示在按钮**上方**、同一个 section 内——菜单本来就向上弹，提示放上方不会把按钮挤出侧栏底部；重试直接 `logout.mutate()`。
- **窄屏对话框**：`AppShell` 里两处 `<IdentityPanel />` 换成 `<UserMenu />` 与 `<UserMenu onNavigate={() => setMenuOpen(false)} />`。在对话框里打开时菜单渲染在对话框内（§4.1）。选“设置”：`useMenu` 先关菜单（焦点回到用户按钮），再调用 `onNavigate` 关闭对话框（`ModalDialog` 把焦点还给“菜单”按钮），随后导航；`AppShell` 的路径变化效果再把焦点移到主内容区。退出失败的提示显示在对话框里的那一份 `UserMenu` 上（两份各自持有 `useLogout`，与现在一致）。
- 去掉侧栏里的“修改密码”按钮（入口在设置 › 账号）。
- 侧栏 `.identity { margin-top: auto; }` 不变；去掉原面板的 `border-top` 与上内边距以外的旧样式，按钮自身有悬停底色（`@media (hover: hover)` 下 `--color-hover`）与焦点环。

## 5. 导航项（`app/shell/nav-items.ts`）

- 删掉 `/library`、`/members`、`/operations`、`/system/backups` 四项，“系统”分组随之消失；`NavItem["to"]` 联合类型去掉这四个；`import` 去掉 `DatabaseBackup`、`History`、`Users`、`FolderSearch`（它们改由 `settings-sections.ts` 引入）。
- “资料库”分组剩“元数据问题、待认领、可能是同一位”（后者仍带 `artists: true`）。
- `nav-items.test.ts` 改为：
  - 成员：只有“聆听”五项（不变）。
  - 管理员：`聆听` 五项 + `资料库: ["元数据问题", "待认领", "可能是同一位"]`，没有“系统”。用例名改为“管理员额外看到资料库分组”。
  - 艺人不可用：管理员 `资料库: ["元数据问题", "待认领"]`。

## 6. 测试影响

### 6.1 Go

| 文件 | 改动 |
| --- | --- |
| `internal/backup/files_test.go` | 新增 `DeleteBackup` 单元测试：成功删除且同目录其他文件不动；同名再删 `ErrNotFound`；非法名（含 `../`、`\`、NUL、`roomusic-x.dump`、状态文件名、`.partial` 名）`ErrNotFound`；指向目录外与目录内的符号链接、同名目录、FIFO 都 `ErrNotFound` 且链接、目标、目录都还在；目录不存在 `ErrNotFound`；目录 `0500`（非 root）时 `ErrNotWritable` 且有 `backup.delete_failed` 日志；成功有 `backup.deleted` 日志且带 `actor_id` |
| `internal/backup/service_test.go`（或 files_test） | `retain()` 遇到已被删掉的过期文件不打 `backup.retention_failed` |
| `internal/app/system_api_integration_test.go` | `TestPostgreSQLSystemAuthorizationAndOrigin` 加 DELETE：匿名 401、成员 403 `forbidden`、管理员无 Origin 403 `origin_required`、跨源 403 `origin_forbidden`，且这些情况下文件仍在；新增删除测试：200 体是 Overview 且不含被删项、文件已从目录消失、再删 404 `backup_not_found`；路径安全子测试沿用下载测试的那组非法名与链接/目录，断言 404 且目录内容（含 `outside` 文件、链接目标）不变；目录不可写（非 root）503 `backup_directory_not_writable` |
| `internal/app/system_samples_integration_test.go` | 录制 `system/backup-deleted`（200） |
| `testdata/api-samples/system/backup-deleted.json` | 新文件（`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 生成后人工核对） |

第二轮新增（R1b、R1c）：

| 文件 | 改动 |
| --- | --- |
| `internal/backup/retention_test.go`（新，纯单元） | `retentionPlan` 表驱动：只有天数、只有份数、两者同时（取并集）、都不限为空、最新一份即使超期或 `KeepCount=1` 也保留、四种来源一起计数、时间相同按名字倒序决定先后；规则文件读写：不存在 → 环境变量天数 + 份数不限；有效文件优先于环境变量；损坏 / 版本不对 / 越界 / 多余 JSON 值 → 读不到；写入是原子的（临时文件不残留，`0600`）；`retain()` 读不到规则时不删任何文件并记 `backup.retention_unreadable`；`SaveRetention`：未确认且会删 → 不写文件不删文件；未确认且不删 → 保存；确认 → 保存并删，`deleted` 为实际数；目录不可写（非 root）→ `ErrNotWritable` 且没有删文件 |
| `internal/backup/files_test.go` 追加 | `DeleteBackups`：混合成功 / 不存在 / 非法名 / 符号链接，名单分类正确、去重、顺序保持；目录不存在全部 `missing` |
| `internal/backup/service_test.go` 追加 | 备份成功后按保存的规则清理（天数与份数同时生效，A1d）；从未保存时只按 `KeepDays`（与现在一致）；`Overview.Retention` 三种状态 |
| `internal/backup/backup_integration_test.go` 或 `restore_integration_test.go` | 恢复数据库后规则文件仍在、规则不变（证明“恢复不回退规则”） |
| `internal/app/system_api_integration_test.go` 追加 | 两个新接口的授权与来源矩阵（同 §6.1 单删）；批量删除 200 名单与目录内容；输入校验（空、501 个、非字符串）；保留规则：未确认 → `confirmation_required` 且文件都在；确认 → 只剩最新 N 份；两项不限 → 不弹确认、不删；校验（缺键、`0`、`3651`、`1.5`、`"3"`、`confirm` 非布尔）；**新建 `backup.Service` 模拟重启后规则仍在**（A1c） |
| `internal/app/system_samples_integration_test.go` 追加 | 录制 `system/backups-deleted`、`errors/backup-deletions-invalid`、`system/retention-confirmation-required`、`system/retention-saved`、`system/retention-saved-unlimited`、`errors/backup-retention-invalid`；重录已有四个带 Overview 的样本 |

### 6.2 Vitest

需要随地址或结构更新的现有文件：

| 文件 | 原因 |
| --- | --- |
| `app/shell/nav-items.test.ts` | §5 |
| `app/app.test.tsx` | “当前用户”区里不再有“退出登录”按钮：先点用户按钮，再点菜单项“退出登录”（102–348 行多处，含窄屏对话框 348 行） |
| `app/account-password.test.tsx` | 修改密码入口改到 `/settings/account`；`/members` 路径 |
| `app/members.test.tsx` | `/members` → `/settings/members`；修改密码相关 |
| `app/library-scans.test.tsx`（约 45 处）、`library-roots.test.tsx`、`scan-feedback.test.tsx` | `/library`、`/library/scans/...` 地址与 URL 断言 |
| `app/operations.test.tsx`（约 20 处）、`default-version.test.tsx`、`artist-governance-step5.test.tsx`、`claim-flow.test.tsx`、`claims.test.tsx`、`issues.test.tsx`、`albums.test.tsx` | 指向 `/operations`、`/library` 的链接断言 |
| `app/login-throttle.test.tsx:24` | 旧地址在登录守卫前跳转，`redirect` 参数变为 `/settings/members` |
| `app/system-backups.test.tsx` | 路径 `/settings/backups`；73 行“非管理员看不到备份链接”改为看设置分栏 |
| `src/test/system-data.ts`、`src/test/fake-system.ts` | 替身支持 `DELETE /system/backups/{name}`（成功返回 Overview、不存在 404、可注入 503 `backup_directory_not_writable`）；Playwright 的 `e2e/support/api-mock.ts` 共用 `system-data.ts` |

`features/auth/session/redirect.test.ts` 只是字符串用例，**不需要改**。

新增：

| 测试 | 覆盖 |
| --- | --- |
| `shared/ui/menu-button.test.tsx` 追加 | `placement="above"` 时菜单底边在触发器上方；菜单项图标不进可访问名称；触发器在 `<dialog open>`（`showModal`）里时菜单渲染在该对话框内；原三个用例不改 |
| `app/routes/settings-sections.test.ts` | 成员 1 项、管理员 5 项且顺序固定 |
| `app/settings.test.tsx`（新） | A4：管理员五栏、成员一栏；成员访问 `/settings/members` 显示“需要管理员权限”且没有请求 `/api/v1/users`；`/settings` 跳到 `/settings/account`；A5：五个旧地址各带查询参数（`/library?scan_page=2`、`/library/scans/<uuid>`、`/members?tab=1`、`/operations?page=2&release=…&group=…&artist=…`、`/system/backups`）跳到新地址且参数不变、历史项被替换；匿名访问旧地址时登录页 `redirect` 是新地址 |
| `app/user-menu.test.tsx`（新，或并入 `app.test.tsx`） | A7：按钮显示首字、用户名、角色；点击/键盘打开“设置 / 退出登录”；Esc 回到按钮；选“设置”进入 `/settings/account`；在设置页时按钮有 `data-current`；退出失败显示“退出失败”与“重试”、仍在当前页；A8：窄屏对话框里选“设置”后对话框关闭 |
| `app/system-backups.test.tsx` 追加 | A1/A3：删除 → 确认框（标题含时间、正文“删除后不能恢复。”）→ 取消不发请求；确认后发 `DELETE`、按钮忙碌、成功后行消失、焦点到“备份文件”标题；404 时对话框关闭且列表刷新、无错误；503 时对话框内显示“备份目录不能写入，没有删除。”，可重试 |
| 解码样本 | `system/backup-deleted.json` 经 `decodeBackupOverview` 解码 |
| `app/system-backups.test.tsx` 追加（第二轮，A1b） | 勾选两行 → 出现“删除所选（2）”→ 确认框“删除 2 份备份？”→ 一个 `POST` 带两个名字 → 行消失、勾选清空、焦点到标题；全选 / 部分选中时 `indeterminate` / 再点取消全选；部分失败时对话框留着、标题变“删除 1 份备份？”、列出没删掉的时间，重试只发剩下的；`missing` 不显示为失败；轮询返回的列表少了某份时它的勾选被去掉 |
| `app/system-backups.test.tsx` 追加（第二轮，A1c） | 初值来自 `retention`；勾选“不限”禁用数字框；越界不发请求并显示字段错误；不会删 → 一次请求、“已保存。”；会删 → 确认框“将删除 3 份旧备份。删除后不能恢复。”，取消不再发请求，确认发 `confirm: true`、显示“已保存，删除了 3 份旧备份。”；503 在按钮下方 / 确认框内显示；`retention: null` 时显示“没能读取当前设置。” |
| 解码样本（第二轮） | `system/backups-deleted.json`、三个 `retention-*` 样本、重录后的 `system/backups.json` |
| 替身（第二轮） | `src/test/system-data.ts` 增加 `POST …/deletions`、`PUT …/retention` 的行为（规则保存在替身状态里，`retentionPlan` 与后端同算法，可注入 `failed`），Vitest 与 Playwright 共用 |

### 6.3 Playwright

模拟后端（`frontend/e2e/*.spec.ts`）需要改地址或交互的：

| 文件 | 改动 |
| --- | --- |
| `auth.spec.ts` | 31/132/199 “当前用户”区仍可用；36/65/99 退出改为“用户按钮 → 退出登录”；56–63 `/members?tab=1` 登录后 URL 变为 `/settings/members?tab=1`；80 主导航“扫描目录”链接改为从用户菜单进设置；117/128 `/library`；150–298 修改密码改为在 `/settings/account` 触发，窄屏菜单内修改密码的用例（259、288）改为“窄屏菜单 → 设置 → 账号 → 修改密码” |
| `support/account-password.ts` | 场景“身份区”“成功关闭后身份区”改为账号栏；51 `/members`；124 退出按钮边界改为用户按钮边界 |
| `a11y.spec.ts`、`responsive.spec.ts` | 场景路径改到 `/settings/...`；新增场景：设置 › 账号、设置 › 备份（含删除确认框）、用户菜单展开（宽屏）、窄屏对话框内用户菜单展开 |
| `library.spec.ts`、`scan-feedback.spec.ts`、`keyboard.spec.ts`、`members.spec.ts`、`governance.spec.ts`、`claims.spec.ts`、`default-version.spec.ts` | 地址与 URL 正则；`keyboard.spec.ts:38` 主导航“成员”、`:150` 对话框里“扫描目录”改为设置分栏；`governance.spec.ts:343,450`、`library.spec.ts:207` 主导航链接改为设置分栏链接 |
| `system-backups.spec.ts` | 地址 `/settings/backups`；1440 与 375 下“下载/删除”同一行单行文字；新增删除流程与确认框布局；第二轮：勾选 + “删除所选（n）”流程、工具行与“自动清理”区段在 1440 / 375 下单行不折行、无横向溢出、axe 无 serious，截图交审（A9） |

真实服务（`frontend/e2e/real/`，由 `scripts/playback-e2e.sh` / `playwright.real.config.ts` 运行）：

| 文件 | 改动 |
| --- | --- |
| `account.real.ts` | 63–85 修改密码改到 `/settings/account`；129/146 `/members` |
| `claims.real.ts:196`、`governance.real.ts:96,172` | `/operations` → `/settings/operations`；174 “主导航没有操作记录”断言保持（现在对所有人都成立，可改为断言设置分栏里成员看不到） |
| `scan-feedback.real.ts:75` | `/library/scans/` → `/settings/library/scans/`；118 断言保持 |
| `playback.real.ts:394` | “退出登录”改为用户菜单操作 |

### 6.4 验收对照

| 验收 | 主要证据 |
| --- | --- |
| A1 | Go 集成测试（删除、再删 404）+ Vitest 删除流程 + `system-backups.spec.ts` |
| A2 | Go 授权/来源/路径安全测试 |
| A3 | Vitest 确认框取消、失败重试 |
| A1b | Go 批量删除集成测试 + Vitest 批量删除 + `system-backups.spec.ts` |
| A1c | Go 保留规则集成测试（含新建 Service 模拟重启）+ `retentionPlan` 单元测试 + Vitest 保存流程 |
| A1d | `service_test.go` 备份后清理两项同时生效、从未保存时只按 `KeepDays` |
| A4 | `settings.test.tsx` + `settings-sections.test.ts` |
| A5 | `settings.test.tsx` 旧地址用例 + §2.3 grep 门槛 |
| A6 | `nav-items.test.ts` |
| A7 | `user-menu.test.tsx` + `menu-button.test.tsx` + `auth.spec.ts` |
| A8 | `user-menu.test.tsx` 窄屏 + `auth.spec.ts`/`responsive.spec.ts` 375 |
| A9 | `responsive.spec.ts`、`system-backups.spec.ts` 截图（1440、375），交用户审阅 |
| A10 | 上述受影响文件逐个跑通；只跑相关文件，不跑全量（AGENTS.md 测试范围约束） |

## 7. 界面文案

- 不写防御性说明：设置页、账号栏、用户菜单、备份删除都不解释权限、隔离或实现细节。
- 标题与按钮已说清楚的不加描述：账号栏没有描述段落；设置分栏没有说明；用户菜单只有“设置”“退出登录”。
- 必要的一句：删除确认框正文只有“删除后不能恢复。”；目录不可写时“备份目录不能写入，没有删除。”；退出中“正在退出…”。
- 第二轮新增的文案全集：区段标题“自动清理”；字段“保留天数”“保留份数”“不限”“天”“份”；按钮“保存”“正在保存…”“全选”“删除所选（n）”；确认框“删除 n 份备份？”“保存清理设置？”“将删除 n 份旧备份。删除后不能恢复。”“保存并删除”；结果“已保存。”“已保存，删除了 n 份旧备份。”“已删除 n 份。”（只给读屏）“有 n 份没能删除”“有 n 份旧备份没能删除。”“没能读取当前设置。”“备份目录不能写入，没有保存。”；字段错误“填 1 到 3650 之间的整数。”“填 1 到 1000 之间的整数。”。除此之外不加说明。
- 现有页面（成员、操作记录、无权限页）的文案不在本任务里改。

## 8. 规格同步（Phase 3.3）

- `.trellis/spec/backend/core0-runtime-contracts.md:67`：备份接口加 `DELETE /api/v1/system/backups/{name}`，写明 200 Overview、错误码与并发表要点。
- 同文件 163–165 行（第二轮）：保留规则从“保留 `ROOMUSIC_BACKUP_KEEP_DAYS` 天”改为“天数与份数两个上限、最新一份不删、规则存于备份目录 `.roomusic-backup-retention.json`，从未保存时用环境变量”；接口清单加 `POST …/deletions`、`PUT …/retention`；Overview 多 `retention`。
- `deploy/production/README.md`：`ROOMUSIC_BACKUP_KEEP_DAYS` 一行改为“从未在网页上设置过保留规则时的默认天数”；第 4 节补“可在设置 › 备份调整自动清理”。`deploy/production/example.env` 第 16 行注释同步。
- `.trellis/spec/frontend/component-guidelines.md:43`：外壳底部是用户菜单（向上弹出，设置 / 退出登录）；`MenuButton` / `useMenu` 的 `placement`、图标、对话框内渲染；菜单项导航的例外。
- `frontend/DESIGN.md` 第 4 节 URL 形状与“底部为当前身份”，`DESIGN-components.md:105` 导航分组；新增 `--layout-settings-nav-width`。

## 9. 回滚与风险

**回滚**：没有数据库迁移、没有配置项。后端只新增路由与 Service 方法（第二轮后共三条新路由：单删、批量删、保存规则），前端是路由与组件重排；按提交整体 `git revert` 即可。回滚后旧地址重新直接可用，新地址 `/settings/...` 变成 404 页（书签只在本版本期间产生，影响很小）。**已删除的备份文件不会因回滚恢复**——这是功能本身的性质，由确认框说明。

**第二轮补充**：保留规则不用数据库，仍然**没有迁移**。代码回滚或镜像降级后，旧程序不认识 `.roomusic-backup-retention.json`，忽略它、只按 `ROOMUSIC_BACKUP_KEEP_DAYS` 清理；文件留在目录里，再升级回来规则恢复生效。需要彻底撤掉规则时删掉这个文件即可（之后按环境变量）。降级前若管理员把天数设成了“不限”或大于环境变量的值，旧程序在下一次备份成功后会按环境变量删掉更旧的备份——降级说明里要提醒先把 `ROOMUSIC_BACKUP_KEEP_DAYS` 调到需要的值。

| 风险 | 应对 |
| --- | --- |
| 误删备份 | 确认框（初始焦点在“取消”）；只删单个；自动备份照常；删除最新一份后若当天时刻已过会自动补一份 |
| 路径穿越 / 链接攻击 | 正则白名单 + `os.OpenRoot` + `Lstat` 拒非普通文件；Go 测试覆盖与下载同一组非法输入 |
| `MenuButton` 改动波及播放器模式菜单与曲目“更多”菜单 | 对外 API 不变；原测试不改并必须通过；`player.spec.ts` 中模式菜单相关用例与曲目菜单的 Playwright 用例纳入回归 |
| 模态对话框内的菜单不可用 | portal 目标取最近的 `<dialog>`；Vitest 与 375 Playwright 覆盖 |
| 站内残留旧链接（类型检查发现不了） | §2.3 grep 门槛写进 `implement.md` 的验收 |
| 旧地址重定向丢参数 | 用原始 `searchStr` 与 hash 拼接；Vitest 逐个旧地址断言 |
| 测试改动面大（约 30 个文件） | 只改地址与交互，不改断言语义；按文件分批跑，失败先定位不扩大范围 |
| 规则设得太紧，一次删掉很多份 | 会删时必先确认并给出份数；服务端不带确认绝不删；最新一份永远保留 |
| 确认后实际删除数与确认框不同 | 只可能因期间新备份或别处删除而变化；响应给出实际数并刷新列表（PRD 已接受） |
| 规则文件损坏 | 不退回环境变量、跳过清理并告警，Overview 给 `null`，界面提示后可直接保存覆盖 |
| 全选 500 份一次删除 | 一个请求、一次目录打开；逐份结果分类返回，部分失败可只重试剩下的 |
| 降级后按环境变量清理（见上方回滚说明） | 发布说明与部署文档写明 |
