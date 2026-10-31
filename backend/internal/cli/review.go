package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	reviewSubmitRetryWindow   = 30 * time.Second
	reviewSubmitRetryInterval = 250 * time.Millisecond
)

// reviewRun mirrors the daemon's domain.ReviewRun for the CLI client.
type reviewRun struct {
	ID                string     `json:"id"`
	ReviewID          string     `json:"reviewId"`
	SessionID         string     `json:"sessionId"`
	BatchID           string     `json:"batchId"`
	Harness           string     `json:"harness"`
	PRURL             string     `json:"prUrl"`
	TargetSHA         string     `json:"targetSha"`
	Status            string     `json:"status"`
	Verdict           string     `json:"verdict"`
	TriggerSource     string     `json:"triggerSource,omitempty"`
	Body              string     `json:"body"`
	PublishState      string     `json:"publishState"`
	PublishError      string     `json:"publishError,omitempty"`
	GithubReviewID    string     `json:"githubReviewId"`
	GithubReviewState string     `json:"githubReviewState"`
	CreatedAt         time.Time  `json:"createdAt"`
	DeliveredAt       *time.Time `json:"deliveredAt,omitempty"`
}

type reviewState struct {
	PRURL       string     `json:"prUrl"`
	PRNumber    int        `json:"prNumber"`
	Title       string     `json:"title"`
	TargetSHA   string     `json:"targetSha"`
	Status      string     `json:"status"`
	LatestRun   *reviewRun `json:"latestRun,omitempty"`
	PreviousRun *reviewRun `json:"previousRun,omitempty"`
}

type listReviewsResponse struct {
	ReviewerHandleID string        `json:"reviewerHandleId"`
	Reviews          []reviewState `json:"reviews"`
	Runs             []reviewRun   `json:"runs"`
}

// triggerReviewResponse mirrors controllers.TriggerReviewResponse: Created
// reports whether a new pass started, and the runs name which reviewer and
// commit it covers.
type triggerReviewResponse struct {
	Created           bool          `json:"created"`
	AutoInjectEnabled bool          `json:"autoInjectEnabled"`
	Runs              []reviewRun   `json:"runs"`
	Reviews           []reviewState `json:"reviews"`
}

// triggerReviewRequest mirrors controllers.TriggerReviewRequest.
type triggerReviewRequest struct {
	Harness            string             `json:"harness,omitempty"`
	AgentConfig        *reviewAgentConfig `json:"agentConfig,omitempty"`
	PRURL              string             `json:"prUrl,omitempty"`
	Source             string             `json:"source,omitempty"`
	RejectReviewedHead bool               `json:"rejectReviewedHead,omitempty"`
	Rerun              bool               `json:"rerun,omitempty"`
	EnableAutoInject   bool               `json:"enableAutoInject,omitempty"`
}

// reviewAgentConfig mirrors the model/effort subset of domain.AgentConfig a
// reviewer pass may override.
type reviewAgentConfig struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// reviewRunResponse mirrors controllers.ReviewRunResponse.
type reviewRunResponse struct {
	Review           reviewRun   `json:"review"`
	Reviews          []reviewRun `json:"reviews"`
	ReviewerHandleID string      `json:"reviewerHandleId"`
}

// submitReviewComment is one inline finding in a submit request.
type submitReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// submitReviewItem mirrors controllers.SubmitReviewItem.
type submitReviewItem struct {
	RunID          string `json:"runId"`
	Verdict        string `json:"verdict"`
	Body           string `json:"body,omitempty"`
	GithubReviewID string `json:"githubReviewId,omitempty"`
}

// submitReviewRequest mirrors controllers.SubmitReviewInput.
type submitReviewRequest struct {
	RunID          string                `json:"runId,omitempty"`
	Verdict        string                `json:"verdict,omitempty"`
	Body           string                `json:"body,omitempty"`
	GithubReviewID string                `json:"githubReviewId,omitempty"`
	Comments       []submitReviewComment `json:"comments,omitempty"`
	Reviews        []submitReviewItem    `json:"reviews,omitempty"`
}

type reviewSubmitOptions struct {
	session      string
	runID        string
	verdict      string
	body         string
	bodyFile     string
	reviewID     string
	reviews      string
	commentPaths []string
	commentLines []int
	commentBodys []string
}

type reviewSessionOptions struct {
	session string
}

type reviewTriggerOptions struct {
	session  string
	prURL    string
	harness  string
	model    string
	effort   string
	rerun    bool
	noInject bool
}

// envReviewSessionID is set only inside reviewer panes (see
// review.agentLauncher.runtimeEnv). Reviewers review; they never start reviews.
const envReviewSessionID = "AO_REVIEW_SESSION_ID"

// reviewTargetSession resolves the worker session a review command acts on:
// the explicit argument or --session, otherwise the calling AO session.
func reviewTargetSession(args []string, flag string) string {
	if len(args) == 1 {
		return strings.TrimSpace(args[0])
	}
	if session := strings.TrimSpace(flag); session != "" {
		return session
	}
	return strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
}

type reviewListOptions struct {
	json bool
}

func newReviewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage AO code reviews of a worker's PR",
	}
	cmd.AddCommand(newReviewListCommand(ctx))
	cmd.AddCommand(newReviewSubmitCommand(ctx))
	cmd.AddCommand(newReviewCancelCommand(ctx))
	cmd.AddCommand(newReviewTriggerCommand(ctx))
	cmd.AddCommand(newReviewPRCommand(ctx))
	return cmd
}

// Review modes of the unified PR review entrypoint. Own PRs get a full
// implementation worker whose review findings loop back as fixes; foreign PRs
// get a review-only session that cannot mutate code.
const (
	reviewModeOwn     = "own"
	reviewModeForeign = "foreign"
)

type reviewPROptions struct {
	project    string
	mode       string
	name       string
	noTakeover bool
}

// ghPRFacts carries the provider metadata ownership detection needs. Author is
// an object in `gh pr view --json author`; only the login is read.
type ghPRFacts struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

func newReviewPRCommand(ctx *commandContext) *cobra.Command {
	var opts reviewPROptions
	cmd := &cobra.Command{
		Use:   "pr <pr-ref>",
		Short: "One entry point to review a PR: spawns the worker, claims own PRs, starts the reviewer",
		Long: `Review a pull request through one entry point that branches by ownership.

The PR repository is compared against the project's canonical repo (see
` + "`ao project set-config --canonical-repo-url`" + `), and its author and repository
owner against the project's own accounts (` + "`--own-account`" + `). A match spawns a
full implementation worker (review+fix): the PR is claimed, and when the project
has auto-review enabled the AO reviewer is triggered, its inline findings are
delivered to the worker, and the worker pushes fixes until approval. Anything
else spawns a review-only session (foreign): the launch denies file-editing and
push tools, nothing is claimed, and the reviewer's verdict is published on
GitHub with no fix loop.

Pass --mode own or --mode foreign to override detection. The active mode is
stated in the spawn output, the session prompt, and ` + "`ao session get`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.reviewPR(cmd, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.project, "project", "", "Project id to spawn the review session in (default: AO_PROJECT_ID or the current registered repo)")
	cmd.Flags().StringVar(&opts.mode, "mode", "", "Force the review mode: own (review+fix) or foreign (review-only; default: detect by ownership)")
	cmd.Flags().StringVar(&opts.name, "name", "", "Display name for the spawned session (default: derived from the PR title)")
	cmd.Flags().BoolVar(&opts.noTakeover, "no-takeover", false, "Own mode: refuse if another active session owns the PR")
	return cmd
}

func (c *commandContext) reviewPR(cmd *cobra.Command, ref string, opts reviewPROptions) error {
	switch opts.mode {
	case "", reviewModeOwn, reviewModeForeign:
	default:
		return usageError{fmt.Errorf(`--mode must be "own" or "foreign"`)}
	}
	if opts.noTakeover && opts.mode == reviewModeForeign {
		return usageError{fmt.Errorf("--no-takeover applies to own PRs only")}
	}
	project, err := c.resolveSpawnProject(cmd.Context(), opts.project)
	if err != nil {
		return err
	}
	if isScratchProject(project) {
		return usageError{fmt.Errorf("scratch projects do not support PR review")}
	}
	prURL, err := c.resolvePRRef(cmd.Context(), ref, project)
	if err != nil {
		return err
	}
	// One gh lookup in the project checkout provides every ownership fact: the
	// PR's repository, its author, and a display title.
	raw, err := c.deps.CommandOutputInDir(cmd.Context(), project.Path, "gh", "pr", "view", prURL, "--json", "url,number,title,author")
	if err != nil {
		return fmt.Errorf("gh could not read PR %s: %w", prURL, err)
	}
	var facts ghPRFacts
	if err := json.Unmarshal(raw, &facts); err != nil || strings.TrimSpace(facts.URL) == "" {
		return fmt.Errorf("gh returned unreadable PR metadata for %s", prURL)
	}
	var config projectConfig
	if project.Config != nil {
		config = *project.Config
	}
	mode := detectReviewMode(opts.mode, config.CanonicalRepoURL, project.Repo, config.OwnAccounts, facts.URL, facts.Author.Login)
	name := strings.TrimSpace(opts.name)
	if name == "" {
		name = formatReviewSessionName(facts.Title, facts.Number, mode)
	}

	prompt := reviewSessionPrompt(mode, prURL, facts.URL)
	req := spawnRequest{
		ProjectID:   project.ID,
		Kind:        "worker",
		Harness:     config.Worker.Agent,
		Mode:        "tui",
		Prompt:      prompt,
		DisplayName: name,
		ReviewOnly:  mode == reviewModeForeign,
	}
	var res spawnResult
	if err := c.postSetupJSON(cmd.Context(), "sessions", req, &res); err != nil {
		return err
	}
	if strings.TrimSpace(res.Session.ID) == "" {
		return fmt.Errorf("daemon returned empty session id for review spawn")
	}

	out := cmd.OutOrStdout()
	modeLabel := "review+fix (own)"
	if mode == reviewModeForeign {
		modeLabel = "review-only (foreign)"
	}
	if _, err := fmt.Fprintf(out, "spawned %s review session %s %q for %s\n", mode, res.Session.ID, name, prURL); err != nil {
		return err
	}

	if mode == reviewModeOwn {
		var claim claimPRResponse
		if err := c.postJSON(cmd.Context(), "sessions/"+url.PathEscape(res.Session.ID)+"/pr/claim", claimPRRequest{PR: prURL, AllowTakeover: !opts.noTakeover}, &claim); err != nil {
			if killErr := c.rollbackSpawnedSession(cmd.Context(), res.Session.ID); killErr != nil {
				return fmt.Errorf("failed to claim PR %s: %w; rollback of session %s failed: %w", prURL, err, res.Session.ID, killErr)
			}
			return fmt.Errorf("failed to claim PR %s: %w; rolled back session %s", prURL, err, res.Session.ID)
		}
		claimed := prURL
		if len(claim.PRs) > 0 && claim.PRs[0].URL != "" {
			claimed = claim.PRs[0].URL
		}
		if _, err := fmt.Fprintf(out, "claimed %s\n", claimed); err != nil {
			return err
		}
		if !config.AutoReview {
			_, err := fmt.Fprintf(out, "project auto-review is off; start the reviewer with `ao review trigger %s`\n", res.Session.ID)
			return err
		}
	} else if _, err := fmt.Fprintf(out, "mode %s: editing tools are denied on this session; nothing will be claimed or pushed\n", modeLabel); err != nil {
		return err
	}

	// Own mode: the reviewer's inline findings must reach the worker (the fix
	// loop), so auto-inject stays on. Foreign mode: findings stay in the
	// session's review record (--no-inject), and only the verdict is published.
	triggerOpts := reviewTriggerOptions{
		session:  res.Session.ID,
		prURL:    prURL,
		noInject: mode == reviewModeForeign,
	}
	if err := c.triggerReview(cmd, nil, triggerOpts); err != nil {
		// The auto-review coordinator may have started the same pass seconds
		// earlier; an already-running or already-reviewed head is success for
		// the entrypoint, not an error.
		if isAlreadyReviewedError(err) {
			_, err := fmt.Fprintf(out, "reviewer already covering %s\n", prURL)
			return err
		}
		return err
	}
	_, err = fmt.Fprintf(out, "mode: %s\n", modeLabel)
	return err
}

// isAlreadyReviewedError reports whether a review trigger failed only because
// the head is already being reviewed or already has a review.
func isAlreadyReviewedError(err error) bool {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorBody.Code {
	case "REVIEW_ALREADY_RUNNING", "REVIEW_HEAD_ALREADY_REVIEWED":
		return true
	}
	return false
}

// detectReviewMode resolves the unified entrypoint's review mode. The manual
// override wins. Otherwise the PR is own when its repository matches the
// project's canonical repo (or the checkout origin when canonical is unset),
// or when its author or repository owner is one of the project's own
// accounts. An empty ownAccounts list keeps the repository match as the only
// ownership rule, and anything else — including an unparsable repository —
// resolves to the foreign-safe default.
func detectReviewMode(override, canonicalRepo, originRepo string, ownAccounts []string, prRepoURL, prAuthor string) string {
	switch override {
	case reviewModeOwn, reviewModeForeign:
		return override
	}
	prHost, prOwner, prName, prOK := cliRepoIdentityFromPR(prRepoURL)
	for _, own := range []string{canonicalRepo, originRepo} {
		if !prOK {
			break
		}
		host, owner, name, err := cliRepoFromURL(own)
		if err != nil {
			continue
		}
		if strings.EqualFold(host, prHost) && strings.EqualFold(owner, prOwner) && strings.EqualFold(name, prName) {
			return reviewModeOwn
		}
	}
	login := domain.NormalizeOwnAccount(prAuthor)
	for _, account := range ownAccounts {
		normalized := domain.NormalizeOwnAccount(account)
		if normalized == "" {
			continue
		}
		if login != "" && normalized == login {
			return reviewModeOwn
		}
		if prOK && normalized == domain.NormalizeOwnAccount(prOwner) {
			return reviewModeOwn
		}
	}
	return reviewModeForeign
}

// cliRepoIdentityFromPR normalizes a PR/MR URL (or a bare repository URL)
// into its repository identity: host, owner, and name.
func cliRepoIdentityFromPR(prURL string) (host, owner, name string, ok bool) {
	if host, owner, name, _, err := cliParsePRURL(prURL); err == nil {
		return host, owner, name, true
	}
	host, owner, name, err := cliRepoFromURL(prURL)
	if err != nil {
		return "", "", "", false
	}
	return host, owner, name, true
}

// sameCLIRepo compares two repository URLs by normalized host, owner, and
// name, the way providers treat them: case-insensitive, .git suffix ignored.
func sameCLIRepo(a, b string) bool {
	hostA, ownerA, nameA, errA := cliRepoFromURL(a)
	hostB, ownerB, nameB, errB := cliRepoFromURL(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(hostA, hostB) && strings.EqualFold(ownerA, ownerB) && strings.EqualFold(nameA, nameB)
}

// formatReviewSessionName derives the spawned session's sidebar label.
func formatReviewSessionName(title string, number int, mode string) string {
	label := "review"
	if mode == reviewModeForeign {
		label = "review-only"
	}
	name := fmt.Sprintf("%s #%d: %s", label, number, strings.TrimSpace(title))
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxDisplayNameLen {
		runes := []rune(name)
		name = string(runes[:maxDisplayNameLen])
	}
	return name
}

// reviewSessionPrompt builds the spawned worker's task prompt. The mode is
// stated in the prompt itself so the session's report always carries it.
func reviewSessionPrompt(mode, prURL, repoURL string) string {
	var b strings.Builder
	if mode == reviewModeForeign {
		fmt.Fprintf(&b, "AO review mode: review-only (foreign). PR under review: %s (repository %s).\n", prURL, repoURL)
		b.WriteString("This session is review-only: file-editing and push tools are denied by the launch policy. Do not attempt to modify files, commit, or push.\n")
		b.WriteString("The AO native reviewer is reviewing this PR and will publish the verdict on GitHub; findings stay in this session's review record (`ao review ls`).\n")
		b.WriteString("Summarize the review outcome in your report. Do not post comments to the PR yourself.\n")
		b.WriteString("State the active mode (review-only (foreign)) in every report you emit.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "AO review mode: review+fix (own). PR under review: %s — it is claimed by this session; work in this worktree.\n", prURL)
	b.WriteString("The AO native reviewer posts inline findings on the PR, and they are delivered here automatically.\n")
	b.WriteString("For each finding: fix it, commit, and push to the PR branch, then wait for the next review pass. Repeat until the AO review approves.\n")
	b.WriteString("State the active mode (review+fix (own)) in every report you emit.\n")
	return b.String()
}

func newReviewListCommand(ctx *commandContext) *cobra.Command {
	var opts reviewListOptions
	cmd := &cobra.Command{
		Use:     "ls [worker-session-id]",
		Aliases: []string{"list"},
		Short:   "List reviews for a worker session (default: this session)",
		Args:    atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := reviewTargetSession(args, "")
			if session == "" {
				return usageError{errors.New("usage: worker session id is required outside an AO session")}
			}
			var res listReviewsResponse
			path := "sessions/" + url.PathEscape(session) + "/reviews"
			if err := ctx.getJSON(cmd.Context(), path, &res); err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeReviewList(cmd, session, res)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output reviews as JSON")
	return cmd
}

func newReviewSubmitCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSubmitOptions
	cmd := &cobra.Command{
		Use:   "submit [worker-session-id]",
		Short: "Record a reviewer's result for a worker's PR",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.submitReview(cmd, args, opts)
		},
	}
	// Reviewer agents routinely spell flags with underscores (--review_id) rather
	// than hyphens (--review-id); normalize so both resolve to the same flag.
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&opts.runID, "run", "", "Review run id (required)")
	cmd.Flags().StringVar(&opts.verdict, "verdict", "", "Review verdict: approved or changes_requested (required)")
	cmd.Flags().StringVar(&opts.body, "body", "", "Review body: - to read the Markdown from stdin (so nothing is written into the worktree)")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "Review body: read the Markdown from this file (operators and scripts; reviewers pipe from stdin with --body -)")
	cmd.Flags().StringArrayVar(&opts.commentPaths, "comment-path", nil, "Inline finding path; repeat together with --comment-line and --comment-body, one occurrence group per finding")
	cmd.Flags().IntSliceVar(&opts.commentLines, "comment-line", nil, "Inline finding line; repeat together with --comment-path and --comment-body")
	cmd.Flags().StringArrayVar(&opts.commentBodys, "comment-body", nil, "Single-line inline finding body; repeat together with --comment-path and --comment-line")
	cmd.Flags().StringVar(&opts.reviews, "reviews", "", "JSON review results array or object: a path, or - to read from stdin")
	// Legacy input from the pre-#5701 contract, kept only to fail with a
	// message that names the replacement instead of "unknown flag".
	cmd.Flags().StringVar(&opts.reviewID, "review-id", "", "Removed: GitHub review ids are outputs; AO publishes the review and records the id")
	_ = cmd.Flags().MarkHidden("review-id")
	return cmd
}

func (c *commandContext) submitReview(cmd *cobra.Command, args []string, opts reviewSubmitOptions) error {
	session := strings.TrimSpace(opts.session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session)")}
	}
	if strings.TrimSpace(opts.reviews) != "" {
		return c.submitReviewBatch(cmd, session, opts)
	}
	if strings.TrimSpace(opts.reviewID) != "" {
		return usageError{errors.New("--review-id was removed: GitHub review ids are outputs; AO publishes the review and records the id")}
	}
	runID := strings.TrimSpace(opts.runID)
	if runID == "" {
		return usageError{errors.New("usage: --run is required")}
	}
	verdict := strings.TrimSpace(opts.verdict)
	if verdict == "" {
		return usageError{errors.New("usage: --verdict is required (approved or changes_requested)")}
	}
	var body string
	if opts.bodyFile != "" {
		if strings.TrimSpace(opts.body) != "" {
			return usageError{errors.New("use either --body or --body-file, not both")}
		}
		// Operators and scripts stage the body with an editor and pass the path;
		// reviewers cannot (their sandbox has no write tools), so their path
		// stays stdin. See #6021.
		raw, err := os.ReadFile(opts.bodyFile)
		if err != nil {
			return usageError{fmt.Errorf("--body-file: %w", err)}
		}
		body = string(raw)
	} else {
		switch bodyArg := strings.TrimSpace(opts.body); bodyArg {
		case "":
		case "-":
			// Read the review from stdin so the reviewer never has to write a file
			// into its checkout (where it could be committed onto the worker branch).
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return usageError{fmt.Errorf("read review body: %w", err)}
			}
			body = string(raw)
		default:
			return usageError{errors.New("--body only accepts - (stdin): pipe the review Markdown from stdin, or pass a staged file with --body-file <path>")}
		}
	}
	findings, err := pairReviewFindings(opts.commentPaths, opts.commentLines, opts.commentBodys)
	if err != nil {
		return err
	}
	if verdict == "changes_requested" && strings.TrimSpace(body) == "" {
		return usageError{errors.New("a changes_requested review requires a body: pipe the Markdown from stdin with --body -, or pass a staged file with --body-file <path>")}
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/submit"
	var res reviewRunResponse
	if err := c.postReviewJSON(cmd.Context(), path, submitReviewRequest{RunID: runID, Verdict: verdict, Body: body, Comments: findings}, &res); err != nil {
		return err
	}
	// A submit response always carries the recorded run's ID and verdict; a
	// structurally valid but half-populated result is a broken contract, not
	// a success to print.
	if strings.TrimSpace(res.Review.ID) == "" || strings.TrimSpace(res.Review.Verdict) == "" {
		return fmt.Errorf("daemon returned empty review result for %s", session)
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "recorded %s review for %s\n", res.Review.Verdict, session); err != nil {
		return err
	}
	// The publication result rides on the same invocation. Nothing here fails
	// the CLI: the review result itself is recorded; only the daemon-side
	// GitHub publication can report a problem.
	switch res.Review.PublishState {
	case "published":
		_, err = fmt.Fprintf(out, "published GitHub review %s\n", strings.TrimSpace(res.Review.GithubReviewID))
	case "":
		// A current daemon always reports a publish state (the publication
		// migration backfills it). An empty state means a CLI newer than its
		// daemon: the recorded result is real, but a "published" line would be
		// a guess with an empty id.
		_, err = fmt.Fprintf(out, "recorded; publication outcome unknown (daemon predates publication tracking)\n")
	case "failed":
		msg := strings.TrimSpace(res.Review.PublishError)
		if msg == "" {
			msg = "provider rejected the review"
		}
		_, err = fmt.Fprintf(out, "GitHub publication failed: %s\nThe review result is recorded; rerun the same command to retry publication\n", msg)
	case "uncertain", "pending", "publishing":
		reason := strings.TrimSpace(res.Review.PublishError)
		if reason == "" {
			reason = "interrupted mid-publish"
		}
		_, err = fmt.Fprintf(out, "GitHub publication outcome unknown (%s); check the pull request before resubmitting\n", reason)
	default:
		_, err = fmt.Fprintf(out, "GitHub publication state %q\n", res.Review.PublishState)
	}
	return err
}

func (c *commandContext) submitReviewBatch(cmd *cobra.Command, session string, opts reviewSubmitOptions) error {
	if strings.TrimSpace(opts.runID) != "" || strings.TrimSpace(opts.verdict) != "" || strings.TrimSpace(opts.body) != "" || strings.TrimSpace(opts.reviewID) != "" {
		return usageError{errors.New("usage: --reviews cannot be combined with --run, --verdict, --body, or --review-id")}
	}
	reviews, err := readReviewItems(cmd, strings.TrimSpace(opts.reviews))
	if err != nil {
		return err
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/submit"
	var res reviewRunResponse
	if err := c.postReviewJSON(cmd.Context(), path, submitReviewRequest{Reviews: reviews}, &res); err != nil {
		return err
	}
	// Batch success is the recorded runs array: every returned entry must
	// carry its run ID and verdict. An empty array falls back to requiring
	// a fully-populated single review. Anything less is a broken contract,
	// not a success to print.
	if len(res.Reviews) > 0 {
		for _, run := range res.Reviews {
			if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Verdict) == "" {
				return fmt.Errorf("daemon returned empty review result for %s", session)
			}
		}
	} else if strings.TrimSpace(res.Review.ID) == "" || strings.TrimSpace(res.Review.Verdict) == "" {
		return fmt.Errorf("daemon returned empty review result for %s", session)
	}
	count := len(res.Reviews)
	if count == 0 {
		count = len(reviews)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "recorded %d review(s) for %s\n", count, session)
	return err
}

// pairReviewFindings builds the inline findings from the repeatable comment
// flags. Values pair by occurrence: the n-th occurrence of each flag forms one
// finding, and any incomplete group is rejected instead of silently reordered.
// Finding bodies are single-line by contract — multi-line prose belongs in the
// review body, not in shell-quoted flag values.
func pairReviewFindings(paths []string, lines []int, bodys []string) ([]submitReviewComment, error) {
	if len(paths) == 0 && len(lines) == 0 && len(bodys) == 0 {
		return nil, nil
	}
	if len(paths) != len(lines) || len(paths) != len(bodys) {
		return nil, usageError{fmt.Errorf("incomplete inline finding: --comment-path, --comment-line, and --comment-body must occur the same number of times (got %d, %d, %d)", len(paths), len(lines), len(bodys))}
	}
	findings := make([]submitReviewComment, 0, len(paths))
	for i := range paths {
		findPath := strings.TrimSpace(paths[i])
		findBody := bodys[i]
		switch {
		case findPath == "":
			return nil, usageError{fmt.Errorf("inline finding %d: --comment-path must not be blank", i+1)}
		case strings.ContainsAny(findPath, "\n\r"):
			return nil, usageError{fmt.Errorf("inline finding %d: --comment-path must be a single line", i+1)}
		case lines[i] <= 0:
			return nil, usageError{fmt.Errorf("inline finding %d: --comment-line must be a positive line number", i+1)}
		case strings.TrimSpace(findBody) == "":
			return nil, usageError{fmt.Errorf("inline finding %d: --comment-body must not be blank", i+1)}
		case strings.ContainsAny(findBody, "\n\r"):
			return nil, usageError{fmt.Errorf("inline finding %d: --comment-body must be single-line; multi-line prose belongs in the review body", i+1)}
		}
		findings = append(findings, submitReviewComment{Path: findPath, Line: lines[i], Body: findBody})
	}
	return findings, nil
}

// postReviewJSON retries only transport-level daemon unavailability. The
// service accepts an identical completed result idempotently, so replay is safe
// even when a connection drops after the daemon committed the first request.
// Validation/API errors still return immediately and are never retried.
func (c *commandContext) postReviewJSON(ctx context.Context, path string, body submitReviewRequest, out *reviewRunResponse) error {
	retryCtx, cancel := context.WithTimeout(ctx, reviewSubmitRetryWindow)
	defer cancel()

	var lastErr error
	for {
		err := c.postJSON(retryCtx, path, body, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errDaemonUnavailable) {
			return err
		}
		lastErr = err
		if retryCtx.Err() != nil {
			return fmt.Errorf("%w; could not confirm the review result after retrying for %s. It may already be recorded; retry with the same review results", lastErr, reviewSubmitRetryWindow)
		}

		// Deps.Sleep keeps this loop deterministic in unit tests. The short
		// interval bounds cancellation latency without adding a retry goroutine.
		c.deps.Sleep(reviewSubmitRetryInterval)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if retryCtx.Err() != nil {
			return fmt.Errorf("%w; could not confirm the review result after retrying for %s. It may already be recorded; retry with the same review results", lastErr, reviewSubmitRetryWindow)
		}
	}
}

func newReviewCancelCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSessionOptions
	cmd := &cobra.Command{
		Use:     "cancel [worker-session-id]",
		Aliases: []string{"stop"},
		Short:   "Cancel every running review for a worker's PR (default: this session)",
		Args:    atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.stopReview(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	return cmd
}

func newReviewTriggerCommand(ctx *commandContext) *cobra.Command {
	var opts reviewTriggerOptions
	cmd := &cobra.Command{
		Use:     "trigger [worker-session-id]",
		Aliases: []string{"execute", "restart"},
		Short:   "Start an AO review of a worker's PR (default: this session's PR)",
		Long: `Start an independent AO reviewer on the worker session's open PR heads.

With no session argument the calling AO session is reviewed, so a worker can
request a review of its own PR. AO fetches the session's PRs fresh from the
provider first, so the pass reviews the commit really on the PR. Right after
opening a PR, pass --pr <url>: AO attaches it to the session if it does not
track it yet (never taking it from another active session) and reviews only it. The reviewer defaults to the session's reviewer
choice, then the project's reviewer config, then the project's default worker
agent and model; --agent, --model, and --effort override it for this pass only.

A head that is already being reviewed, or already has a review, is not reviewed
again: the command fails and says why. Pass --rerun to review the same commit
again, or to add a different --agent alongside one that is still running.

The worker session's review auto-inject is turned on so the reviewer's inline
review comments are delivered to the worker; pass --no-inject to leave
that setting unchanged. AO's review is internal: an approval is not a GitHub
approval and does not authorize merging.`,
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.triggerReview(cmd, args, opts)
		},
	}
	f := cmd.Flags()
	f.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "agent" {
			name = "harness"
		}
		return pflag.NormalizedName(name)
	})
	f.StringVar(&opts.session, "session", "", "Worker session id (or pass it positionally; default: this AO session)")
	f.StringVar(&opts.prURL, "pr", "", "Review only this pull request URL, attaching it to the session if AO does not track it yet (default: every eligible PR on the session)")
	f.StringVar(&opts.harness, "harness", "", "Reviewer agent / --agent for this pass only (e.g. claude-code, codex)")
	f.StringVar(&opts.model, "model", "", "Reviewer model for this pass only")
	f.StringVar(&opts.effort, "effort", "", "Reviewer reasoning effort for this pass only")
	f.BoolVar(&opts.rerun, "rerun", false, "Review heads that already have a review again, or add another reviewer alongside a running one")
	f.BoolVar(&opts.noInject, "no-inject", false, "Leave the session's review auto-inject setting unchanged")
	return cmd
}

func (c *commandContext) triggerReview(cmd *cobra.Command, args []string, opts reviewTriggerOptions) error {
	if strings.TrimSpace(os.Getenv(envReviewSessionID)) != "" {
		return usageError{errors.New("ao review trigger cannot run inside a reviewer: reviewers review the requested PR and submit with `ao review submit`; they never start reviews")}
	}
	session := reviewTargetSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session) outside an AO session")}
	}
	req := triggerReviewRequest{
		Harness:            strings.TrimSpace(opts.harness),
		PRURL:              strings.TrimSpace(opts.prURL),
		RejectReviewedHead: !opts.rerun,
		Rerun:              opts.rerun,
		EnableAutoInject:   !opts.noInject,
	}
	if model, effort := strings.TrimSpace(opts.model), strings.TrimSpace(opts.effort); model != "" || effort != "" {
		req.AgentConfig = &reviewAgentConfig{Model: model, Effort: effort}
	}
	// A request from inside an AO session (a worker reviewing its own PR, or an
	// orchestrator) is labelled as such, so it is never mistaken for a person.
	if strings.TrimSpace(os.Getenv("AO_SESSION_ID")) != "" {
		req.Source = "agent"
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/trigger"
	var res triggerReviewResponse
	if err := c.postJSON(cmd.Context(), path, req, &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !res.Created {
		// Only reachable from an older daemon that ignores the same-commit
		// policy; a current daemon answers 409 instead.
		_, err := fmt.Fprintf(out, "reused the existing review for %s\n", session)
		return err
	}
	if _, err := fmt.Fprintf(out, "started a new review for %s\n", session); err != nil {
		return err
	}
	for _, run := range startedRuns(res) {
		if _, err := fmt.Fprintf(out, "  %s reviewing %s at %s (run %s)\n", run.Harness, run.PRURL, shortReviewSHA(run.TargetSHA), run.ID); err != nil {
			return err
		}
	}
	switch {
	case res.AutoInjectEnabled:
		_, err := fmt.Fprintf(out, "turned on review auto-inject for %s; the reviewer's comments will be delivered to the session, and `ao review ls` shows the verdict\n", session)
		return err
	case opts.noInject:
		_, err := fmt.Fprintf(out, "left review auto-inject unchanged for %s; check results with `ao review ls %s`\n", session, session)
		return err
	}
	return nil
}

// startedRuns returns the running passes on the PR heads the trigger covered.
func startedRuns(res triggerReviewResponse) []reviewRun {
	var out []reviewRun
	for _, review := range res.Reviews {
		if review.LatestRun != nil && review.LatestRun.Status == "running" {
			out = append(out, *review.LatestRun)
		}
	}
	return out
}

func shortReviewSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (c *commandContext) stopReview(cmd *cobra.Command, args []string, opts reviewSessionOptions) error {
	session := reviewTargetSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session) outside an AO session")}
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/cancel"
	if err := c.postJSON(cmd.Context(), path, struct{}{}, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "cancelled review for %s\n", session)
	return err
}

func writeReviewList(cmd *cobra.Command, session string, res listReviewsResponse) error {
	out := cmd.OutOrStdout()
	if len(res.Reviews) == 0 {
		_, err := fmt.Fprintf(out, "No reviews found for %s.\n", session)
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PR\tSTATUS\tVERDICT\tREVIEWERS\tTITLE"); err != nil {
		return err
	}
	for _, review := range res.Reviews {
		verdict := "-"
		if review.LatestRun != nil && review.LatestRun.Verdict != "" {
			verdict = review.LatestRun.Verdict
		}
		if _, err := fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\n", review.PRNumber, review.Status, verdict, reviewersOnHead(res.Runs, review.PRURL, review.TargetSHA), review.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// reviewersOnHead summarizes the distinct reviewers with a live or completed
// pass on one PR head and the trigger source that added each, so `ao review ls`
// answers "how many reviewers covered this head and who asked for them",
// including when a project allows more than one per head.
func reviewersOnHead(runs []reviewRun, prURL, targetSHA string) string {
	var reviewers []string
	seen := map[string]bool{}
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA {
			continue
		}
		if run.Status != "running" && run.Verdict != "approved" && run.Verdict != "changes_requested" {
			continue
		}
		harness := run.Harness
		if harness == "" {
			harness = "unknown"
		}
		if seen[harness] {
			continue
		}
		seen[harness] = true
		if run.TriggerSource != "" {
			reviewers = append(reviewers, harness+" ("+run.TriggerSource+")")
		} else {
			reviewers = append(reviewers, harness)
		}
	}
	if len(reviewers) == 0 {
		return "-"
	}
	return fmt.Sprintf("%d: %s", len(reviewers), strings.Join(reviewers, ", "))
}

func readReviewItems(cmd *cobra.Command, path string) ([]submitReviewItem, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, usageError{fmt.Errorf("read review results: %w", err)}
	}
	var req submitReviewRequest
	if err := json.Unmarshal(raw, &req); err == nil && len(req.Reviews) > 0 {
		return req.Reviews, nil
	}
	var reviews []submitReviewItem
	if err := json.Unmarshal(raw, &reviews); err != nil {
		return nil, usageError{fmt.Errorf("decode review results JSON: %w", err)}
	}
	if len(reviews) == 0 {
		return nil, usageError{errors.New("usage: --reviews requires at least one review result")}
	}
	return reviews, nil
}
