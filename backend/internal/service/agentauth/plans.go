package agentauth

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// plans is the code-reviewed authentication allowlist in stable Harness
// settings order. Commands must be added here, never supplied by clients.
// qwenAuthInput works with Qwen's default editor and its optional Vim mode.
// In NORMAL, i enters INSERT and Backspace is harmless on the empty prompt; in
// INSERT/default mode, Backspace removes the literal i before /auth is entered.
const qwenAuthInput = "i\x7f/auth\r"

var plans = []Plan{
	loginMenuPlan("claude-code", "claude-login", nil, "Log in to Claude Code", []string{"claude", "auth", "login"}, "Choose Claude subscription, Anthropic Console, or SSO", "https://code.claude.com/docs/en/installation"),
	loginMenuPlan("codex", "codex-login", []string{"--use-default-credential-store"}, "Log in to Codex", []string{"codex", "login"}, "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex"),
	plan("opencode", ActionLogin, "Log in to OpenCode", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://github.com/anomalyco/opencode"),
	plan("opencode-v2", ActionLogin, "Log in to OpenCode 2", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://opencode.ai/v2/docs"),
	documentationPlan("memcode", ActionSetup, "Set up memcode", "Configure a native endpoint and provider credentials before launching AO", "https://github.com/memcode-ai/memcode"),
	kimiLoginPlan(),
	piLoginPlan(),
	plan("amp", ActionLogin, "Log in to Amp", []string{"amp", "login"}, "Native browser flow", "https://ampcode.com/manual"),
	terminalInputPlan("droid", ActionLogin, "Log in to Droid", []string{"droid"}, "/login\r", "Select Open login after Droid finishes starting", "https://docs.factory.ai/droid-cli/cli-reference"),
	plan("goose", ActionSetup, "Set up Goose", []string{"goose", "configure"}, "Native provider configuration; AO forwards terminal input without persisting or logging the raw input, while Goose controls credential storage", "https://block.github.io/goose/index.html"),
	terminalInputPlan("qwen", ActionSetup, "Set up Qwen", []string{"qwen"}, qwenAuthInput, "Select Open setup after Qwen finishes starting to configure a model provider", "https://qwenlm.github.io/qwen-code-docs/en/users/configuration/auth/"),
	plan("agy", ActionLogin, "Log in to Agy", []string{"agy"}, "Native first-run browser sign-in", "https://github.com/google-antigravity/antigravity-cli"),
	plan("autohand", ActionLogin, "Log in to Autohand", []string{"autohand", "login"}, "Native Autohand account sign-in", "https://docs.autohand.ai/working-with-autohand-code/cli-reference"),
	ompLoginPlan(),
	plan("fx", ActionLogin, "Log in to fx", []string{"fx", "login"}, "Select Vercel, Codex, or Grok in fx's native login flow", "https://fx.sh/docs"),
}

func terminalInputPlan(agentID string, action Action, title string, command []string, terminalInput, guidance, docs string) Plan {
	p := plan(agentID, action, title, command, guidance, docs)
	p.terminalInput = terminalInput
	return p
}

// kimiLoginPlan opens Kimi's TUI and injects /login so the native platform
// picker (Kimi Code browser login and Kimi Platform API keys) is offered;
// the bare `kimi login` subcommand only runs the device-code flow. The input
// is sent automatically once Kimi renders its unauthenticated ready message,
// without relying on a fixed startup delay. initialInput carries no trailing
// Enter because terminal delivery
// (SendMessage) presses it. Kimi's first-run "Trust this folder?" dialog would
// swallow that input in AO's private auth workspace, so the workspace trust
// record is seeded first.
func kimiLoginPlan() Plan {
	p := plan("kimi", ActionLogin, "Log in to Kimi", []string{"kimi"}, "Kimi opens its login picker automatically", "https://moonshotai.github.io/kimi-code/en/")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{{Text: "Run /login or /provider to get started."}}
	p.prepareWorkspace = kimi.EnsureWorkspaceTrusted
	return p
}

// piLoginPlan opens Pi's TUI and injects /login automatically; Pi has no
// login subcommand. The primary ready marker is the footer's context stat: a
// fresh Pi session always renders "0.0%/<window>" there, with or without a
// selected model and even with quietStartup, which drops the onboarding line
// kept as a second marker. Pi keeps text typed during its brief startup tail
// in the editor, and the runtime's pause before Enter covers that window. If a
// future Pi renders neither marker, /login is still sent when the ready wait
// expires: by then Pi is at its prompt, where /login is always valid.
func piLoginPlan() Plan {
	p := plan("pi", ActionLogin, "Log in to Pi", []string{"pi"}, "Pi opens its login picker automatically", "https://github.com/earendil-works/pi")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{
		{Text: "0.0%/"},
		{Text: "Pi can explain its own features"},
	}
	p.sendInitialInputOnReadyTimeout = true
	return p
}

// ompLoginPlan uses OMP's native onboarding command, whose first scene is
// provider sign-in even when setup is already complete. OMP paints its prompt
// before opening first-run setup, so prompt markers cannot safely gate /login
// injection. Let OMP own the picker without sending any terminal input.
func ompLoginPlan() Plan {
	return plan("omp", ActionLogin, "Log in to OMP", []string{"omp", "setup"}, "Sign in to a provider in OMP's setup wizard, then finish or skip the remaining setup steps", "https://github.com/can1357/oh-my-pi")
}

func documentationPlan(agentID string, action Action, title, guidance, docs string) Plan {
	p := plan(agentID, action, title, nil, guidance, docs)
	p.LaunchMode = LaunchDocumentation
	return p
}

func loginMenuPlan(agentID, launcher string, launcherArgs []string, title string, command []string, guidance, docs string) Plan {
	p := plan(agentID, ActionLogin, title, command, guidance, docs)
	p.launcher = launcher
	p.launcherArgs = launcherArgs
	return p
}

var planByAgentID = func() map[string]Plan {
	out := make(map[string]Plan, len(plans))
	for _, plan := range plans {
		out[plan.AgentID] = plan
	}
	return out
}()

func plan(agentID string, action Action, title string, command []string, guidance, docs string) Plan {
	logout := logoutCommand(agentID)
	return Plan{
		LogoutCommand:    strings.Join(logout, " "),
		logoutCommand:    logout,
		AgentID:          agentID,
		Action:           action,
		LaunchMode:       LaunchTerminal,
		DisplayCommand:   strings.Join(command, " "),
		Guidance:         guidance,
		DocumentationURL: docs,
		command:          command,
		title:            title,
	}
}

// Only documented CLI subcommands belong here. An absent entry explicitly
// means unsupported; do not infer logout from login or delete credential files.
func logoutCommand(agentID string) []string {
	switch agentID {
	case "claude-code":
		return []string{"claude", "auth", "logout"}
	case "codex":
		return []string{"codex", "logout"}
	case "opencode", "opencode-v2":
		return []string{"opencode", "auth", "logout"}
	case "amp":
		return []string{"amp", "logout"}
	default:
		return nil
	}
}
