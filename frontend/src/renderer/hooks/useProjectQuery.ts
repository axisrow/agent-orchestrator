// Shared React Query key for a single project's read-model (GET
// /api/v1/projects/{id}, resolved to the bare Project object). Centralized so
// every consumer that reads, writes, or invalidates a project stays on the same
// cache entry — a PUT from one component (e.g. PromptOverrideDialog) correctly
// refetches the others (e.g. ProjectSettingsForm). Without this, two components
// independently writing `["project", id]` would silently desync if either
// changed the shape.
export const projectQueryKey = (id: string) => ["project", id] as const;

// Key for the prompt-override read-model of the same endpoint. The endpoint's
// raw envelope ({status, project, defaultWorkerPrompt, defaultOrchestratorPrompt})
// is a different shape from the bare Project above, so PromptOverrideDialog
// caches its derived PromptLoad under this NESTED key instead of the shared one:
// writing its shape to ["project", id] would clobber the bare-Project readers
// (ProjectSettingsForm, the shell prefetch, ProjectScriptsSettings) and vice
// versa. Nesting keeps invalidation coupling for free: any
// invalidateQueries(["project", id]) also refetches the overrides entry.
export const projectPromptOverridesQueryKey = (id: string) =>
  [...projectQueryKey(id), "prompt-overrides"] as const;
