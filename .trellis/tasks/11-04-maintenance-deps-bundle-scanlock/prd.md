# 维护：依赖漏洞、前端分包、扫描锁测试干扰

## 目标

处理 Core 1 收口记录里的三项遗留（见 `archive/2026-11/09-27-classical-core-1/prd.md` 的“Core 1 收口记录”）。三项互不依赖，各自一个提交；完成后连同 Core 1 的提交一起推送（用户 2026-11-04 授权）。

## A. 扫描锁在并行测试中互相干扰（后端）

**现状与原因**：集成测试按 schema 隔离（`postgrestest.NewSchema` 固定 `search_path`），扫描互斥却是整库共享的 session advisory lock，键固定为 `0x5343414e`（`internal/library/scans.go`）。`go test` 并行跑 `internal/library` 与 `internal/app` 时，一个包的扫描持锁，另一个包的 `StartScan` 拿不到锁，在自己的 schema 里又查不到活动扫描，于是返回 `503 scan_coordination_unavailable`。生产只有一个 schema，不受影响。

**关键决定**：锁的范围收窄到当前 schema，与它保护的 `scan_runs` 表一致。改用两参数形式 `pg_try_advisory_lock(0x5343414e::int, hashtext(current_schema()))`（类别仍是 ASCII 的 `SCAN`；与操作锁的类别 `OPER`、迁移锁的单 bigint 键不冲突）。取得锁时记下实际的键，解锁用同一个键，不重新计算。

**验收**：
- 同一 schema 里两条独立连接竞争时仍只有一个成功，失败的一方复用活动扫描；不同 schema 互不阻塞（新增集成测试）。
- 原有扫描协调测试（恢复、`MaxOpenConns=1` 快速失败、unlock 后可重新获取）照常通过。
- 修复前尽量复现一次失败（记录命令与次数；复现不了也照实写）；修复后在隔离测试库上用 `go test -count=5 ./internal/library/... ./internal/app/...` 并行跑，没有失败，集成测试没有 SKIP。
- 同步 `.trellis/spec/backend/scan-coordination-contracts.md` 第 2、6 节的键描述。

## B. npm audit 漏洞（前端依赖）

**现状**：`npm audit` 报 17 个（13 moderate、4 high）：vite 8.2.2（high）、vitest/@vitest/mocker 4.1.11、undici、zod、source-map-js、seroval（经 TanStack Router 进入运行时代码），以及 eslint → minimatch 一串（上游暂无修复）。

**关键决定**：
- 只做不跨主版本的升级：直接依赖改成已修复的补丁或次版本，继续写确切版本号（项目不用 `^`）；传递依赖靠刷新锁文件。不用 `npm audit fix --force`。
- eslint → minimatch 一串：先试 `overrides`，前提是 lint 结论不变；不行就保留，并在结果里写明只影响开发期的 lint、不进入产物。
- Node 仍是 24.22.1。

**验收**：没有 high；剩下的每一项都写明原因。`npm ci`、lint、类型检查、构建通过；Vitest、Playwright 结论不变。

## C. 主 JS 包超过 500 kB（前端构建）

**现状**：单个 `index-*.js` 559.57 kB，没有分包，构建会报体积警告。

**关键决定**：用 Vite 配置把第三方库（react/react-dom、TanStack、lucide 等）拆成独立的块，应用代码不改，不引入路由懒加载（懒加载会改变页面加载行为，不在本次范围）。不能只调高 `chunkSizeWarningLimit`。拆出的第三方块在应用代码更新时文件名不变，浏览器可以继续用缓存。

**验收**：构建没有体积警告，每个 JS 块小于 500 kB；`backend/cmd/roomusic/web` 已重新生成并与构建一致；Vitest、Playwright 与真实服务用例通过；`cmd/roomusic` 的 Go 测试（内嵌资源与缓存头）通过。

## 收尾

- 在导出的提交树上跑一次：后端改动涉及的包、前端 lint/类型检查/Vitest/Playwright、`scripts/playback-e2e.sh` 真实服务用例。
- 界面不变，不需要截图复核。
- 推送前做只读检查（远端头是本地祖先、没有疑似密钥、新增大文件），然后 `git push origin main`。
