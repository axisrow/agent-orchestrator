package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// forkReservedFloor is the first migration version reserved for fork-local
// migrations. Upstream numbers its migrations sequentially from 1 and will not
// reach this range, so a rebase can never make an upstream file collide with a
// fork one.
const forkReservedFloor int64 = 9000

// forkLocalMigrations lists the migrations that exist only on this fork.
// They must live at or above forkReservedFloor.
//
// Why this test exists: every sync rebase used to renumber these files onto
// whatever number was free next, and each move burned that number in
// goose_db_version on every live install. The upstream migration that later
// took the number was then skipped forever, and the daemon died at boot on a
// missing column. Measured over this fork's history:
//
//	add_user_config                       5 numbers (28, 32, 37, 41, 89)
//	normalize_activity_last_at            7 numbers (89, 95, 96, 99, 102, 103, 105)
//	conversation_provider_ownership_epochs 2 numbers (100, 101)
//
// The 0100 collision (add_user_config had held it, then session_model took it)
// is what killed a daemon with "no such column: model". Pinning fork
// migrations above forkReservedFloor removes the collision at the source
// instead of adding one schemaRepairs entry per casualty.
var forkLocalMigrations = []string{
	"conversation_provider_ownership_epochs",
	"add_user_config",
	"normalize_activity_last_at",
}

func TestForkMigrationsUseReservedRange(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		for _, fork := range forkLocalMigrations {
			if !strings.Contains(name, fork) {
				continue
			}
			found[fork] = true
			version, err := goose.NumericComponent(name)
			if err != nil {
				t.Errorf("fork migration %q has no version goose can parse: %v", name, err)
				continue
			}
			if version < forkReservedFloor {
				t.Errorf("fork-local migration %q sits at version %d, below the reserved floor %d. "+
					"Upstream will eventually claim that number, and the next sync rebase will renumber "+
					"this file — burning %d on every install that already applied it and silently skipping "+
					"the upstream migration that inherits it. Move it to %d+.",
					name, version, forkReservedFloor, version, forkReservedFloor)
			}
		}
	}

	for _, fork := range forkLocalMigrations {
		if !found[fork] {
			t.Errorf("fork-local migration %q is listed here but no file matches it: "+
				"either it was merged upstream (drop it from forkLocalMigrations) or it was lost", fork)
		}
	}
}

// TestReservedRangeIsFreeOfUpstreamMigrations is the other half of the
// contract: the reserved range only works while upstream stays out of it.
// If upstream ever ships a migration at 9000+, the reservation must move
// rather than silently start colliding again.
func TestReservedRangeIsFreeOfUpstreamMigrations(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		version, err := goose.NumericComponent(name)
		if err != nil {
			continue // covered by TestMigrationVersionsAreUnique
		}
		if version < forkReservedFloor {
			continue
		}
		isFork := false
		for _, fork := range forkLocalMigrations {
			if strings.Contains(name, fork) {
				isFork = true
				break
			}
		}
		if !isFork {
			t.Errorf("migration %q occupies reserved version %d but is not listed in forkLocalMigrations. "+
				"If upstream now ships migrations at %d+, raise forkReservedFloor; if this is a new "+
				"fork-local migration, add it to the list.", name, version, forkReservedFloor)
		}
	}
}

// forkProviderEpochsVersion is the reserved-range version of
// 9001_conversation_provider_ownership_epochs.sql. Tests that need the schema
// that migration produces reference this instead of a literal, so renumbering
// it cannot silently strand them at a version where its effect is absent —
// the failure mode that left TestMigration0101… red for weeks.
const forkProviderEpochsVersion int64 = 9001

// TestForkRenumberMoveKeepsExistingRows guards the data-loss path in the
// reserved-range move. A database that applied conversation_provider_ownership_epochs
// under its old number (100/101/103) must NOT re-run it at 9001: that migration
// rebuilds conversation_branches into a new table, so a replay would drop every
// row written since. repairForkMigrationRenumbering records 9001 as applied
// when the schema already carries its effect; without that, this test loses the
// seeded branch.
func TestForkRenumberMoveKeepsExistingRows(t *testing.T) {
	db := openTestDB(t)
	upTo(t, db, forkProviderEpochsVersion)

	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO projects (id, path, display_name, registered_at)
VALUES ('reserved-move', '/tmp/reserved-move', 'reserved move', ?);
INSERT INTO sessions (
    id, project_id, num, harness, session_mode, activity_last_at,
    provider_conversation_id, created_at, updated_at
) VALUES ('reserved-move-1', 'reserved-move', 1, 'claude-code', 'chat', ?, '', ?, ?);
INSERT INTO conversations (
    id, scope, project_id, session_id, current_session_id,
    active_branch_id, created_at, updated_at
) VALUES ('conv-rm', 'session', 'reserved-move', 'reserved-move-1',
    'reserved-move-1', 'conv-rm:root', ?, ?);
INSERT INTO conversation_branches (
    id, conversation_id, session_id, provider_conversation_id,
    fork_after_sequence, created_at
) VALUES ('conv-rm:root', 'conv-rm', 'reserved-move-1', 'native-rm', 0, ?);`,
		now, now, now, now, now, now, now)

	// Simulate the pre-move history: the migration was applied under an old
	// number, and 9001 has never been recorded.
	mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id = ?`, forkProviderEpochsVersion)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (103, 1)`)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate after the reserved-range move: %v", err)
	}

	var branches int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM conversation_branches WHERE id = 'conv-rm:root'`,
	).Scan(&branches); err != nil {
		t.Fatalf("count branches: %v", err)
	}
	if branches != 1 {
		t.Errorf("conversation_branches row was lost (found %d): version %d was re-run on a database "+
			"that already had its effect, rebuilding the table and dropping existing rows",
			branches, forkProviderEpochsVersion)
	}
}
