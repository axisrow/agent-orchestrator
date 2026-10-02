package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	projectDir := t.TempDir()
	projects := func(ctx context.Context, projectID string) (string, error) {
		if projectID != "p1" {
			t.Fatalf("unexpected project lookup %q", projectID)
		}
		return projectDir, nil
	}
	svc := New(projects)
	svc.claudeDir = func() (string, error) { return dir, nil }
	return svc, dir
}

func TestGetEmpty(t *testing.T) {
	svc, _ := testService(t)
	config, err := svc.Get(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if config.App.TokenSet || config.App.BaseURL != "" {
		t.Fatalf("expected empty app scope, got %+v", config.App)
	}
	if config.Project != nil {
		t.Fatal("project scope should be absent without a projectId")
	}
	if config.Effective.Source != "" {
		t.Fatalf("expected no effective source, got %q", config.Effective.Source)
	}
}

func TestSetAppWritesThroughAndPreservesFile(t *testing.T) {
	svc, dir := testService(t)
	settings := filepath.Join(dir, "settings.json")
	existing := map[string]any{"model": "claude-opus-4-5", "env": map[string]any{"OTHER": "keep"}}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(settings, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	token := "gw-token-1"
	baseURL := "https://gw.example.com"
	model := "glm-5"
	if _, err := svc.Set(context.Background(), SetInput{
		Scope: ScopeApp, BaseURL: &baseURL, Token: &token, Model: &model,
	}); err != nil {
		t.Fatal(err)
	}

	// A nil key leaves the stored value: a token-only rotation must not wipe
	// the entry's other keys.
	token2 := "gw-token-2"
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, Token: &token2}); err != nil {
		t.Fatal(err)
	}
	var rotated struct {
		Env map[string]string `json:"env"`
	}
	data2, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data2, &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Env[keyToken] != token2 || rotated.Env[keyBaseURL] != baseURL || rotated.Env[keyModel] != model {
		t.Fatalf("nil keys must leave stored values: %+v", rotated.Env)
	}
	var stored struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Model != "claude-opus-4-5" || stored.Env["OTHER"] != "keep" {
		t.Fatalf("sibling fields lost: %+v", stored)
	}
	if stored.Env[keyBaseURL] != baseURL || stored.Env[keyToken] != token2 || stored.Env[keyModel] != model {
		t.Fatalf("gateway keys not written: %+v", stored.Env)
	}

	config, err := svc.Get(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != "https://gw.example.com" || !config.App.TokenSet {
		t.Fatalf("unexpected app scope %+v", config.App)
	}
	if strings.Contains(config.App.BaseURL, token) || config.Effective.Source != "app" {
		t.Fatalf("unexpected effective %+v", config.Effective)
	}
}

func TestSetClearsKeysOnEmptyValues(t *testing.T) {
	svc, _ := testService(t)
	token := "tok"
	empty := ""
	baseURL := "https://gw.example.com"
	model := "glm-5"
	if _, err := svc.Set(context.Background(), SetInput{
		Scope: ScopeApp, BaseURL: &baseURL, Token: &token, Model: &model,
	}); err != nil {
		t.Fatal(err)
	}
	// A nil key leaves the stored value; an empty one clears it.
	config, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: &empty, Model: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != "" || config.App.Model != "" || !config.App.TokenSet {
		t.Fatalf("expected keys cleared but token kept, got %+v", config.App)
	}
	config, err = svc.Set(context.Background(), SetInput{Scope: ScopeApp, Token: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != "" || config.App.TokenSet || config.App.Model != "" {
		t.Fatalf("expected cleared app scope, got %+v", config.App)
	}
	if config.Effective.Source != "" {
		t.Fatalf("expected no effective source, got %+v", config.Effective.Source)
	}
}

func TestProjectOverridesApp(t *testing.T) {
	svc, _ := testService(t)
	appURL := "https://app.example.com"
	projectURL := "https://project.example.com"
	model := "glm-5"
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: &appURL}); err != nil {
		t.Fatal(err)
	}
	config, err := svc.Set(context.Background(), SetInput{
		Scope: ScopeProject, ProjectID: "p1", BaseURL: &projectURL, Model: &model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Project == nil || config.Project.BaseURL != "https://project.example.com" || config.Project.TokenSet {
		t.Fatalf("unexpected project scope %+v", config.Project)
	}
	if config.Effective.Source != "project" || config.Effective.BaseURL != "https://project.example.com" || config.Effective.Model != "glm-5" {
		t.Fatalf("project should win: %+v", config.Effective)
	}
}

func TestSetRejectsBadScopeAndURL(t *testing.T) {
	svc, _ := testService(t)
	badURL := "not a url"
	if _, err := svc.Set(context.Background(), SetInput{Scope: "global"}); err == nil {
		t.Fatal("expected unknown scope rejection")
	}
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: &badURL}); err == nil {
		t.Fatal("expected invalid URL rejection")
	}
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeProject}); err == nil {
		t.Fatal("expected missing projectId rejection")
	}
}

func TestProbeValidatesAgainstGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("authorization") != "Bearer gw-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5","display_name":"GLM"},{"id":"other"}]}`))
	}))
	defer server.Close()
	svc, _ := testService(t)
	result, err := svc.Probe(context.Background(), server.URL, "gw-secret")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "valid" {
		t.Fatalf("expected valid, got %q (%s)", result.State, result.Detail)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "glm-5" || result.Models[0].DisplayName != "GLM" {
		t.Fatalf("unexpected models %+v", result.Models)
	}

	rejected, err := svc.Probe(context.Background(), server.URL, "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.State != "invalid" {
		t.Fatalf("expected invalid, got %q (%s)", rejected.State, rejected.Detail)
	}
	if _, err := svc.Probe(context.Background(), "", "x"); err == nil {
		t.Fatal("expected missing base URL rejection")
	}
	if _, err := svc.Probe(context.Background(), "ftp://x", "x"); err == nil {
		t.Fatal("expected non-http scheme rejection")
	}
}
