# Engineering Principles

## Smallest Complete Change

Make the smallest complete behavior change at the true owner. Complete includes
the contract, implementation, error behavior, observability where relevant, and
tests needed to prove the important path. It never means patching the nearest
UI or SQL layer while leaving the owning policy inconsistent.

Avoid unrelated cleanup, dependency replacement, or speculative future design.
When a local refactor is necessary, separate the behavior-preserving step from
the behavior change and verify both.

## Focused Functions

- Give each function one coherent responsibility and one abstraction level.
- Prefer explicit inputs and outputs. Hidden globals, implicit current users,
  ambient transactions, and package-level mutable state increase coupling.
- Separate pure decisions from effects. A parser can return observations; an
  application use case decides how to persist them inside a transaction.
- Return typed, classified errors instead of logging and continuing silently.
- Split mixed policy/I/O/decoding/transaction functions, but avoid tiny wrappers
  that only make a flow harder to trace.

Example: `reconcileMissing(observed, previous, scanOutcome)` can be a pure
domain decision. Walking a directory, loading previous rows, and committing the
decision remain explicit surrounding steps.

## Search And Reuse Before Adding

Use `rg` to find the policy name, DTO field, error code, endpoint, table, and
similar test before creating anything. Reuse the existing owner when meaning,
lifecycle, and authority match.

Extract shared code only when it has:

- a stable domain-neutral name;
- multiple real consumers;
- the same semantics and change cadence;
- a narrower contract than the duplicated code it replaces; and
- tests at the new owner.

Two blocks that merely look similar can represent different policies. Password
input validation and a library path label may both check length, but combining
them into `ValidateString` loses ownership rather than creating useful reuse.

## Evidence And Decisions

仓库已有 Go 后端、迁移与测试；2026-09-27 的前端处于源码删除后的重建阶段，不能把
这两种现状混为“项目没有源码”。依据真实文件核对现状，再决定复用、恢复或新增。
新 router、SQL/state/validation/styling 库或测试工具必须解决当前需求、遵守模块边界，
并在获批任务/ADR 中记录选择及验证命令。草案、原型按钮和旧任务授权均不等于本轮
已经采纳或实现。

Use the source hierarchy in [Product Goals](./product-goals.md). V0 code can
teach a domain lesson, but copying an old scaffold or dependency requires a new
decision based on Core 0.

## Proportional Verification

Match tests to risk and blast radius:

- Pure parser or rule: table-driven unit tests with representative and malformed
  inputs.
- Repository or transaction: PostgreSQL integration test, including conflict
  and rollback behavior.
- REST contract: handler/transport test for status, safe error body, auth, and
  request correlation.
- Cross-layer workflow: one focused end-to-end or integration path plus layer
  tests at the policy owners.
- Frontend behavior: typecheck, component/interaction tests, and accessibility
  checks proportional to the change.

Do not use snapshots or mocks as a substitute for asserting business outcomes.
Do not chase arbitrary coverage percentages while missing path escape, session
revocation, incomplete-scan reconciliation, or revision conflict cases.

## Review Questions

1. Is this behavior at its true capability owner?
2. Did the change add a wider interface or new global state than necessary?
3. Is untrusted data decoded once and policy enforced again at the backend
   authority boundary?
4. Are file, database, clock, network, and process effects visible?
5. Does the failure path preserve data, identity, and recovery guarantees?
6. Is future infrastructure still optional?

An anti-pattern is calling a change "minimal" because it omits the transaction,
error contract, security check, or test required to make the behavior correct.
