package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// migrationVersion resolves a migration's goose version from the distinctive
// part of its filename.
//
// Fork migrations get renumbered on most upstream syncs, because upstream keeps
// claiming the next free number for its own migrations. A test that hardcodes
// the number has to be edited in lockstep with every rename — and a test that
// silently keeps migrating to the *old* number still passes while exercising
// the wrong schema. Looking the version up by name removes both failure modes:
// the number becomes an implementation detail of the filename.
func migrationVersion(t *testing.T, nameSuffix string) int64 {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var found string
	for _, e := range entries {
		// Match on the suffix after the numeric prefix so the lookup is not
		// itself sensitive to the number it is meant to discover.
		if _, rest, ok := strings.Cut(e.Name(), "_"); ok && rest == nameSuffix+".sql" {
			if found != "" {
				t.Fatalf("migrations %q and %q both end in %q", found, e.Name(), nameSuffix)
			}
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatalf("no migration ending in %q; was it renamed?", nameSuffix)
	}
	version, err := goose.NumericComponent(found)
	if err != nil {
		t.Fatalf("parse version from %q: %v", found, err)
	}
	return version
}

// Sessions whose activity was last written from a local-zone clock carry the
// zone (and sometimes a monotonic reading) in the column, because the driver
// stores a time.Time by its String() form. activity_last_at is compared
// directly in SQL, so such a row stops behaving like a timestamp: a "+0800"
// wall clock sorts above the UTC rendering of a later instant, and the
// agent-switch source-stop predicate then matches zero rows and strands the
// saga. The normalize_activity_last_at migration rewrites those rows to the
// canonical UTC form.
func TestNormalizeActivityLastAtMigrationRewritesLocalZoneTimestamps(t *testing.T) {
	db := openTestDB(t)

	version := migrationVersion(t, "normalize_activity_last_at")

	// 87 is the schema point that seeds below need (projects and sessions
	// exist, activity_last_at is not yet normalized) — a fixed upstream
	// version, deliberately not derived from the migration under test.
	upTo(t, db, 87)

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at)
		VALUES ('p1', '/tmp/p1', 'proj', ?)`, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	seed := []struct {
		id      string
		written string
		want    string
	}{
		// Monotonic reading and an east-of-UTC zone: the shape a bare time.Now()
		// leaves behind, and the one that reproduced the stranded switch.
		{"ao-1", "2026-06-28 18:45:08.349363 +0800 CST m=+25660.013723251", "2026-06-28 10:45:08.349363 +0000 UTC"},
		// Same, with the fractional second one digit shorter — Go trims trailing
		// zeros, which moves the offset's position in the string.
		{"ao-2", "2026-07-02 18:05:06.42722 +0800 CST m=+3515.018840501", "2026-07-02 10:05:06.42722 +0000 UTC"},
		// Local zone without any monotonic reading; equally uncomparable.
		{"ao-3", "2026-08-12 22:14:57.047745 +0700 +07", "2026-08-12 15:14:57.047745 +0000 UTC"},
		// Crossing back over midnight.
		{"ao-4", "2026-07-27 07:03:53.393954 +0800 CST m=+50790.035115335", "2026-07-26 23:03:53.393954 +0000 UTC"},
		// West of UTC shifts forward instead.
		{"ao-5", "2026-07-27 07:03:53.393954 -0500 EST m=+50790.035115335", "2026-07-27 12:03:53.393954 +0000 UTC"},
		// Already canonical: must be left byte-for-byte alone.
		{"ao-6", "2026-07-27 07:03:53.393954 +0000 UTC", "2026-07-27 07:03:53.393954 +0000 UTC"},
	}
	for n, s := range seed {
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
			VALUES (?, 'p1', ?, 'worker', 'exited', ?, 0, ?, ?)`, s.id, n+1, s.written, now, now); err != nil {
			t.Fatalf("seed session %s: %v", s.id, err)
		}
	}

	upTo(t, db, version)

	// CAST to TEXT to see the stored bytes: scanning the column into a string
	// lets the driver re-render it as RFC 3339, which would hide the very
	// difference under test.
	for _, s := range seed {
		var got string
		if err := db.QueryRow(`SELECT CAST(activity_last_at AS TEXT) FROM sessions WHERE id = ?`, s.id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", s.id, err)
		}
		if got != s.want {
			t.Errorf("%s activity_last_at = %q, want %q", s.id, got, s.want)
		}
	}

	// The normalized column has to compare as a timestamp again: this is the
	// predicate the agent-switch source stop runs, and the value it compares
	// against is a later instant in UTC.
	var matching int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE activity_last_at <= '2026-08-12 16:00:00.000000 +0000 UTC'`,
	).Scan(&matching); err != nil {
		t.Fatal(err)
	}
	if matching != len(seed) {
		t.Errorf("rows comparing as timestamps = %d, want %d", matching, len(seed))
	}
}
