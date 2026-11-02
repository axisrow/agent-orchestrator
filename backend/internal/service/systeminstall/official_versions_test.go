package systeminstall

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
)

func TestParseOfficialVersionReadsEachVendorFormat(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target Target
		body   string
		want   string
	}{
		{name: "plain text", target: TargetClaudeCode, body: "2.1.291\n", want: "2.1.291"},
		{name: "v-prefixed text", target: TargetClaudeCode, body: "v2.1.291", want: "2.1.291"},
		{name: "prefixed json tag", target: TargetCodex, body: `{"assets":[],"tag_name":"rust-v0.160.1"}`, want: "0.160.1"},
		{name: "github release", target: TargetOMP, body: `{"tag_name":"v0.47.4"}`, want: "0.47.4"},
		{name: "opencode 2 release document", target: TargetOpencodeV2, body: `{"channel":"latest","name":"cli","distribution":"npm","version":"2.1.4","metadata":{"package":"@opencode/cli"}}`, want: "2.1.4"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source, ok := officialSourceFor(tt.target, "darwin", "arm64")
			if !ok {
				t.Fatalf("no official source for %s", tt.target)
			}
			got, err := parseOfficialVersion(source, []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseOfficialVersionRejectsMetadataWithoutVersion(t *testing.T) {
	source, _ := officialSourceFor(TargetOpencodeV2, "linux", "amd64")
	if _, err := parseOfficialVersion(source, []byte(`{"packages":[]}`)); err == nil {
		t.Fatal("expected an error for a manifest without a version")
	}
	source, _ = officialSourceFor(TargetClaudeCode, "linux", "amd64")
	if _, err := parseOfficialVersion(source, []byte("<html>maintenance</html>")); err == nil {
		t.Fatal("expected an error for a non-version body")
	}
}

func TestOfficialSourceForSelectsAntigravityPlatformManifest(t *testing.T) {
	source, ok := officialSourceFor(TargetAgy, "linux", "amd64")
	if !ok || source.ref != "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/linux_amd64.json" {
		t.Fatalf("source = %+v ok=%t", source, ok)
	}
	if _, ok := officialSourceFor(TargetAgy, "windows", "amd64"); ok {
		t.Fatal("windows has no verified Antigravity manifest")
	}
}

// Every harness AO updates with its official installer into a release of its
// own needs the release that installer would pick, or the notice never
// offers the update.
func TestOfficialManagedRecipesHaveAReleaseSource(t *testing.T) {
	for target := range officialManagedRecipes {
		for _, goarch := range []string{"amd64", "arm64"} {
			for _, goos := range []string{"darwin", "linux"} {
				if _, ok := officialSourceFor(target, goos, goarch); !ok {
					t.Errorf("%s has no release source on %s/%s", target, goos, goarch)
				}
			}
		}
	}
}

func TestUpdateAdvisoryFallsBackToOfficialReleaseForUnownedBinary(t *testing.T) {
	s := newTestService("darwin", "npm", "brew")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "2.1.200 (Claude Code)\n"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	var packageLookup atomic.Bool
	s.managedVersion = func(context.Context, Plan) (string, error) {
		packageLookup.Store(true)
		return "9.9.9", nil
	}
	s.officialVersion = func(_ context.Context, target Target) (string, error) {
		if target != TargetClaudeCode {
			t.Fatalf("official lookup for %s", target)
		}
		return "2.1.291", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != officialReleaseSource || advisory.CurrentVersion != "2.1.200" || advisory.LatestVersion != "2.1.291" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if packageLookup.Load() {
		t.Fatal("package registry was queried for a binary no package manager owns")
	}
}

func TestUpdateAdvisoryPrefersOwningPackageOverOfficialRelease(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{Output: "codex-cli 1.3.0"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) { return method == "npm", nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("official release queried for a package-owned binary")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusCurrent || advisory.Source != "npm" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryUnknownWithoutOfficialSource(t *testing.T) {
	s := newTestService("darwin")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "codex 0.1.0"}, nil })
	s.officialVersion = func(context.Context, Target) (string, error) { return "", errNoOfficialSource }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.LatestVersion != "" || advisory.Reason != "unsupported_source" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryComparesOfficialPrereleaseWithLatest(t *testing.T) {
	for _, tt := range []struct {
		installed string
		want      UpdateStatus
	}{
		{installed: "2.0.0-beta.1", want: UpdateStatusBehindLatest},
		{installed: "2.1.0-beta.1", want: UpdateStatusAheadOfLatest},
	} {
		t.Run(tt.installed, func(t *testing.T) {
			s := newTestService("darwin")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "claude " + tt.installed}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "2.0.0", nil }
			advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode, false)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != tt.want || advisory.LatestVersion != "2.0.0" || advisory.Reason != "" {
				t.Fatalf("advisory = %+v", advisory)
			}
		})
	}
}

func TestOfficialGitHubVersionReadsReleaseRedirect(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.String())
		response := managerHTTPResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://github.com/can1357/oh-my-pi/releases/tag/v0.47.4")
		return response, nil
	})}
	got, err := officialReleaseVersionWith(client, "darwin", "arm64")(context.Background(), TargetOMP)
	if err != nil || got != "0.47.4" {
		t.Fatalf("version=%q err=%v", got, err)
	}
	if want := []string{"HEAD https://github.com/can1357/oh-my-pi/releases/latest"}; !slices.Equal(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestOfficialGitHubVersionFallsBackToAPIWithoutReleaseRedirect(t *testing.T) {
	for _, tt := range []struct {
		name     string
		redirect func() *http.Response
	}{
		{name: "no releases", redirect: func() *http.Response {
			response := managerHTTPResponse(http.StatusFound, "")
			response.Header.Set("Location", "https://github.com/can1357/oh-my-pi/releases")
			return response
		}},
		{name: "blocked", redirect: func() *http.Response { return managerHTTPResponse(http.StatusTooManyRequests, "") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "github.com" {
					return tt.redirect(), nil
				}
				if request.URL.String() != "https://api.github.com/repos/can1357/oh-my-pi/releases/latest" {
					t.Fatalf("URL = %s", request.URL)
				}
				return managerHTTPResponse(http.StatusOK, `{"tag_name":"v1.9.0"}`), nil
			})}
			got, err := officialReleaseVersionWith(client, "darwin", "arm64")(context.Background(), TargetOMP)
			if err != nil || got != "1.9.0" {
				t.Fatalf("version=%q err=%v", got, err)
			}
		})
	}
}

func TestOfficialRegistrySourcesShareThePackageLookups(t *testing.T) {
	for _, tt := range []struct {
		target Target
		url    string
		body   string
		want   string
	}{
		{target: TargetPi, url: "https://registry.npmjs.org/-/package/@earendil-works%2Fpi-coding-agent/dist-tags", body: `{"latest":"1.19.4"}`, want: "1.19.4"},
	} {
		t.Run(string(tt.target), func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != tt.url {
					t.Fatalf("URL = %s, want %s", request.URL, tt.url)
				}
				return managerHTTPResponse(http.StatusOK, tt.body), nil
			})}
			got, err := officialReleaseVersionWith(client, "darwin", "arm64")(context.Background(), tt.target)
			if err != nil || got != tt.want {
				t.Fatalf("version=%q err=%v, want %q", got, err, tt.want)
			}
		})
	}
}
