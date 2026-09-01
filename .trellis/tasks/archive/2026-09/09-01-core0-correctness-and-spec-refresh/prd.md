# 修复 Core 0 实现偏差并更新 Trellis 规范

## Goal

修复当前 Core 0 中已经确认的启动、安全和数据一致性问题，使生产启动、
前端 REST 边界、用户管理和扫描结果都遵守既有产品合同；同时把代码中已经
确定的工具链、运行边界、API 约束和已知技术债写入 Trellis 长期规范，避免
后续实现继续依据过时的“尚未选择”描述。

## Background And Confirmed Facts

- 生产脚本从仓库根目录执行 `go run "$ROOT_DIR/backend/cmd/roomusic"`，而
  Go module 根位于 `backend/`，当前路径会导致启动失败（`scripts/prod.sh:18`）。
- 前端已采用 React + TypeScript + Vite、原生 `fetch`、手写 runtime decoder、
  Vitest 和 ESLint，但 `frontend/src/api.ts` 仍为缺失角色默认 `admin`，
  `frontend/src/main.tsx:186,192` 仍有未经验证的响应 cast/identity decoder。
- 用户禁用/启用流程在 `backend/cmd/roomusic/application.go:522-554` 忽略
  查询和写入错误，且最后管理员检查与写入不在同一个可靠的事务边界。
- 扫描器在 `backend/cmd/roomusic/scanner.go:188-193` 对不支持格式只记录诊断，
  仍可能以成功状态执行 `missing` 对账；保存观察时始终复用第一个 Medium，
  无法保留多碟结构（约 `:249-263`）。
- 目录新增在 `backend/cmd/roomusic/application.go:236-246` 对已停用路径不恢
  复状态，却固定返回 `status: active`，响应与数据库事实不一致。
- 播放器样式当前使用 `900px`/`620px` 断点、移除输入框 outline 且存在负字距，
  与 `player-design-guidelines.md` 的响应式和焦点约束不完全一致；播放器按钮
  也需要保持可访问名称与演示边界。
- 多份 `.trellis/spec/` 文档仍描述不存在的应用、工具和目录，并引用已归档前
  的 `tasks/08-31-roomusic-core-0-rebuild` 路径。

## Requirements

### R1. 生产启动与运行合同

- `scripts/prod.sh` 必须从仓库根目录和显式 `ROOMUSIC_ENV_FILE` 路径可靠启动，
  在正确的 Go module 工作目录执行应用；现有安全 Cookie 前置检查保持不变。
- 为修复增加 shell 层可重复验证；不得覆盖工作树中已有的 `.env.dev` 改动。

### R2. 前端 REST 类型安全

- `decodeSession` 缺少或未知 `role` 必须拒绝，不能以管理员身份降级或默认放行。
- 创建用户、更新用户状态和目录列表响应都必须有与实际 wire payload 匹配的
  decoder；移除 `main.tsx` 中的 raw cast/identity decoder。
- 目录列表提供稳定的安全显示名称，DTO、后端 presenter 和 UI 读取同一字段；
  不把原始服务器路径带入浏览器。
- 为缺失字段、未知枚举、有效响应和错误回退补充单元测试。

### R3. 用户生命周期原子性

- 用户启用/禁用必须在一个 PostgreSQL 事务中完成目标用户状态、最后管理员
  保护和相关 session 撤销；任何查询或写入失败都返回分类错误，不能返回成功。
- 并发管理请求不得绕过最后一个启用管理员保护；不存在的用户返回稳定的
  `not_found`，成功响应反映数据库最终状态。

### R4. 扫描数据安全与多碟结构

- 不支持格式、无法解析的 CUE、权限/遍历错误和目录不可用必须使扫描不能进入
  “完整成功”门槛，从而禁止本次 `missing` 对账；诊断仍需有界并继续处理其他
  合法文件。
- 观察保存按 `disc_number` 选择或创建对应 Medium，重扫保持既有 Track 身份，
  不把多碟文件全部挤入第一个 Medium。
- 现有 `canceled` wire 枚举和“只有完整成功才负向对账”语义保持一致；尚未
  提供的用户取消端点不伪装成已实现能力。

### R5. 目录新增响应一致性

- 重复注册已停用路径时，接口必须返回真实持久化状态和 revision；不得返回与
  数据库不符的 `active`。显式恢复仍通过现有 restore 操作完成，并保留幂等/审计
  合同。

### R6. 播放器界面安全与可访问性

- 修复焦点可见性、按钮 aria 名称、长文本完整名称和响应式断点漂移；固定播放器
  不遮挡内容，演示播放状态不暗示真实音频服务。
- 不引入新的 UI、播放服务或浏览器凭据存储依赖。

### R7. 回归证据

- 为每项行为修复增加最小单元或集成测试，覆盖权限、事务回滚、未知角色、重复
  目录、unsupported/incomplete 扫描、多碟 Medium 和前端 decoder。
- 运行与改动直接相关的 Go、前端和 shell 验证；PostgreSQL 集成测试在未配置
  `ROOMUSIC_TEST_DATABASE_URL` 时必须明确标记为跳过，而不是冒充已执行。

### R8. Trellis 长期规范与根文档

- 更新 frontend/backend index、目录、质量、类型、状态、hook、数据库、错误和
  日志规范，区分当前 Core 0 过渡实现与目标架构，记录实际选型和 API/运行合同。
- 更新环境/构建/生产边界、错误 envelope、session 安全、分页/请求上限、扫描
  状态、唯一嵌入产物目录和未实现门禁；修复失效任务链接。
- README 与相关根级约束文件必须与脚本实际行为一致；新增或修改文档使用简体中文。
- 对本次不实现的迁移版本追踪、持久取消端点、完整 HTTP 观测字段、用户操作
  Journal 和前端 feature 拆分，登记为明确的后续技术债，而不是写成已完成能力。

## Acceptance Criteria

- [ ] 从仓库根目录执行生产脚本时，Go module 能正确解析；配置失败仍安全退出。
- [ ] 前端所有受影响 REST 响应经过 runtime decoder；缺失/未知角色不会被当成
      管理员，`npm run typecheck`、`npm run lint` 和 decoder 测试通过。
- [ ] 用户状态变更在数据库错误、并发最后管理员场景下不会返回虚假成功，且
      session 撤销与用户禁用原子提交。
- [ ] 含 unsupported/解析/遍历问题的扫描不会执行 `missing` 对账；多碟 fixture
      产生对应多个 Medium，既有同路径 Track ID 回归通过。
- [ ] 已停用目录重复注册的响应与持久状态一致；已有目录操作幂等与 revision
      测试继续通过。
- [ ] 播放器在桌面和窄屏布局无遮挡，焦点和 aria 名称可见/可读，且不宣称真实
      音频播放能力。
- [ ] 相关 Trellis 规范不再把已实现工具/目录写成“尚未选择”，失效任务链接已
      修复，并明确当前实现、目标架构和延期技术债。
- [ ] 代码、规范和任务记录完成一次提交；用户明确要求的 `git push` 在提交验
      证通过后执行，并报告远端结果。

## Out Of Scope / Deferred

- 全量迁移执行器重构（schema version 跳过、分布式锁、整体事务、校验和）及其
  生产回填策略。
- 新增持久化扫描取消 API、跨进程任务队列或恢复服务。
- 把当前单体 `main.tsx`/`api.ts` 拆成完整 feature/router/query-cache 架构。
- 为用户创建/禁用/会话撤销新增独立 Operation Journal/Change Set 表。
- 引入真实音频流、下载、Range 服务、Redis/Meilisearch 或新的 UI 依赖。

## Scope Decision

本次按已确认范围只落地 R1-R8 中可在现有 Core 0 边界内验证的修复；迁移执行器
重构和持久化扫描取消 API 延期，并按 `Out Of Scope / Deferred` 登记为后续技术债。
