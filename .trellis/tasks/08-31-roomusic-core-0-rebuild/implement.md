# ROOMusic Core 0 集成交付计划

## 执行原则

本任务是版本级父任务，不直接启动或修改产品代码。产品实现由子任务按依赖顺序完成；每个子任务必须独立规划、验证和归档。父任务只维护跨子任务合同、检查阶段间兼容性并执行最终集成验收。

## 子任务执行顺序

### 1. 首个可浏览纵向切片

- 子任务：`09-01-core-0-first-browse-slice`。
- 建立从单管理员初始化、受限目录注册、FLAC/MP3 扫描到 Release 详情浏览的最小端到端闭环。
- 首次确认工程结构、REST 错误、迁移、会话、路径安全、scan run 和最小图谱合同。

集成门：干净环境仅依赖 PostgreSQL 即可完成 setup、login、注册目录、扫描、列表和详情浏览。

### 2. 基础搜索

- 子任务：`09-01-core-0-basic-search`。
- 在已稳定的读模型上实现 PostgreSQL 搜索，不引入 Meilisearch。

集成门：搜索结果只能投影权威图谱，不能成为写模型。

### 3. 格式与 CUE 扩展

- 子任务：`09-01-core-0-format-cue-expansion`。
- 增加 OGG、Opus、WAV、常见 CUE 和边界样本，不改变既有 FLAC/MP3 身份。

集成门：扩展格式与 CUE 重扫不破坏既有来源、Track ID 和 missing 规则。

### 4. Release 封面

- 子任务：`09-01-core-0-release-artwork`。
- 增加目录图和内嵌图发现、受控 data 存储与鉴权资源接口。

集成门：封面失败不阻塞音频扫描，且不泄露原始路径。

### 5. 私有多用户

- 子任务：`09-01-core-0-private-multi-user`。
- 在单管理员闭环上增加普通用户生命周期和只读权限矩阵。

集成门：所有后端管理端点独立授权，前端角色显示不是权限边界。

### 6. 目录操作治理

- 子任务：`09-01-core-0-root-operation-governance`。
- 基于目录新增、停用和恢复的具体用例实现 revision、幂等、审计事件和恢复语义。
- 只有出现第二类真实持久化操作后，才评估抽取通用 Change Set 或 Reversible Executor。

集成门：冲突必须 fail closed；重复请求不产生重复副作用；恢复不覆盖更新后的 revision。

### 7. 父任务最终集成审查

- 汇总子任务验证结果，运行干净环境端到端验收。
- 检查跨子任务迁移、DTO、身份、来源、诊断、日志和前端状态是否漂移。
- 确认 Redis、Meilisearch、GraphQL、Agent runtime 和插件基础设施没有成为隐式依赖。
- 父任务通过最终验收后再归档。

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
