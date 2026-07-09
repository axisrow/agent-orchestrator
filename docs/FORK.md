# Fork notes (axisrow/agent-orchestrator)

This fork tracks `AgentWrapper/agent-orchestrator` (`origin/main`) and carries a
small, rebase-friendly delta on top of it. Everything else is kept byte-identical
to upstream so each sync is a clean rebase, not a merge with conflicts.

## The delta

`main` = `origin/main` + three commits:

1. **`feat(send): blocked activity state + guarded send confirmation`**
   Splits the overloaded `waiting_input` state into `waiting_input` (agent idle
   at an empty prompt — safe for automation to message) and `blocked` (agent
   paused on a permission/approval decision — automation must never inject
   input). Adds the `sessionguard` package (one guarded pane-write primitive:
   `Deliver` refuses a blocked/missing/terminated session and reports why via a
   typed `Outcome`) and a post-send confirmation loop that re-sends a bare Enter
   until the durable `Activity.State` flips to active, fixing multiline prompts
   whose single Enter was absorbed into an unsubmitted draft. `blocked` is
   cleared by correlating the approved tool's `post-tool-use` with the dialog
   that blocked the session (lifecycle tool-flight), which only claude-code and
   its hook-delegators (grok/continue/devin) can do; every other harness maps
   its permission signal to `waiting_input` and opts out of the Enter nudge via
   `EmitsBlockedActivity()`. Submitted upstream as **PR #2357**.

2. **`feat(desktop): AO_KEEP_DAEMON…`**
   Opt-out flag so the desktop app's daemon outlives the window (for a
   headless/containerized daemon). Default unset = upstream behavior unchanged.
   Documented as one row in `docs/cli/README.md`. Submitted upstream as **PR #2231**.

3. **`docs: fork notes`** — this file. Fork-only, never part of a PR.

`README.md` is kept **byte-identical to upstream** on purpose: it is the
highest-churn file upstream, so any fork edit there maximizes rebase conflicts.
Fork-specific documentation lives here and in `docs/cli/README.md`
(the `AO_KEEP_DAEMON` row).

## Branches

- `main` — the three commits above, force-pushed to `fork/main`.
- `upstream-send-confirmation` — the head of PR #2357. Points at commit 1
  (`main~2`), so a `--update-refs` rebase moves it automatically.
- `feat/keep-daemon-alive-flag` — the head of PR #2231. Commit 2 replayed
  directly onto `origin/main` (the PR must contain only that one commit, not the
  send-confirmation work).

## Syncing with upstream

`git rerere` is enabled (`git config rerere.enabled true`) so a conflict resolved
once is reapplied on later rebases.

```bash
git fetch origin
git tag -f backup/pre-sync main                       # disposable safety anchor
git rebase origin/main main --update-refs             # moves main AND upstream-send-confirmation
git rebase --onto origin/main main~2 feat/keep-daemon-alive-flag   # keep-daemon alone on origin/main
git switch main
git push --force-with-lease fork main upstream-send-confirmation feat/keep-daemon-alive-flag
```

A convenience alias lives in `.git/config` (not tracked, so it adds no conflict
surface): `git sync-upstream`.

When upstream **merges** a PR it squashes, so the merged commit is not
patch-identical to ours and `git rebase` will not drop it automatically. After a
merge, drop the corresponding local commit by hand (interactive rebase or reset)
and delete its PR branch.
