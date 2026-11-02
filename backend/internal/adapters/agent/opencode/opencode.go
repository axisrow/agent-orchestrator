// Package opencode holds the binary-resolution and v2 data-home surface shared
// by the opencode-v2 agent adapter, its ACP chat driver, and the AO skill
// install. The OpenCode 1 adapter (Plugin, launch/restore vocabulary, activity
// plugin) has been removed; opencode-v2 is the only supported OpenCode harness.
//
// The shared pieces live here because OpenCode 1 and 2 share an executable
// name, so binary discovery must enumerate every install location (managed
// launchers, PATH, npm, the official installer, Homebrew formulas for both
// majors) and let ResolveBinaryForMajor pick the one matching the harness
// major, rejecting the other with a classified version error.
package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

var opencodeUnixPaths = []string{
	"/usr/local/bin/opencode",
	"/opt/homebrew/bin/opencode",
}

// BinaryCandidates returns every distinct opencode executable in
// resolution order: the LookPath winner, other PATH entries, then well-known
// install locations. OpenCode 1 and 2 share an executable name, so a machine can
// carry both and a harness must be able to pick the one matching its major.
func BinaryCandidates(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var found []string
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" {
			return
		}
		key := filepath.Clean(path)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		found = append(found, path)
	}

	// AO's managed copy, when there is one, is tried first.
	for _, name := range []string{"opencode", "opencode-v2"} {
		if managed, ok := binaryutil.ManagedBinary(name); ok {
			add(managed)
		}
	}
	names := []string{"opencode"}
	if runtime.GOOS == "windows" {
		names = []string{"opencode.cmd", "opencode.exe", "opencode"}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			add(path)
			break
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if hookutil.IsExecutableFile(candidate) {
				add(candidate)
				break
			}
		}
	}

	var candidates []string
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "opencode.cmd"),
				filepath.Join(appData, "npm", "opencode.exe"),
			)
		}
		candidates = append(candidates, binaryutil.WindowsPackageManagerBinCandidates("opencode")...)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".opencode", "bin", "opencode.exe"))
		}
	} else {
		candidates = append(candidates, opencodeUnixPaths...)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, ".npm-global", "bin", "opencode"),
				filepath.Join(home, ".npm", "bin", "opencode"),
				filepath.Join(home, ".local", "bin", "opencode"),
				filepath.Join(home, ".opencode", "bin", "opencode"),
			)
			candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "opencode")...)
			nodeManagerCandidates, err := binaryutil.UnixNodeManagerBinCandidates(ctx, home, "opencode")
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, nodeManagerCandidates...)
		}
	}
	if dir, err := V2NPMBinDir(); err == nil {
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(dir, "opencode.cmd"), filepath.Join(dir, "opencode.exe"))
		} else {
			candidates = append(candidates, filepath.Join(dir, "opencode"))
		}
	}
	if runtime.GOOS != "windows" {
		if dir, err := V2InstallerBinDir(); err == nil {
			candidates = append(candidates, filepath.Join(dir, "opencode"))
		}
		candidates = append(candidates, v2HomebrewCandidates()...)
		// v1 formula locations stay in discovery so a leftover OpenCode 1
		// binary is found and classified as the wrong major for opencode-v2.
		candidates = append(candidates, v1HomebrewCandidates()...)
	}
	for _, candidate := range candidates {
		if hookutil.IsExecutableFile(candidate) {
			add(candidate)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return found, nil
}
