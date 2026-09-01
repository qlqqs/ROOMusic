# Product Goals And Phase Boundaries

## Product Identity

ROOMusic is a self-hosted, AI-native music library steward for individuals,
families, and small private groups with large NAS collections. Its core value is
release-aware knowledge management: music is modeled as
`ReleaseGroup -> Release -> Medium -> Track`, not as a folder browser with a chat
feature attached.

The long-term product should let users inspect, search, play, organize, and
govern their library with evidence-backed assistance. An AI capability may
propose or execute only through fixed identity, permission, confirmation,
audit, transaction, and recovery boundaries. It never becomes the source of
authority merely because it is called an Agent or plugin.

This repository-local guide preserves the relevant conclusions from the V0
product definition, requirements, roadmap, and Roon phase-one alignment
research. The original V0 planning workspace is historical input, not a
required sibling checkout, current source code, or a mandate to restore the V0
runtime.

## Long-Term Steward Model

The product has one user-facing Agent concept: **Music Steward**. Assistant,
Steward, and Operator are execution modes with different approval paths; they
are not three independent Agents and must not grow three separate authority
implementations.

| Mode | Approval path | Intended use | Core authority |
| --- | --- | --- | --- |
| `assistant` | The current user explicitly approves the proposed operation | Explain the library, prepare plans, and perform safe user-scoped actions | Cannot approve its own dangerous proposal |
| `steward` | An independent Review Subagent returns a structured approval, subject to policy | Autonomous evidence-backed organization and bounded maintenance | The main Agent cannot act as its own reviewer |
| `operator` | No user confirmation or AI review step | Explicit administrator-controlled direct execution | Still limited to registered tools and backend validation |

The approval path is the product distinction. Execution is shared: every mode
submits a typed command to the backend authority, which checks identity,
capability, target scope, physical safety, revision, and idempotency before any
side effect. Operator mode skips approval, not validation, logging, or recovery
metadata. "Direct execution" never means unrestricted shell, arbitrary SQL, or
unbounded host filesystem access.

The Review Subagent is a separate reviewer role, not a second general-purpose
Assistant. It receives a bounded operation proposal and safe evidence summary,
then returns `approved`, `rejected`, or `needs_human_confirmation` with reasons,
risk, scope, and recovery requirements. Its output is an approval input, never
the authority to write data. The backend re-evaluates the proposal and policy;
an Agent or model cannot manufacture approval by placing an approval field in
its own payload.

The deterministic library pipeline remains separate from Music Steward. Parsing,
identity assignment, conservative Release grouping, and source observation are
program-owned behavior. AI is used for unresolved interpretation and user
assistance, not as a replacement for deterministic rules that are already
reliable.

## Long-Term Change Management

ROOMusic does not use runtime logs as a substitute for Git. Logs are ephemeral
operational evidence; durable business recovery uses four separate concepts:

- **Change Set**: the intent and complete scope of one business operation.
- **Operation Journal**: immutable lifecycle events, actor, mode, tool, status,
  and correlation identifiers.
- **Checkpoint**: the minimum before-state, revision, manifest, or hash needed
  to recover the affected resources.
- **Reversible Executor**: typed inverse actions that can restore a supported
  operation, rather than an Agent guessing how to undo natural-language history.

Core 0 uses this model for directory configuration changes first. It does not
implement full Event Sourcing, Git-like branches, or a full file recovery
system. Future file and tag mutation must add an explicit recovery strategy
before the capability is enabled. A physical purge may be irreversible, but
the operation must state that fact instead of claiming rollback support.

## Core 0 Outcome

Core 0 is the smallest useful local-library loop:

1. Initialize one administrator and authenticate private users with opaque,
   server-side sessions carried by secure cookie settings.
2. Register only a configured, allowed library root or one of its descendants.
3. Scan music read-only and parse the initial supported formats: FLAC, MP3, OGG,
   Opus, WAV, and common CUE sheets.
4. Build a conservative, evidence-linked Release Graph and expose browse,
   detail, scan status, and basic search through versioned REST.
5. Serve the production web build and REST API from the Go application, using
   PostgreSQL as the only required business authority.
6. Route user-initiated persistent management changes through Change Set and
   Operation Journal contracts; directory add, disable, and restore are the
   first reversible example.

The authoritative acceptance behavior is in the current
[Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).

## Permanent Invariants

These rules survive later phases and adapter changes:

- The backend owns authentication, authorization, path safety, transactions,
  revision conflicts, idempotency, and recovery semantics.
- Music roots are read-only by default. Core 0 never writes tags, renames,
  moves, or deletes source music files.
- Source evidence remains inspectable. Paths describe a local observation; they
  are not the business identity of a Release.
- Versions remain distinct, and multi-disc releases use multiple Medium
  entities. Weak title, artist, year, or folder similarity cannot perform an
  authoritative cross-directory merge.
- A failed, cancelled, offline, permission-denied, or incomplete scan cannot
  mark previously observed sources missing. Only a complete successful scan can
  perform negative reconciliation.
- Plugins, providers, Agents, and execution adapters use published capabilities;
  they cannot bypass core authority or mutate private module storage.

The inherited Roon alignment conclusion is that local files become an object
graph, versions remain distinct, and tags and source evidence remain
inspectable. Those conclusions are captured by the Core 0 outcome and permanent
invariants above so a standalone clone does not depend on external planning
files.

## Current Supersessions

| Historical V0 choice | Core 0 contract | Reason |
| --- | --- | --- |
| GraphQL-first product API | Versioned REST | Deliver a narrow, inspectable first loop |
| PostgreSQL + required Redis/Meilisearch | PostgreSQL-only product loop | Avoid queues and projections before a real need |
| JWT access/refresh tokens | Opaque server-side sessions in HttpOnly cookies | Immediate revocation and no browser-readable bearer token |
| Following directory symlinks | Do not follow directory symlinks by default; file targets must remain inside the same allowed root | Fail closed on cycles and boundary escapes |
| Runnable Agent and operator foundation | Mode and authority contracts only | Agent runtime begins after Core 0 |
| Playback and file operations | Deferred | The first loop is stewardship and read-only browsing |

The current [Compose file](../../../compose.yaml) contains Redis and Meilisearch
for development and future adapters. Their presence is not evidence that Core 0
may depend on them. The [README](../../../README.md) explicitly says capabilities
and configuration are added only when needed.

## Long-Term Direction, Not Current Scope

Later phases may add external metadata providers, dedicated search, durable job
queues, Music Steward runtime and review, playback, notifications, PWA behavior,
and tightly scoped file or tag execution. Each is an optional adapter or feature
behind the permanent invariants above. No roadmap entry pre-approves a library,
protocol, database bypass, or unsafe file capability.

Core 0 explicitly does not include GraphQL, Redis sessions, Meilisearch search,
runtime plugins, model calls, playback, transcoding, file mutation, automatic
cross-directory ReleaseGroup merges, or full Event Sourcing.

## Scope Test

A proposed Core 0 change is aligned when it directly closes the initialize,
authenticate, register, scan, browse, search, or reversible-directory-management
loop and preserves the invariants. It is misaligned when it adds infrastructure
only because the V0 repository once had it or because a future plugin might use
it.
