# Worker isolation + MCP profiles + fork-logging (handoff)

**Status: BLOCKED on AO update.** Saved here to track against upstream and to
contribute the feature back to agent-orchestrator.

## Problem
AO workers (claude-code sessions in tmux + git worktree) inherit the **global
`~/.claude/settings.json`** — shared with the orchestrator session. No isolation:
orchestrator-specific `env`, `permissions` (e.g. `gh pr merge` ask), `statusLine`,
SessionStart hooks, and enabledPlugins all leak into every worker.

## Goal
1. Two worker settings templates: **no-MCP (default, lightweight)** / **with-MCP
   (explicit, for codegraph/browser tasks)**.
2. Isolate worker settings from orchestrator.
3. Auto-log fork decisions: when a worker hits `AskUserQuestion`, log the question
   + all options (PreToolUse) and the chosen answer (PostToolUse) to the linked
   GitHub issue (branch `feat/issue-N` → issue N).

## Why blocked
Installed `@aoagents/ao@0.9.5` only reads `agentConfig.model` + `agentConfig.permissions`
for the claude-code plugin. The claude launch flags are `--append-system-prompt`,
`--dangerously-skip-permissions`, `--model`, `--print`, `--resume` — **no**
`--settings` / `--mcp-config` / `--setting-sources`. So per-worker settings/MCP
isolation can only be done via per-worktree files (`.claude/settings.json` + `.mcp.json`)
as a workaround, not natively.

The native capability is actively being built upstream (open PR/issue):
- #2116 / #222 / #219 — separate orchestrator vs worker config
- #2126 — configurable orchestrator permissions
- #1382 — `customInstructions` on top of worker/orchestrator defaults
- #1100 — tool configurability through ao config
- #500 / #462 — auto-accept MCP servers in non-interactive sessions
- hook-injection bugs into worker `.claude/settings.json`: #2001 / #1398 / #2091 / #2160

**Decision (owner):** do NOT hack 0.9.5 — update AO (or run dev), then configure
natively; help develop the feature upstream.

## Artifacts here
- `PLAN.md` — full implementation plan (templates, isolation, hook wiring, verification).
- `log-askuserquestion-pre.sh` / `log-askuserquestion-post.sh` — ready hook scripts
  (parse payload, resolve issue from branch, `gh issue comment`, no-op outside a
  worker branch, never-block). PRE tested on a fake payload.

## Resume trigger
When AO ships worker.agentConfig / customInstructions / settings-override (or on a
dev build): wire the no-MCP default + explicit MCP profile, embed the
AskUserQuestion hook in the worker template, verify on ONE worker (empty env, no
`gh pr merge` ask, fork logged to issue), then roll out.

## Notes
- Known limit: `AskUserQuestion` auto-resolves empty in pure headless (`claude -p`,
  claude-code #50728). Our workers run in tmux with live stdin (the menu actually
  shows), so the hook fires — verify on one worker first.
- DNS workaround for this env (GitHub DNS flaps): `dig +short @1.1.1.1 api.github.com`
  → pin IP via `curl --resolve api.github.com:443:<IP> -H "Authorization: token $(gh auth token)"`.
