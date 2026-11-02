package modelcatalog

import (
	"context"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// browseProviderCredentials reports, per harness, the providers it holds a
// credential for, read from that harness's own configuration, credential
// store and environment variables. Presence is the evidence: whether the
// provider accepts the credential is not proven. Each harness registers its
// check from its own file. OpenCode, Pi and OMP use configuredProviders instead.
var browseProviderCredentials = map[string]func(workingDir string, env map[string]string) []string{}

// markConfiguredProviders marks the providers a catalog's harness holds a
// credential for, so Settings can tell them apart from the rest.
func markConfiguredProviders(ctx context.Context, catalog *ports.AgentModelCatalog, request ports.AgentModelDiscoveryRequest) {
	check := browseProviderCredentials[request.AgentID]
	if check == nil {
		if len(catalog.ConfiguredProviders) == 0 {
			catalog.ConfiguredProviders = configuredProviders(ctx, request.AgentID, request.Env)
		}
		return
	}
	configured := uniqueProviders(append(catalog.ConfiguredProviders, check(request.WorkingDir, request.Env)...))
	sort.Strings(configured)
	catalog.ConfiguredProviders = configured
}
