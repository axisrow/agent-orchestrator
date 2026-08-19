package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// restartAllOptions collects the flags of `ao session restart-all`.
type restartAllOptions struct {
	project              string
	json                 bool
	includeOrchestrators bool
	orchestratorsOnly    bool
	exclude              []string
	self                 string
	dryRun               bool
	yes                  bool
	settleDelay          time.Duration
}

// restartOutcome is the per-session result of a kill+restore round trip.
type restartOutcome struct {
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

const (
	restartStatusRestarted = "restarted"
	restartStatusPreserved = "restarted (workspace preserved)"
	restartStatusSkipped   = "skipped"
	restartStatusFailed    = "failed"
	restartStatusPlanned   = "planned"
)

type restartAllOutput struct {
	Data []restartOutcome `json:"data"`
	Meta struct {
		Total     int `json:"total"`
		Restarted int `json:"restarted"`
		Skipped   int `json:"skipped"`
		Failed    int `json:"failed"`
	} `json:"meta"`
}

func newSessionRestartAllCommand(ctx *commandContext) *cobra.Command {
	var opts restartAllOptions
	cmd := &cobra.Command{
		Use:   "restart-all",
		Short: "Kill and restore every live session so it relaunches on the current agent binary",
		Long: `Kill and restore every live session in sequence.

A running agent holds the inode of its binary, so a new agent-CLI build is only
picked up by a fresh exec. This command performs the supported kill+restore round
trip for each live session, which relaunches it through the same restore path used
at daemon boot (native --resume where a transcript exists, so history is kept).

Workers are restarted by default. Orchestrators are skipped unless
--include-orchestrators (both) or --orchestrators-only (just them) is passed, since
killing an orchestrator interrupts the work it is coordinating.

The invoking session is always excluded: killing it would terminate this very
process mid-run. It is detected through AO_SESSION_ID.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.restartAllSessions(cmd.Context(), cmd, opts)
		},
	}
	flags := cmd.Flags()
	addSessionProjectFlag(flags, &opts.project, "Restart only sessions of this project")
	flags.BoolVar(&opts.json, "json", false, "Emit machine-readable JSON")
	flags.BoolVar(&opts.includeOrchestrators, "include-orchestrators", false,
		"Also restart orchestrator sessions (skipped by default)")
	flags.BoolVar(&opts.orchestratorsOnly, "orchestrators-only", false,
		"Restart only orchestrator sessions")
	flags.StringSliceVar(&opts.exclude, "exclude", nil,
		"Session ids to leave running (repeatable, or comma-separated)")
	flags.StringVar(&opts.self, "self", "",
		"Id of the session running this command; excluded from the restart (default: AO_SESSION_ID)")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "List what would be restarted and exit")
	flags.BoolVarP(&opts.yes, "yes", "y", false, "Do not ask for confirmation")
	flags.DurationVar(&opts.settleDelay, "settle-delay", 2*time.Second,
		"Pause between killing a session and restoring it")
	return cmd
}

func (c *commandContext) restartAllSessions(ctx context.Context, cmd *cobra.Command, opts restartAllOptions) error {
	if opts.includeOrchestrators && opts.orchestratorsOnly {
		return usageError{errors.New("--include-orchestrators and --orchestrators-only are mutually exclusive")}
	}

	targets, self, err := c.restartAllTargets(ctx, opts)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(targets) == 0 {
		if opts.json {
			return writeJSON(out, restartAllOutput{Data: []restartOutcome{}})
		}
		_, err := fmt.Fprintln(out, "no live sessions match the selection")
		return err
	}

	// Without a known self-id we cannot guarantee this very session stays out of
	// the list, so make that explicit rather than discovering it by being killed.
	if self == "" && !opts.json {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"warning: AO_SESSION_ID is not set, so the calling session cannot be identified.\n"+
				"         If you are running inside an AO session, pass --self <id> (or --exclude <id>)\n"+
				"         to keep it from being killed mid-run.")
	}

	if opts.dryRun {
		return writeRestartPlan(cmd, targets, opts.json)
	}

	if !opts.yes && !opts.json {
		ok, err := confirmRestart(cmd, targets)
		if err != nil {
			return err
		}
		if !ok {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}

	results := c.runRestartAll(ctx, cmd, targets, opts)
	return writeRestartResults(cmd, results, opts.json)
}

// restartAllTargets resolves the live sessions selected by opts, in a stable order.
func (c *commandContext) restartAllTargets(ctx context.Context, opts restartAllOptions) ([]sessionDTO, string, error) {
	params := url.Values{}
	if opts.project != "" {
		params.Set("project", opts.project)
	}
	params.Set("active", "true")

	var res sessionListResponse
	if err := c.getJSON(ctx, apiPath("sessions", params), &res); err != nil {
		return nil, "", err
	}

	excluded := make(map[string]struct{}, len(opts.exclude)+1)
	for _, raw := range opts.exclude {
		if id := strings.TrimSpace(raw); id != "" {
			excluded[id] = struct{}{}
		}
	}
	// Never restart the session this command runs in: the kill would take down
	// the very process issuing the restore that follows it.
	self := strings.TrimSpace(opts.self)
	if self == "" {
		self = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	}
	if self != "" {
		excluded[self] = struct{}{}
	}

	targets := make([]sessionDTO, 0, len(res.Sessions))
	for _, sess := range res.Sessions {
		if sess.IsTerminated {
			continue
		}
		if _, skip := excluded[sess.ID]; skip {
			continue
		}
		isOrchestrator := sess.Kind == "orchestrator"
		switch {
		case opts.orchestratorsOnly && !isOrchestrator:
			continue
		case !opts.orchestratorsOnly && !opts.includeOrchestrators && isOrchestrator:
			continue
		}
		targets = append(targets, sess)
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ProjectID != targets[j].ProjectID {
			return targets[i].ProjectID < targets[j].ProjectID
		}
		return targets[i].ID < targets[j].ID
	})
	return targets, self, nil
}

// runRestartAll performs the kill+restore round trip for each target in order.
//
// The sequence is deliberately serial: restore recreates the tmux session under a
// deterministic name, so the old one has to be reaped first, and each call also
// does git-worktree work. RestoreAll in the daemon is serial for the same reason.
func (c *commandContext) runRestartAll(ctx context.Context, cmd *cobra.Command, targets []sessionDTO, opts restartAllOptions) []restartOutcome {
	results := make([]restartOutcome, 0, len(targets))
	progress := cmd.ErrOrStderr()

	for i, sess := range targets {
		outcome := restartOutcome{SessionID: sess.ID, ProjectID: sess.ProjectID, Kind: sess.Kind}

		if !opts.json {
			_, _ = fmt.Fprintf(progress, "[%d/%d] %s ... ", i+1, len(targets), sess.ID)
		}

		preserved, err := c.restartKill(ctx, sess.ID)
		if err != nil {
			outcome.Status = restartStatusFailed
			outcome.Detail = "kill: " + err.Error()
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "failed (kill)")
			}
			continue
		}

		// Let the runtime finish reaping the tmux session before restore recreates
		// one under the same name.
		if opts.settleDelay > 0 {
			select {
			case <-time.After(opts.settleDelay):
			case <-ctx.Done():
				outcome.Status = restartStatusFailed
				outcome.Detail = "interrupted after kill; session left terminated"
				results = append(results, outcome)
				if !opts.json {
					_, _ = fmt.Fprintln(progress, "interrupted")
				}
				return results
			}
		}

		if err := c.restartRestore(ctx, sess.ID); err != nil {
			outcome.Status = restartStatusFailed
			outcome.Detail = "restore: " + err.Error()
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "failed (restore) — session left terminated")
			}
			continue
		}

		outcome.Status = restartStatusRestarted
		if preserved {
			outcome.Status = restartStatusPreserved
		}
		results = append(results, outcome)
		if !opts.json {
			_, _ = fmt.Fprintln(progress, "ok")
		}
	}
	return results
}

func (c *commandContext) restartKill(ctx context.Context, id string) (preserved bool, err error) {
	var res killSessionResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/kill", struct{}{}, &res); err != nil {
		return false, err
	}
	return !res.Freed, nil
}

func (c *commandContext) restartRestore(ctx context.Context, id string) error {
	var res restoreSessionResponse
	return c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/restore", struct{}{}, &res)
}

func writeRestartPlan(cmd *cobra.Command, targets []sessionDTO, asJSON bool) error {
	if asJSON {
		out := restartAllOutput{Data: make([]restartOutcome, 0, len(targets))}
		for _, sess := range targets {
			out.Data = append(out.Data, restartOutcome{
				SessionID: sess.ID,
				ProjectID: sess.ProjectID,
				Kind:      sess.Kind,
				Status:    restartStatusPlanned,
			})
		}
		out.Meta.Total = len(targets)
		return writeJSON(cmd.OutOrStdout(), out)
	}

	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "would restart %s:\n", pluralSessions(len(targets))); err != nil {
		return err
	}
	for _, sess := range targets {
		if _, err := fmt.Fprintf(w, "  %s  (%s, %s)\n", sess.ID, sess.ProjectID, sess.Kind); err != nil {
			return err
		}
	}
	return nil
}

func writeRestartResults(cmd *cobra.Command, results []restartOutcome, asJSON bool) error {
	var restarted, failed, skipped int
	for _, r := range results {
		switch r.Status {
		case restartStatusRestarted, restartStatusPreserved:
			restarted++
		case restartStatusFailed:
			failed++
		case restartStatusSkipped:
			skipped++
		}
	}

	if asJSON {
		out := restartAllOutput{Data: results}
		if out.Data == nil {
			out.Data = []restartOutcome{}
		}
		out.Meta.Total = len(results)
		out.Meta.Restarted = restarted
		out.Meta.Skipped = skipped
		out.Meta.Failed = failed
		if err := writeJSON(cmd.OutOrStdout(), out); err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d sessions failed to restart", failed, len(results))
		}
		return nil
	}

	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "restarted %d of %s\n", restarted, pluralSessions(len(results))); err != nil {
		return err
	}
	if failed == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "failures:"); err != nil {
		return err
	}
	for _, r := range results {
		if r.Status != restartStatusFailed {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", r.SessionID, r.Detail); err != nil {
			return err
		}
	}
	return fmt.Errorf("%d of %d sessions failed to restart", failed, len(results))
}

func confirmRestart(cmd *cobra.Command, targets []sessionDTO) (bool, error) {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "about to kill and restore %s:\n", pluralSessions(len(targets))); err != nil {
		return false, err
	}
	for _, sess := range targets {
		if _, err := fmt.Fprintf(w, "  %s  (%s, %s)\n", sess.ID, sess.ProjectID, sess.Kind); err != nil {
			return false, err
		}
	}
	if _, err := fmt.Fprint(w, "continue? [y/N] "); err != nil {
		return false, err
	}

	// A bare newline or a closed stdin is a decline, not a failure: the prompt
	// defaults to "no", so treat an empty read as such and report anything else.
	var answer string
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "unexpected newline") {
			return false, nil
		}
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func pluralSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
