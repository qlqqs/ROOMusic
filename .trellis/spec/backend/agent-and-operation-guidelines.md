# Music Steward And Operation Guidelines

## Status And Scope

This document records the long-term product contract for ROOMusic's Music
Steward and its durable operation model. Core 0 defines the contracts and the
directory-management example; it does not implement model calls, a Review
Subagent, file mutation, or an Operator runtime.

The system has one product Agent, **Music Steward**, with three execution modes:
`assistant`, `steward`, and `operator`. These are approval modes, not separate
business authorities. The deterministic scanner and parser are ordinary program
logic, not Agents.

## Authority And Execution Flow

All modes eventually submit a typed operation command to the backend authority:

```text
mode-specific decision
  -> typed Change Set
  -> backend authorization and tool lookup
  -> target, scope, revision, and physical-safety validation
  -> transactional or staged executor
  -> Operation Journal + structured log
```

The Agent, model, Review Subagent, browser, and CLI are callers. None of them
may write private tables, open a database connection, access an arbitrary host
path, or execute an unregistered tool.

### Mode contract

| Mode | Approval requirement | Allowed authority |
| --- | --- | --- |
| `assistant` | Current user explicitly approves a dangerous Change Set | Prepare plans, explain evidence, and execute approved operations |
| `steward` | Independent Review Subagent returns structured approval and policy allows it | Execute bounded autonomous operations after review |
| `operator` | No user confirmation or AI review step | Administrator-selected direct execution of registered tools |

Operator direct execution skips approval only. It does not skip backend
authorization, allowlists, argument validation, target checks, revision or
idempotency checks, journaling, logging, or an explicit irreversible marker.
There is no unrestricted-shell or arbitrary-SQL capability in the product
contract.

## Review Subagent Contract

The Review Subagent is an independent reviewer role. It receives a bounded
proposal and AI-safe evidence projection, not raw database rows, secrets, audio
bytes, or unrestricted paths. It returns a strict result such as:

```json
{
  "decision": "approved",
  "risk": "low",
  "scope_digest": "sha256:...",
  "reasons": ["target is unambiguous", "inverse action is available"],
  "requires_human_confirmation": false
}
```

The exact wire schema is selected when the Agent runtime is implemented. The
stable rules are:

- The main Agent cannot approve its own proposal by setting an approval field.
- Approval is bound to the proposal, target, evidence summary, policy version,
  scope, and expiry; changes require a new review.
- `approved`, `rejected`, and `needs_human_confirmation` are distinct outcomes.
- The backend re-checks current authorization and physical constraints after
  review and before execution.
- Review approval never grants a capability that the backend policy denies.
- A reviewer failure or unavailable reviewer fails closed for Steward mode; it
  does not silently downgrade to Operator mode.

## Change Set And Recovery Contract

Every persistent management operation has a durable Change Set and Operation
Journal. At minimum, the operation records:

- `operation_id` and `change_set_id`;
- actor, session/request, mode, and optional Agent/reviewer run identifiers;
- registered tool and canonical target scope;
- planned, running, succeeded, failed, or partially-failed status;
- expected resource revision and idempotency key/fingerprint;
- before/after state or a checkpoint reference;
- rollback availability, inverse action, and irreversible status.

Use a typed inverse operation. Do not store only natural-language instructions
such as "undo the previous change". A supported operation is reversible only if
the system has enough recorded state to execute its inverse and can detect a
later conflicting change.

Core 0's first supported Change Set is library-root add, disable, and restore.
Scanner-derived catalog state uses scan runs and source observations rather than
one Change Set per discovered field. Full file checkpoints, quarantine, tag
write-back recovery, and physical file operations require a later reviewed
capability.

## Tool Registration Rules

Tools are named, typed capabilities owned by the backend. A tool definition
must specify:

- input and output schema;
- minimum role and allowed mode;
- target scope and resource limits;
- whether it changes state;
- whether it is reversible or permanently irreversible;
- transaction/staging behavior;
- idempotency and revision requirements;
- error classes and correlation fields.

Examples of future capability names are `library_search`,
`release_evidence_query`, `metadata_overlay_apply`, `file_quarantine`, and
`tag_write_back`. Names are not permission: each call is authorized again using
the current principal and resource state.

Never expose a generic `run_shell`, `run_sql`, or `operate_path` escape hatch.
If a future Operator feature needs a system action, add a narrow named tool with
bounded arguments and an explicit recovery contract.

## Validation And Error Matrix

| Condition | Required behavior |
| --- | --- |
| Assistant dangerous operation without current approval | Reject with `approval_required`; no executor call |
| Steward without independent review approval | Reject with `review_required` or `review_unavailable`; no downgrade |
| Operator non-admin principal | Reject with `permission_denied` |
| Unknown tool or unsupported mode/tool pair | Reject with `capability_denied` |
| Stale target revision or changed proposal digest | Reject with `revision_conflict` or `precondition_failed` |
| Same idempotency key and same canonical request | Return the recorded result without repeating side effects |
| Same idempotency key and different request | Reject with `idempotency_conflict` |
| Executor fails before commit/stage completion | Record failure and recover according to tool contract |
| Permanent purge or equivalent irreversible action | Require explicit Operator-only tool policy and record `rollback_available=false` |

## Good, Base, And Bad Cases

- **Good:** Steward proposes a bounded metadata operation, the independent
  reviewer approves the exact digest, the backend re-checks the target revision,
  executes the registered inverse-capable tool, and records the result.
- **Base:** Operator directly runs an administrator-approved tool, the tool
  validates its arguments and scope, and the journal records that no AI review
  was required.
- **Bad:** The model emits `approved: true` together with a shell command and
  the worker executes it without a backend capability check.

## Required Tests

- Unit-test mode/approval matrix and exhaustive tool policy decisions.
- Test that Assistant cannot execute a dangerous proposal before user approval.
- Test that Steward cannot self-approve and fails closed when the reviewer is
  unavailable or returns `needs_human_confirmation`.
- Test that Operator bypasses approval but still enforces admin, tool, scope,
  revision, idempotency, and irreversible-action rules.
- Integration-test one Change Set through success, failure, repeated request,
  idempotency conflict, stale revision, and inverse-action paths.
- Assert that journal records and logs contain correlation IDs but no secrets,
  prompt bodies, raw provider responses, SQL, or unrestricted paths.
- When file tools are introduced, add filesystem integration tests for path
  containment, content hash verification, interruption, quarantine, restore,
  and permanent purge markers.

## Wrong Versus Correct

### Wrong

```text
worker-ai -> shell("mv " + model_output.source + " " + model_output.target)
```

This gives the model physical authority, has no bounded scope, and cannot
reliably recover from partial execution.

### Correct

```text
Steward -> Review Subagent -> typed Change Set
        -> backend tool authorization
        -> bounded move tool(source resource ID, target relative name)
        -> checkpoint/hash verification
        -> journaled result and typed inverse action
```

The implementation may use a sidecar or another model adapter later, but the
authority and recovery contract does not move out of the Go backend.

