package agentlaunch

import (
	"os"
	"path/filepath"
	"testing"
)

// The foreign sandbox spelling from issue #248: a daemon restarted from a
// shell that pointed HOME at another session's per-session directory.
const foreignSandboxHome = "/private/tmp/ao228-home"

func TestRealUserHome_IgnoresEnvironment(t *testing.T) {
	t.Setenv("HOME", foreignSandboxHome)
	home := RealUserHome()
	if home == "" {
		t.Skip("platform cannot resolve the account home (pure-Go user database)")
	}
	if home == foreignSandboxHome {
		t.Fatalf("RealUserHome returned the environment value %q; it must come from the user database", home)
	}
	if !filepath.IsAbs(home) {
		t.Fatalf("RealUserHome = %q, want an absolute path", home)
	}
}

func TestRepairForeignHome_ResetsForeignSandbox(t *testing.T) {
	if RealUserHome() == "" {
		t.Skip("platform cannot resolve the account home (pure-Go user database)")
	}
	t.Setenv("HOME", foreignSandboxHome)
	previous, repaired := RepairForeignHome()
	if !repaired {
		t.Fatalf("RepairForeignHome reported no repair for HOME=%q", foreignSandboxHome)
	}
	if previous != foreignSandboxHome {
		t.Fatalf("previous HOME = %q, want %q", previous, foreignSandboxHome)
	}
	if current := os.Getenv("HOME"); current == foreignSandboxHome {
		t.Fatalf("process HOME still %q after repair", current)
	}
}

func TestRepairForeignHome_KeepsAccountHome(t *testing.T) {
	real := RealUserHome()
	if real == "" {
		t.Skip("platform cannot resolve the account home (pure-Go user database)")
	}
	t.Setenv("HOME", real)
	if _, repaired := RepairForeignHome(); repaired {
		t.Fatalf("RepairForeignHome repaired a HOME already at the account home %q", real)
	}
}

func TestRepairForeignHome_FillsMissingHome(t *testing.T) {
	if RealUserHome() == "" {
		t.Skip("platform cannot resolve the account home (pure-Go user database)")
	}
	t.Setenv("HOME", "")
	previous, repaired := RepairForeignHome()
	if !repaired {
		t.Fatal("RepairForeignHome did not fill an absent HOME")
	}
	if previous != "" {
		t.Fatalf("previous HOME = %q, want empty for an absent variable", previous)
	}
	if os.Getenv("HOME") != RealUserHome() {
		t.Fatalf("process HOME = %q, want %q", os.Getenv("HOME"), RealUserHome())
	}
}

func TestStampSessionHome(t *testing.T) {
	real := RealUserHome()
	env := map[string]string{"AO_SESSION_ID": "s-1"}
	StampSessionHome(env)
	if real != "" && env["HOME"] != real {
		t.Fatalf("stamped HOME = %q, want the account home %q", env["HOME"], real)
	}

	env = map[string]string{"HOME": "/proj/home"}
	StampSessionHome(env)
	if env["HOME"] != "/proj/home" {
		t.Fatalf("explicit project HOME = %q, want it preserved", env["HOME"])
	}

	// A Windows-style differently-cased key must not be overridden or duplicated.
	env = map[string]string{"home": "/proj/home"}
	StampSessionHome(env)
	homeKeys := make([]string, 0, 2)
	for key := range env {
		if key == "HOME" || key == "home" {
			homeKeys = append(homeKeys, key)
		}
	}
	if len(homeKeys) != 1 || env["home"] != "/proj/home" {
		t.Fatalf("differently-cased HOME handling = %#v, want the single explicit value", env)
	}

	StampSessionHome(nil) // must not panic
}
