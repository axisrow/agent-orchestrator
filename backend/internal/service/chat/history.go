package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const nativeEditHandoffLimit = 45 * time.Second

// Conversation history operations: rollback, fork, and the thread title.
//
// The three share one property that shapes everything here: they change the
// provider's own record of the conversation, not AO's view of it. Rollback is the
// sharp one — it changes what the agent REMEMBERS — so AO's durable rows have to
// follow, or the timeline goes on showing prose the agent cannot recall.
//
// Each is optional on the driver and feature-detected, following the Models
// precedent: a provider that cannot do one of these gets a typed refusal the client
// can render as an absent affordance rather than a failed request.

// Typed outcomes for the history operations. Each one distinguishes a permanent
// answer from something worth retrying, because "this agent cannot undo" and "stop
// the agent first" ask completely different things of the person reading it.
var (
	// ErrRollbackUnsupported reports a provider that cannot discard history.
	ErrRollbackUnsupported = errors.New("chat driver cannot roll back history")
	// ErrForkUnsupported reports a provider that cannot branch a conversation.
	ErrForkUnsupported = errors.New("chat driver cannot fork a conversation")
	// ErrRenameUnsupported reports a provider whose thread carries no title.
	ErrRenameUnsupported = errors.New("chat driver cannot set a thread title")
	// ErrOrchestratorRename refuses a title for the project orchestrator's
	// conversation: the orchestrator is never renamed.
	ErrOrchestratorRename = errors.New("the project orchestrator cannot be renamed")
	// ErrTurnRunning refuses a rollback while the agent is working. Retryable once
	// the turn ends, which is why it is separate from every other refusal here.
	ErrTurnRunning = errors.New("cannot roll back while a turn is running")
	// ErrTurnNotRollbackable reports a turn the provider never accepted. There is
	// no provider history to discard, so an undo would only hide AO's own rows and
	// leave the agent remembering more than the timeline shows.
	ErrTurnNotRollbackable = errors.New("turn was never dispatched to the provider")
	// ErrTurnProviderMismatch reports a durable turn owned by an earlier agent
	// provider. Its opaque provider turn id is invalid in the current controller.
	ErrTurnProviderMismatch = errors.New("conversation turn belongs to a different agent provider")
	// ErrTitleRequired refuses a blank thread title. The provider refuses one too,
	// and reporting it here keeps the error about the caller's input.
	ErrTitleRequired = errors.New("thread title is required")
	// ErrProviderRefused reports something the provider declined on its own terms
	// while the conversation stayed healthy. It is a conflict, never an internal
	// failure: the message it carries is the provider's own explanation.
	ErrProviderRefused = errors.New("the agent refused the request")
	// ErrBranchProviderMismatch refuses a historical branch whose opaque provider
	// conversation id belongs to an earlier agent ownership epoch.
	ErrBranchProviderMismatch = errors.New("conversation branch belongs to a different agent provider")
)

// providerRefusal is satisfied by driver errors that mean "the provider said no"
// rather than "the call did not get through".
//
// Declared here and satisfied structurally, so this package needs no import of any
// adapter and no adapter needs to know this package's vocabulary. Without the
// distinction every ordinary refusal would surface as a 500.
type providerRefusal interface {
	ChatRefusal() bool
}

// classify folds a driver error into the typed vocabulary above.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var refusal providerRefusal
	if errors.As(err, &refusal) && refusal.ChatRefusal() {
		return fmt.Errorf("%w: %w", ErrProviderRefused, err)
	}
	return err
}

// maxTitleRunes bounds a thread title, matching the automatic-semantic-task-titles
// design's contract. Longer output is treated as generation failure rather than
// truncated into something that reads like a sentence someone cut off.
const maxTitleRunes = 80

// Rollback discards a turn and everything after it, from the agent's memory and
// from AO's timeline.
//
// Refused while a turn is running, and refused BEFORE the provider is asked. The
// provider refuses this too, but relying on that alone would make the outcome a
// race: AO would have already decided to hide rows by the time it learned it could
// not. The controller holds its dispatch lock across the check and the call, so
// within AO the answer cannot change underneath.
func (s *Service) Rollback(ctx context.Context, id domain.SessionID, turnID string) (int, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return 0, err
	}
	defer release()
	if _, ok := controller.conv.(ports.ChatRollbacker); !ok {
		return 0, ErrRollbackUnsupported
	}
	return controller.Rollback(ctx, turnID)
}

// ForkConversation branches this session's provider conversation and returns the
// new provider handle.
//
// Deliberately not reachable over HTTP yet, and the reason is not effort. AO's
// schema allows one conversation per session, so a fork has to become a second AO
// session — and a second session gets a fresh worktree, while the provider's own
// documentation is explicit that a fork copies conversation history and does NOT
// revert or copy the file changes the agent made. The forked agent would remember
// editing files that are not in its tree, which is a worse failure than the absent
// feature: it would look like it worked.
//
// Landing a fork therefore needs the spawn path to adopt the source session's
// worktree state, which is a workspace decision and not a chat one. Until then this
// exists so the provider call is written, tested, and honest about what it returns,
// rather than half-wired into a UI that cannot be correct.
func (s *Service) ForkConversation(ctx context.Context, id domain.SessionID) (string, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	forker, ok := controller.conv.(ports.ChatForker)
	if !ok {
		return "", ErrForkUnsupported
	}
	forked, err := forker.Fork(ctx, nil)
	if err != nil {
		return "", classify(fmt.Errorf("fork conversation for %s: %w", id, err))
	}
	return forked, nil
}


// ActivateBranch resumes a durable provider branch in the same worktree and
// swaps controllers without sending a new prompt.
func (s *Service) ActivateBranch(ctx context.Context, id domain.SessionID, branchID string) (string, error) {
	_, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return "", err
	}
	defer gate.unlock()
	return s.activateBranchLocked(ctx, id, branchID)
}

func (s *Service) activateBranchLocked(ctx context.Context, id domain.SessionID, branchID string) (string, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return "", err
	}
	source, err := s.Controller(id)
	if err != nil {
		return "", err
	}
	branch, err := s.store.ConversationBranch(ctx, source.conversation.ID, branchID)
	if err != nil {
		return "", err
	}
	activeBranch, err := s.store.ConversationBranch(
		ctx, source.conversation.ID, source.conversation.ActiveBranchID)
	if err != nil {
		return "", fmt.Errorf("load active conversation branch: %w", err)
	}
	if branch.Active {
		return branch.ID, nil
	}
	cfg, driver, err := s.branchLaunchConfig(source)
	if err != nil {
		return "", err
	}
	if branch.ProviderBindingID != activeBranch.ProviderBindingID {
		// Provider scopes may differ between approximate siblings, but their
		// durable binding epoch must match. Crossing it could hand an opaque
		// conversation handle to a different adapter/configuration owner.
		return "", ErrBranchProviderMismatch
	}
	if err := source.BeginIdleBranchHandoff(ctx); err != nil {
		return "", err
	}
	abortSource := true
	defer func() {
		if abortSource {
			source.AbortHandoff()
		}
	}()
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeEditHandoffLimit)
	defer cancel()
	if err := source.closeForBranchHandoff(operationCtx); err != nil {
		closeErr := fmt.Errorf("close source before branch activation: %w", err)
		if source.State() == ports.ChatControllerStopped {
			if restoreErr := s.restoreClosedSourceController(
				operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
				closeErr = errors.Join(closeErr, restoreErr)
			} else {
				abortSource = false
			}
		}
		return "", closeErr
	}
	launchEnv, err := s.prepareBranchControllerEnv(operationCtx, cfg)
	if err != nil {
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			err = errors.Join(err, restoreErr)
		} else {
			abortSource = false
		}
		return "", err
	}
	provider, err := driver.Resume(operationCtx, ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ProviderConversationID: branch.ProviderConversationID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: launchEnv,
		Model: cfg.Model, Effort: cfg.Effort,
		Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		ProviderScopeID:       branch.ProviderScopeID,
		ProviderIDsScoped:     branch.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
	})
	if err != nil {
		resumeErr := fmt.Errorf("resume conversation branch %s: %w", branchID, err)
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			resumeErr = errors.Join(resumeErr, restoreErr)
		} else {
			abortSource = false
		}
		return "", resumeErr
	}
	generation := s.newID()
	conversation := source.conversation
	conversation.ActiveBranchID = branch.ID
	replacement := newController(id, source.owner(), conversation, generation, source.harness, provider, s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.prChanged, s.onCodexCapacityChanged, s.reader)
	if err := s.store.ActivateConversationBranch(operationCtx, id, conversation.ID, branch.ID,
		branch.ProviderConversationID, generation, s.now()); err != nil {
		_ = cleanupUnpublishedConversation(provider, true)
		activateErr := err
		if restoreErr := s.restoreClosedSourceController(
			operationCtx, id, source, activeBranch, cfg, driver); restoreErr != nil {
			activateErr = errors.Join(activateErr, restoreErr)
		} else {
			abortSource = false
		}
		return "", activateErr
	}
	if err := s.installBranchController(operationCtx, id, source, replacement, source.conversation.ActiveBranchID); err != nil {
		return "", err
	}
	abortSource = false
	return branch.ID, nil
}

func (s *Service) branchLaunchConfig(
	source *Controller,
) (StartConfig, ports.ChatDriver, error) {
	s.mu.RLock()
	cfg, ok := s.startConfigs[source.owner()]
	// Controllers are registered by typed owner. Session history operations still
	// address the worker by session id, so use the source's owner here rather
	// than treating the legacy session index as the authority. In particular, a
	// branch replacement must update the typed entry before the old controller's
	// cleanup goroutine runs, otherwise that goroutine can remove the new
	// session controller.
	current := s.ownerControllers[source.owner()]
	s.mu.RUnlock()
	if !ok || current != source {
		return StartConfig{}, nil, ErrControllerHandoff
	}
	driver, err := s.drivers.Driver(cfg.Harness)
	if err != nil {
		return StartConfig{}, nil, fmt.Errorf("chat driver for %s: %w", cfg.Harness, err)
	}
	return cloneStartConfig(cfg), driver, nil
}

func (s *Service) prepareBranchControllerEnv(ctx context.Context, cfg StartConfig) (map[string]string, error) {
	if cfg.PrepareControllerEnv == nil {
		return cloneStartConfig(cfg).Env, nil
	}
	env, err := cfg.PrepareControllerEnv(ctx, cfg.ExpectedControllerOwner)
	if err != nil {
		return nil, fmt.Errorf("prepare replacement controller environment: %w", err)
	}
	return env, nil
}

// restoreClosedSourceController reopens the original provider branch when a
// native edit had to release its writer but the replacement could not be kept.
// The new generation fences any late events from the process that was closed.
func (s *Service) restoreClosedSourceController(
	ctx context.Context,
	id domain.SessionID,
	source *Controller,
	branch domain.ConversationBranch,
	cfg StartConfig,
	driver ports.ChatDriver,
) (returnErr error) {
	defer func() {
		if returnErr != nil {
			source.reportFailedBranchHandoff(ctx)
		}
	}()
	recoveryCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), nativeEditHandoffLimit)
	defer cancel()
	// A closed event stream can project "stopped" before host termination
	// succeeds. Preserve any termination error before rotating its credentials.
	if err := source.closeForBranchHandoff(recoveryCtx); err != nil {
		return fmt.Errorf("confirm source stopped before failed native edit recovery: %w", err)
	}
	providerConversationID := source.ProviderConversationID()
	launchEnv, err := s.prepareBranchControllerEnv(recoveryCtx, cfg)
	if err != nil {
		return fmt.Errorf("rotate source browser capability after failed native edit: %w", err)
	}
	provider, err := driver.Resume(recoveryCtx, ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ProviderConversationID: providerConversationID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: launchEnv,
		Model: cfg.Model, Effort: cfg.Effort,
		Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		ProviderScopeID:       branch.ProviderScopeID,
		ProviderIDsScoped:     branch.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers,
	})
	if err != nil {
		return fmt.Errorf("resume source after failed native edit: %w", err)
	}
	generation := s.newID()
	conversation := source.conversation
	conversation.ActiveBranchID = branch.ID
	replacement := newController(
		id, source.owner(), conversation, generation, source.harness, provider, s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.prChanged, s.onCodexCapacityChanged, s.reader)
	if err := s.store.ActivateConversationBranch(recoveryCtx, id, conversation.ID, branch.ID,
		providerConversationID, generation, s.now()); err != nil {
		_ = provider.Close()
		return fmt.Errorf("reactivate source after failed native edit: %w", err)
	}
	if err := s.installBranchController(recoveryCtx, id, source, replacement, branch.ID); err != nil {
		return fmt.Errorf("install source after failed native edit: %w", err)
	}
	return nil
}

func (s *Service) installBranchController(
	ctx context.Context,
	id domain.SessionID,
	source, replacement *Controller,
	sourceBranchID string,
) error {
	replacement.start()
	return s.installStartedBranchController(ctx, id, source, replacement, sourceBranchID)
}

// installStartedBranchController publishes a controller whose provider event
// stream is already being consumed. Edit bootstraps use this after recording the
// replacement prompt privately; ordinary branch activation starts and delegates
// through installBranchController above.
func (s *Service) installStartedBranchController(
	ctx context.Context,
	id domain.SessionID,
	source, replacement *Controller,
	sourceBranchID string,
) error {
	owner := source.owner()
	s.mu.Lock()
	if s.ownerControllers[owner] != source {
		s.mu.Unlock()
		_ = replacement.Terminate(ctx)
		if err := s.store.ActivateConversationBranch(ctx, id, source.conversation.ID,
			sourceBranchID, source.ProviderConversationID(), source.generation, s.now()); err != nil {
			return fmt.Errorf("restore source branch after controller swap conflict: %w", err)
		}
		return ErrControllerHandoff
	}
	source.prepareBranchHandoffStop()
	s.ownerControllers[owner] = replacement
	// Keep the session index as the backwards-compatible lookup path used by
	// worker chat commands. Review owners never share this index, so replacing a
	// worker controller cannot affect a reviewer controller for the same session.
	if owner.Kind == domain.ConversationOwnerSession {
		s.controllers[id] = replacement
	}
	if cfg, ok := s.startConfigs[owner]; ok {
		cfg.ExpectedControllerOwner.Harness = cfg.Harness
		cfg.ExpectedControllerOwner.Mode = domain.SessionModeChat
		cfg.ExpectedControllerOwner.IsTerminated = false
		cfg.ExpectedControllerOwner.RuntimeLaunchID = ""
		cfg.ExpectedControllerOwner.ProviderConversationID = replacement.ProviderConversationID()
		cfg.ExpectedControllerOwner.ControllerGeneration = replacement.Generation()
		s.startConfigs[owner] = cfg
	}
	s.mu.Unlock()

	go func() {
		replacement.Wait()
		replacement.waitForBranchHandoff()
		s.mu.Lock()
		if current := s.ownerControllers[owner]; current == replacement {
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, id)
			}
		}
		s.mu.Unlock()
	}()
	source.completeBranchHandoff()
	if err := source.closeForBranchHandoff(ctx); err != nil {
		s.log.Error("close source controller after branch swap", "session", id, "error", err)
	}
	return nil
}

// SetTitle names the provider's thread and returns the normalized title.
//
// Nothing is written to AO's rows here. The provider answers, then emits its own
// rename notification, and the projection applies it — so the title AO stores is
// always one the provider confirmed. Writing it optimistically as well would give
// one fact two authors and no way to tell which lost.
func (s *Service) SetTitle(ctx context.Context, id domain.SessionID, title string) (string, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return "", err
	}
	normalized := NormalizeTitle(title)
	if normalized == "" {
		return "", ErrTitleRequired
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	if controller.conversation.Scope == domain.ConversationScopeProject {
		return "", ErrOrchestratorRename
	}
	renamer, ok := controller.conv.(ports.ChatRenamer)
	if !ok {
		return "", ErrRenameUnsupported
	}
	if err := renamer.SetTitle(ctx, normalized); err != nil {
		return "", classify(fmt.Errorf("set title for %s: %w", id, err))
	}
	if err := controller.applyThreadTitle(ctx, normalized, s.now()); err != nil {
		return "", err
	}
	return normalized, nil
}

// NormalizeTitle reduces a title to the one-line label AO is willing to show.
//
// The rules come from the automatic-semantic-task-titles design: one line, no
// wrapper punctuation, no trailing punctuation, and a hard length bound. They are
// applied to whatever the provider says rather than trusted, because a title is
// model output and arrives with heading markers, surrounding quotes, and full stops
// often enough that a client would otherwise render them.
//
// Over-length input is truncated at a word boundary rather than rejected: the
// provider already accepted the name, and refusing to display a title AO's own
// session is now carrying would leave the two disagreeing.
func NormalizeTitle(raw string) string {
	title := raw
	if idx := strings.IndexAny(title, "\r\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	// Markdown heading and list markers, which a model reaches for when asked for a
	// title and told nothing else.
	title = strings.TrimLeft(title, "#*->+ \t")
	// Wrapper punctuation, stripped from both ends so a quoted title is not shown
	// quoted and a backticked one is not shown as code.
	title = strings.Trim(title, "\"'`“”‘’ \t")
	title = strings.Join(strings.Fields(title), " ")
	title = strings.TrimRight(title, ".,;:!。 \t")

	if utf8.RuneCountInString(title) > maxTitleRunes {
		title = truncateAtWord(title, maxTitleRunes)
	}
	// A title made entirely of punctuation is not a title.
	if strings.IndexFunc(title, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}) < 0 {
		return ""
	}
	return title
}

// truncateAtWord cuts at the last space inside the limit, falling back to a hard cut
// for a language that does not put spaces between words.
func truncateAtWord(title string, limit int) string {
	runes := []rune(title)
	if len(runes) <= limit {
		return title
	}
	cut := string(runes[:limit])
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		return strings.TrimRight(cut[:idx], " ")
	}
	return strings.TrimRight(cut, " ")
}

// Explicit JSON names freeze the durable request identity independently of the
// provider port's Go field names. A later refactor must not turn a safe retry into
// an idempotency conflict after an app upgrade.
type deliveryRequestSettings struct {
	Model    string               `json:"model,omitempty"`
	Effort   string               `json:"effort,omitempty"`
	Approval ports.PermissionMode `json:"approval,omitempty"`
}
