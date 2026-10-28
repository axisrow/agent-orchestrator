package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pr42IdentityBody = `{"number":42,"url":"https://github.com/acme/widgets/pull/42","headSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`

type prMergeCapture struct {
	getPath  string
	postPath string
	postBody string
	order    []string
}

// prMergeServer answers GET /prs/42 with identityBody and the merge POST with
// mergeBody, recording both requests in order.
func prMergeServer(t *testing.T, identityBody, mergeBody string) (*httptest.Server, *prMergeCapture) {
	t.Helper()
	capture := &prMergeCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/prs/42":
			capture.getPath = r.URL.Path
			capture.order = append(capture.order, "get")
			_, _ = io.WriteString(w, identityBody)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/prs/42/merge":
			capture.postPath = r.URL.Path
			capture.postBody = string(reqBody)
			capture.order = append(capture.order, "post")
			_, _ = io.WriteString(w, mergeBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestPRMergeResolvesIdentityBeforeMerging(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := prMergeServer(t, pr42IdentityBody, `{"ok":true,"prNumber":42,"method":"squash"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "#42")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.Join(capture.order, ",") != "get,post" {
		t.Fatalf("request order = %v, want get,post", capture.order)
	}
	if capture.getPath != "/api/v1/prs/42" || capture.postPath != "/api/v1/prs/42/merge" {
		t.Fatalf("paths = %q, %q", capture.getPath, capture.postPath)
	}
	var req struct {
		PRURL           string `json:"prUrl"`
		ExpectedHeadSHA string `json:"expectedHeadSha"`
	}
	if err := json.Unmarshal([]byte(capture.postBody), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.PRURL != "https://github.com/acme/widgets/pull/42" || req.ExpectedHeadSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("merge body = %q", capture.postBody)
	}
	if !strings.Contains(out, "merged PR #42 using squash") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRMergeSurfacesIdentityError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Unknown PR","code":"PR_NOT_FOUND","requestId":"req-9"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	for _, want := range []string{"Unknown PR", "PR_NOT_FOUND", "req-9"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err, want)
		}
	}
}

func TestPRMergeOmitsMissingMethodFromOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := prMergeServer(t, pr42IdentityBody, `{"ok":true,"prNumber":42}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if out != "merged PR #42\n" {
		t.Fatalf("stdout = %q, want %q", out, "merged PR #42\n")
	}
}

func TestPRMergeRejectsInvalidNumber(t *testing.T) {
	setConfigEnv(t)

	for _, number := range []string{"0", "-1", "abc", "#"} {
		t.Run(number, func(t *testing.T) {
			_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", number)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", got, err)
			}
		})
	}
}

func TestPRMergeRequiresExactlyOneArgument(t *testing.T) {
	setConfigEnv(t)

	for _, args := range [][]string{
		{"pr", "merge"},
		{"pr", "merge", "42", "43"},
	} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args = %v, exit code = %d, want 2; err=%v", args, got, err)
		}
	}
}

func TestPRMergeSurfacesDaemonError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusConflict, `{"message":"PR is not mergeable","code":"PR_NOT_MERGEABLE","requestId":"req-1"}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	for _, want := range []string{"PR is not mergeable", "PR_NOT_MERGEABLE", "req-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err, want)
		}
	}
}

func TestPRResolveCommentsPostsIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":2}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42", "thread-1", "thread-2")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/prs/42/resolve-comments" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req resolveCommentsRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.CommentIDs) != 2 || req.CommentIDs[0] != "thread-1" || req.CommentIDs[1] != "thread-2" {
		t.Fatalf("comment ids = %v", req.CommentIDs)
	}
	if !strings.Contains(out, "resolved 2 review thread(s) on PR #42") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRResolveCommentsAllowsNoIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":3}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.TrimSpace(capture.body) != "{}" {
		t.Fatalf("body = %q, want {}", capture.body)
	}
}

func TestPRResolveCommentsRequiresPRNumber(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, err)
	}
}
