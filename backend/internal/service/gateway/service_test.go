package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeStore struct{ entries map[[2]string]Entry }

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[[2]string]Entry{}}
}

func (f *fakeStore) GetGatewayEntry(_ context.Context, scope Scope, projectID string) (Entry, bool, error) {
	entry, ok := f.entries[[2]string{string(scope), projectID}]
	return entry, ok, nil
}

func (f *fakeStore) UpsertGatewayEntry(_ context.Context, entry Entry, _ time.Time) error {
	f.entries[[2]string{string(entry.Scope), entry.ProjectID}] = entry
	return nil
}

func (f *fakeStore) ListGatewayEntries(context.Context) ([]Entry, error) {
	entries := make([]Entry, 0, len(f.entries))
	for _, entry := range f.entries {
		entries = append(entries, entry)
	}
	return entries, nil
}

func testService(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	projects := func(ctx context.Context, projectID string) (string, error) {
		if projectID != "p1" {
			t.Fatalf("unexpected project lookup %q", projectID)
		}
		return t.TempDir(), nil
	}
	svc := New(store, projects)
	return svc, store
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

func TestSetAppTriState(t *testing.T) {
	svc, _ := testService(t)
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
	config, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, Token: &token2})
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != baseURL || config.App.Model != model || !config.App.TokenSet {
		t.Fatalf("nil keys must leave stored values: %+v", config.App)
	}
	if config.Effective.Source != "app" {
		t.Fatalf("unexpected effective %+v", config.Effective)
	}

	// An empty key clears it.
	empty := ""
	config, err = svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: &empty, Model: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != "" || config.App.Model != "" || !config.App.TokenSet {
		t.Fatalf("expected keys cleared but token kept, got %+v", config.App)
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

// The whole point of AO-owned storage: importing a legacy entry reads the
// user's Claude settings file once and never writes it back.
func TestImportLegacySeedsOnceAndNeverWrites(t *testing.T) {
	svc, _ := testService(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	original := `{"model":"claude-opus-4-5","env":{"OTHER":"keep","ANTHROPIC_BASE_URL":"https://gw.example.com","ANTHROPIC_AUTH_TOKEN":"gw-secret","ANTHROPIC_MODEL":"glm-5"}}`
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := svc.ImportLegacy(context.Background(), []LegacySource{{Scope: ScopeApp, Path: settings}})
	if err != nil {
		t.Fatal(err)
	}
	if imported != 1 {
		t.Fatalf("imported %d entries, want 1", imported)
	}
	config, err := svc.Get(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if config.App.BaseURL != "https://gw.example.com" || !config.App.TokenSet || config.App.Model != "glm-5" {
		t.Fatalf("unexpected imported app scope %+v", config.App)
	}
	if raw, err := os.ReadFile(settings); err != nil || string(raw) != original {
		t.Fatalf("legacy file must stay byte-identical (err=%v)", err)
	}

	// A second import is a no-op once storage holds an entry: the user's file
	// must never win over what the settings screen saved.
	if imported, err := svc.ImportLegacy(context.Background(), []LegacySource{{Scope: ScopeApp, Path: settings}}); err != nil || imported != 0 {
		t.Fatalf("second import = (%d, %v), want (0, nil)", imported, err)
	}
	empty := `{"env":{}}`
	if err := os.WriteFile(settings, []byte(empty), 0o600); err != nil {
		t.Fatal(err)
	}
	if imported, err := svc.ImportLegacy(context.Background(), []LegacySource{{Scope: ScopeApp, Path: settings}}); err != nil || imported != 0 {
		t.Fatalf("import after save = (%d, %v), want (0, nil)", imported, err)
	}
	if config, err := svc.Get(context.Background(), ""); err != nil || !config.App.TokenSet {
		t.Fatalf("stored entry must survive a changed legacy file: %+v (%v)", config.App, err)
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
