# ROOMusic Shared Engineering Guides

These guides apply to every package before the backend- or frontend-specific
rules. They are project contracts for the upcoming Core 0 implementation, not
claims that application code already exists.

## Read First

| Guide | Purpose | Use when |
| --- | --- | --- |
| [Product Goals](./product-goals.md) | Preserve user value, Music Steward modes, change management, and phase boundaries | Scoping any feature, Agent, operation, or infrastructure |
| [Modular Design](./modular-design.md) | Define ownership, dependency direction, cohesion, and coupling limits | Creating or changing a module boundary |
| [Engineering Principles](./engineering-principles.md) | Define smallest-complete changes, focused functions, and proportional verification | Planning and reviewing every change |
| [Code Reuse Thinking Guide](./code-reuse-thinking-guide.md) | Reuse the correct policy owner without premature abstraction | Adding helpers, types, components, or constants |
| [Cross-Layer Thinking Guide](./cross-layer-thinking-guide.md) | Trace data and authority across REST, services, storage, and UI | Changing a workflow that crosses a boundary |

Backend work must also read [Backend Guidelines](../backend/index.md). Frontend
work must also read [Frontend Guidelines](../frontend/index.md).

## Rule Priority

When sources disagree, apply this order:

1. The user-approved task and current Core 0 PRD define current scope and
   acceptance behavior.
2. V0 planning defines inherited product intent and long-term direction.
3. V0 implementation choices are historical evidence only.
4. Current README, Compose, and environment files describe repository and local
   environment facts.

For example, rich Release Graph queries remain a product goal, but Core 0 uses
versioned REST and PostgreSQL search. Historical GraphQL and Meilisearch choices
do not override that current contract.

## Working Trigger

Before changing code, answer four questions:

1. Which capability owns the behavior?
2. Which boundary accepts and validates the input?
3. Which authority decides whether the action is allowed?
4. What focused test proves the behavior and the important failure path?

An anti-pattern is beginning from a convenient technical folder such as
`utils`, `handlers`, or `queries` and allowing that location to become the
policy owner.

## Evidence

- [Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md)
- [Current README](../../../README.md)
- [Architecture canvas](../../../docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx)
- [Inherited V0 product goals and phase decisions](./product-goals.md)

The original V0 planning workspace is historical input, not a required sibling
checkout. The repository-local product goals and Core 0 PRD preserve the
decisions that remain authoritative for this project.
