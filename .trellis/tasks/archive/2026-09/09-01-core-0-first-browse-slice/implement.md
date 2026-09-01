# Core 0 首个可浏览纵向切片实现计划

## 执行原则

按可验证阶段实现，每阶段通过局部门禁后再继续。不得创建未被当前 PRD 使用的 search、artwork、operations、Agent、plugin 或 profile 目录与接口。不得直接复制 V0 schema 或框架选型。

## 1. 选型记录与工程骨架

- 确认并记录 Go module、PostgreSQL driver、迁移工具、密码 hash 方案。
- 确认并记录前端 package manager、build tool、router、REST 数据机制、runtime decoder、样式与测试工具。
- 创建最小 backend/frontend 目录、统一开发命令和 PostgreSQL-only 配置。
- 实现 config validation、JSON logger、request ID、health/readiness 和迁移入口。
- 建立前端 dev proxy 与 Go 生产静态资源托管 smoke path。

验证：Go build/test/vet，前端 lint/typecheck/test/build，`docker compose config --quiet`，PostgreSQL 存活/缺失和迁移失败 readiness 测试。

回退点：只包含可启动骨架和已记录选型，不包含业务表。

## 2. Identity 纵向切片

- 建立 setup、users、sessions 迁移与约束。
- 实现一次性 setup use case、密码 hash、opaque session token/hash、expiry/revocation。
- 实现 setup/auth REST、Cookie 和 CSRF/Origin middleware、统一安全错误。
- 实现前端 setup/login/session bootstrap/logout 及 loading/error/expired 状态。

验证：重复 setup、并发 setup、错误密码、token 不落库、Cookie 属性、logout、expiry、revocation、Origin 拒绝和前端 auth 流程。

回退点：identity 能独立通过 API 和 UI smoke test。

## 3. Library root 安全边界

- 建立 library root 最小迁移，只支持新增和列表。
- 实现 allowed roots 配置、规范化、realpath containment 与安全错误。
- 明确目录 symlink 拒绝和文件 symlink 同根规则。
- 实现 REST 与前端目录注册/list 状态，不提供任意服务器目录枚举。

验证：允许根本身/子目录、`..` 穿越、前缀碰撞、越界 symlink、断链、目录不存在、重复注册和路径泄露检查。

回退点：尚未读取音频，目录配置可独立验证。

## 4. Scan run 与 filesystem discovery

- 建立 scan run、root state 和 bounded diagnostic 迁移。
- 实现全局单运行协调、重复触发复用 ID、root 稳定排序和启动恢复时 `running -> incomplete`。
- 实现不跟随目录 symlink 的遍历和同根 file symlink 验证。
- 实现取消传播、状态机、聚合日志和安全诊断。

验证：并发触发、多个 root 顺序、取消、权限错误、离线/断链、进程遗留状态和诊断上限。

回退点：scanner 可仅发现文件事实，不写 catalog。

## 5. FLAC/MP3 parser 与最小 Catalog

- 选择并封装 parser adapter，建立小型合法 fixture；不让第三方库类型越过 parser 边界。
- 建立最小 Release Graph、track source 与 field observation 迁移。
- 实现发行目录/常规多碟规则、FLAC/MP3 observations 和确定性 filename fallback。
- 通过 catalog 窄合同批量保存；实现同路径身份和完整成功才 missing。
- unsupported 和 parse failure 保存诊断。

验证：FLAC/MP3、缺标签、损坏文件、unsupported、多碟、重复扫描、rename/move、missing、恢复、失败/取消/incomplete 禁止对账和数据库约束测试。

回退点：保留扫描历史；catalog 迁移和写入可独立回退或前向修复。

## 6. Release REST 与 Web 浏览

- 实现稳定分页的 Release 列表和一次有界装配的 Release/Medium/Track 详情。
- 定义 runtime-decodable DTO，不返回原始数据库行或完整服务器路径。
- 实现前端 library/catalog 页面、scan 有界轮询、Release 列表/详情、来源标签和 loading/empty/error/incomplete 状态。
- 验证生产前端 fallback 不吞掉 `/api/v1` 错误或资源响应。

验证：handler integration、DTO decoder、Medium/Track 顺序、分页、401/403/404、安全字段、键盘/可访问状态、窄屏与生产静态资源 smoke test。

## 7. 文档与最终质量门

- 更新 README、环境变量示例、支持格式、allowed roots、只读挂载、启动和质量命令。
- 更新 backend/frontend spec 中首次选定的工具和精确命令。
- 运行 cross-layer 数据流审查：setup/session、root、scan status、diagnostic、Release DTO 与 UI 状态。
- 确认 Redis、Meilisearch、GraphQL、search、artwork、普通用户、operations 和 Agent/plugin 代码未成为依赖。

预期门禁，以 scaffold 固化的精确脚本为准：

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./...
npm run format:check
npm run lint
npm run typecheck
npm run test
npm run build
docker compose config --quiet
```

数据库与 filesystem 安全测试使用 disposable PostgreSQL 18 和临时目录。若环境无法运行某一门禁，必须记录原因和明确补验方式，不能把缺失工具当作通过。
