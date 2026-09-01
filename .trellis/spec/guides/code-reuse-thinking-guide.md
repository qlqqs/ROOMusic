# Code Reuse Thinking Guide

## Purpose

Reuse should preserve one semantic owner, not maximize the number of callers per
helper. ROOMusic favors capability-local code until a stable abstraction has
multiple real consumers and no feature-specific policy.

## Search First

Before adding a function, type, component, constant, endpoint, or query, search
for its vocabulary and behavior:

```bash
rg -n "revision_conflict|expected_revision" .
rg -n "allowed_library_roots|realpath|symlink" .
rg -n "ReleaseGroup|Medium|Track" .
```

Also search tests and specifications. Similar naming can reveal the existing
owner; similar syntax alone does not prove shared semantics.

## Reuse Decision

| Situation | Action |
| --- | --- |
| Same policy and same lifecycle | Extend the existing owner and its tests |
| Same wire payload consumed in several places | Create one boundary decoder and typed projection |
| Same domain-neutral mechanism with real consumers | Extract a narrow shared package/component |
| Similar code with different authority or change cadence | Keep it local |
| Only a hypothetical future consumer | Do not abstract yet |

Constants follow ownership too. Supported scan formats belong to the scanner
contract; HTTP status mappings belong to the transport error mapper; UI labels
belong to presentation or localization. A single global constants package would
couple unrelated changes.

## ROOMusic Examples

Good reuse:

- One server-side path containment implementation used by root registration and
  scan startup, with filesystem-adapter tests for traversal and symlink escape.
- One REST error decoder used by frontend features, returning a typed
  `ApiError` rather than repeated casts from `unknown`.
- One presentational scan-status component reused where its props and interaction
  are identical, while feature hooks continue to own fetching.

Keep separate:

- Setup password policy and ordinary display-name validation.
- Scan-run state transitions and Operation Journal state transitions.
- Release title normalization and a search-input display formatter.

These may look similar but have different invariants and reasons to change.

## Extraction Threshold

Before moving code to shared scope, verify all of the following:

- The abstraction has a precise name that does not contain `misc`, `common`, or
  `helpers`.
- At least two implemented consumers need the same semantics.
- The new API is smaller and easier to test than the duplicated behavior.
- Ownership of errors, configuration, and lifecycle remains clear.
- Moving it does not create a module cycle or expose private types.

Prefer duplication of a trivial representation over a misleading abstraction.
Prefer a stable domain value type over duplicated interpretation of raw strings.

## Anti-Patterns

- Copying permission checks into handlers and frontend route guards.
- Letting multiple modules canonicalize library paths differently.
- Creating a generic repository base that leaks SQL or transactions everywhere.
- Creating a mega-component with flags for unrelated features because two pages
  share markup.
- Restoring a V0 helper without proving its assumptions match the current
  REST/PostgreSQL-only Core 0 contract.

## Completion Check

- [ ] Searched for the policy and all current consumers.
- [ ] Reused or extended the semantic owner when appropriate.
- [ ] Kept feature policy out of shared code.
- [ ] Updated every typed contract consumer.
- [ ] Added tests where ownership or behavior moved.
- [ ] Did not add infrastructure for a future-only consumer.

See [Modular Design](./modular-design.md) and the current
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
