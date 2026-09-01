# Frontend Directory Structure

## Status And Intent

No frontend source tree exists yet. Use this planned feature-oriented layout when
the React/TypeScript application is initialized. Create only directories needed
by implemented Core 0 behavior; the tree is a boundary guide, not an instruction
to generate empty scaffolding.

## Planned Layout

```text
frontend/
├── src/
│   ├── app/
│   │   ├── routes/                 # route definitions and page composition
│   │   ├── providers/              # selected framework providers
│   │   ├── shell/                  # authenticated navigation/layout
│   │   └── main.tsx
│   ├── features/
│   │   ├── auth/
│   │   ├── library/
│   │   ├── catalog/
│   │   ├── search/
│   │   └── operations/
│   ├── shared/
│   │   ├── api/                    # HTTP mechanics and common error decoder
│   │   ├── ui/                     # policy-free, reusable presentation
│   │   └── lib/                    # stable domain-neutral mechanics only
│   ├── assets/
│   └── test/
│       └── setup/                  # runner/browser setup, not feature tests
└── public/
```

A feature adds only the subfolders it needs:

```text
features/library/
├── api/                            # DTO, decoder, endpoints
├── components/
├── hooks/
├── model/                          # feature/view state, pure projections
├── routes/
├── index.ts                        # narrow public surface when needed
└── *.test.tsx
```

Tests normally live beside the unit they prove. Large cross-feature flows may
live in the selected integration/end-to-end test location once the tooling is
chosen.

## Ownership

- `app` composes routes, top-level providers, session bootstrap, and shell. It
  does not absorb feature policy.
- `auth` owns setup/login forms, current-session presentation, logout, and
  admin/user UI capabilities. The server remains the authentication authority.
- `library` owns root/scan administration and scan diagnostics presentation.
- `catalog` owns Release browse/detail, Medium/Track display, provenance, and
  cover presentation.
- `search` owns query/filter URL state and result presentation.
- `operations` owns Change Set/Operation Journal status and supported recovery
  UI, not generic toast messages.
- `shared/api` owns base URL, credentials, request correlation, response body
  mechanics, and the common REST error envelope. Feature endpoints and DTOs stay
  with their feature.
- `shared/ui` contains reusable presentational primitives with no REST calls,
  role policy, or feature store imports.

## Import Rules

- `app` may compose feature public surfaces.
- Features may import `shared` and a deliberately published contract from
  another feature. They do not import another feature's private component, hook,
  API endpoint, or state.
- `shared` never imports `features` or `app`.
- Feature-to-feature cycles are forbidden. Move orchestration to `app` or
  publish a smaller typed contract.
- Avoid deep relative imports across boundaries. A public entry is narrow; it is
  not a barrel that re-exports the whole feature.

## Naming

- React components and their exported types use `PascalCase`; hooks start with
  `use`; functions/values use `camelCase`.
- Files use one consistent lowercase convention selected in the initial
  scaffold; route and feature directory names reflect product vocabulary, not
  backend table names.
- Wire types end in `DTO` when distinction from a view/domain model matters.
  Runtime decoders use verbs such as `decodeReleaseDetail`.
- Tests use `*.test.ts` or `*.test.tsx` beside the owner. Do not create a
  repository-wide `__tests__` mirror tree.

## Example Placement

A Release page route composes `ReleaseDetail` and its feature hook. The hook
calls `catalog/api/get-release-detail.ts`, which uses the shared HTTP transport
and a catalog-owned decoder. A reusable skeleton may move to `shared/ui` only
when another implemented feature needs the same presentation contract.

The page never imports a PostgreSQL-shaped row, exposes an absolute artwork path,
or combines a scan mutation into catalog display merely because both mention a
Release.

## Production Boundary

The frontend build output is embedded or served by the Go application in
production. Do not add a production Node server, cross-origin authentication
scheme, or CORS dependency for Core 0. Local development may use a dev-server
proxy to the Go REST API.

## Anti-Patterns

- Global `components`, `hooks`, `services`, or `types` buckets containing
  unrelated feature policy.
- One `api.ts` file with every endpoint and DTO.
- Importing private feature state to avoid defining a narrow contract.
- A generic `utils.ts` that accumulates path, permission, date, and Release
  rules.
- Creating player, Agent chat, plugin marketplace, or Meilisearch client folders
  before those phases are approved.

This structure mirrors backend capability ownership in
[Modular Design](../guides/modular-design.md) and the current
[Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).
