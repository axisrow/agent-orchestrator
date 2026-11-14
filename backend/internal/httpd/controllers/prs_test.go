package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

type fakePRService struct {
	identity      prsvc.Identity
	identityErr   error
	identityID    string
	mergeResult   prsvc.MergeResult
	mergeErr      error
	mergeRequest  prsvc.MergeRequest
	resolveResult prsvc.ResolveResult
	resolveErr    error
	retargetReq   prsvc.RetargetRequest
	retargetRes   prsvc.RetargetResult
	retargetErr   error
	retargetCalls int
	branch        string
	branchErr     error
}

func (f *fakePRService) GetIdentity(_ context.Context, prID string) (prsvc.Identity, error) {
	f.identityID = prID
	return f.identity, f.identityErr
}

func (f *fakePRService) Merge(_ context.Context, request prsvc.MergeRequest) (prsvc.MergeResult, error) {
	f.mergeRequest = request
	return f.mergeResult, f.mergeErr
}

func (f *fakePRService) ResolveComments(_ context.Context, _ string, _ []string) (prsvc.ResolveResult, error) {
	return f.resolveResult, f.resolveErr
}

func (f *fakePRService) Retarget(_ context.Context, request prsvc.RetargetRequest) (prsvc.RetargetResult, error) {
	f.retargetCalls++
	f.retargetReq = request
	return f.retargetRes, f.retargetErr
}

func (f *fakePRService) DefaultBranch(_ context.Context, _, _ string) (string, error) {
	return f.branch, f.branchErr
}

func newPRTestServer(t *testing.T, svc prsvc.ActionManager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{PRs: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

const validMergeBody = `{"prUrl":"https://github.com/acme/widgets/pull/42","expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`

// ---- Nil service → 503 SCM_NOT_CONFIGURED ----

func TestPRsRoutes_NilService_MergeReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPRsRoutes_NilService_ResolveCommentsReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// ---- Default branch and retarget ----

func TestPRsRoutes_DefaultBranch_200(t *testing.T) {
	svc := &fakePRService{branch: "master"}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/prs/42/default-branch?prUrl=https://github.com/acme/widgets/pull/42", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		DefaultBranch string `json:"defaultBranch"`
	}
	mustJSON(t, body, &resp)
	if resp.DefaultBranch != "master" {
		t.Errorf("defaultBranch = %q, want master", resp.DefaultBranch)
	}
}

func TestPRsRoutes_DefaultBranch_MissingURL400(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{branch: "master"})
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/prs/42/default-branch", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
}

func TestPRsRoutes_DefaultBranch_ProviderUnavailable501(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{branchErr: prsvc.ErrUnsupported})
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/prs/42/default-branch?prUrl=https://github.com/acme/widgets/pull/42", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "SCM_UNSUPPORTED")
}

func TestPRsRoutes_Retarget_200(t *testing.T) {
	svc := &fakePRService{retargetRes: prsvc.RetargetResult{Child: 604, Base: "master", StateChangedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/603/retarget", `{"prUrl":"https://github.com/acme/widgets/pull/603","child":604}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Child int    `json:"child"`
		Base  string `json:"base"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.Child != 604 || resp.Base != "master" {
		t.Errorf("unexpected retarget response: %+v", resp)
	}
	if svc.retargetCalls != 1 || svc.retargetReq.PRID != "603" || svc.retargetReq.Child != 604 {
		t.Fatalf("retarget request = %#v", svc.retargetReq)
	}
}

func TestPRsRoutes_Retarget_RequiresChild(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{})
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/603/retarget", `{"prUrl":"https://github.com/acme/widgets/pull/603"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
}

func TestPRsRoutes_Retarget_Rejected422(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{retargetErr: prsvc.ErrPRRejected})
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/603/retarget", `{"prUrl":"https://github.com/acme/widgets/pull/603","child":604}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_ACTION_REJECTED")
}

// ---- Merge: 200 ----

func TestPRsRoutes_Merge_200(t *testing.T) {
	stateChangedAt := time.Date(2026, 10, 19, 11, 6, 22, 0, time.UTC)
	svc := &fakePRService{mergeResult: prsvc.MergeResult{
		PRNumber: 42, Method: "squash", State: domain.PRStateMerged,
		MergeCommitSHA: "merge-sha", StateChangedAt: stateChangedAt,
	}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", validMergeBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK             bool           `json:"ok"`
		PRNumber       int            `json:"prNumber"`
		Method         string         `json:"method"`
		State          domain.PRState `json:"state"`
		StateChangedAt time.Time      `json:"stateChangedAt"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.PRNumber != 42 || resp.Method != "squash" || resp.State != domain.PRStateMerged || !resp.StateChangedAt.Equal(stateChangedAt) {
		t.Errorf("unexpected merge response: %+v", resp)
	}
	if svc.mergeRequest.PRID != "42" || svc.mergeRequest.PRURL != "https://github.com/acme/widgets/pull/42" || svc.mergeRequest.ExpectedHeadSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("merge request = %#v", svc.mergeRequest)
	}
}

// ---- Merge: 400 missing required fields ----

func TestPRsRoutes_Merge_MissingFieldsAreInvalidPR(t *testing.T) {
	svc := &fakePRService{mergeResult: prsvc.MergeResult{PRNumber: 42}}
	srv := newPRTestServer(t, svc)

	for _, reqBody := range []string{`{}`, `{"prUrl":"https://github.com/acme/widgets/pull/42"}`, `{"expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`} {
		resp, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", reqBody)
		assertJSON(t, headers)
		assertErrorCode(t, resp, status, http.StatusBadRequest, "INVALID_PR")
	}
	if svc.mergeRequest.PRID != "" {
		t.Fatalf("service reached with request = %#v", svc.mergeRequest)
	}
}

func TestPRsRoutes_Merge_InvalidJSON(t *testing.T) {
	svc := &fakePRService{mergeResult: prsvc.MergeResult{PRNumber: 42}}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", `{"prUrl":`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.mergeRequest.PRID != "" {
		t.Fatalf("service reached with request = %#v", svc.mergeRequest)
	}
}

// After the service's bounded retries the banner must carry the underlying
// cause, not just "try again".
func TestPRsRoutes_Merge_ProviderUnavailableMessageCarriesCause(t *testing.T) {
	svc := &fakePRService{mergeErr: fmt.Errorf("%w: refresh pull request before merge: %w", prsvc.ErrPRProviderUnavailable, errors.New("net/http: TLS handshake timeout"))}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusServiceUnavailable, "PR_PROVIDER_UNAVAILABLE")
	if !strings.Contains(string(body), "net/http: TLS handshake timeout") || !strings.Contains(string(body), "after retries") {
		t.Fatalf("banner missing cause: %s", body)
	}
}

// ---- Get: 200 ----

func TestPRsRoutes_GetPR_200(t *testing.T) {
	svc := &fakePRService{identity: prsvc.Identity{Number: 42, URL: "https://github.com/acme/widgets/pull/42", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/prs/42", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Number  int    `json:"number"`
		URL     string `json:"url"`
		HeadSHA string `json:"headSha"`
	}
	mustJSON(t, body, &resp)
	if resp.Number != 42 || resp.URL != svc.identity.URL || resp.HeadSHA != svc.identity.HeadSHA {
		t.Errorf("resp = %+v, want %+v", resp, svc.identity)
	}
	if svc.identityID != "42" {
		t.Fatalf("identity request id = %q, want 42", svc.identityID)
	}
}

// ---- Get: 404 / 422 / 400 / 501 ----

func TestPRsRoutes_GetPR_ErrorMapping(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{identityErr: prsvc.ErrPRNotFound})
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/prs/99", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")

	srv = newPRTestServer(t, &fakePRService{identityErr: prsvc.ErrPRPreconditions})
	body, status, headers = doRequest(t, srv, "GET", "/api/v1/prs/42", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")

	srv = newPRTestServer(t, &fakePRService{})
	for _, id := range []string{"0", "abc", "-1"} {
		body, status, headers = doRequest(t, srv, "GET", "/api/v1/prs/"+id, "")
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
	}

	srv = newPRTestServer(t, nil)
	body, status, headers = doRequest(t, srv, "GET", "/api/v1/prs/42", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// ---- Merge: 404 ----

func TestPRsRoutes_Merge_404(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- Merge: 409 ----

func TestPRsRoutes_Merge_409(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotMergeable}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusConflict, "PR_NOT_MERGEABLE")
}

// ---- Merge: 422 ----

func TestPRsRoutes_Merge_422(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRPreconditions}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")
}

// ---- ResolveComments: 200 ----

func TestPRsRoutes_ResolveComments_200(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 3}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", `{"commentIds":["T_1","T_2","T_3"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK       bool `json:"ok"`
		Resolved int  `json:"resolved"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.Resolved != 3 {
		t.Errorf("resp = %+v, want {ok:true resolved:3}", resp)
	}
}

func TestPRsRoutes_ResolveComments_200_NoBody(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 2}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
}

// ---- ResolveComments: 404 ----

func TestPRsRoutes_ResolveComments_404(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- ResolveComments: 422 ----

func TestPRsRoutes_ResolveComments_422(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrNothingToResolve}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "NOTHING_TO_RESOLVE")
}

func TestPRsRoutes_ResolveComments_InvalidPRID(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 0}}
	srv := newPRTestServer(t, svc)

	for _, id := range []string{"0", "abc", "-1"} {
		body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/"+id+"/resolve-comments", "")
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
	}
}
