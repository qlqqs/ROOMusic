# Frontend Quality Guidelines

## Status

当前前端 package 位于 `frontend/`，使用 npm + `package-lock.json`、Vite、
TypeScript、ESLint 和 Vitest。Node 版本由 `.mise.toml` 管理；安装使用
`npm ci`。仓库目前没有 formatter 或自动化 accessibility runner，相关门禁仍
需在后续工具选型后补充。

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

Run all gates affected by a change. A production build must be consumable by the
Go server and must not require a standalone Node process. The exact package
manager, formatter, linter, bundler, test runner, and browser automation tool are
selection hooks; do not name unchosen dependencies in feature work.

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
