package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	scmmulti "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// readinessFailuresBinder late-binds the SCM observer's readiness-failure
// tracker into the session service. *sessionsvc.Service satisfies it
// structurally — same pattern as scmProxySource.
type readinessFailuresBinder interface {
	SetReadinessFailures(p sessionsvc.ReadinessFailuresProvider)
}

// scmHTTPTimeout matches the providers' own default client timeout: an injected
// *http.Client is taken verbatim, so the wiring owns the deadline.
const scmHTTPTimeout = 30 * time.Second

// scmProxySource supplies the user's explicit SCM proxy URL ("" = environment
// default). *settingssvc.Service satisfies it structurally.
type scmProxySource interface {
	ScmProxyURL(ctx context.Context) string
}

// scmProxyFunc resolves the outbound proxy on every dial, so a setting change
// applies without rebuilding the providers. An explicit setting wins over the
// environment; an empty — or unreadable — setting keeps the standard
// ProxyFromEnvironment behavior. A malformed stored value is logged once and
// treated as unset: the transport must keep working rather than fail every SCM
// request.
func scmProxyFunc(src scmProxySource, logger *slog.Logger) func(*http.Request) (*url.URL, error) {
	var warned atomic.Bool
	return func(req *http.Request) (*url.URL, error) {
		raw := ""
		if src != nil {
			raw = strings.TrimSpace(src.ScmProxyURL(req.Context()))
		}
		if raw == "" {
			return http.ProxyFromEnvironment(req)
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			if warned.CompareAndSwap(false, true) {
				logger.Warn("scm proxy setting is invalid; using environment default", "err", err)
			}
			return http.ProxyFromEnvironment(req)
		}
		return parsed, nil
	}
}

// newSCMHTTPClient builds the shared outbound client for both SCM providers.
// The proxy decision is deferred to dial time (scmProxyFunc), so the client and
// the providers constructed from it can stay singletons for the daemon's life.
func newSCMHTTPClient(src scmProxySource, logger *slog.Logger) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:errcheck // DefaultTransport is always a *http.Transport; the assertion cannot fail
	transport.Proxy = scmProxyFunc(src, logger)
	return &http.Client{Transport: transport, Timeout: scmHTTPTimeout}
}

// startSCMObserver wires the provider-neutral SCM observer with both GitHub
// and GitLab providers via a multi Provider dispatcher. Missing credentials
// for one provider do not prevent the other from starting; the observer is
// disabled only when no provider has usable credentials. A nil httpClient
// leaves both providers on their default clients. kick, when non-nil, is bound
// to the running observer so a worker push can wake the poll loop immediately.
func startSCMObserver(ctx context.Context, store *sqlite.Store, lcm *lifecycle.Manager, gitlabCfg config.GitLabConfig, logger *slog.Logger, httpClient *http.Client, kick *scmPollKick, readiness readinessFailuresBinder) <-chan struct{} {
	var named []scmmulti.NamedProvider

	ghProvider, ghErr := newGitHubSCMProvider(logger, httpClient)
	if ghErr != nil {
		logSCMProviderDisabled(logger, "github", ghErr)
	} else {
		named = append(named, scmmulti.NamedProvider{Key: "github", Provider: ghProvider})
	}

	glProvider, glErr := newGitLabSCMProvider(gitlabCfg, logger, httpClient)
	if glErr != nil {
		logSCMProviderDisabled(logger, "gitlab", glErr)
	} else {
		named = append(named, scmmulti.NamedProvider{Key: "gitlab", Provider: glProvider})
	}

	if len(named) == 0 {
		logger.Warn("scm observer disabled: no usable SCM provider")
		return closedDone()
	}
	provider := scmmulti.New(named...)
	observer := scmobserve.New(provider, store, lcm, scmobserve.Config{Logger: logger, ScopedIdentityResolver: provider})
	if kick != nil {
		kick.bind(observer)
	}
	if readiness != nil {
		readiness.SetReadinessFailures(observer)
	}
	return observer.Start(ctx)
}

// scmPollKick hands push events to the SCM observer once it is running.
// Before then, or without any SCM provider, a kick is dropped and the observer
// never starts — the push-driven review trigger does not depend on either.
type scmPollKick struct {
	observer atomic.Pointer[scmobserve.Observer]
}

func (k *scmPollKick) bind(o *scmobserve.Observer) {
	if k != nil {
		k.observer.Store(o)
	}
}

func (k *scmPollKick) request() {
	if k != nil {
		if o := k.observer.Load(); o != nil {
			o.RequestPoll()
		}
	}
}

// RequestSessionRefresh forwards a session-targeted early refresh to the bound
// observer: a finished agent command probably changed the session's pull
// request on the provider (`gh pr merge`, `git push`). Implements the API's
// PRRefresher interface.
func (k *scmPollKick) RequestSessionRefresh(id domain.SessionID) {
	if k != nil {
		if o := k.observer.Load(); o != nil {
			o.RequestSessionRefresh(id)
		}
	}
}

func newGitHubSCMProvider(logger *slog.Logger, httpClient *http.Client) (*scmgithub.Provider, error) {
	tokens := scmgithub.FallbackTokenSource{
		scmgithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}},
		&scmgithub.GHTokenSource{},
	}
	return scmgithub.NewProvider(scmgithub.ProviderOptions{Token: tokens, SkipTokenPreflight: true, Logger: logger, HTTPClient: httpClient})
}

func newGitLabSCMProvider(gitlabCfg config.GitLabConfig, logger *slog.Logger, httpClient *http.Client) (*scmgitlab.Provider, error) {
	tokens := scmgitlab.FallbackTokenSource{
		scmgitlab.EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}},
		&scmgitlab.GLabTokenSource{},
	}
	hostTokens := make(map[string]scmgitlab.TokenSource, len(gitlabCfg.HostTokens))
	for host, token := range gitlabCfg.HostTokens {
		hostTokens[host] = scmgitlab.StaticTokenSource(token)
	}
	return scmgitlab.NewProvider(scmgitlab.ProviderOptions{
		Token:              tokens,
		SkipTokenPreflight: true,
		Logger:             logger,
		AllowedHosts:       gitlabCfg.AllowedHosts,
		HostTokens:         hostTokens,
		HTTPClient:         httpClient,
	})
}

func logSCMProviderDisabled(logger *slog.Logger, provider string, err error) {
	if errors.Is(err, scmgithub.ErrNoToken) || errors.Is(err, scmgithub.ErrAuthFailed) ||
		errors.Is(err, scmgitlab.ErrNoToken) || errors.Is(err, scmgitlab.ErrAuthFailed) {
		logger.Warn("scm provider disabled: no usable token", "provider", provider, "err", err)
	} else {
		logger.Warn("scm provider disabled: setup failed", "provider", provider, "err", err)
	}
}

// newMultiSCMProvider builds a multi-provider for use outside the polling
// observer (e.g. session service PR claiming). Returns nil when no provider
// has usable credentials — callers must tolerate a nil SCM. A nil httpClient
// leaves both providers on their default clients.
func newMultiSCMProvider(gitlabCfg config.GitLabConfig, logger *slog.Logger, httpClient *http.Client) *scmmulti.Provider {
	var named []scmmulti.NamedProvider
	if gh, err := newGitHubSCMProvider(logger, httpClient); err == nil {
		named = append(named, scmmulti.NamedProvider{Key: "github", Provider: gh})
	}
	if gl, err := newGitLabSCMProvider(gitlabCfg, logger, httpClient); err == nil {
		named = append(named, scmmulti.NamedProvider{Key: "gitlab", Provider: gl})
	}
	if len(named) == 0 {
		return nil
	}
	return scmmulti.New(named...)
}

// newMultiSCMMerger builds a multi-merger for PR merge actions, registering
// both GitHub and GitLab providers. When one provider is unavailable (missing
// token), the multi-merger still routes to the healthy one — same
// degrade-gracefully pattern as newMultiSCMProvider. Returns nil when no
// provider has usable credentials. A nil httpClient leaves both providers on
// their default clients.
func newMultiSCMMerger(gitlabCfg config.GitLabConfig, logger *slog.Logger, httpClient *http.Client) *scmmulti.Merger {
	var named []scmmulti.NamedMerger
	if gh, err := newGitHubSCMProvider(logger, httpClient); err == nil {
		named = append(named, scmmulti.NamedMerger{Key: "github", Merger: gh})
	}
	if gl, err := newGitLabSCMProvider(gitlabCfg, logger, httpClient); err == nil {
		named = append(named, scmmulti.NamedMerger{Key: "gitlab", Merger: gl})
	}
	if len(named) == 0 {
		return nil
	}
	return scmmulti.NewMerger(named...)
}

func closedDone() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
