# ROOMusic Core 0 实现计划

## 执行原则

按依赖顺序分成可独立验证的工作单元。每个单元完成后先运行对应验证，再进入下一个单元；不得直接从 V0 复制全部代码或迁移历史 schema。产品代码只在用户批准本规划并执行 `task.py start` 后开始修改。

## 阶段清单

### 1. 工程骨架与本地运行

- 建立 Go module、`cmd/roomusic`、配置加载、数据库连接和 HTTP 服务入口。
- 建立 React/TypeScript 前端、开发 server、API 代理和生产构建脚本。
- 建立 Go 静态资源托管接口和前端 fallback 路由。
- 补充只需 PostgreSQL 的本地运行文档；Redis/Meilisearch 不参与启动检查。

验证：Go format/build/test、前端 install/lint/typecheck/build、Compose PostgreSQL 启动和服务健康检查。

### 2. 数据库迁移与基础领域模型

- 设计并实现 setup、users、sessions、library_roots、scan_runs、scan diagnostics 和最小 Release Graph 表。
- 为源观察、Track 来源、ReleaseGroup/Release/Medium/Track 生命周期和 `missing` 状态建立约束。
- 为 artwork 元数据和 data 目录 storage key 建表，不把图片二进制放进 PostgreSQL。
- 为 Change Set、Operation Journal、operation events、resource revision 和 idempotency 建表。
- 迁移必须可重复执行；启动迁移失败时服务不得宣称 ready。

验证：迁移空库、重复迁移、回滚/失败行为、数据库约束、repository 集成测试。

### 3. 初始化、会话和用户权限

- 实现一次性 setup admin，确保 setup 状态和管理员创建原子化。
- 实现 opaque Cookie session、token hash、过期、登出、撤销和禁用用户即时生效。
- 实现 admin/user 中间件、同源/Origin 或等价 CSRF 检查、稳定错误响应。
- 实现管理员创建/禁用普通用户和撤销会话。

验证：setup 重复请求、登录失败、Cookie 属性、token 不落库、过期/撤销/禁用、普通用户越权和 CSRF 测试。

### 4. 路径策略与目录变更闭环

- 实现 `allowed_library_roots` 配置解析、规范化路径、realpath 和同根符号链接检查。
- 实现 library root 新增、停用、恢复 REST API。
- 将这些操作接入 Change Set、Operation Journal、before/after、revision、幂等键和明确逆操作。
- 对旧 revision 和同 key 不同 payload 返回稳定冲突；同 key 同 payload 返回原结果。

验证：路径穿越、白名单外目录、符号链接越界、目录不存在、重复请求、revision 冲突、恢复成功和恢复失败测试。

### 5. 只读扫描器与 Release Graph

- 实现全局串行进程内调度和重复触发复用当前 scan run。
- 实现目录遍历：默认不跟随目录符号链接；合法同根文件符号链接可读取；异常只记录诊断。
- 实现 `FLAC`、`MP3`、`OGG`、`Opus`、`WAV` 标签和基础文件事实解析。
- 实现常见 CUE 虚拟 Track 和多碟 Medium 识别。
- 一个发行目录默认形成独立 ReleaseGroup/Release；不做跨目录弱启发式归组。
- 写入 source observation、字段来源、scan run 统计和 unsupported/parse failure 诊断。
- 同 root 同规范化相对路径保持 Track ID；rename/move 不自动继承身份。
- 只有完整成功扫描执行负向对账并标记 `missing`；失败、取消、离线、权限错误或不完整扫描不执行。

验证：格式 fixture、CUE fixture、多碟 fixture、重复扫描、软缺失、恢复、rename/move、符号链接、失败扫描隔离和扫描中重复触发测试。

### 6. Release-level 封面

- 扫描同目录明确命名的 folder artwork 和音频内嵌封面。
- 固化并测试默认封面优先级。
- 受控复制到 data 目录，按 hash key 幂等保存元数据。
- 提供鉴权 artwork resource API，隐藏原始路径并返回正确 MIME/缓存头。

验证：目录图片优先级、内嵌封面 fallback、重复 hash、损坏图片、只读音乐目录、权限和路径泄露测试。

### 7. 读模型 REST API 与前端页面

- 实现 setup/auth、library roots、scans、releases、release detail、search、artwork 和 admin users/change history API。
- 保持稳定分页、错误 code、request ID 和角色可见性。
- 实现 setup/login、library list、release detail、scan status/diagnostics、admin roots/users/change history 页面。
- 生产构建由 Go 托管；前端不把 session token 写入 localStorage。

验证：handler integration tests、权限矩阵、前端 typecheck/lint、核心页面交互和生产静态资源 smoke test。

### 8. 文档与质量门禁收敛

- 更新 README、环境变量示例、Core 0 API 和运行说明。
- 记录明确的支持格式、软缺失、身份和 ReleaseGroup 限制。
- 固化最小质量命令并在干净环境运行。
- 运行 cross-layer review，确认 DTO、数据库状态、日志字段和前端状态没有漂移。

## 风险与回退点

- 若音频解析库无法覆盖首批格式，先保留 parser interface 和明确 unsupported 诊断，不扩大格式承诺。
- 若 PostgreSQL 查询在样本库上不足，记录基准结果并把 Meilisearch 作为后续任务，不在 Core 0 中临时混入。
- 若扫描模型出现身份或分组歧义，选择保留独立实体和诊断，不自动合并。
- 若前端生产托管影响开发体验，保留 dev proxy，禁止因此拆分生产服务。
- 每个阶段的数据库迁移、鉴权、扫描和静态资源改动都应保存可独立回退的 Git 快照；当前仓库仍需由用户决定何时创建提交。

## 预期核心验证命令

```bash
gofmt -w .
go test ./...
go vet ./...
npm run lint
npm run typecheck
npm run test
npm run build
docker compose config --quiet
```

实际脚本名称以实现阶段建立的 `package.json` 和 `mise` 任务为准；命令缺失或环境不可用时必须记录原因和补验方式。
