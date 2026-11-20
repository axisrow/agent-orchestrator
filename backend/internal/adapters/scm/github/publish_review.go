package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMReviewPublisher = (*Provider)(nil)
var _ ports.SCMReviewPublicationFinder = (*Provider)(nil)

// PublishReview creates one review on a GitHub pull request: the summary body
// plus optional inline comments, anchored at the reviewed commit. The daemon
// owns review publication since #5701; reviewers submit to AO.
func (p *Provider) PublishReview(ctx context.Context, request ports.SCMReviewPublishRequest) (ports.SCMReviewPublishResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: review publisher is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: invalid pull request reference")
	}
	// Fork issue #372: GitHub rejects the whole review with 422 "Path could not
	// be resolved" when any inline anchor names a path outside the PR diff —
	// the service then fell back to a plain COMMENT and the verdict was lost.
	// Resolve every anchor against the diff's file list first: paths that match
	// stay inline, the rest are demoted into the body so the verdict survives.
	body, comments := p.resolveAnchors(ctx, request)
	event := strings.TrimSpace(request.Event)
	if event == "" {
		event = "COMMENT"
	}
	payload := struct {
		Event    string                   `json:"event"`
		CommitID string                   `json:"commit_id,omitempty"`
		Body     string                   `json:"body"`
		Comments []map[string]interface{} `json:"comments,omitempty"`
	}{Event: event, CommitID: strings.TrimSpace(request.CommitSHA), Body: body}
	for _, comment := range comments {
		payload.Comments = append(payload.Comments, map[string]interface{}{
			"path": comment.Path,
			"line": comment.Line,
			"body": comment.Body,
		})
	}
	resp, err := p.client.doREST(ctx, http.MethodPost,
		repoPath(request.PR.Repo.Owner, request.PR.Repo.Name, "pulls", strconv.Itoa(request.PR.Number), "reviews"),
		nil, payload)
	if err != nil {
		// A transport failure (status unset) or a 5xx leaves the review's
		// existence unknown: GitHub may have processed the request before the
		// failure. 4xx rejections are definitive — no review was created. The
		// service maps the unknown case to an uncertain publication instead of
		// a failed one, so a rerun cannot post a duplicate review.
		if resp.StatusCode == 0 || resp.StatusCode >= 500 {
			return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: %w", ports.ErrSCMPublishOutcomeUnknown, err)
		}
		return ports.SCMReviewPublishResult{}, err
	}
	var created struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	// A 2xx response means GitHub accepted the review; only the handle is
	// unreadable. Like a transport outage, the review's existence is known and
	// its identity is not, so the service must record uncertainty instead of a
	// definitive failure a rerun would turn into a duplicate review.
	if err := json.Unmarshal(resp.Body, &created); err != nil {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: github scm: decode created review: %w", ports.ErrSCMPublishOutcomeUnknown, err)
	}
	if created.ID == 0 {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: github scm: created review returned no id", ports.ErrSCMPublishOutcomeUnknown)
	}
	return ports.SCMReviewPublishResult{ReviewID: strconv.FormatInt(created.ID, 10), HTMLURL: created.HTMLURL, State: strings.TrimSpace(created.State)}, nil
}

// FindPublishedReview locates a review this daemon previously published by
// scanning the pull request's reviews for one whose body carries bodyMarker —
// the review-run marker the service embeds at publication time. It is the
// recovery path for runs whose publication outcome was lost before it could be
// recorded: a found review proves the publication happened, so the run can be
// marked published instead of guessing or reposting a duplicate. Only the most
// recent page of reviews is scanned; AO publications are recent by construction.
func (p *Provider) FindPublishedReview(ctx context.Context, ref ports.SCMPRRef, bodyMarker string) (ports.SCMReviewPublishResult, bool, error) {
	if p == nil || p.client == nil {
		return ports.SCMReviewPublishResult{}, false, fmt.Errorf("github scm: review publisher is not configured")
	}
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" || bodyMarker == "" {
		return ports.SCMReviewPublishResult{}, false, fmt.Errorf("github scm: invalid review lookup reference")
	}
	q := url.Values{"per_page": []string{"100"}}
	resp, err := p.client.doREST(ctx, http.MethodGet,
		repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number), "reviews"),
		q, nil)
	if err != nil {
		// Mirror PublishReview: a transport failure or 5xx leaves the lookup
		// unresolved, never "not found" — the caller must keep treating the
		// run's outcome as unknown rather than as evidence of absence.
		if resp.StatusCode == 0 || resp.StatusCode >= 500 {
			return ports.SCMReviewPublishResult{}, false, fmt.Errorf("%w: %w", ports.ErrSCMPublishOutcomeUnknown, err)
		}
		return ports.SCMReviewPublishResult{}, false, err
	}
	var reviews []struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(resp.Body, &reviews); err != nil {
		return ports.SCMReviewPublishResult{}, false, fmt.Errorf("github scm: decode review list: %w", err)
	}
	for _, review := range reviews {
		if strings.Contains(review.Body, bodyMarker) {
			return ports.SCMReviewPublishResult{ReviewID: strconv.FormatInt(review.ID, 10), HTMLURL: review.HTMLURL, State: strings.TrimSpace(review.State)}, true, nil
		}
	}
	return ports.SCMReviewPublishResult{}, false, nil
}

// resolveAnchors splits a review's inline comments into anchors GitHub will
// accept and anchors it would reject with 422 "Path could not be resolved".
// Accepted anchors keep their inline placement (with the path rewritten to the
// diff's spelling when it differed only by a repo-root/worktree prefix);
// rejected anchors are demoted into the body as text, each keeping its
// path:line binding, so one bad path can no longer cost the review its
// verdict or its valid inlines (fork issue #372). A files-list failure leaves
// the request untouched: the service's rejection fallback chain (#221) stays
// the safety net for what resolution could not see.
func (p *Provider) resolveAnchors(ctx context.Context, request ports.SCMReviewPublishRequest) (string, []ports.SCMReviewComment) {
	if len(request.Comments) == 0 {
		return request.Body, request.Comments
	}
	files, err := p.pullRequestDiffPaths(ctx, request.PR)
	if err != nil {
		slog.Default().WarnContext(ctx, "review anchor resolution skipped: pull request file list unavailable", "pr", request.PR.Number, "err", err)
		return request.Body, request.Comments
	}
	kept := make([]ports.SCMReviewComment, 0, len(request.Comments))
	var demoted []ports.SCMReviewComment
	var rewritten []string
	for _, comment := range request.Comments {
		path, ok := matchDiffPath(comment.Path, files)
		if !ok {
			demoted = append(demoted, comment)
			continue
		}
		if path != comment.Path {
			rewritten = append(rewritten, comment.Path+" -> "+path)
			comment.Path = path
		}
		kept = append(kept, comment)
	}
	if len(demoted) > 0 {
		anchors := make([]string, 0, len(demoted))
		for _, comment := range demoted {
			anchors = append(anchors, fmt.Sprintf("%s:%d", comment.Path, comment.Line))
		}
		slog.Default().WarnContext(ctx, "review inline anchors not in PR diff; demoting to body", "pr", request.PR.Number, "anchors", strings.Join(anchors, ", "))
	}
	if len(rewritten) > 0 {
		slog.Default().InfoContext(ctx, "review inline anchors rewritten to diff paths", "pr", request.PR.Number, "rewrites", strings.Join(rewritten, ", "))
	}
	if len(demoted) == 0 {
		return request.Body, kept
	}
	var b strings.Builder
	b.WriteString(request.Body)
	b.WriteString("\n\n**Findings without a diff anchor (path not in the PR diff; kept as text):**")
	for _, comment := range demoted {
		fmt.Fprintf(&b, "\n- `%s:%d` — %s", comment.Path, comment.Line, comment.Body)
	}
	return b.String(), kept
}

// matchDiffPath maps an anchor path onto the PR diff's file list. Exact names
// pass through. A path differing only by a repo-root or worktree-subdir prefix
// is rewritten to the diff's spelling — but only when exactly one diff file
// matches, so an ambiguous prefix can never anchor a finding to the wrong
// file. Anything else is unresolvable and must be demoted instead.
func matchDiffPath(path string, files []string) (string, bool) {
	for _, file := range files {
		if file == path {
			return file, true
		}
	}
	match := ""
	for _, file := range files {
		if strings.HasSuffix(file, "/"+path) || strings.HasSuffix(path, "/"+file) {
			if match != "" {
				return "", false
			}
			match = file
		}
	}
	return match, match != ""
}

// pullRequestDiffPaths lists the file paths in the pull request's diff — the
// only paths GitHub accepts as inline review anchors. Page size matches the
// API maximum; the loop walks pages until a short page ends the list.
func (p *Provider) pullRequestDiffPaths(ctx context.Context, ref ports.SCMPRRef) ([]string, error) {
	var paths []string
	for page := 1; ; page++ {
		q := url.Values{"per_page": []string{"100"}, "page": []string{strconv.Itoa(page)}}
		resp, err := p.client.doREST(ctx, http.MethodGet,
			repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number), "files"),
			q, nil)
		if err != nil {
			return nil, err
		}
		var files []struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(resp.Body, &files); err != nil {
			return nil, fmt.Errorf("github scm: decode pull request files: %w", err)
		}
		for _, file := range files {
			paths = append(paths, file.Filename)
		}
		if len(files) < 100 {
			return paths, nil
		}
	}
}
