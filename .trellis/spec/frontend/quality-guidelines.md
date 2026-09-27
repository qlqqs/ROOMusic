# Frontend Quality Guidelines

## Status

前端 package 位于 `frontend/`，保留 npm + `package-lock.json`、Vite、TypeScript、
ESLint 和 Vitest 配置，但 2026-09-27 基线没有 `src/`。脚本存在、类型检查通过或
Vitest 的 `--passWithNoTests` 退出成功，均不能证明客户端可用。CI 在入口缺失时只检查
内嵌占位页，不运行真实 production build 或资产漂移检查。

M0 只做文档检查。M1 恢复源码时须执行真实构建与相关行为测试，恢复构建/内嵌资产门禁，
不能保留重置分支作为功能完成依据。Node 版本由 `.mise.toml` 管理；安装使用 `npm ci`。
仓库没有 formatter 或自动化 accessibility runner；浏览器测试、可访问性工具若需新增，
须经具体子任务选型，不声称 Playwright 已配置。

The current [README](../../../README.md) establishes Node.js 24.16.0 through the
repository development toolchain. Do not rely on an undocumented global version.

## Required Practices

- Organize behavior by feature and expose narrow typed contracts.
- Decode all REST data at API boundaries and keep TypeScript strict.
- Separate server, URL, local, form, and session state.
- Keep components accessible and focused on rendering/intent.
- Keep side effects in feature hooks/API clients with cancellation and cleanup.
- Handle loading, empty, stale, error, permission, conflict, canceled, and
  incomplete states that apply to the workflow.
- Keep backend authority visible: the UI handles denied responses and never
  substitutes a role check for security.
- Preserve same-origin opaque-cookie authentication and read-only library
  assumptions.
- Make changes at the owning feature and avoid unrelated visual or dependency
  refactors.

## Forbidden Patterns

- `any`, ignored type errors, raw JSON casts, or unchecked non-null assertions.
- Direct `fetch` calls scattered through components.
- Browser-readable bearer tokens in localStorage, sessionStorage, IndexedDB,
  URLs, or JavaScript state.
- A global store containing copies of server resources.
- Business rules based only on hidden/disabled controls.
- Raw absolute paths, secrets, backend stack traces, or SQL errors in UI.
- Unbounded rendering of the whole music library.
- Snapshot-only tests for meaningful workflows.
- New UI, state, or styling libraries added without a concrete need and recorded
  decision.
- GraphQL, Agent runtime, playback, plugin marketplace, Meilisearch-specific, or
  offline PWA code introduced as Core 0 scaffolding.

## Test Requirements

| Unit | Minimum evidence |
| --- | --- |
| Runtime decoder | valid, missing, malformed, unknown enum, and safe error fallback |
| Pure view projection | Medium/Track ordering, provenance labels, empty values |
| Component | loading/empty/error/ready states, keyboard interaction, accessible names |
| Feature hook | query success/error, cancellation/stale response, mutation conflict, polling stop |
| Auth flow | setup closure as applicable, login, logout, expired/revoked session, 401/403 handling |
| Library admin | allowed input UX, pending state, revision conflict, retry/recovery presentation |
| Catalog/search | Release hierarchy, pagination/filter URL state, no raw path disclosure |
| Cross-layer smoke path | login -> browse -> detail/search; admin root/scan path separately |

Use the selected browser/component runner for behavior. Tests assert outcomes and
accessible interaction rather than implementation calls or incidental markup.
Backend integration tests remain responsible for real authorization, path
containment, transactions, and session cookie flags; frontend tests prove that
the UI sends the contract and handles results correctly.

## Accessibility And Responsive Gate

Run automated accessibility checks on representative screens and manually verify
keyboard focus, dialogs, form errors, landmarks/headings, status announcements,
and contrast. Test narrow mobile and desktop widths. Text, controls, tables, and
cover media must not overlap or resize the surrounding layout unexpectedly.

Automated accessibility output is a signal, not proof. Critical workflows require
keyboard and screen-reader-semantic review.

## 当前脚本

当前 package 已提供以下稳定脚本：

```text
lint
typecheck
test
build
```

按改动选择最小相关测试集；仅文档变更检查链接、任务上下文和 diff，不为了“全绿”运行
无关全量测试。实现阶段必须记录实际命令、跳过原因和尚缺证据。

运行改动影响到的适用门禁。恢复后的 production build 必须能由 Go 服务提供，不依赖
独立 Node 生产进程。npm、ESLint、Vite 和 Vitest 已有配置，不再列为尚未选择的工具；
formatter、浏览器自动化与无障碍 runner 若需新增，仍须单独选型，不凭规范示例引入依赖。

## Review Checklist

- [ ] Change belongs to one feature or an explicit app/shared owner.
- [ ] Shared extraction has multiple real policy-free consumers.
- [ ] REST input/output is runtime-decoded and typed.
- [ ] State has one source of truth and query identity is complete.
- [ ] Mutation handles pending, success, classified failure, and conflict.
- [ ] Backend authorization remains required and denied responses render safely.
- [ ] No token, secret, or server path reaches browser storage or display.
- [ ] Semantic HTML, keyboard behavior, labels, and focus are verified.
- [ ] Relevant unit/component/cross-layer tests pass.
- [ ] Core 0 production still uses the Go-hosted same-origin build.
- [ ] No future-only infrastructure was made mandatory.

## Anti-Example

A Release page that fetches raw JSON in `useEffect`, casts it, copies it into a
global store, hides an admin button based on a local role string, and renders
every Track at once may compile but violates ownership, type, security, state,
and scale contracts.

See [Engineering Principles](../guides/engineering-principles.md) and the
[Core 0 acceptance criteria](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
