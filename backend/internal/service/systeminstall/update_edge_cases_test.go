package systeminstall

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestShimOwnershipRequiresAnExecutableArgumentInsideExactPackage(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	for _, pkg := range []string{"codex", "codex-extra"} {
		bin := filepath.Join(root, "node_modules", "@openai", pkg, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "codex.js"), []byte("cli"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{"cmd", `@"%dp0%\node_modules\@openai\codex\bin\codex.js" %*`, true},
		{"powershell", `& "node$exe" "$basedir/node_modules/@openai/codex/bin/codex.js" $args`, true},
		{"shell", `exec node "$basedir/node_modules/@openai/codex/bin/codex.js" "$@"`, true},
		{"prefix collision", `@"%dp0%\node_modules\@openai\codex-extra\bin\codex.js" %*`, false},
		{"comment", `# "$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"echo argument", `exec echo "$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"assignment", `target="$basedir/node_modules/@openai/codex/bin/codex.js"`, false},
		{"path traversal", `exec node "$basedir/node_modules/@openai/codex/../codex-extra/bin/codex.js"`, false},
		{"missing target", `exec node "$basedir/node_modules/@openai/codex/missing.js"`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			shim := filepath.Join(root, "codex.cmd")
			if err := os.WriteFile(shim, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			owned, _ := nodeShimTargetsPackage(shim, packageRoot)
			if owned != tt.want {
				t.Fatalf("owned = %t, want %t", owned, tt.want)
			}
		})
	}
}

func TestWingetInventoryRequiresTheExactPackageDirectory(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		directory string
		want      bool
	}{
		{"SST.Opencode_Microsoft.Winget.Source_8wekyb3d8bbwe", true},
		{"SST.OpencodePreview_Microsoft.Winget.Source_8wekyb3d8bbwe", false},
		{"Vendor.Other_Microsoft.Winget.Source_8wekyb3d8bbwe", false},
	} {
		t.Run(tt.directory, func(t *testing.T) {
			dir := filepath.Join(root, "WinGet", "Packages", tt.directory)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "opencode.exe")
			if err := os.WriteFile(binary, []byte("cli"), 0o755); err != nil {
				t.Fatal(err)
			}
			owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
				_, err := io.WriteString(stdout, "OpenCode  SST.opencode  1.2.0  winget\n")
				return err
			}))
			owned, err := owner(context.Background(), binary, "winget", "SST.opencode", false)
			if err != nil || owned != tt.want {
				t.Fatalf("owned=%t want=%t err=%v", owned, tt.want, err)
			}
		})
	}
}

func TestNpmRegistryVersionReadsOnlyLatestDistTag(t *testing.T) {
	for _, tt := range []struct {
		name, tags, want string
	}{
		{"ignores prerelease tags", `{"latest":"1.9.0","next":"2.0.0-beta.3"}`, "1.9.0"},
		// Some packages publish prereleases on latest.
		{"prerelease on latest", `{"latest":"2.0.0-rc.2"}`, "2.0.0-rc.2"},
		{"no latest tag", `{"next":"2.0.0-rc.3"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return managerHTTPResponse(http.StatusOK, tt.tags), nil
			})}
			result, err := npmRegistryVersion(context.Background(), client, "@openai/codex", versionSemver)
			if tt.want == "" {
				if !errors.Is(err, errLatestUnparseable) {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result != tt.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestVersionProbeRejectsAmbiguousOrPartialVersions(t *testing.T) {
	for _, tt := range []struct {
		input, want string
	}{
		{"Node 22.1.0 warning\ncodex 1.2.3", ""},
		{"codex 1.2.3\nruntime 22.1.0", ""},
		{"codex 1.2.3 1.2.4", ""},
		{"codex 1.2.3\nversion 1.2.3", "1.2.3"},
		{"rust-v0.160.1", "0.160.1"},
		{"cli 1.2.3.4.5", ""},
		{"cli 1.2-beta..2", ""},
	} {
		got, ok := versionSemver.find(tt.input)
		if ok != (tt.want != "") || got.display != tt.want {
			t.Errorf("versionSemver.find(%q) = %q, %t; want %q", tt.input, got.display, ok, tt.want)
		}
	}
}
