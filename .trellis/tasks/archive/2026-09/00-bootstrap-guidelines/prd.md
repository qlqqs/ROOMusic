# Bootstrap Project Development Guidelines

## Goal

Replace the generated `.trellis/spec/` templates with practical, project-specific
guidance for ROOMusic. The guidance must preserve the product goal, distinguish
Core 0 from the long-term direction, and keep implementation modular,
high-cohesion, low-coupling, reuse-oriented, and narrowly scoped.

## Scope

- Populate `.trellis/spec/backend/` and `.trellis/spec/frontend/`.
- Add shared guidance for product goals, modular design, and engineering
  principles; tailor the generated reuse and cross-layer guides to ROOMusic.
- Align all guide and layer indexes with the final document set.
- Document conventions confirmed by project documentation and the Core 0
  planning task, clearly distinguishing repository facts from implementation
  contracts that Core 0 must follow.
- Add concise examples and references from `README.md`, `compose.yaml`,
  `.env.example`, and `roomusic-modular-plugin-architecture.canvas.tsx`.
- Derive product goal, core value, target users, long-term direction, and
  non-goals from `../ROOMusic-V0/.planning/PROJECT.md`,
  `../ROOMusic-V0/.planning/REQUIREMENTS.md`,
  `../ROOMusic-V0/.planning/ROADMAP.md`, and
  `../ROOMusic-V0/.planning/references/roon-phase1-alignment.md`; record where
  the current Core 0 PRD intentionally narrows or supersedes V0 implementation
  choices.
- Do not implement Go, React, database migrations, or runtime plugins as part of
  this task.

## Architecture Context

- The repository currently contains development environment configuration only;
  no application source or test suite exists yet.
- Core 0 is a modular monolith: first-party modules compile into one Go binary
  and collaborate through typed contracts.
- Core 0 exposes versioned REST APIs, uses PostgreSQL as the only required
  business authority, performs scans in-process, and uses PostgreSQL for basic
  search. Redis and Meilisearch remain optional post-Core 0 adapters.
- The domain model is `ReleaseGroup -> Release -> Medium -> Track`; source
  observations and scan runs explain derived metadata.
- Authentication uses opaque server-side sessions in HttpOnly cookies. Admin and
  ordinary-user permissions are enforced by the backend, not by the UI.
- Library roots are read-only and constrained by configured
  `allowed_library_roots`; path and symlink checks are mandatory.
- User-initiated persistent changes go through Change Set and Operation Journal
  boundaries. Plugins, agents, or future execution adapters never gain authority
  by declaring it themselves.
- The long-term direction is a self-hosted music stewardship platform with
  optional metadata, Agent/Review, playback, notification, and controlled
  execution capabilities. This direction is inherited from V0's `What This Is`,
  `Core Value`, and active requirements, not invented by this task. These
  capabilities extend the fixed authority core; they do not replace its
  identity, permission, transaction, or recovery rules.
- V0's product target is an AI-native Release Graph music steward for personal,
  family, or small private groups with large NAS collections (20,000+ albums),
  Docker/Compose deployment, reverse-proxy access, multi-user admin/listener
  roles, evidence-backed metadata governance, and eventual lossless playback.
- V0's Roon alignment establishes the domain emphasis: local files become a
  `ReleaseGroup -> Release -> Medium -> Track` object graph; versions remain
  distinct, multi-disc structure is preserved, tags are preferred over folder
  guesses, and source evidence remains inspectable.
- Current Core 0 intentionally supersedes several V0 implementation choices:
  REST-first instead of requiring GraphQL, PostgreSQL-only instead of requiring
  Redis/Meilisearch, opaque cookie sessions instead of JWT, conservative
  no-follow directory symlink behavior, and no runnable Agent/playback/plugin
  runtime. These are scope decisions, not changes to the product goal.

## Files To Update

- `.trellis/spec/backend/index.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/backend/database-guidelines.md`
- `.trellis/spec/backend/error-handling.md`
- `.trellis/spec/backend/logging-guidelines.md`
- `.trellis/spec/backend/quality-guidelines.md`
- `.trellis/spec/frontend/index.md`
- `.trellis/spec/frontend/directory-structure.md`
- `.trellis/spec/frontend/component-guidelines.md`
- `.trellis/spec/frontend/hook-guidelines.md`
- `.trellis/spec/frontend/state-management.md`
- `.trellis/spec/frontend/type-safety.md`
- `.trellis/spec/frontend/quality-guidelines.md`
- `.trellis/spec/guides/index.md`
- `.trellis/spec/guides/product-goals.md` (new)
- `.trellis/spec/guides/modular-design.md` (new)
- `.trellis/spec/guides/engineering-principles.md` (new)
- `.trellis/spec/guides/code-reuse-thinking-guide.md`
- `.trellis/spec/guides/cross-layer-thinking-guide.md`

## Rules

- Organize code by business capability and ownership, not by generic utility
  buckets or transport/database leakage.
- Make the smallest complete change in the owning module. Do not combine feature
  work with unrelated cleanup, dependency replacement, or speculative redesign.
- Keep functions small by responsibility and cognitive load, not by an arbitrary
  line limit. A function should operate at one abstraction level with explicit
  inputs, outputs, and side effects.
- Search before adding code. Reuse or extend the existing policy owner when the
  semantics match; extract shared code only when the abstraction is stable and
  avoids meaningful duplication. Do not create generic utility dumping grounds.
- Keep high-cohesion units responsible for one business policy and expose narrow
  interfaces; depend on ports/contracts rather than concrete providers.
- Keep dependency direction inward: transport and adapters depend on application
  services, application services depend on domain contracts, and persistence or
  external systems implement ports.
- Validate at boundaries once, normalize into typed values, and pass typed
  contracts across layers.
- Treat security, path safety, transactionality, revision checks, and recovery
  semantics as backend authority. Frontend checks are usability aids only.
- Mark future Agent, plugin, Redis, Meilisearch, playback, and file-write
  behaviors as deferred adapters; do not make them Core 0 prerequisites.
- Use evidence-backed examples and explicitly label planned conventions because
  application source does not exist yet.
- Treat V0 planning documents as product-history references, not as permission to
  copy their entire implementation or resurrect retired scaffolds.
- Remove generated placeholder sections and keep indexes synchronized with the
  actual spec files.

## Acceptance Criteria

- [x] Every backend and frontend spec file contains concrete ROOMusic rules,
      references, examples, and anti-patterns; no template placeholder remains.
- [x] The rules explicitly cover modular boundaries, high cohesion, low coupling,
      dependency inversion, and forbidden cross-layer access.
- [x] Shared guidance records the software goal, Core 0 outcome, long-term
      direction, permanent authority invariants, and explicit non-goals.
- [x] Engineering guidance defines smallest-complete-change discipline, focused
      function design, reuse-first search, extraction thresholds, and the limits
      of premature abstraction.
- [x] Core 0 REST/PostgreSQL-only/read-only-library/session/security boundaries
      are consistent across indexes and layer documents.
- [x] Product-goal guidance cites V0 sources and clearly separates inherited
      product intent from Core 0 technical supersessions.
- [x] Future integrations are clearly separated from Core 0 and cannot be read as
      required runtime dependencies.
- [x] Index files link to every final guideline and no removed or broken links
      remain.
- [x] `grep -R "To be filled\\|TODO: fill\\|Replace with your actual" .trellis/spec`
      returns no matches, and Markdown links/paths resolve.

## Open Questions

There are no product decisions blocking this documentation pass. The long-term
section records direction rather than a committed release schedule. Exact library
choices (Go router, SQL package, React query library, and test runner) remain
implementation decisions and must be recorded when selected rather than guessed
in these bootstrap specs.
