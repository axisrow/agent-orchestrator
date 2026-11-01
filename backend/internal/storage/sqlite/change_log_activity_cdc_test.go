package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Issue #109 reported the CDC change_log as silent: "triggers in the schema,
// zero rows per session for its whole lifetime, including session_created".
// The premise does not reproduce — on a freshly migrated database the trigger
// pipeline records session creation and every activity_state change, so
// activity history stays recoverable. This synthetic run locks that in.
func TestChangeLogRecordsSessionCreationAndActivityChanges(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) string {
		t.Helper()
		var v string
		if err := db.QueryRow(query).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	exec(`INSERT INTO projects (id, path, registered_at) VALUES ('cdc', '/cdc', CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
		VALUES ('cdc-1', 'cdc', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	if got := read(`SELECT event_type FROM change_log WHERE session_id = 'cdc-1'`); got != "session_created" {
		t.Fatalf("insert event = %q, want session_created", got)
	}

	// An activity_state change must land exactly one session_updated row whose
	// payload names the new state: the recovery fact the issue called lost.
	exec(`UPDATE sessions SET activity_state = 'active' WHERE id = 'cdc-1'`)
	if got := read(`SELECT json_extract(payload, '$.activity') FROM change_log
		WHERE session_id = 'cdc-1' AND event_type = 'session_updated'`); got != "active" {
		t.Fatalf("activity_state change payload = %q, want active", got)
	}

	// A write the CDC WHEN clause does not watch stays silent.
	exec(`UPDATE sessions SET session_permissions = 'acceptEdits' WHERE id = 'cdc-1'`)
	if got := read(`SELECT COUNT(*) FROM change_log WHERE session_id = 'cdc-1'`); got != "2" {
		t.Fatalf("change_log rows = %s, want 2 (created + activity change)", got)
	}
}
