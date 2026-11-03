package agentauth

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// plans is the code-reviewed authentication allowlist in stable Harness
// settings order. Commands must be added here, never supplied by clients.
var plans = []Plan{
	loginMenuPlan("claude-code", "claude-login", nil, "Log in to Claude Code", []string{"claude", "auth", "login"}, "Choose Claude subscription, Anthropic Console, or SSO", "https://code.claude.com/docs/en/installation"),
	loginMenuPlan("codex", "codex-login", []string{"--use-default-credential-store"}, "Log in to Codex", []string{"codex", "login"}, "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex"),
	plan("opencode", "Log in to OpenCode", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://github.com/anomalyco/opencode"),
	plan("opencode-v2", "Log in to OpenCode 2", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://opencode.ai/v2/docs"),
	piLoginPlan(),
	plan("agy", "Log in to Agy", []string{"agy"}, "Native first-run browser sign-in", "https://github.com/google-antigravity/antigravity-cli"),
	ompLoginPlan(),
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
	p := plan("pi", "Log in to Pi", []string{"pi"}, "Pi opens its login picker automatically", "https://github.com/earendil-works/pi")
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
	return plan("omp", "Log in to OMP", []string{"omp", "setup"}, "Sign in to a provider in OMP's setup wizard, then finish or skip the remaining setup steps", "https://github.com/can1357/oh-my-pi")
}

func loginMenuPlan(agentID, launcher string, launcherArgs []string, title string, command []string, guidance, docs string) Plan {
	p := plan(agentID, title, command, guidance, docs)
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

// plan builds a login plan: the table's entries are all logins, so plan()
// pins ActionLogin instead of taking it as a parameter.
func plan(agentID, title string, command []string, guidance, docs string) Plan {
	logout := logoutCommand(agentID)
	return Plan{
		LogoutCommand:    strings.Join(logout, " "),
		logoutCommand:    logout,
		AgentID:          agentID,
		Action:           ActionLogin,
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
	default:
		return nil
	}
}
