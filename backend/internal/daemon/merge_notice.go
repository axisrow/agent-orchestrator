package daemon

// prMergeNoticesEnabledEnv turns on the orchestrator PR merge notice (fork
// issue #243) when set to "1" at daemon start. Off by default: the bell row
// records merges either way, only the orchestrator-conversation notice is
// gated behind the opt-in.
const prMergeNoticesEnabledEnv = "AO_ENABLE_PR_MERGE_NOTICES"

// prMergeNoticesEnabled reports the opt-in. Only the exact value "1" enables;
// an unset or any other value keeps the notice unwired.
func prMergeNoticesEnabled(getenv func(string) string) bool {
	return getenv(prMergeNoticesEnabledEnv) == "1"
}
