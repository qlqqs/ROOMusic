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

This intent comes from the V0 [product definition](../../../../ROOMusic-V0/.planning/PROJECT.md),
[requirements](../../../../ROOMusic-V0/.planning/REQUIREMENTS.md), and
[roadmap](../../../../ROOMusic-V0/.planning/ROADMAP.md). Those files are product
history, not current source code or a mandate to restore the V0 runtime.

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

The Roon alignment reference explains why local files become an object graph,
why versions remain distinct, and why tags and source evidence matter:
[Roon phase-one alignment](../../../../ROOMusic-V0/.planning/references/roon-phase1-alignment.md).

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
