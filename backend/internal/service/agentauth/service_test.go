package agentauth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

func TestStartRejectsUnstartablePlans(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	svc := New(foundExecutables(nil), opener)

	cases := []struct {
		name    string
		agentID string
		code    string
	}{
		{name: "unknown target", agentID: "not-a-harness", code: "AGENT_AUTH_TARGET_UNKNOWN"},
		{name: "unavailable command", agentID: "codex", code: "AGENT_AUTH_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Start(context.Background(), tc.agentID)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != tc.code {
				t.Fatalf("Start(%q) error = %#v, want invalid %s", tc.agentID, err, tc.code)
			}
		})
	}
	if opener.calls != 0 {
		t.Fatalf("OpenCommandTerminal calls = %d, want 0", opener.calls)
	}
}

func TestStartPiInjectsLoginAutomatically(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	svc := New(foundExecutable("pi"), opener)

	got, err := svc.Start(context.Background(), "pi")
	if err != nil {
		t.Fatalf("Start(pi): %v", err)
	}
	wantInput := shellterm.OpenCommandTerminalInput{
		Argv:         []string{"/test/bin/pi"},
		Title:        "Log in to Pi",
		InitialInput: "/login",
		InitialInputReadyStates: []shellterm.InitialInputReadyState{
			{Text: "0.0%/"},
			{Text: "Pi can explain its own features"},
		},
		SendInitialInputOnReadyTimeout: true,
	}
	if !reflect.DeepEqual(opener.input, wantInput) {
		t.Fatalf("OpenCommandTerminal input = %#v, want %#v", opener.input, wantInput)
	}
	if got.TerminalInput != "" {
		t.Fatalf("Start(pi) terminal input = %q, want none so the login is not held behind a button", got.TerminalInput)
	}
}

func TestStartOMPOpensNativeProviderSetupWithoutInjectingInput(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	svc := New(foundExecutable("omp"), opener)

	got, err := svc.Start(context.Background(), "omp")
	if err != nil {
		t.Fatalf("Start(omp): %v", err)
	}
	wantInput := shellterm.OpenCommandTerminalInput{
		Argv:  []string{"/test/bin/omp", "setup"},
		Title: "Log in to OMP",
	}
	if !reflect.DeepEqual(opener.input, wantInput) {
		t.Fatalf("OpenCommandTerminal input = %#v, want %#v", opener.input, wantInput)
	}
	if got.TerminalInput != "" {
		t.Fatalf("Start(omp) terminal input = %q, want none because OMP owns provider sign-in", got.TerminalInput)
	}
}

// Screens captured from Pi 0.85.1 in an AO auth terminal (120 columns), so the
// reviewed markers are checked against what Pi actually renders.
const (
	piDefaultStartupScreen = ` pi v0.85.1
 escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more
 Press ctrl+o to show full startup help and loaded resources.

 Pi can explain its own features and look up its docs. Ask it how to use or extend Pi.

────────────────────────────────────────────────────────────────────────────────
~\.ao\data\auth-workspace\shellterm-a15141f25c4872b3
0.0%/262k (auto)                                         (moonshotai) kimi-k2.6 • medium`
	piQuietStartupWithModelScreen = `────────────────────────────────────────────────────────────────────────────────
~\.ao\data\auth-workspace\shellterm-a86fe63f7a8f957d
0.0%/1.0M (auto)                                       (zai-coding-cn) glm-5.3 • high`
	piQuietStartupWithoutModelScreen = `────────────────────────────────────────────────────────────────────────────────
~\.ao\data\auth-workspace\shellterm-0c1d2e3f4a5b6c7d
0.0%/0 (auto)                                                                   no-model`
	piStillLoadingScreen = ` pi v0.85.1
 escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more`
)

func TestPiReadyStatesMatchRenderedPiScreens(t *testing.T) {
	t.Parallel()

	readyStates := planByAgentID["pi"].initialInputReadyStates
	for _, tc := range []struct {
		name   string
		screen string
		ready  bool
	}{
		{name: "default startup", screen: piDefaultStartupScreen, ready: true},
		{name: "quietStartup with a selected model", screen: piQuietStartupWithModelScreen, ready: true},
		{name: "quietStartup without a model", screen: piQuietStartupWithoutModelScreen, ready: true},
		{name: "before the footer renders", screen: piStillLoadingScreen, ready: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellterm.MatchInitialInputReadyState(tc.screen, readyStates)
			if (got != nil) != tc.ready {
				t.Fatalf("MatchInitialInputReadyState = %#v, want ready %v", got, tc.ready)
			}
		})
	}
}

func TestStartFallsBackToAgentResolvedBinaryOutsidePATH(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "claude-code", path: "/Users/test/.claude/local/claude"}
	svc := NewWithAgentResolver(resolver, resolver, opener, "")
	svc.selfExecutable = func() (string, error) { return "/Applications/AO.app/Contents/MacOS/ao", nil }

	_, err := svc.Start(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Start(claude-code): %v", err)
	}
	if got := opener.input.Argv; !reflect.DeepEqual(got, []string{"/Applications/AO.app/Contents/MacOS/ao", "claude-login", "--executable", "/Users/test/.claude/local/claude"}) {
		t.Fatalf("terminal argv = %#v, want trusted Claude login menu with adapter-resolved binary", got)
	}
}

func TestStartOpensCodexLoginMethodMenu(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "codex", path: "/managed/bin/codex"}
	svc := NewWithAgentResolver(resolver, resolver, opener, "")
	svc.selfExecutable = func() (string, error) { return "/Applications/AO.app/Contents/MacOS/ao", nil }

	_, err := svc.Start(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Start(codex): %v", err)
	}
	want := []string{"/Applications/AO.app/Contents/MacOS/ao", "codex-login", "--executable", "/managed/bin/codex", "--use-default-credential-store"}
	if got := opener.input.Argv; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal argv = %#v, want trusted Codex login menu", got)
	}
}

func TestStartPrefersAdapterResolvedBinaryOverGenericPATHMatch(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "agy", path: "/validated/agy/agy"}
	svc := NewWithAgentResolver(foundExecutable("agy"), resolver, opener, "")

	_, err := svc.Start(context.Background(), "agy")
	if err != nil {
		t.Fatalf("Start(agy): %v", err)
	}
	if got := opener.input.Argv; !reflect.DeepEqual(got, []string{"/validated/agy/agy"}) {
		t.Fatalf("terminal argv = %#v, want adapter-validated Agy binary", got)
	}
}

type recordingTerminalOpener struct {
	calls    int
	input    shellterm.OpenCommandTerminalInput
	terminal shellterm.ShellTerminal
}

type managedExecutableResolver struct {
	agentID string
	path    string
}

func (m managedExecutableResolver) LookPath(string) (string, error) {
	return "", errors.New("not found on PATH")
}

func (m managedExecutableResolver) ResolveAgentBinary(_ context.Context, agentID string) (string, error) {
	if agentID != m.agentID {
		return "", errors.New("unknown agent")
	}
	return m.path, nil
}

func foundExecutable(executable string) ExecutableFinder {
	return executableFinderFunc(func(name string) (string, error) {
		if name != executable {
			return "", errors.New("not found")
		}
		return "/test/bin/" + executable, nil
	})
}

func (o *recordingTerminalOpener) OpenCommandTerminal(_ context.Context, in shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	o.calls++
	o.input = in
	return o.terminal, nil
}

func TestLogoutUsesAdapterExecutableAndNeverLoginLauncher(t *testing.T) {
	for _, tc := range []struct {
		agent string
		args  []string
	}{
		{"claude-code", []string{"auth", "logout"}},
		{"codex", []string{"logout"}},
		{"opencode", []string{"auth", "logout"}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "logout-terminal"}}
			resolver := managedExecutableResolver{agentID: tc.agent, path: "/managed/agent"}
			svc := NewWithAgentResolver(nil, resolver, opener, "")
			result, err := svc.Logout(context.Background(), tc.agent)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{"/managed/agent"}, tc.args...)
			if !reflect.DeepEqual(opener.input.Argv, want) {
				t.Fatalf("argv = %v, want %v", opener.input.Argv, want)
			}
			if result.Action != "logout" || result.Terminal.HandleID != "logout-terminal" || result.TerminalInput != "" || opener.input.InitialInput != "" {
				t.Fatalf("unexpected logout: %+v, input %+v", result, opener.input)
			}
		})
	}
}

func TestLogoutRejectsUnknownUnsupportedAndMissingExecutable(t *testing.T) {
	for _, tc := range []struct{ agent, code string }{
		{"not-a-harness", "AGENT_AUTH_TARGET_UNKNOWN"},
		{"goose", "AGENT_AUTH_TARGET_UNKNOWN"},
		{"pi", "AGENT_LOGOUT_UNSUPPORTED"},
		{"codex", "AGENT_AUTH_UNAVAILABLE"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			opener := &recordingTerminalOpener{}
			_, err := New(foundExecutables(nil), opener).Logout(context.Background(), tc.agent)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if opener.calls != 0 {
				t.Fatal("rejected logout opened a terminal")
			}
		})
	}
}
