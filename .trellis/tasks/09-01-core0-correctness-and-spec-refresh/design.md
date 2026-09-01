# Core 0 实现偏差修复技术设计

## 设计边界

本任务保持现有模块边界和 REST 版本，不引入新的服务、数据库表或前端依赖。
代码仍位于当前 Core 0 的过渡目录：`backend/cmd/roomusic`、
`frontend/src` 和 `scripts/`。规范更新会明确该过渡形态与架构文档中的目标
feature 分层，不以本任务为由进行大规模目录重构。

## 变更面与数据流

```text
生产脚本 -> backend module cwd -> Go server

浏览器 unknown JSON -> api.ts decoder -> typed DTO -> App state
                                      -> classified ApiRequestError

管理员 PATCH user -> origin/auth -> transaction
                   -> lock active admins + target user
                   -> user state + session revocation -> commit -> response

scan run -> WalkDir/parser -> bounded diagnostic/observation
         -> terminal status -> only succeeded: missing reconciliation

POST root -> canonical path + idempotency -> transaction/upsert
          -> actual status/revision + operation journal -> response
```

### 生产启动

`scripts/prod.sh` 保留现有环境文件检查、生产环境标记和安全 Cookie 前置条件，
只把 `go run` 放到 `backend` module 根并使用 `go run ./cmd/roomusic`。构建仍由
前端脚本写入唯一的 `backend/cmd/roomusic/web` 嵌入目录。验证使用 shell 语法检查
和跳过构建的启动 smoke 检查，避免要求本地数据库可用。

### 前端 REST 边界

- 在 `api.ts` 增加窄 DTO decoder：session role、创建用户结果、用户 disabled
  结果和 library root 列表名称；使用小型 `requireRole`/`require...` 辅助函数，
  不以 `as` 恢复 unknown 数据。
- 创建用户接口继续返回当前简短 payload，因此使用单独的
  `CreatedUserDTO`，不强迫后端为本次修复扩展时间字段。
- 更新用户接口使用 `decodeUserStatus`，而不是 identity decoder；目录列表和
  后端 presenter 提供同名安全 label（仅 basename，不传原始绝对路径）。
- `decodeApiErrorResponse` 保持现有线上 envelope（`error` 对象加顶层
  `request_id`）以兼容已有客户端；长期规范记录该事实，不在本任务中做 wire
  迁移。

### 用户状态事务

`updateUser` 的事务按固定顺序执行，避免两个管理员同时禁用时绕过最后管理员
保护：

1. 开始 `BeginTx`，使用 `SELECT id ... WHERE role='admin' AND disabled_at IS NULL
   ORDER BY id FOR UPDATE` 锁定所有当前启用管理员并统计数量。
2. 使用 `SELECT role, disabled_at ... FOR UPDATE` 锁定目标用户；无行返回
   `not_found`。
3. 禁用启用管理员且统计数小于等于 1 时返回 `last_admin`；其他状态按请求更新。
4. 禁用操作在同一事务中撤销目标用户所有未撤销 session，并检查两条 SQL 的
   `error`；启用只清除 `disabled_at`。
5. 提交后返回数据库确认的 `disabled` 状态。任一步失败均回滚并返回
   `database_unavailable`/`database_error`，绝不返回成功。

锁顺序先锁管理员集合、再锁目标用户；所有用户生命周期请求走同一顺序，避免
 交叉锁死。事务不写 Operation Journal，因为本任务遵守现有 Core 0 只为目录
 操作记录 Journal 的范围。

### 扫描安全与多碟

- 将 unsupported 格式、CUE 解析/引用错误、权限/断链/目录不可用和诊断写入
  失败统一视为当前 root 不完整，记录 bounded diagnostic 后继续遍历其他文件。
 这样终态最多为 `incomplete`/`failed`，不会进入 `succeeded`，因此不会调用
 `markMissing`。已成功解析的其他文件仍可保存观察。
- `saveObservation` 按 release 内 `media.position = observation.DiscNumber`
  查找 Medium；不存在时创建该 position。首次创建 Release 仍创建其首个
  Medium，已有 Track 重扫只更新其原有来源身份和对应 Medium。
- 扫描任务继续使用进程拥有的背景上下文；本任务不新增取消端点。`canceled`
  wire 状态保留为未来任务合同，规范明确当前不可由用户触发。

### 目录新增响应

`POST /api/v1/library-roots` 的 upsert 不改变已停用行的状态；SQL 同时返回
`status` 和 `revision`，响应使用真实值。`CreatedLibraryRootDTO.status` 扩展
为 `active | disabled`，显式 restore 仍负责恢复并产生自己的 revision/Journal
记录。新增 `name` 字段时只使用安全 basename，兼容旧客户端的 `path` label。

### 界面可访问性

在现有 CSS/单体组件范围内做最小修复：补 `:focus-visible`，移除违反约束的负
字距，统一移动端堆叠断点为 `768px`，为动态长文本补 `title`/aria 名称，并让
播放/暂停 aria 状态随状态变化。上一首/下一首仍是无音频服务的演示控件，不新增
虚假的播放 API。

## 兼容性与迁移

- 不新增 SQL migration；仅使用已有列和约束。
- 现有客户端可继续读取 `path`、`status` 和简短用户响应；新增 `name` 是向后
  兼容字段。停用目录重复 POST 的状态现在如实反映数据库，属于错误修复而非
  隐式恢复行为。
- `canceled`、错误码和顶层 `request_id` 保持现有 wire 拼写；规范同步实际实现。
- 测试 fixture 通过 `ROOMUSIC_TEST_DATABASE_URL` 使用隔离 schema；未配置时
  保持明确跳过。

## 失败处理与回滚

- 每一项代码修改均可独立回退：脚本、前端 decoder、用户事务、scanner 和
  presenter 没有共享新迁移。
- 若多碟集成测试暴露既有目录归属假设，回退到只修复 unsupported/事务安全，
  不改变数据库 schema；该风险在检查阶段单独报告。
- 规范文档不得把延期的迁移追踪、持久取消、用户 Journal 或 feature 拆分写成
  已完成能力。

## 观测与安全

保留现有 request ID、错误脱敏和 HttpOnly session 机制；不把绝对路径、密码、
token 或数据库 URL 引入新响应、日志或 UI。HTTP 日志字段扩充和迁移锁治理不在
本任务范围，规范会记录为后续技术债。
