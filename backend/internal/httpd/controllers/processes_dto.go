package controllers

import "time"

// Wire shapes for the /system/processes surface. Kept beside the controller
// (not in dto.go) per the codex_accounts_dto.go precedent: the spec reflects
// these types verbatim.

type ProcessGroupSummaryDTO struct {
	PID      int   `json:"pid"`
	Present  bool  `json:"present"`
	RSSBytes int64 `json:"rssBytes"`
}

type ProcessTreeDTO struct {
	SessionID  string `json:"sessionId"`
	RootPID    int    `json:"rootPid"`
	RootLstart string `json:"rootLstart"`
	PIDCount   int    `json:"pidCount"`
	RSSBytes   int64  `json:"rssBytes"`
	// Kind is the DB session kind ("worker" | "orchestrator"); empty when the
	// session has no live row (orphan/foreign trees).
	Kind string `json:"kind"`
	// State is owned | orphan | foreign. Only orphan trees are killable.
	State string `json:"state"`
	// Attached reports whether the root's parent is this daemon; an owned tree
	// with attached=false was adopted after a daemon restart.
	Attached bool `json:"attached"`
}

type ProcessRemnantDTO struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	RSSBytes  int64  `json:"rssBytes"`
}

type ProcessTotalsDTO struct {
	SessionsCount    int   `json:"sessionsCount"`
	SessionsRSSBytes int64 `json:"sessionsRssBytes"`
	OrphansCount     int   `json:"orphansCount"`
	OrphansRSSBytes  int64 `json:"orphansRssBytes"`
	ForeignCount     int   `json:"foreignCount"`
	ForeignRSSBytes  int64 `json:"foreignRssBytes"`
	DaemonRSSBytes   int64 `json:"daemonRssBytes"`
	TmuxRSSBytes     int64 `json:"tmuxRssBytes"`
}

type ProcessInventoryResponse struct {
	GeneratedAt time.Time              `json:"generatedAt"`
	Daemon      ProcessGroupSummaryDTO `json:"daemon"`
	Tmux        ProcessGroupSummaryDTO `json:"tmux"`
	Trees       []ProcessTreeDTO       `json:"trees"`
	Remnants    []ProcessRemnantDTO    `json:"remnants"`
	Totals      ProcessTotalsDTO       `json:"totals"`
}

type ProcessKillTargetDTO struct {
	SessionID  string `json:"sessionId"`
	RootPID    int    `json:"rootPid"`
	RootLstart string `json:"rootLstart"`
}

type ProcessKillRequest struct {
	Targets []ProcessKillTargetDTO `json:"targets"`
}

type ProcessKillResultDTO struct {
	SessionID string `json:"sessionId"`
	RootPID   int    `json:"rootPid"`
	// Status is killed | already_gone | skipped | failed. Skipped means the
	// tree drifted out of orphan state between render and confirm — normal,
	// not an error.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type ProcessKillResponse struct {
	Results []ProcessKillResultDTO `json:"results"`
}
