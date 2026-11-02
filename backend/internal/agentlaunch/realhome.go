package agentlaunch

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// RealUserHome resolves the OS account's home from the user database (getpwuid)
// rather than the environment, so a daemon started with a foreign HOME cannot
// pass the mistake on: every value derived from this one describes the real
// account even when $HOME already points at another session's sandbox
// (issue #248). Empty when the platform cannot resolve the account, which
// leaves every caller's environment untouched.
func RealUserHome() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	home := strings.TrimSpace(u.HomeDir)
	if home == "" || !filepath.IsAbs(home) {
		return ""
	}
	return home
}

// RepairForeignHome replaces this process's HOME with RealUserHome when the two
// disagree, and reports the value it replaced. Children (pty hosts, the tmux
// server and its panes, chat providers, git) inherit os.Environ(), so a daemon
// restarted from a shell that pointed HOME at another session's sandbox would
// hand that sandbox to everything it spawns. A no-op when the real home cannot
// be resolved.
func RepairForeignHome() (previous string, repaired bool) {
	real := RealUserHome()
	if real == "" {
		return "", false
	}
	current, _ := os.LookupEnv("HOME")
	if strings.TrimSpace(current) == real {
		return "", false
	}
	_ = os.Setenv("HOME", real)
	return current, true
}

// StampSessionHome pins the environment's HOME to the OS account's real home
// unless it already carries one, so a session can never inherit a HOME from
// whatever process happened to launch it. The tmux server keeps the HOME of the
// daemon that started it and panes only export what this map carries, so the
// stamp must not trust even a repaired daemon process (issue #248). An explicit
// project value wins; case-insensitive so a Windows value under a different
// case is neither overridden nor duplicated.
func StampSessionHome(env map[string]string) {
	if env == nil || hasEnvKey(env, "HOME") {
		return
	}
	if home := RealUserHome(); home != "" {
		env["HOME"] = home
	}
}

func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}
