# Spec Bootstrap Design

## Approach

Use the existing backend/frontend topic split, but rewrite each document around
ROOMusic's ownership boundaries instead of generic framework advice. Because the
repository has no application source yet, every rule will be classified as one of:

1. **Product-history fact**: product intent and long-term direction evidenced by
   `../ROOMusic-V0/.planning/PROJECT.md`, `REQUIREMENTS.md`, `ROADMAP.md`, and
   `references/roon-phase1-alignment.md`.
2. **Repository fact**: current development environment evidenced by `README.md`,
   `compose.yaml`, `.env.example`, or the architecture canvas.
3. **Core 0 contract**: explicitly required by
   `.trellis/tasks/08-31-roomusic-core-0-rebuild/prd.md`.
4. **Selection hook**: a rule about where a future implementation decision must
   be recorded, without naming an unchosen library.

The V0 product intent is authoritative for the goal and differentiation. The
current Core 0 PRD is authoritative for what is implemented now when it narrows a
V0 technical choice. A retired V0 scaffold, dependency, or phase is never
implicitly brought back merely because it appears in historical planning.

Add three shared documents that apply before either layer-specific index:

- `product-goals.md`: user value, Core 0 outcome, long-term direction, permanent
  invariants, and phase boundaries.
- `modular-design.md`: capability ownership, dependency direction, contracts,
  cohesion/coupling tests, and cycle prevention.
- `engineering-principles.md`: smallest complete changes, focused functions,
  reuse-first development, abstraction thresholds, and proportional tests.

The existing reuse and cross-layer guides will be shortened and rewritten around
ROOMusic examples and contracts rather than Trellis runtime/template examples.

## Layer Boundaries

### Backend

`transport -> application service -> domain/ports -> adapters` is the default
dependency direction. A capability module owns its use cases, validation after
boundary decoding, and port interfaces. Repositories, scanners, and external
providers implement ports and do not define domain policy. PostgreSQL transactions,
session authorization, path validation, Change Set/Operation Journal, and recovery
remain backend authority.

### Frontend

`route/page -> feature components/hooks -> typed API client -> REST boundary` is
the default direction. Features own their view state and mapping from API DTOs to
display models. Shared components remain presentational and do not import domain
repositories or issue arbitrary requests. Server state, URL state, and ephemeral
UI state are kept separate.

## Cohesion/Coupling Rules

- One module owns one policy and its tests; a module may consume another module's
  published contract but not its private tables, components, or helpers.
- Interfaces belong at the consuming boundary (the port owner), with adapters
  implementing them. Avoid a global service locator and avoid interfaces that
  expose unrelated capabilities.
- Cross-layer payloads have one decoder/type owner. Consumers do not repeatedly
  cast `unknown` fields or reimplement validation.
- Shared code must be genuinely policy-free and stable; otherwise keep it local
  to the owning module.
- Cycles between modules are forbidden. If two modules need each other, extract a
  smaller domain contract or introduce an application orchestration boundary.

## Small-Change and Function Rules

- "Minimal change" means the smallest complete behavior change at the true owner,
  including necessary tests and contract updates. It does not justify patching the
  wrong layer, duplicating policy, or omitting error handling.
- Avoid unrelated refactors. When a local refactor is required to make the change
  safe, isolate it and demonstrate behavior preservation before adding behavior.
- "Small function" is a cohesion rule, not a line-count target. Split functions
  that mix policy, I/O, decoding, transaction control, or unrelated branches;
  avoid tiny wrappers that only scatter a readable flow.
- Prefer pure parsing and transformation functions. Keep filesystem, database,
  clock, network, and process side effects explicit at ports/adapters.
- Search for an existing owner before writing a helper. Reuse when meaning and
  lifecycle match; do not merge superficially similar rules from different
  domains into one generic helper.
- Promote code to shared scope only when it has a stable name, stable contract,
  multiple real consumers, and no feature-specific policy.

## Compatibility and Deferred Work

The specs will describe REST and PostgreSQL as Core 0 defaults. Redis,
Meilisearch, dynamic plugin protocols, Agent runtime, WASM, playback, and file
mutation are documented as future adapters only. This keeps module seams useful
without forcing premature infrastructure or runtime loading complexity.

Long-term guidance is directional, not a promise that all future adapters must be
built. New infrastructure is added only when a concrete product capability needs
it and must preserve the fixed authority boundary.

## Product Goal Source Hierarchy

When documents appear to disagree, resolve them in this order:

1. User-approved current task requirements and the current Core 0 PRD for scope
   and acceptance behavior.
2. V0 `PROJECT.md` / `REQUIREMENTS.md` / `ROADMAP.md` for product goal, core
   value, target users, and long-term direction.
3. V0 implementation conventions and historical phase plans for reusable lessons
   only; they are not current requirements.
4. Current repository README/Compose files for environment facts.

For example, V0's GraphQL and Redis choices describe a later implementation path;
Core 0's REST/PostgreSQL-only decision wins for current work while the product
goal of rich Release Graph queries remains intact.

## Verification

After writing the specs:

- scan all documents for generated placeholders;
- verify every index link and listed file;
- search for contradictory Core 0 claims (`GraphQL`, required Redis/Meilisearch,
  unrestricted file writes, or implemented Agent runtime);
- inspect the diff for accidental edits outside `.trellis/spec/` and the task
  planning artifacts.
