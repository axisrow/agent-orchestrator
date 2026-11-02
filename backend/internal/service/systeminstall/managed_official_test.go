package systeminstall

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func envValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		if name, v, ok := strings.Cut(entry, "="); ok && name == key {
			value = v // the last entry wins, as it does for the process
		}
	}
	return value
}

// Every recipe pins the release, installs inside the release directory, keeps
// the user's HOME and PATH out of the installer, and names where the CLI lands.
func TestOfficialManagedRecipesInstallInsideTheRelease(t *testing.T) {
	dir := "/data/harnesses/x/versions/v3"
	for target, want := range map[Target]struct {
		env  map[string]string
		args []string
	}{
		TargetOMP: {env: map[string]string{"PI_INSTALL_DIR": dir + "/bin"}, args: []string{"--binary", "--ref", "v1.19.3"}},
		TargetCodex: {env: map[string]string{
			"CODEX_HOME": dir + "/codex-home", "CODEX_INSTALL_DIR": dir + "/bin",
			"CODEX_RELEASE": "1.19.3", "CODEX_NON_INTERACTIVE": "1",
		}},
		TargetOpencodeV2: {env: map[string]string{"VERSION": "1.19.3"}, args: []string{"--no-modify-path"}},
		TargetAgy:        {args: []string{"--dir", dir + "/bin"}},
	} {
		t.Run(string(target), func(t *testing.T) {
			recipe, ok := officialManagedRecipes[target]
			if !ok {
				t.Fatal("no recipe")
			}
			env := recipe.env(dir, "1.19.3")
			if home := envValue(env, "HOME"); !strings.HasPrefix(home, dir+"/") {
				t.Fatalf("HOME = %q, want a directory inside the release", home)
			}
			if path := envValue(env, "PATH"); path != managedInstallerPath {
				t.Fatalf("PATH = %q, want system tools only", path)
			}
			// A fish user's SHELL sends fx's PATH line to a profile folder
			// the private HOME does not have, and the install fails.
			if shell := envValue(append([]string{"SHELL=/opt/homebrew/bin/fish"}, env...), "SHELL"); shell != "/bin/sh" {
				t.Fatalf("SHELL = %q, want a plain sh whatever the user's shell", shell)
			}
			for key, value := range want.env {
				if got := envValue(env, key); got != value {
					t.Fatalf("%s = %q, want %q", key, got, value)
				}
			}
			if got := recipe.args(dir, "1.19.3"); !slices.Equal(got, want.args) {
				t.Fatalf("args = %q, want %q", got, want.args)
			}
			if recipe.binRel == "" || filepath.IsAbs(recipe.binRel) {
				t.Fatalf("binRel = %q, want a path inside the release", recipe.binRel)
			}
		})
	}
}

// assertResolvesInto fails unless launcher resolves to a file inside release.
// Both sides are resolved: on macOS t.TempDir is under /var, a link to
// /private/var, and only the launcher's resolved form says so.
func assertResolvesInto(t *testing.T, launcher, release string) {
	t.Helper()
	target, err := filepath.EvalSymlinks(launcher)
	if err == nil {
		release, err = filepath.EvalSymlinks(release)
	}
	if err != nil || !strings.HasPrefix(target, release+string(filepath.Separator)) {
		t.Fatalf("launcher resolves to %q (%v), want inside the new release %s", target, err, release)
	}
}

// Codex's cask upgrade deletes the codex-resources a running session can
// still load, so AO updates a cask install with Codex's own installer into a
// release of its own and leaves the cask alone.
func TestCodexCaskUpdatesWithItsInstallerBesideARunningSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	root := t.TempDir()
	s := newTestService("darwin", "brew", "sh")
	s.managedRoot = root
	s.releases = &releaseStoreStub{}
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "cx-1", Harness: domain.HarnessCodex}}}
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	caskCopy := "/opt/homebrew/bin/codex"
	launcher := filepath.Join(root, "bin", "codex")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		if target, err := filepath.EvalSymlinks(launcher); err == nil {
			return VerifyResult{ResolvedPath: launcher, Output: strings.TrimSpace(readFile(target))}, nil
		}
		return VerifyResult{ResolvedPath: caskCopy, Output: "0.161.0"}, nil
	})
	var mu sync.Mutex
	var scripts []ports.InstallScriptCommand
	var commands []ports.InstallCommand
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		mu.Lock()
		scripts = append(scripts, command)
		mu.Unlock()
		// Like chatgpt.com/codex/install.sh: $CODEX_INSTALL_DIR/codex at
		// $CODEX_RELEASE, and a PATH line appended to a profile in $HOME,
		// which it never creates.
		profile, err := os.OpenFile(filepath.Join(envValue(command.Env, "HOME"), ".zprofile"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return ports.InstallScriptResult{}, err
		}
		if err := profile.Close(); err != nil {
			return ports.InstallScriptResult{}, err
		}
		bin := envValue(command.Env, "CODEX_INSTALL_DIR")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return ports.InstallScriptResult{}, err
		}
		return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "codex"), []byte(envValue(command.Env, "CODEX_RELEASE")+"\n"), 0o755)
	})
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, command)
		return nil
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})
	s.mu.Lock()
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {
		AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, LatestVersion: "0.163.0",
		MaintenanceMethod: "homebrew", maintenanceCask: true, maintenancePackage: "codex", CheckedAt: time.Now(),
	}}
	s.mu.Unlock()

	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatalf("update with a session running = %v, want it to install beside the session", err)
	}
	s.workers.Wait()
	job, _ := s.Status(context.Background(), TargetCodex)
	if job.Status != StatusSucceeded {
		t.Fatalf("job = %+v", job)
	}
	for _, command := range commands {
		if slices.Contains(command.Argv, "brew") {
			t.Fatalf("ran %q, want the cask left alone", command.Argv)
		}
	}
	if len(scripts) != 1 || scripts[0].URL != codexInstallScriptURL || !scripts[0].Isolated {
		t.Fatalf("installer runs = %+v, want one isolated Codex installer", scripts)
	}
	release := filepath.Join(root, string(TargetCodex), "versions", "v1")
	if got := envValue(scripts[0].Env, "CODEX_INSTALL_DIR"); got != filepath.Join(release, "bin") {
		t.Fatalf("CODEX_INSTALL_DIR = %q, want inside %s", got, release)
	}
	if got := envValue(scripts[0].Env, "CODEX_RELEASE"); got != "0.163.0" {
		t.Fatalf("CODEX_RELEASE = %q, want the advisory's release", got)
	}
	assertResolvesInto(t, launcher, release)
}

// managedOfficialFixture is a Linux service that updates target with its
// official installer into AO's releases. The fake installer writes the CLI
// where the recipe says, reporting the release it was pinned to; sessions run
// the user's copy until AO's launcher exists.
type managedOfficialFixture struct {
	s        *Service
	root     string
	launcher string
	mu       sync.Mutex
	latest   string
	scripts  []ports.InstallScriptCommand
}

func newManagedOfficialFixture(t *testing.T, target Target, userCopy, userVersion, latest string, pin func(env []string) string) *managedOfficialFixture {
	t.Helper()
	f := &managedOfficialFixture{root: t.TempDir(), latest: latest}
	s := newTestService("linux", "bash", "sh")
	s.managedRoot = f.root
	s.releases = &releaseStoreStub{}
	// No package manager owns the copy.
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	s.managedVersion = fixedManagedVersion("9.9.9", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.latest, nil
	}
	recipe := officialManagedRecipes[target]
	f.launcher = filepath.Join(f.root, "bin", filepath.Base(userCopy))
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		if resolved, err := filepath.EvalSymlinks(f.launcher); err == nil {
			return VerifyResult{ResolvedPath: f.launcher, Output: strings.TrimSpace(readFile(resolved))}, nil
		}
		return VerifyResult{ResolvedPath: userCopy, Output: userVersion}, nil
	})
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		f.mu.Lock()
		f.scripts = append(f.scripts, command)
		f.mu.Unlock()
		// The release directory is the private HOME's parent.
		binary := filepath.Join(filepath.Dir(envValue(command.Env, "HOME")), recipe.binRel)
		if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
			return ports.InstallScriptResult{}, err
		}
		return ports.InstallScriptResult{}, os.WriteFile(binary, []byte(pin(command.Env)+"\n"), 0o755)
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})
	f.s = s
	return f
}

func (f *managedOfficialFixture) setLatest(latest string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest = latest
}

// After AO updates a harness into a release of its own, sessions run AO's
// launcher, which is not where the vendor's installer puts the binary. The
// next advisory must still name the official installer, or AO stays on that
// release.
func TestManagedOfficialReleaseStaysUpdatable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	f := newManagedOfficialFixture(t, TargetOpencodeV2, "/Users/me/.opencode/bin/opencode-v2", "opencode, version 1.19.2", "1.19.3",
		func(env []string) string { return "opencode, version " + envValue(env, "VERSION") })
	ctx := context.Background()
	for i, latest := range []string{"1.19.3", "1.19.4"} {
		f.setLatest(latest)
		advisory, err := f.s.UpdateAdvisory(ctx, TargetOpencodeV2, true)
		if err != nil || advisory.Status != UpdateStatusBehindLatest || advisory.MaintenanceMethod != "official-installer" || advisory.LatestVersion != latest {
			t.Fatalf("update %d: advisory = %+v, %v; want official-installer behind %s", i+1, advisory, err, latest)
		}
		if _, err := f.s.StartAgentOperation(ctx, TargetOpencodeV2, "", AgentOperationUpdate, ""); err != nil {
			t.Fatalf("update %d: %v", i+1, err)
		}
		f.s.workers.Wait()
		if job, _ := f.s.Status(ctx, TargetOpencodeV2); job.Status != StatusSucceeded {
			t.Fatalf("update %d: job = %+v", i+1, job)
		}
	}
	var pins []string
	for _, script := range f.scripts {
		pins = append(pins, envValue(script.Env, "VERSION"))
	}
	if !slices.Equal(pins, []string{"1.19.3", "1.19.4"}) {
		t.Fatalf("installer pins = %q, want one per update", pins)
	}
	assertResolvesInto(t, f.launcher, filepath.Join(f.root, string(TargetOpencodeV2), "versions", "v2"))
	if advisory, err := f.s.UpdateAdvisory(ctx, TargetOpencodeV2, true); err != nil || advisory.Status != UpdateStatusCurrent || advisory.MaintenanceMethod != "official-installer" {
		t.Fatalf("advisory after the second update = %+v, %v", advisory, err)
	}
}

// Harnesses with a managed recipe but no update command of their own are
// offered their official installer, which installs into AO's releases
// wherever the user's copy is, and are compared with the release that
// installer would install.
func TestOfficialInstallsOfferTheirManagedRecipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	for _, tt := range []struct {
		target   Target
		userCopy string
	}{
		{TargetOpencodeV2, "/Users/me/.opencode/bin/opencode-v2"},
		{TargetAgy, "/Users/me/.local/bin/agy"},
	} {
		t.Run(string(tt.target), func(t *testing.T) {
			f := newManagedOfficialFixture(t, tt.target, tt.userCopy, "0.4.0", "0.5.0", func([]string) string { return "0.5.0" })
			var looked []Target
			f.s.officialVersion = func(_ context.Context, target Target) (string, error) {
				looked = append(looked, target)
				return "0.5.0", nil
			}
			advisory, err := f.s.UpdateAdvisory(context.Background(), tt.target, false)
			if err != nil || advisory.Status != UpdateStatusBehindLatest || advisory.MaintenanceMethod != "official-installer" ||
				advisory.Source != officialReleaseSource || !slices.Equal(looked, []Target{tt.target}) {
				t.Fatalf("advisory = %+v, %v, looked up %v", advisory, err, looked)
			}
			// Without AO's releases only a harness's own update command can
			// update the user's copy.
			want := ""
			if _, ok := vendorUpdateCommands[tt.target]; ok {
				want = "official-installer"
			}
			f.s.managedRoot = ""
			advisory, err = f.s.UpdateAdvisory(context.Background(), tt.target, true)
			if err != nil || advisory.MaintenanceMethod != want {
				t.Fatalf("advisory without managed installs = %+v, %v; want method %q", advisory, err, want)
			}
		})
	}
}

// Reinstall repairs the Codex copy sessions run: AO's release, installed by
// Codex's own installer at the recorded release, never brew's cask.
func TestCodexCaskReinstallRepairsTheManagedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	root := t.TempDir()
	s := newTestService("darwin", "brew", "sh")
	s.managedRoot = root
	s.releases = &releaseStoreStub{}
	record := managedRecord{Method: "homebrew", Package: "codex", CLI: "codex", Version: "0.163.0", Dir: "v1", BinRel: filepath.Join("bin", "codex")}
	writeFileAt(t, filepath.Join(root, string(TargetCodex), "managed.json"), string(mustJSON(record)))
	launcher := filepath.Join(root, "bin", "codex")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		resolved, err := filepath.EvalSymlinks(launcher)
		if err != nil {
			return VerifyResult{ResolvedPath: launcher}, err
		}
		return VerifyResult{ResolvedPath: launcher, Output: strings.TrimSpace(readFile(resolved))}, nil
	})
	var mu sync.Mutex
	var scripts []ports.InstallScriptCommand
	var commands []ports.InstallCommand
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		mu.Lock()
		scripts = append(scripts, command)
		mu.Unlock()
		bin := envValue(command.Env, "CODEX_INSTALL_DIR")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return ports.InstallScriptResult{}, err
		}
		return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "codex"), []byte(envValue(command.Env, "CODEX_RELEASE")+"\n"), 0o755)
	})
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, command)
		return nil
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})

	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.resolveAgentMethod(TargetCodex, "homebrew", AgentOperationReinstall)
	if err != nil || !plan.managed || plan.managedPin != "0.163.0" || plan.Script == nil || plan.Script.URL != codexInstallScriptURL || slices.Contains(plan.Command, "brew") {
		t.Fatalf("reinstall plan = %+v, %v; want Codex's installer into a managed release at 0.163.0", plan, err)
	}
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "homebrew", AgentOperationReinstall, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	if job, _ := s.Status(context.Background(), TargetCodex); job.Status != StatusSucceeded {
		t.Fatalf("job = %+v", job)
	}
	for _, command := range commands {
		if slices.Contains(command.Argv, "brew") {
			t.Fatalf("ran %q, want the cask left alone", command.Argv)
		}
	}
	if len(scripts) != 1 || envValue(scripts[0].Env, "CODEX_RELEASE") != "0.163.0" {
		t.Fatalf("installer runs = %+v, want one pinned to the recorded release", scripts)
	}
	assertResolvesInto(t, launcher, filepath.Join(root, string(TargetCodex), "versions", "v1"))
}
