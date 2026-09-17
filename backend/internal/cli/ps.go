package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Mirrored wire shapes for GET /api/v1/system/processes (fork delta). Kept
// local per the mirror-don't-import convention; the daemon endpoint is the
// single source of truth.

type processGroupSummaryDTO struct {
	PID      int   `json:"pid"`
	Present  bool  `json:"present"`
	RSSBytes int64 `json:"rssBytes"`
}

type processTreeDTO struct {
	SessionID  string `json:"sessionId"`
	RootPID    int    `json:"rootPid"`
	RootLstart string `json:"rootLstart"`
	PIDCount   int    `json:"pidCount"`
	RSSBytes   int64  `json:"rssBytes"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Attached   bool   `json:"attached"`
}

type processRemnantDTO struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	RSSBytes  int64  `json:"rssBytes"`
}

type processTotalsDTO struct {
	SessionsCount    int   `json:"sessionsCount"`
	SessionsRSSBytes int64 `json:"sessionsRssBytes"`
	OrphansCount     int   `json:"orphansCount"`
	OrphansRSSBytes  int64 `json:"orphansRssBytes"`
	ForeignCount     int   `json:"foreignCount"`
	ForeignRSSBytes  int64 `json:"foreignRssBytes"`
	DaemonRSSBytes   int64 `json:"daemonRssBytes"`
	TmuxRSSBytes     int64 `json:"tmuxRssBytes"`
}

type processInventoryResponse struct {
	GeneratedAt string                 `json:"generatedAt"`
	Daemon      processGroupSummaryDTO `json:"daemon"`
	Tmux        processGroupSummaryDTO `json:"tmux"`
	Trees       []processTreeDTO       `json:"trees"`
	Remnants    []processRemnantDTO    `json:"remnants"`
	Totals      processTotalsDTO       `json:"totals"`
}

func newPsCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "Show the AO process footprint (daemon, tmux server, session trees, orphans)",
		Long: "Snapshot every AO-owned process tree with resident memory: the daemon, the tmux " +
			"server, live session trees, and orphaned trees left behind by a daemon that exited " +
			"without cleaning up. Read-only — kill orphaned trees from the AO app's status bar.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var res processInventoryResponse
			if err := ctx.getJSON(cmd.Context(), "system/processes", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeProcessInventory(cmd, res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the inventory as JSON")
	return cmd
}

func writeProcessInventory(cmd *cobra.Command, res processInventoryResponse) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "daemon       PID %d   RSS %s\n", res.Daemon.PID, formatBytesCLI(res.Daemon.RSSBytes)); err != nil {
		return err
	}
	tmux := "not running"
	if res.Tmux.Present {
		tmux = fmt.Sprintf("PID %d   RSS %s", res.Tmux.PID, formatBytesCLI(res.Tmux.RSSBytes))
	}
	if _, err := fmt.Fprintf(out, "tmux server  %s\n\n", tmux); err != nil {
		return err
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	owned, orphans, foreign := splitTrees(res.Trees)
	if _, err := fmt.Fprintf(table, "sessions (%d, %s)\n", res.Totals.SessionsCount, formatBytesCLI(res.Totals.SessionsRSSBytes)); err != nil {
		return err
	}
	if err := writeTreeTable(table, owned); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(table, "\norphaned trees (%d, %s)\n", res.Totals.OrphansCount, formatBytesCLI(res.Totals.OrphansRSSBytes)); err != nil {
		return err
	}
	if err := writeTreeTable(table, orphans); err != nil {
		return err
	}
	if err := table.Flush(); err != nil {
		return err
	}

	if len(foreign) > 0 {
		if _, err := fmt.Fprintf(out, "\n%d foreign tree(s) (%s) managed by another live AO daemon — not shown, not killable.\n", res.Totals.ForeignCount, formatBytesCLI(res.Totals.ForeignRSSBytes)); err != nil {
			return err
		}
	}
	if res.Totals.OrphansCount == 0 {
		_, err := fmt.Fprintln(out, "\nNo orphaned trees. `ao ps` is read-only; kill orphans from the AO app status bar.")
		return err
	}
	_, err := fmt.Fprintf(out, "\n%d orphaned tree(s) using ~%s. `ao ps` is read-only — kill them from the AO app status bar.\n", res.Totals.OrphansCount, formatBytesCLI(res.Totals.OrphansRSSBytes))
	return err
}

func writeTreeTable(table *tabwriter.Writer, trees []processTreeDTO) error {
	if len(trees) == 0 {
		_, err := fmt.Fprintln(table, "  (none)")
		return err
	}
	if _, err := fmt.Fprintln(table, "  SESSION\tROOT PID\tPROCS\tRSS\tKIND\tSTATE"); err != nil {
		return err
	}
	for _, tree := range trees {
		attached := ""
		if tree.State == "owned" && !tree.Attached {
			attached = " (adopted)"
		}
		line := fmt.Sprintf("  %s\t%d\t%d\t%s\t%s\t%s%s", tree.SessionID, tree.RootPID, tree.PIDCount, formatBytesCLI(tree.RSSBytes), emptyDash(tree.Kind), tree.State, attached)
		if _, err := fmt.Fprintln(table, line); err != nil {
			return err
		}
	}
	return nil
}

func splitTrees(trees []processTreeDTO) (owned, orphans, foreign []processTreeDTO) {
	for _, tree := range trees {
		switch tree.State {
		case "owned":
			owned = append(owned, tree)
		case "orphan":
			orphans = append(orphans, tree)
		case "foreign":
			foreign = append(foreign, tree)
		}
	}
	return owned, orphans, foreign
}

// formatBytesCLI renders a byte count the way the desktop status bar does:
// binary units, one decimal below 100.
func formatBytesCLI(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	units := []string{"KB", "MB", "GB", "TB"}
	divisions := 0
	for value >= unit && divisions < len(units) {
		value /= unit
		divisions++
	}
	decimals := 1
	if value >= 100 {
		decimals = 0
	}
	number := strconv.FormatFloat(value, 'f', decimals, 64)
	number = strings.TrimRight(strings.TrimRight(number, "0"), ".")
	return number + " " + units[divisions-1]
}
