#!/usr/bin/env bash
# Rebuild the local self-build from the current checkout: ao CLI (via
# daemon-build.sh), the Electron .app (which bundles a fresh daemon), codesign,
# and a swap into /Applications. Quits a running app first — warn anyone with
# live sessions that the daemon restarts (worker sessions survive in tmux).
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
app_name="Agent Orchestrator.app"
packaged_app="${repo_root}/frontend/out/Agent Orchestrator-darwin-arm64/${app_name}"
installed_app="/Applications/${app_name}"
codesign_identity="AO-Local-CodeSign"

# Pin node to an nvm-managed LTS before anything invokes npm or forge: brew
# node 26 silently truncates `electron-forge package` (exit 0, empty out/, the
# breakage only surfaces later at ditto). A rewrite of this script dropped the
# pin once (2026-10-25) and the build "succeeded" with no app bundle —
# frontend/src/main/rebuild-script-pin.test.ts guards this block textually;
# keep selector and test in sync.
nvm_dir="${NVM_DIR:-${HOME}/.nvm}"
node_dir=""
for candidate in "${nvm_dir}"/versions/node/v20.* "${nvm_dir}"/versions/node/v22.*; do
	if [[ -x "${candidate}/bin/node" ]]; then
		node_dir="${candidate}"
		break
	fi
done
if [[ -z "${node_dir}" ]]; then
	printf '!! nvm node v20/v22 not found under %s — brew/system node silently breaks forge package; install an nvm LTS.\n' "${nvm_dir}" >&2
	exit 1
fi
export PATH="${node_dir}/bin:${PATH}"

printf '==> building ao CLI (node: %s)\n' "${node_dir}"
bash "${script_dir}/daemon-build.sh"

printf '==> packaging %s (bundles the same daemon)\n' "${app_name}"
(cd "${repo_root}/frontend" && npm run package)

if [[ ! -d "${packaged_app}" ]]; then
	printf 'packaged app bundle missing at %s\n' "${packaged_app}" >&2
	exit 1
fi

printf '==> codesigning with %s\n' "${codesign_identity}"
codesign --force --deep --sign "${codesign_identity}" "${packaged_app}"

if pgrep -f "/Applications/${app_name}/Contents/MacOS/" >/dev/null 2>&1; then
	printf '==> quitting running app\n'
	osascript -e "tell application \"${app_name%.app}\" to quit"
	sleep 3
fi

printf '==> installing to %s\n' "${installed_app}"
ditto "${packaged_app}" "${installed_app}"

printf '==> launching app\n'
# Apps launched via `open` only see what this script exports: the standard
# proxy variables are the fallback layer of the SCM proxy resolution chain
# ("empty SCM proxy setting → ProxyFromEnvironment" in the daemon). Exactly
# the variables Go honors: HTTP_PROXY/HTTPS_PROXY/NO_PROXY — ALL_PROXY is
# curl semantics and would be dead weight. NO_PROXY must ride along so a
# forwarded HTTPS_PROXY does not also bend intentionally-direct traffic
# (LLM providers) through the proxy.
for proxy_var in HTTPS_PROXY HTTP_PROXY NO_PROXY; do
	if [[ -n "${!proxy_var:-}" ]]; then
		export "${proxy_var}"
	fi
done
open "${installed_app}"

printf '==> done\n'
