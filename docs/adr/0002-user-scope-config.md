# 2. A User-scope config layer above projects, merged wholesale

Date: 2026-07-23
Status: Proposed

## Context

Today AO has exactly **one config layer**: per-project. A project's `config` is a
nullable JSON blob on the `projects` SQLite row (`migrations/0008_add_project_config.sql`),
decoded into a typed `ProjectConfig` (`domain/projectconfig.go`). At spawn,
`effectiveAgentConfig` merges the role override (`Worker`/`Orchestrator`) over the
project's base `AgentConfig`, with role fields winning when set
(`session_manager/manager.go`). There is no config layer above the project: no global,
user, or domain config — verified by routes, filesystem refs, and `config.Load`.

This is the first brick of a multi-scope config model (the direction explored in
#2844). Claude Code carries four settings scopes (Managed/Local/Project/User); Codex
carries a layered TOML model with named profiles. AO has none above the project, so a
worker today cannot inherit "my usual agent setup" across projects — every project
re-declares its `AgentConfig` from scratch, and any value a user wants everywhere must
be copied into each project's blob.

The blocker for any scope-above-project is the **absent-vs-empty** distinction. Once a
User layer exists, an unset field on the project must mean "inherit from User," while a
deliberately cleared field must mean "drop the User value." Today the codebase resolves
this with a single dominant pattern:

- **Zero-value-means-inherit** is the rule for every `AgentConfig` field that exists
  (`Model`, `Permissions`): `effectiveAgentConfig` applies `if override.X != ""`
  (`manager.go`). Empty always means "inherit"; there is no way to explicitly clear a
  single field. (An earlier draft of this ADR referenced a `*MCPConfig` pointer field and
  a `SystemPrompt` field; neither exists in the codebase today. The pointer-for-third-state
  pattern is recorded here only as the established precedent to reach for, *if* a future
  field ever needs per-field explicit-clear.)

There is no ADR recording the pattern; the rule lives only in code comments.

## Decision

Introduce a **User-scope config layer**, stored as a **singleton row in a new SQLite
table** (not a file, not a column on `projects`), exposing **the current `AgentConfig`**
(`model` and `permissions` — the only fields `domain.AgentConfig` carries today, per
`agentconfig.go`). Precedence is **Project over User**: the project's `AgentConfig` wins
over the User layer, mirroring Claude Code where User is the lowest-precedence settings
scope.

The surface is deliberately scoped to **exactly the fields `domain.AgentConfig` has
today** (Model, Permissions). This keeps the first PR a pure storage/transport addition
with zero changes to the domain package. When `AgentConfig` later gains fields (env,
systemPrompt, mcp — currently absent), the User layer picks them up automatically with no
schema change, because it reuses the same type end to end.

The User layer merges **wholesale**, not field-by-field. Concretely:

- The User config is a single `AgentConfig` that the project layer either inherits (when
  the project leaves a field at zero value) or overrides (when the project sets it).
- This reuses the project's **existing** zero-value-means-inherit merge semantics in
  `effectiveAgentConfig` (`manager.go`): the project base `cfg.AgentConfig` becomes the
  result of merging User-over-nothing, so an unset project field falls through to User
  before falling through to built-in defaults. The role override
  (`roleOverride(...).AgentConfig`) then merges over that, unchanged.
- **No field-level explicit-clear is supported in this first layer.** A project cannot
  say "drop just the User model, keep the rest" — clearing is all-or-nothing at the
  project level (the project either declares an `AgentConfig` or inherits User). This
  matches how `SetConfig` already replaces project config wholesale (`service.go`,
  `dto.go`: "Config replaces the project's stored config wholesale; a zero-value config
  clears it").

Storage, endpoint, and merge are the scope of the first PR:

- **Storage:** new table, one row, `AgentConfig` JSON blob, migrated via goose.
- **Endpoint:** new `/api/v1/user-config` (GET/PUT), a separate resource for a separate
  scope; the project endpoint's semantics are unchanged.
- **Merge:** `effectiveAgentConfig` resolves project-over-User before role-over-project.
  Existing project blobs are untouched.
- **No UI in this PR.** The desktop form (`ProjectSettingsForm`) is unchanged; a User
  settings surface is a follow-up.

Backward compatibility is by construction: a missing User row decodes to a zero
`AgentConfig`, so existing projects and the 8k current users resolve to **exactly today's
behavior** until a User config is set.

Security (ties to #2951): a User `env` value reaches **every** worker across **every**
project, so the layer must accept only explicitly-set values and must **never** copy or
persist `os.Environ()`. The User layer is opt-in typed config, not a reflection of the
host environment.

## Consequences

- A user gains one place to set a cross-project agent profile, removing per-project
  duplication. Projects remain free to override any field.
- Absent-vs-empty is deliberately **not** solved per-field. Clearing a single User field
  from the project layer is impossible in this PR; clearing is wholesale at the project
  level, consistent with existing `SetConfig`. If fine-grained clear becomes necessary
  later, the established precedent is a pointer for the field that needs a third state
  (no such field exists in `AgentConfig` today); promote fields to pointers one at a time
  as the need arises, without reworking the layer.
- This ADR records the wholesale-replace principle for the User layer so the choice is
  visible to reviewers; the older code-comment-only rule for project config is
  unchanged.
- The first PR is backend-only (storage + endpoint + merge) to keep the change narrow
  and reviewable. UI, additional scopes (Local, Managed), `ProfileSource` across
  layers, and Codex-style profile presets are follow-up PRs that build on this
  foundation.
