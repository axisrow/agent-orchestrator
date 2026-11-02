package modelcatalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMarkConfiguredProvidersMarksCredentialedProviders(t *testing.T) {
	const agentID = "test-browse-providers"
	browseProviderCredentials[agentID] = func(string, map[string]string) []string { return []string{"zai", "anthropic"} }
	t.Cleanup(func() { delete(browseProviderCredentials, agentID) })
	catalog := ports.AgentModelCatalog{
		ConfiguredProviders: []string{"zai"},
		Models:              []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}, {ID: "gpt", Provider: "openai"}},
	}

	markConfiguredProviders(context.Background(), &catalog, ports.AgentModelDiscoveryRequest{AgentID: agentID})

	if want := []string{"anthropic", "zai"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", catalog.ConfiguredProviders, want)
	}

	// A harness without a registered check is left exactly as discovered.
	untouched := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}}}
	markConfiguredProviders(context.Background(), &untouched, ports.AgentModelDiscoveryRequest{AgentID: "unregistered"})
	if untouched.ConfiguredProviders != nil {
		t.Fatalf("unregistered catalog = %#v", untouched)
	}
}
