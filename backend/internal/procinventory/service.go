package procinventory

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Signal identifies the signal the kill path delivers to a process group.
type Signal int

const (
	SigTerm Signal = iota + 1
	SigKill
)

// Service builds inventories and kills orphaned trees. All dependencies are
// injected as plain functions so tests can drive classification and kill
// without processes or SQLite; the daemon wires the real ones once.
type Service struct {
	deps   Deps
	killMu chan struct{}
}

// Deps carries the service's collaborators. Zero fields select the defaults
// documented on each; see New.
type Deps struct {
	// Scan snapshots the process table; default SystemScan (unix only).
	Scan func(ctx context.Context) ([]Entry, error)
	// DaemonPID is the calling daemon's own PID; trees rooted at its children
	// are owned, never orphans.
	DaemonPID int
	// LiveSessions returns the daemon's live session set; default: none.
	LiveSessions func(ctx context.Context) (map[domain.SessionID]domain.SessionRecord, error)
	// TmuxSocketName disambiguates the AO tmux server from a user's personal
	// one; empty means the default socket.
	TmuxSocketName string
	// Grace is how long the kill path waits between SIGTERM and SIGKILL;
	// default 5s (the tmux runtime's reap grace).
	Grace time.Duration
	// Signal delivers a signal to a process group; default signalGroup.
	Signal func(pid int, sig Signal) error
	// Probe reports whether a PID is alive; default processalive.Alive.
	Probe func(pid int) bool
	// HostStats snapshots host memory; default per-platform defaultHostStats
	// (ErrHostStatsUnsupported on windows). An error is not fatal — the
	// inventory is returned without a host section.
	HostStats func(ctx context.Context) (HostStats, error)
	// Unregister drops a killed orphan's PTY-host registry entry; the daemon
	// wires conpty's ptyregistry.Unregister. Nil means skip.
	Unregister func(ctx context.Context, sessionID string) error
	// Now is the clock; default time.Now.
	Now func() time.Time
	// Log receives kill outcomes; default slog.Default().
	Log *slog.Logger
}

// New wires a Service with defaults for unset dependencies.
func New(deps Deps) *Service {
	if deps.Scan == nil {
		deps.Scan = SystemScan
	}
	if deps.LiveSessions == nil {
		deps.LiveSessions = func(context.Context) (map[domain.SessionID]domain.SessionRecord, error) {
			return map[domain.SessionID]domain.SessionRecord{}, nil
		}
	}
	if deps.Grace <= 0 {
		deps.Grace = 5 * time.Second
	}
	if deps.Signal == nil {
		deps.Signal = signalGroup
	}
	if deps.Probe == nil {
		deps.Probe = defaultProbe
	}
	if deps.HostStats == nil {
		deps.HostStats = defaultHostStats
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, killMu: make(chan struct{}, 1)}
}

// Inventory snapshots and classifies the current process table. A host
// memory fetch failure is not an error: the inventory is returned without
// its host section (the status bar hides that section on nil).
func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	entries, err := s.deps.Scan(ctx)
	if err != nil {
		return Inventory{}, err
	}
	live, err := s.deps.LiveSessions(ctx)
	if err != nil {
		return Inventory{}, err
	}
	inv := BuildInventory(entries, live, s.deps.DaemonPID, s.deps.TmuxSocketName, s.deps.Now())
	if host, err := s.deps.HostStats(ctx); err != nil {
		s.deps.Log.Debug("procinventory: host stats unavailable", "err", err)
	} else {
		inv.Host = &host
	}
	return inv, nil
}
