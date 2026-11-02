package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestPlansMatchAuthenticationMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id, title, executable, guidance, docs, terminalInput string
		action                                               Action
		argv                                                 []string
	}{
		{"claude-code", "Log in to Claude Code", "claude", "Choose Claude subscription, Anthropic Console, or SSO", "https://code.claude.com/docs/en/installation", "", ActionLogin, []string{"claude", "auth", "login"}},
		{"codex", "Log in to Codex", "codex", "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex", "", ActionLogin, []string{"codex", "login"}},
		{"opencode", "Log in to OpenCode", "opencode", "Native provider chooser", "https://github.com/anomalyco/opencode", "", ActionLogin, []string{"opencode", "auth", "login"}},
		{"opencode-v2", "Log in to OpenCode 2", "opencode", "Native provider chooser", "https://opencode.ai/v2/docs", "", ActionLogin, []string{"opencode", "auth", "login"}},
		{"memcode", "Set up memcode", "", "Configure a native endpoint and provider credentials before launching AO", "https://github.com/memcode-ai/memcode", "", ActionSetup, nil},
		{"kimi", "Log in to Kimi", "kimi", "Kimi opens its login picker automatically", "https://moonshotai.github.io/kimi-code/en/", "", ActionLogin, []string{"kimi"}},
		{"pi", "Log in to Pi", "pi", "Pi opens its login picker automatically", "https://github.com/earendil-works/pi", "", ActionLogin, []string{"pi"}},
		{"amp", "Log in to Amp", "amp", "Native browser flow", "https://ampcode.com/manual", "", ActionLogin, []string{"amp", "login"}},
		{"droid", "Log in to Droid", "droid", "Select Open login after Droid finishes starting", "https://docs.factory.ai/droid-cli/cli-reference", "/login\r", ActionLogin, []string{"droid"}},
		{"goose", "Set up Goose", "goose", "Native provider configuration; AO forwards terminal input without persisting or logging the raw input, while Goose controls credential storage", "https://block.github.io/goose/index.html", "", ActionSetup, []string{"goose", "configure"}},
		{"qwen", "Set up Qwen", "qwen", "Select Open setup after Qwen finishes starting to configure a model provider", "https://qwenlm.github.io/qwen-code-docs/en/users/configuration/auth/", "i\x7f/auth\r", ActionSetup, []string{"qwen"}},
		{"agy", "Log in to Agy", "agy", "Native first-run browser sign-in", "https://github.com/google-antigravity/antigravity-cli", "", ActionLogin, []string{"agy"}},
		{"autohand", "Log in to Autohand", "autohand", "Native Autohand account sign-in", "https://docs.autohand.ai/working-with-autohand-code/cli-reference", "", ActionLogin, []string{"autohand", "login"}},
		{"omp", "Log in to OMP", "omp", "Sign in to a provider in OMP's setup wizard, then finish or skip the remaining setup steps", "https://github.com/can1357/oh-my-pi", "", ActionLogin, []string{"omp", "setup"}},
		{"fx", "Log in to fx", "fx", "Select Vercel, Codex, or Grok in fx's native login flow", "https://fx.sh/docs", "", ActionLogin, []string{"fx", "login"}},
	}

	svc := New(foundExecutables(cases), nil)
	plans := svc.Plans(context.Background())
	if len(plans) != len(cases) {
		t.Fatalf("Plans() returned %d plans, want %d", len(plans), len(cases))
	}
	seen := make(map[string]bool, len(plans))
	for i, want := range cases {
		got := plans[i]
		wantLaunchMode := LaunchTerminal
		if want.id == "memcode" {
			// Memcode configures providers through files and environment, with no
			// command to drive.
			wantLaunchMode = LaunchDocumentation
		}
		if seen[got.AgentID] {
			t.Fatalf("Plans() returned duplicate id %q", got.AgentID)
		}
		seen[got.AgentID] = true
		if got.AgentID != want.id || got.Action != want.action || got.LaunchMode != wantLaunchMode || !got.Available || got.Guidance != want.guidance || got.DocumentationURL != want.docs {
			t.Fatalf("plan %d = %#v, want id=%q action=%q available=true guidance=%q docs=%q", i, got, want.id, want.action, want.guidance, want.docs)
		}
		wantCommand := append([]string(nil), want.argv...)
		if len(wantCommand) > 0 {
			wantCommand[0] = "/test/bin/" + want.executable
		}
		if got.title != want.title || got.DisplayCommand != strings.Join(want.argv, " ") || !reflect.DeepEqual(got.command, wantCommand) || got.terminalInput != want.terminalInput {
			t.Fatalf("plan %q terminal = title %q display %q argv %#v, want title %q display %q argv %#v", want.id, got.title, got.DisplayCommand, got.command, want.title, strings.Join(want.argv, " "), wantCommand)
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"command":`) || strings.Contains(string(data), `"terminalInput":`) || strings.Contains(string(data), `"initialInput":`) {
			t.Fatalf("plan %q serialized trusted command data: %s", want.id, data)
		}
	}
}
func TestUnknownPlanReturnsStableTargetError(t *testing.T) {
	t.Parallel()

	_, err := New(foundExecutables(nil), nil).Plan(context.Background(), "not-a-harness")
	var targetErr *apierr.Error
	if !errors.As(err, &targetErr) || targetErr.Kind != apierr.KindInvalid || targetErr.Code != "AGENT_AUTH_TARGET_UNKNOWN" {
		t.Fatalf("Plan() error = %v, want AGENT_AUTH_TARGET_UNKNOWN", err)
	}
}

func TestPlanMissingExecutableIsUnavailable(t *testing.T) {
	t.Parallel()

	plan, err := New(foundExecutables(nil), nil).Plan(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Available || plan.Reason == "" {
		t.Fatalf("Plan(codex) = %#v, want unavailable plan with useful reason", plan)
	}
}

func foundExecutables(cases []struct {
	id, title, executable, guidance, docs, terminalInput string
	action                                               Action
	argv                                                 []string
}) ExecutableFinder {
	found := map[string]string{}
	for _, tc := range cases {
		if tc.executable != "" {
			found[tc.executable] = "/test/bin/" + tc.executable
		}
	}
	return executableFinderFunc(func(name string) (string, error) {
		path, ok := found[name]
		if !ok {
			return "", errors.New("not found")
		}
		return path, nil
	})
}
