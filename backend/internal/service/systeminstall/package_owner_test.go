package systeminstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFileAt(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeriveOwnerFromPathReadsEachManagersLayout(t *testing.T) {
	root := t.TempDir()
	shimPrefix := filepath.Join(root, "AppData", "Roaming", "npm")
	writeFileAt(t, filepath.Join(shimPrefix, "node_modules", "@google", "gemini-cli", "dist", "index.js"), "")
	shim := writeFileAt(t, filepath.Join(shimPrefix, "gemini.cmd"), "@ECHO off\r\n\"%_prog%\"  \"%dp0%\\node_modules\\@google\\gemini-cli\\dist\\index.js\" %*\r\n")
	writeFileAt(t, filepath.Join(root, "homebrew", "Cellar", "opencode", "1.18.34", "INSTALL_RECEIPT.json"), `{"source":{"tap":"anomalyco/tap"}}`)
	writeFileAt(t, filepath.Join(root, "homebrew", "Cellar", "block-goose-cli", "1.48.0", "INSTALL_RECEIPT.json"), `{"source":{"tap":"homebrew/core"}}`)
	for _, tt := range []struct {
		name string
		path string
		want derivedOwner
		ok   bool
	}{
		{"homebrew formula", writeFileAt(t, filepath.Join(root, "homebrew", "Cellar", "block-goose-cli", "1.48.0", "bin", "goose"), ""), derivedOwner{method: "homebrew", pkg: "block-goose-cli"}, true},
		{"tapped homebrew formula", writeFileAt(t, filepath.Join(root, "homebrew", "Cellar", "opencode", "1.18.34", "bin", "opencode"), ""), derivedOwner{method: "homebrew", pkg: "anomalyco/tap/opencode"}, true},
		{"homebrew cask", writeFileAt(t, filepath.Join(root, "homebrew", "Caskroom", "devin-cli", "3000.6.11", "bin", "devin"), ""), derivedOwner{method: "homebrew", pkg: "devin-cli", cask: true}, true},
		{"scoped npm package", writeFileAt(t, filepath.Join(root, "node", "lib", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js"), ""), derivedOwner{method: "npm", pkg: "@earendil-works/pi-coding-agent"}, true},
		{"npm package", writeFileAt(t, filepath.Join(root, "node", "lib", "node_modules", "prime-agent", "dist", "cli.js"), ""), derivedOwner{method: "npm", pkg: "prime-agent"}, true},
		{"Windows npm shim", shim, derivedOwner{method: "npm", pkg: "@google/gemini-cli"}, true},
		{"pnpm keeps node_modules too", writeFileAt(t, filepath.Join(root, "pnpm", "global", "5", "node_modules", "@openai", "codex", "bin", "codex.js"), ""), derivedOwner{}, false},
		{"version manager shim", writeFileAt(t, filepath.Join(root, ".asdf", "shims", "codex"), "#!/bin/sh\n"), derivedOwner{}, false},
		{"vendor directory", writeFileAt(t, filepath.Join(root, ".grok", "downloads", "grok-1.0.13"), ""), derivedOwner{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := deriveOwnerFromPath(tt.path)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("owner = %+v, %t; want %+v, %t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestUpdateAdvisoryMaintainsConfirmedOwnerMissingFromHarnessMethods(t *testing.T) {
	binary := writeFileAt(t, filepath.Join(t.TempDir(), "homebrew", "Cellar", "grok-cli", "1.48.0", "bin", "grok"), "")
	s := newTestService("darwin", "brew", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: "1.48.0"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, pkg string, cask bool) (bool, error) {
		return method == "homebrew" && pkg == "grok-cli" && !cask, nil
	}
	s.managedVersion = fixedManagedVersion("1.53.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("a package-owned binary was compared with the vendor channel")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetOMP, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != "homebrew" || advisory.MaintenanceMethod != "homebrew" {
		t.Fatalf("advisory = %+v", advisory)
	}
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.AgentID != string(TargetOMP) {
			continue
		}
		for _, method := range plan.Methods {
			if method.ID == "homebrew" {
				if !method.UpdateAvailable || method.UpdateCommand != "brew update; brew upgrade grok-cli" {
					t.Fatalf("OMP homebrew method = %+v", method)
				}
				return
			}
		}
		t.Fatalf("OMP methods %+v do not list the confirmed Homebrew owner", plan.Methods)
	}
	t.Fatal("OMP plan missing")
}

func TestUpdateAdvisoryIgnoresDerivedOwnerTheManagerDoesNotConfirm(t *testing.T) {
	binary := writeFileAt(t, filepath.Join(t.TempDir(), "homebrew", "Cellar", "grok-cli", "1.48.0", "bin", "grok"), "")
	s := newTestService("darwin", "brew", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: "1.48.0"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	s.managedVersion = fixedManagedVersion("1.53.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) { return "1.53.0", nil }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetOMP, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.MaintenanceMethod != "" || advisory.Source == "homebrew" {
		t.Fatalf("unconfirmed path claim became a maintenance method: %+v", advisory)
	}
	if _, ok := s.derivedOwnerFor(TargetOMP); ok {
		t.Fatal("unconfirmed owner was kept")
	}
}

// A listed formula matches a Cellar path by name alone; when the receipt names
// another tap, the release check and the upgrade must follow that tap.
func TestUpdateAdvisoryFollowsReceiptTapOverListedFormulaOfTheSameName(t *testing.T) {
	cellar := filepath.Join(t.TempDir(), "homebrew", "Cellar", "opencode-v2", "2.1.0")
	binary := writeFileAt(t, filepath.Join(cellar, "bin", "opencode-v2"), "")
	writeFileAt(t, filepath.Join(cellar, "INSTALL_RECEIPT.json"), `{"source":{"tap":"my-org/tools"}}`)
	s := newTestService("darwin", "brew", "bash", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: "2.1.0"}, nil
	})
	// Homebrew's own check is by Cellar name, so it confirms both taps.
	s.ownsInstallation = func(_ context.Context, _ string, method, pkg string, _ bool) (bool, error) {
		return method == "homebrew" && filepath.Base(pkg) == "opencode-v2", nil
	}
	var queried []string
	s.managedVersion = func(_ context.Context, plan Plan) (string, error) {
		queried = append(queried, plan.Package)
		return "2.2.0", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetOpencodeV2, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.MaintenanceMethod != "homebrew" || len(queried) != 1 || queried[0] != "my-org/tools/opencode-v2" {
		t.Fatalf("advisory = %+v, queried %v; want my-org/tools/opencode-v2", advisory, queried)
	}
	owner, ok := s.derivedOwnerFor(TargetOpencodeV2)
	if !ok || owner.pkg != "my-org/tools/opencode-v2" {
		t.Fatalf("derived owner = %+v, %t", owner, ok)
	}
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.AgentID != string(TargetOpencodeV2) {
			continue
		}
		for _, method := range plan.Methods {
			if method.ID == "homebrew" {
				if !strings.Contains(method.UpdateCommand, "brew upgrade my-org/tools/opencode-v2") {
					t.Fatalf("OpenCode v2 homebrew update = %q, want the receipt's tap", method.UpdateCommand)
				}
				return
			}
		}
		t.Fatalf("OpenCode v2 methods %+v have no Homebrew method", plan.Methods)
	}
	t.Fatal("OpenCode plan missing")
}
