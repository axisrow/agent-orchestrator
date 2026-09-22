package procinventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Regression: a scan that overruns its budget (thrashing machine, fork
// storm) must serve the last good inventory instead of hanging the caller.
func TestServiceInventory_ServesStaleOnScanOverrun(t *testing.T) {
	scanCount := 0
	svc := New(Deps{
		Scan: func(context.Context) ([]Entry, error) {
			scanCount++
			if scanCount == 1 {
				entries, err := ParseTable(psTable([]Entry{
					{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
				}))
				return entries, err
			}
			return nil, context.DeadlineExceeded
		},
		DaemonPID: testDaemonPID,
		Now:       func() time.Time { return time.Unix(1758000000+int64(scanCount)*10, 0) },
	})

	first, err := svc.Inventory(context.Background())
	if err != nil {
		t.Fatalf("first Inventory: %v", err)
	}
	if first.Totals.DaemonRSSBytes == 0 {
		t.Fatalf("first inventory empty")
	}

	// Second call: scan overruns -> stale inventory served, no error.
	stale, err := svc.Inventory(context.Background())
	if err != nil {
		t.Fatalf("second Inventory: %v", err)
	}
	if stale.Totals.DaemonRSSBytes != first.Totals.DaemonRSSBytes {
		t.Fatalf("stale inventory differs from last good")
	}
}

// The Settings toggle gates the whole surface at runtime: both Inventory and
// Kill answer ErrDisabled without touching a single process.
func TestServiceDisabled(t *testing.T) {
	scans := 0
	svc := New(Deps{
		Scan: func(context.Context) ([]Entry, error) {
			scans++
			return nil, nil
		},
		Enabled: func(context.Context) bool { return false },
	})

	if _, err := svc.Inventory(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Inventory err = %v, want ErrDisabled", err)
	}
	report, err := svc.Kill(context.Background(), []KillTarget{{SessionID: "s", RootPID: 1}})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Kill err = %v, want ErrDisabled", err)
	}
	if len(report.Results) != 0 {
		t.Fatalf("kill results = %+v, want empty", report.Results)
	}
	if scans != 0 {
		t.Fatalf("scan ran %d times while disabled, want 0", scans)
	}
}
