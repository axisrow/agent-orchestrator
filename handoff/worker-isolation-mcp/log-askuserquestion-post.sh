#!/bin/bash
# PostToolUse hook for AskUserQuestion — logs the chosen answer(s) to the
# linked GitHub issue, completing the question→decision trail.
# No-op unless cwd is a worker worktree on a feat/issue-<N> branch.
set -uo pipefail

PAYLOAD="$(cat)"

CWD="$(printf '%s' "$PAYLOAD" | jq -r '.cwd // empty' 2>/dev/null)"
[ -z "$CWD" ] && exit 0
BRANCH="$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
ISSUE="$(printf '%s' "$BRANCH" | grep -oE 'issue-[0-9]+' | grep -oE '[0-9]+' || true)"
[ -z "$ISSUE" ] && exit 0

# Answers live in tool_input.answers ({question: answer|[answers]}).
ANSWERS="$(printf '%s' "$PAYLOAD" | jq -r '
  (.tool_input.answers // {}) | to_entries
  | map("**" + .key + "** → " + (if (.value|type)=="array" then (.value|join(", ")) else (.value|tostring) end))
  | join("\n")
' 2>/dev/null)"
[ -z "$ANSWERS" ] && exit 0

BODY="$(printf '## ✅ Выбор по развилке\n\n%s' "$ANSWERS")"
gh issue comment "$ISSUE" --body "$BODY" >/dev/null 2>&1 || true
exit 0
