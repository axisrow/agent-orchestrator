package sqlite

import (
	"strings"
	"testing"
)

// TestMigrateDeliveryEventsBackfillsAndLocksTheJournal seeds pre-journal
// delivery rows, applies the delivery_events migration, and asserts the
// «строгий учёт» facts (fork issue #242): every live ledger row gets one
// backfill event mirroring its state, the journal is append-only at the
// database level, and undelivered ledger rows cannot disappear behind the
// journal.
func TestMigrateDeliveryEventsBackfillsAndLocksTheJournal(t *testing.T) {
	db := openMigratedDatabaseCopy(t, migrationVersion(t, "pr_ci_pending_since"))

	const stamp = "2026-11-01 08:00:00"
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p1', '/tmp/p1', 'proj', ?)`, stamp); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
		VALUES ('s1', 'p1', 1, 'worker', 'exited', ?, 0, ?, ?)`, stamp, stamp, stamp); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	seedDelivery := func(id, deliveryState, token string) {
		t.Helper()
		claimedAt := "NULL"
		acknowledgedAt := "NULL"
		switch deliveryState {
		case "claimed":
			claimedAt = "'" + stamp + "'"
		case "acknowledged":
			claimedAt = "'" + stamp + "'"
			acknowledgedAt = "'" + stamp + "'"
		}
		if _, err := db.Exec(`INSERT INTO deliveries (id, session_id, project_id, state, note, message, created_at, delivery_state, available_at, repeat_count, claim_token, claimed_at, delivery_attempts, acknowledged_at, last_error, delivery_batch_id)
			VALUES ('` + id + `', 's1', 'p1', '', '', 'context', '` + stamp + `', '` + deliveryState + `', '` + stamp + `', 1, '` + token + `', ` + claimedAt + `, 1, ` + acknowledgedAt + `, '', '')`); err != nil {
			t.Fatalf("seed delivery %s: %v", id, err)
		}
	}
	seedDelivery("rpt_pending", "pending", "")
	seedDelivery("rpt_claimed", "claimed", "tok")
	seedDelivery("rpt_acked", "acknowledged", "ack")

	if err := migrate(db); err != nil {
		t.Fatalf("migrate to the delivery events journal: %v", err)
	}

	// Backfill: every pre-journal row carries exactly one snapshot event whose
	// to_state mirrors the row's state, so any report's fate is reconstructable.
	var events int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_events`).Scan(&events); err != nil {
		t.Fatalf("read delivery_events: %v", err)
	}
	if events != 3 {
		t.Fatalf("backfill events = %d, want one per seeded row", events)
	}
	var pendingSnapshots int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_events WHERE delivery_id = 'rpt_pending' AND from_state = '' AND to_state = 'pending' AND cause = 'backfill'`).Scan(&pendingSnapshots); err != nil {
		t.Fatalf("read pending backfill: %v", err)
	}
	if pendingSnapshots != 1 {
		t.Fatalf("pending backfill snapshots = %d, want 1", pendingSnapshots)
	}
	if _, err := db.Exec(`SELECT id FROM delivery_events WHERE delivery_id = 'rpt_acked' AND to_state = 'acknowledged'`); err != nil {
		t.Fatalf("acknowledged backfill missing: %v", err)
	}

	// Append-only at the database level: no update, no delete.
	if _, err := db.Exec(`UPDATE delivery_events SET cause = 'rewritten' WHERE delivery_id = 'rpt_pending'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("journal update err = %v, want append-only abort", err)
	}
	if _, err := db.Exec(`DELETE FROM delivery_events WHERE delivery_id = 'rpt_pending'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("journal delete err = %v, want append-only abort", err)
	}

	// Undelivered ledger work is terminalized, never deleted; terminal rows go
	// with their parents as before.
	if _, err := db.Exec(`DELETE FROM deliveries WHERE delivery_state = 'pending'`); err == nil || !strings.Contains(err.Error(), "terminalized") {
		t.Fatalf("undelivered delete err = %v, want guard abort", err)
	}
	if _, err := db.Exec(`DELETE FROM deliveries WHERE delivery_state = 'acknowledged'`); err != nil {
		t.Fatalf("terminal delete rejected: %v", err)
	}

	// Down: the journal, its triggers, and the ledger guard all go away.
	downTo(t, db, migrationVersion(t, "pr_ci_pending_since"))
	var journalTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'delivery_events'`).Scan(&journalTables); err != nil || journalTables != 0 {
		t.Fatalf("delivery_events survived the downgrade: count=%d err=%v", journalTables, err)
	}
	var journalTriggers int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE '%delivery_events%'`).Scan(&journalTriggers); err != nil || journalTriggers != 0 {
		t.Fatalf("journal triggers survived the downgrade: count=%d err=%v", journalTriggers, err)
	}
	if _, err := db.Exec(`DELETE FROM deliveries WHERE delivery_state = 'pending'`); err != nil {
		t.Fatalf("ledger guard survived the downgrade: %v", err)
	}
}
