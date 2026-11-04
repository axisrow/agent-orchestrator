package sqlite

import "testing"

// TestMigratePRCIStateNoneWidensCheckAndKeepsChildren seeds a PR with check
// history and reviews, applies the ci_state 'none' widening (fork issue #170),
// and asserts the facts the fix depends on: existing rows survive the table
// rebuild, the cascading children keep their rows (the rebuild drops pr, which
// under enforced foreign keys would cascade them away, so it must run with
// foreign keys off), a 'none' verdict persists, and the downgrade restores the
// narrow CHECK and rejects 'none' again.
func TestMigratePRCIStateNoneWidensCheckAndKeepsChildren(t *testing.T) {
	db := openMigratedDatabaseCopy(t, migrationVersion(t, "delivery_events"))

	const stamp = "2026-11-04 08:00:00"
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p1', '/tmp/p1', 'proj', ?)`, stamp); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
		VALUES ('s1', 'p1', 1, 'worker', 'exited', ?, 0, ?, ?)`, stamp, stamp, stamp); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pr (url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at, head_sha)
		VALUES ('https://github.com/acme/repo/pull/7', 's1', 7, 'open', 'none', 'unknown', 'mergeable', ?, 'abc123')`, stamp); err != nil {
		t.Fatalf("seed pr: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pr_checks (pr_url, name, commit_hash, status, url, log_tail, created_at)
		VALUES ('https://github.com/acme/repo/pull/7', 'unit', 'abc123', 'passed', '', '', ?)`, stamp); err != nil {
		t.Fatalf("seed pr_checks: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pr_comment (pr_url, comment_id, author, file, line, body, created_at, auto_inject_review)
		VALUES ('https://github.com/acme/repo/pull/7', 'c1', 'alice', 'main.go', 12, 'note', ?, 0)`, stamp); err != nil {
		t.Fatalf("seed pr_comment: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate to the ci_state none widening: %v", err)
	}

	var checks, comments int
	if err := db.QueryRow(`SELECT count(*) FROM pr_checks WHERE pr_url = 'https://github.com/acme/repo/pull/7'`).Scan(&checks); err != nil {
		t.Fatalf("read pr_checks: %v", err)
	}
	if checks != 1 {
		t.Fatalf("pr_checks rows = %d, want 1 (the rebuild must not cascade them away)", checks)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pr_comment WHERE pr_url = 'https://github.com/acme/repo/pull/7'`).Scan(&comments); err != nil {
		t.Fatalf("read pr_comment: %v", err)
	}
	if comments != 1 {
		t.Fatalf("pr_comment rows = %d, want 1", comments)
	}

	if _, err := db.Exec(`UPDATE pr SET ci_state = 'none' WHERE url = 'https://github.com/acme/repo/pull/7'`); err != nil {
		t.Fatalf("persist the none verdict: %v", err)
	}

	// Down: 'none' rows cannot survive the narrow CHECK, and the narrowed
	// schema rejects the verdict again. Reset the verdict first so the seeded
	// children keep a parent (a live daemon would re-poll the PR instead).
	if _, err := db.Exec(`UPDATE pr SET ci_state = 'unknown' WHERE url = 'https://github.com/acme/repo/pull/7'`); err != nil {
		t.Fatalf("reset the none verdict: %v", err)
	}
	downTo(t, db, migrationVersion(t, "delivery_events"))
	if _, err := db.Exec(`INSERT INTO pr (url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at)
		VALUES ('https://github.com/acme/repo/pull/8', 's1', 8, 'open', 'none', 'none', 'mergeable', ?)`, stamp); err == nil {
		t.Fatalf("downgraded schema accepted ci_state 'none', want CHECK rejection")
	}
}
