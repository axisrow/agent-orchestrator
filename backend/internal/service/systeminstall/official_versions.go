package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// officialReleaseSource marks an advisory compared against the vendor's own
// release channel. It is deliberately not an install method ID: a matching
// version does not prove which installer produced the binary, so clients must
// not pick an update method from it.
const officialReleaseSource = "official-release"

// errNoOfficialSource means AO knows no vendor release channel for a harness.
var errNoOfficialSource = errors.New("systeminstall: no official release source")

type officialSourceKind int

const (
	// officialText is a plain-text body holding only the version.
	officialText officialSourceKind = iota
	// officialJSON is a JSON object with the version in one string field.
	officialJSON
	// officialGitHub is a GitHub repository's latest release tag.
	officialGitHub
	// officialNPM is an npm package whose release matches the native build.
	officialNPM
)

// officialSource is where a vendor's installer resolves its latest release,
// which AO pins its managed update to. Each entry follows the lookup the
// vendor's own install script performs; for an npm source, match, when set,
// must match the release, so a release the installer refuses is never offered.
type officialSource struct {
	kind  officialSourceKind
	ref   string
	field string
	match *regexp.Regexp
}

func officialSourceFor(target Target, goos, goarch string) (officialSource, bool) {
	switch target {
	case TargetClaudeCode:
		return officialSource{kind: officialText, ref: "https://downloads.claude.ai/claude-code-releases/latest"}, true
	case TargetCodex:
		return officialSource{kind: officialJSON, ref: "https://releases.openai.com/codex/channels/latest", field: "tag_name"}, true
	case TargetAgy:
		if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
			return officialSource{}, false
		}
		return officialSource{kind: officialJSON, ref: "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/" + goos + "_" + goarch + ".json", field: "version"}, true
	case TargetOpencodeV2:
		// opencode.ai/v2/install reads the same release document.
		return officialSource{kind: officialJSON, ref: "https://opencode.ai/update/api/latest/cli/npm", field: "version"}, true
	case TargetOMP:
		return officialSource{kind: officialGitHub, ref: "can1357/oh-my-pi"}, true
	case TargetPi:
		// Pi's installer is an npm install into a managed prefix.
		return officialSource{kind: officialNPM, ref: "@earendil-works/pi-coding-agent"}, true
	default:
		return officialSource{}, false
	}
}

// officialReleaseVersion fetches the vendor release channel for a harness and
// returns a bare version such as 1.2.3.
func officialReleaseVersion(goos, goarch string) func(context.Context, Target) (string, error) {
	return officialReleaseVersionWith(&http.Client{Timeout: 6 * time.Second}, goos, goarch)
}

func officialReleaseVersionWith(client *http.Client, goos, goarch string) func(context.Context, Target) (string, error) {
	return func(ctx context.Context, target Target) (string, error) {
		source, ok := officialSourceFor(target, goos, goarch)
		if !ok {
			return "", errNoOfficialSource
		}
		var raw string
		var err error
		switch source.kind {
		case officialNPM:
			raw, err = npmLatestTag(ctx, client, source.ref)
			if err == nil && source.match != nil && !source.match.MatchString(strings.TrimPrefix(strings.TrimSpace(raw), "v")) {
				return "", fmt.Errorf("%s latest %q is not a release its installer installs", source.ref, raw)
			}
		case officialGitHub:
			if tag, tagErr := githubLatestReleaseTag(ctx, client, source.ref); tagErr == nil {
				return parseOfficialVersion(officialSource{kind: officialText}, []byte(tag))
			}
			// Fall back to the REST API, which also follows renamed repositories.
			body, fetchErr := fetchRegistry(ctx, client, "https://api.github.com/repos/"+source.ref+"/releases/latest", "application/vnd.github+json")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		default:
			body, fetchErr := fetchRegistry(ctx, client, source.ref, "")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		}
		if err != nil {
			return "", err
		}
		return parseOfficialVersion(officialSource{kind: officialText}, []byte(raw))
	}
}

// parseOfficialVersion reads the version from a release document. npm sources
// are read through the registry lookups shared with package updates, so their
// raw version is parsed as officialText.
func parseOfficialVersion(source officialSource, body []byte) (string, error) {
	var raw string
	switch source.kind {
	case officialText:
		raw = strings.TrimSpace(string(body))
	case officialJSON, officialGitHub:
		field := source.field
		if source.kind == officialGitHub {
			field = "tag_name"
		}
		var document map[string]any
		if err := json.Unmarshal(body, &document); err != nil {
			return "", err
		}
		raw, _ = document[field].(string)
	}
	// Tags such as v1.2.3 and rust-v0.160.1 carry the version after a prefix.
	version, ok := versionSemver.find(raw)
	if !ok {
		return "", fmt.Errorf("no version in official release metadata %q", raw)
	}
	return version.display, nil
}

// githubLatestReleaseTag reads the tag that github.com/<repo>/releases/latest
// redirects to. Like the REST endpoint it skips drafts and prereleases, but it
// is not subject to the REST API's 60 requests per hour unauthenticated limit
// and returns no body.
func githubLatestReleaseTag(ctx context.Context, client *http.Client, repo string) (string, error) {
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/"+repo+"/releases/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := noFollow.Do(request)
	if err != nil {
		return "", err
	}
	_ = response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode > 399 {
		return "", fmt.Errorf("github releases/latest for %s returned status %d", repo, response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	// A repository without releases redirects to its releases list instead.
	prefix := strings.ToLower("/" + repo + "/releases/tag/")
	if !strings.HasPrefix(strings.ToLower(location.Path), prefix) {
		return "", fmt.Errorf("github releases/latest for %s redirected to %q", repo, location.Path)
	}
	tag, err := url.PathUnescape(location.Path[len(prefix):])
	if err != nil || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("github releases/latest for %s has no release tag", repo)
	}
	return tag, nil
}
