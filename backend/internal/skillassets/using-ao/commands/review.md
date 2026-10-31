# ao review

Manage AO's native code reviewer for a worker's PR.

AO's reviewer is an isolated reviewer agent for implementation PRs owned by an
AO worker. To review that worker's PR, use `ao review trigger`. Inside a worker,
the review commands default to the calling session (`AO_SESSION_ID`), so
`ao review trigger` reviews your own implementation PR. An orchestrator passes
the implementation worker's session id.

`ao review pr` is the unified entry point over both routes: it detects whether
the PR is this project's own (canonical repo, or the author/repository owner in
the project's own accounts) and spawns accordingly — an Edit-capable claimed
worker for own PRs, or a review-only session with mutation tools denied for
foreign ones. For a requested review of someone else's PR, prefer that entry
point; spawning a normal review-only worker without claiming or attaching the
target PR works the same way. Do not run `ao review trigger` on a foreign PR
outside its session, create an ownership-only placeholder, or require an
implementation owner just to review the PR.

AO's review is internal. An AO approval is not a GitHub approval: it does not
satisfy required or independent-account reviews or branch protection, and it
does not authorize merging.

## Syntax

```
ao review <subcommand> [args] [flags]
```

The review loop can be inspected, submitted, cancelled, or triggered again.

## Subcommands

---

### ao review pr

One entry point to review a PR; it branches by ownership and spawns the worker
itself. Own PR (repository matches the project's canonical repo, or the PR's
author or repository owner is in the project's own accounts): spawns a full
worker, claims the PR, and — when the project has auto-review enabled — starts
the reviewer whose inline findings loop back to the worker as fixes. Foreign PR
(everything else, including an unparsable repository): spawns a review-only
session whose launch denies Edit/Write and push tools (a mutation tool call
fails at the tool layer), claims nothing, and starts the reviewer with
auto-inject off, so the result is a published verdict on GitHub with no fix
loop. `--mode own|foreign` overrides detection. The active mode is stated in
the command output, the worker's prompt, and `ao session get`
(`review mode: review-only (foreign)`).

**Syntax:**
```
ao review pr <pr-ref> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id to spawn the review session in | `AO_PROJECT_ID`, the calling session's project, or the current registered repo |
| `--mode string` | Force the review mode: `own` or `foreign` | Detect by ownership |
| `--name string` | Display name for the spawned session | Derived from the PR title |
| `--no-takeover` | Own mode: refuse if another active session owns the PR | Takeover allowed |

**Examples:**

```bash
# The entrypoint decides: own PR → review+fix worker, foreign PR → review-only
ao review pr https://github.com/acme/widgets/pull/42
```

```bash
# Force the safe mode regardless of detection
ao review pr 42 --mode foreign
```

---

### ao review ls

List review runs for a worker session (default: the calling session). Alias: `list`.

**Syntax:**
```
ao review ls [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output reviews as JSON | - |

**Example:**

```bash
ao review ls mer-3
```

---

### ao review submit

Record a reviewer's result for a worker's PR.

**Syntax:**
```
ao review submit [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--body string` | Review body: a path to a Markdown file, or `-` to read from stdin | - |
| `--review-id string` | Id of the GitHub PR review just posted (the `.id` from the `gh api` POST that created the review) | - |
| `--reviews string` | JSON review results array or object: a path, or `-` to read from stdin | - |
| `--run string` | Review run id | Required |
| `--session string` | Worker session id (or pass it as the positional argument) | - |
| `--verdict string` | Review verdict: `approved` or `changes_requested` | Required |

If the local daemon is restarting when a result is submitted, AO retains the
parsed result in memory and retries the same idempotent request for up to 30
seconds. Validation errors return immediately. If the daemon remains unavailable,
the command reports failure and can be repeated safely; daemon idempotency
handles the case where an earlier connection dropped after committing the result.

**Examples:**

```bash
# Submit an approved review for session mer-3
ao review submit mer-3 --run review-run-1 --verdict approved
```

```bash
# Submit a changes-requested review with a body from stdin
echo "Please fix the null check on line 42." | ao review submit --session mer-3 --run review-run-1 --verdict changes_requested --body -
```

---

### ao review cancel

Cancel every running review for a worker's PR (default: the calling session). Alias: `stop`.

**Syntax:**
```
ao review cancel [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | - |

**Example:**

```bash
ao review cancel mer-3
```

---

### ao review trigger

Start AO's reviewer on a worker's open PR heads (default: the calling session).
Aliases: `execute`, `restart`. Reviewer panes cannot run it.

**Syntax:**
```
ao review trigger [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | The calling AO session |
| `--pr string` | Review only this session's implementation PR URL, attaching it to the session first if AO does not track it yet (never from another active session). Do not pass an external review target | Every eligible PR on the session |
| `--agent string` | Reviewer agent for this pass only (alias `--harness`) | Session reviewer, then project reviewer, then the project's default worker agent |
| `--model string` | Reviewer model for this pass only | As above |
| `--effort string` | Reviewer reasoning effort for this pass only | As above |
| `--rerun` | Review a head that already has a review again, or add a reviewer with a different agent alongside one that is still running. A project allows one reviewer per head by default (`maxReviewersPerHead`); a second reviewer is rejected unless the project raised the cap | - |
| `--no-inject` | Leave the session's review auto-inject setting unchanged | Auto-inject is turned on |

AO fetches the session's PRs fresh from the provider before deciding what is
due, so the pass covers the commit really on the PR. Right after opening a PR,
pass `--pr <url>`.

A head that is already being reviewed, or already has a review, is not reviewed
again: the command exits 1 with `REVIEW_ALREADY_RUNNING` or
`REVIEW_HEAD_ALREADY_REVIEWED` and says what to do. Push new commits, or pass
`--rerun`. The trigger re-reads the PR from the provider first; if you
just pushed and it still says already reviewed, wait a few seconds and retry. The same reviewer agent never runs twice on one head at the same time.

By default the command turns on the worker session's review auto-inject. The
reviewer posts an inline GitHub comment for each change it requires, and AO
forwards those comments to the worker like any other review comments. An
approval adds no comments, so check the verdict with `ao review ls`. If a run
shows `failed` or `cancelled`, trigger again; nothing else reports it.

A project allows one reviewer per PR head by default (`maxReviewersPerHead`).
Adding a second reviewer on the same head is a documented second-opinion
feature, but it is the owner's call: the daemon rejects the trigger with
`REVIEWER_LIMIT_REACHED` while the cap is 1, and a worker must not add one
without an explicit instruction in its task — report the rejection instead of
retrying. Raise the cap in the project settings to make second opinions
available. `ao review ls` shows how many reviewers cover each head and which
trigger source (manual, agent, auto) added each.

**Examples:**

```bash
# Inside a worker: request a review of this session's PR once it is ready
ao review trigger
```

```bash
# Orchestrator: review a worker's PR with a specific reviewer
ao review trigger mer-3 --agent codex --model gpt-5.5
```

```bash
# Add a second opinion on the same commit while the first reviewer runs
# (rejected with REVIEWER_LIMIT_REACHED unless the project raised maxReviewersPerHead)
ao review trigger --agent claude-code --rerun
```
