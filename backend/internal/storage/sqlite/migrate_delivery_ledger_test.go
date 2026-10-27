package sqlite

import (
	"testing"
)

// TestMigrateReportsToDeliveryLedger replays the phase-1 delivery-ledger
// migration against pre-seeded report rows: every report row — pending,
// claimed, acknowledged — must land in deliveries with kind='report' and its
// outputs attached 1:1, the trigger must follow the renamed table, and the
// down path must bring the old reports shape back (stale rows fold to
// pending).
func TestMigrateReportsToDeliveryLedger(t *testing.T) {
	db := openMigratedDatabaseCopy(t, migrationVersion(t, "review_run_current_head_lookup"))

	const stamp = "2026-10-26 08:00:00"
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p1', '/tmp/p1', 'proj', ?)`, stamp); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
		VALUES ('s1', 'p1', 1, 'worker', 'exited', ?, 0, ?, ?)`, stamp, stamp, stamp); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	seedReport := func(id, deliveryState, token string) {
		t.Helper()
		claimedAt := "NULL"
		if token != "" {
			claimedAt = "'" + stamp + "'"
		}
		acknowledgedAt := "NULL"
		if deliveryState == "acknowledged" {
			acknowledgedAt = "'" + stamp + "'"
		}
		if _, err := db.Exec(`INSERT INTO reports (id, session_id, project_id, state, note, message, created_at, delivery_state, available_at, repeat_count, claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error, delivery_batch_id)
			VALUES ('` + id + `', 's1', 'p1', '', '', 'context', '` + stamp + `', '` + deliveryState + `', '` + stamp + `', 1, '` + token + `', ` + claimedAt + `, 1, ` + acknowledgedAt + `, '', '')`); err != nil {
			t.Fatalf("seed report %s: %v", id, err)
		}
	}
	seedReport("rpt_pending", "pending", "")
	seedReport("rpt_claimed", "claimed", "tok")
	seedReport("rpt_acked", "acknowledged", "ack")
	if _, err := db.Exec(`INSERT INTO report_outputs (report_id, position, kind, reference, label)
		VALUES ('rpt_pending', 0, 'artifact', '/tmp/out.txt', 'Result')`); err != nil {
		t.Fatalf("seed output: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate to the delivery ledger: %v", err)
	}

	var pendingCount, claimedCount, ackedCount int
	if err := db.QueryRow(`SELECT count(*) FROM deliveries WHERE kind = 'report' AND delivery_state = 'pending'`).Scan(&pendingCount); err != nil {
		t.Fatalf("read pending deliveries: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM deliveries WHERE delivery_state = 'claimed' AND claim_token = 'tok'`).Scan(&claimedCount); err != nil {
		t.Fatalf("read claimed deliveries: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM deliveries WHERE delivery_state = 'acknowledged'`).Scan(&ackedCount); err != nil {
		t.Fatalf("read acknowledged deliveries: %v", err)
	}
	if pendingCount != 1 || claimedCount != 1 || ackedCount != 1 {
		t.Fatalf("delivery states = pending %d, claimed %d, acknowledged %d", pendingCount, claimedCount, ackedCount)
	}
	if _, err := db.Exec(`SELECT recipient_session_id FROM deliveries LIMIT 1`); err != nil {
		t.Fatalf("recipient_session_id column missing: %v", err)
	}
	var reportsTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'reports'`).Scan(&reportsTables); err != nil || reportsTables != 0 {
		t.Fatalf("reports table survived the rename: count=%d err=%v", reportsTables, err)
	}
	var outputs int
	if err := db.QueryRow(`SELECT count(*) FROM report_outputs WHERE report_id = 'rpt_pending'`).Scan(&outputs); err != nil || outputs != 1 {
		t.Fatalf("outputs = %d err=%v, want 1:1", outputs, err)
	}

	// The CDC trigger follows the renamed table: a new pr_created output still
	// lands a change_log row.
	if _, err := db.Exec(`INSERT INTO report_outputs (report_id, position, kind, reference, label)
		VALUES ('rpt_pending', 1, 'pr_created', 'https://github.com/o/r/pull/1', '')`); err != nil {
		t.Fatalf("insert output through the migrated trigger: %v", err)
	}
	var cdc int
	if err := db.QueryRow(`SELECT count(*) FROM change_log WHERE project_id = 'p1'`).Scan(&cdc); err != nil {
		t.Fatalf("read change_log: %v", err)
	}
	if cdc == 0 {
		t.Fatal("migrated CDC trigger wrote no change_log row")
	}

	// Stale exists post-migration; the down path folds it back to pending.
	if _, err := db.Exec(`UPDATE deliveries SET delivery_state = 'stale' WHERE id = 'rpt_pending'`); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}
	downTo(t, db, migrationVersion(t, "review_run_current_head_lookup"))
	var restoredPending, total int
	if err := db.QueryRow(`SELECT count(*) FROM reports`).Scan(&total); err != nil {
		t.Fatalf("read reports after downgrade: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM reports WHERE delivery_state = 'pending' AND id = 'rpt_pending'`).Scan(&restoredPending); err != nil {
		t.Fatalf("read downgraded stale row: %v", err)
	}
	if total != 3 || restoredPending != 1 {
		t.Fatalf("downgrade reports = %d total, %d folded stale, want 3 and 1", total, restoredPending)
	}
}
