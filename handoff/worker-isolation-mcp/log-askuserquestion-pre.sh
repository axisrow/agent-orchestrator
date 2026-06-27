#!/bin/bash
# PreToolUse hook for AskUserQuestion — logs the question + all options to the
# linked GitHub issue, so worker forks leave a durable trail.
# No-op (exit 0) unless cwd is a worker worktree on a feat/issue-<N> branch.
# Never blocks the tool (always exit 0).
set -uo pipefail

PAYLOAD="$(cat)"

# Resolve the issue number from the branch name (feat/issue-123 -> 123).
CWD="$(printf '%s' "$PAYLOAD" | jq -r '.cwd // empty' 2>/dev/null)"
[ -z "$CWD" ] && exit 0
BRANCH="$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
ISSUE="$(printf '%s' "$BRANCH" | grep -oE 'issue-[0-9]+' | grep -oE '[0-9]+' || true)"
# Only act inside a worker (feat/issue-N). Orchestrator branch -> no-op.
[ -z "$ISSUE" ] && exit 0

# Build a markdown block with every question + its options.
BODY="$(printf '%s' "$PAYLOAD" | jq -r '
  "## 🔀 Развилка воркера (AskUserQuestion)\n\n" +
  ([.tool_input.questions[]
    | "**❓ " + (.header // "Вопрос") + ":** " + .question + "\n"
      + ([.options[] | "- " + .label + (if .description and .description != "" then " — " + .description else "" end)] | join("\n"))
   ] | join("\n\n"))
  + "\n\n_Ответ/решение будет дописан после выбора._"
' 2>/dev/null)"
[ -z "$BODY" ] && exit 0

# Best-effort: never fail the tool if gh/network is down.
gh issue comment "$ISSUE" --body "$BODY" >/dev/null 2>&1 || true
exit 0
