import type { ForgeConfig } from "@electron-forge/shared-types";
import { AutoUnpackNativesPlugin } from "@electron-forge/plugin-auto-unpack-natives";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { FusesPlugin } from "@electron-forge/plugin-fuses";
import { FuseV1Options, FuseVersion } from "@electron/fuses";
import { rebuild } from "@electron/rebuild";
import electronPackage from "electron/package.json";
import MakerNSIS from "./makers/maker-nsis";
import MakerDMG, { sealDmg, verifyDmg } from "./makers/maker-dmg";
import { machoHasX86_64Slice } from "./makers/macho-archs";
import MakerAppImage from "./makers/maker-appimage";
import {
	assertMacReleaseCredentials,
	isForgePublishCommand,
	macPackagerSecurity,
} from "./mac-release-security";
import { existsSync, readdirSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { BUNDLED_TMUX_VERSION } from "./scripts/tmux-version.mjs";

// Default GitHub release target (production). Releases land on Untrivial-ai
// (the org the repo was transferred to in July 2026; AgentWrapper and aoagents
// are prior homes). Builds cut by CI must NOT rely on this fallback: the
// workflows set AO_RELEASE_REPO to the repo they run in, so a future org/repo
// rename fails the build instead of stranding the fleet on a redirect (#3523).
const DEFAULT_RELEASE_REPO = "Untrivial-ai/agent-orchestrator";

// The packaged binary name (no extension). Single source of truth: the packager
// names the exe/ELF from this, and the NSIS + deb makers must point their
// shortcut/launcher at the SAME name. Drift here means a broken Start menu
// shortcut on Windows (#2414) or "could not find the Electron app binary" on deb.
const EXECUTABLE_NAME = "agent-orchestrator";
const AUTH_PROTOCOL = {
	name: "Agent Orchestrator authentication callback",
	schemes: ["ao-app"],
};
const AUTH_PROTOCOL_MIME_TYPE = "x-scheme-handler/ao-app";
const PACKAGED_EXTERNAL_DEPENDENCIES = [
	"/node_modules/better-sqlite3",
	"/node_modules/bindings",
	"/node_modules/file-uri-to-path",
];

function ignoreFromVitePackage(file: string): boolean {
	if (!file) return false;
	if (file.startsWith("/.vite")) return false;
	if (file === "/node_modules") return false;
	return !PACKAGED_EXTERNAL_DEPENDENCIES.some(
		(dependency) => file === dependency || file.startsWith(`${dependency}/`),
	);
}

async function prepareNativeDependencies(platform: NodeJS.Platform, arch: string): Promise<void> {
	// Rebuild in the source tree, where prebuild-install and its helper packages
	// are available. The Vite package intentionally carries only the resulting
	// native runtime, not the install-time download toolchain.
	await rebuild({
		buildPath: process.cwd(),
		electronVersion: electronPackage.version,
		platform,
		arch,
		onlyModules: ["better-sqlite3"],
		force: true,
	});
}

export function extraResourcesForPlatform(platform: NodeJS.Platform): string[] {
	return [
		"daemon",
		"agent-browser",
		"resources/acp-runtime",
		...(platform === "darwin" ? ["wake-helper"] : []),
		...(platform === "darwin" || platform === "linux" ? ["tmux"] : []),
		"assets/icon.png",
		"assets/icon.ico",
		"assets/trayIconTemplate.png",
		"assets/trayIconTemplate@2x.png",
	];
}

const ACP_RUNTIME_NODE_PATH = "/Contents/Resources/acp-runtime/node/bin/node";
// V8 uses MAP_JIT on arm64 and mprotect on x64. electron-osx-sign's default
// allows only the former, so its re-signing makes the bundled Node crash on
// Intel (#3879). Which entitlements a file needs is a property of its bytes,
// not of the signing machine: @electron/osx-sign hands optionsForFile only the
// path, and one signing host may sign both the arm64 and the x64 artifact —
// the canonical release signer does exactly that. So the selector below keys
// off the Mach-O header of the file being signed (thin x64, or a universal
// carrying an x64 slice), never off process.arch. The public CI build stays
// unsigned, so this hook runs for locally signed builds and stands as the
// reference implementation the canonical signer mirrors; see
// frontend/docs/desktop-release.md.
const ACP_RUNTIME_NODE_ENTITLEMENTS = [
	"com.apple.security.cs.allow-jit",
	"com.apple.security.cs.allow-unsigned-executable-memory",
];

export function macSignOptionsForFile(filePath: string): { entitlements?: string[] } {
	// Cheap gate first: optionsForFile is invoked for every Mach-O in the
	// bundle, and only the nested Node path can need an override.
	if (!filePath.endsWith(ACP_RUNTIME_NODE_PATH)) return {};
	// Fail closed: an unreadable or unparseable binary throws out of
	// optionsForFile and aborts the signing pass. Never fall back to
	// process.arch or to "no entitlements" — silently signing the Intel Node
	// without allow-unsigned-executable-memory is exactly the #3879 crash.
	return machoHasX86_64Slice(filePath) ? { entitlements: ACP_RUNTIME_NODE_ENTITLEMENTS } : {};
}

// parseReleaseRepo turns an "owner/repo" string (from AO_RELEASE_REPO) into the
// publisher-github { owner, name } shape, falling back to the production default
// when unset or malformed.
function parseReleaseRepo(value: string | undefined): { owner: string; name: string } {
	const [owner, name] = (value || DEFAULT_RELEASE_REPO).split("/");
	if (!owner || !name) {
		const [defOwner, defName] = DEFAULT_RELEASE_REPO.split("/");
		return { owner: defOwner, name: defName };
	}
	return { owner, name };
}

// The public artifact workflow deliberately runs unsigned; the private release
// conductor seals those artifacts later (#4501). Direct Forge publication,
// however, must never emit an unsigned macOS update: validate at config load
// for an immediate publish failure and again in prePackage for an explicit
// target. Ported from upstream PR #4833 (closed unmerged).
assertMacReleaseCredentials(
	process.env,
	process.platform,
	isForgePublishCommand(process.argv),
);
const macSecurity = macPackagerSecurity(process.env);

const config: ForgeConfig = {
	packagerConfig: {
		asar: true,
		// The Vite plugin normally packages only .vite. better-sqlite3 must stay
		// external so Electron can load its native binary, so include its minimal
		// runtime dependency tree explicitly; AutoUnpackNativesPlugin then places
		// the .node binary outside app.asar.
		ignore: ignoreFromVitePackage,
		appBundleId: "dev.agent-orchestrator.desktop",
		name: "Agent Orchestrator",
		executableName: EXECUTABLE_NAME,
		protocols: [AUTH_PROTOCOL],
		appCategoryType: "public.app-category.developer-tools",
		// App icon. electron-packager appends the per-platform extension
		// (.icns on macOS, .ico on Windows); Linux menu icons come from the
		// deb/rpm makers below, and the runtime window icon from src/main.ts.
		icon: "assets/icon",
		extraResource: extraResourcesForPlatform(process.platform),
		// Notarization. Two paths:
		//  - CI: an App Store Connect API key. APPLE_API_KEY is a PATH to the .p8
		//    (the workflow decodes APPLE_API_KEY_BASE64 to a temp file), plus the
		//    key id + issuer uuid. Matches the proven local runbook creds.
		//  - Local: AO_NOTARY_PROFILE, a notarytool keychain profile created with
		//    `notarytool store-credentials`. See ao-macos-signed-release runbook.
		// Both are valid NotaryToolCredentials, so no cast is needed.
		// macPackagerSecurity fails closed on partial credentials (a lone
		// APPLE_API_KEY, signing without notarization) and pins Hardened
		// Runtime plus explicit parent/child entitlements instead of relying
		// on @electron/osx-sign defaults (#4501). The #3879 per-file override
		// below must survive every change to this block: without it
		// electron-osx-sign's default re-signing crashes the Intel ACP Node
		// (exit 133) inside a perfectly sealed bundle.
		osxSign: macSecurity.osxSign
			? { ...macSecurity.osxSign, optionsForFile: macSignOptionsForFile }
			: undefined,
		osxNotarize: macSecurity.osxNotarize,
	},
	hooks: {
		prePackage: async (_forgeConfig, platform, arch) => {
			assertMacReleaseCredentials(
				process.env,
				platform as NodeJS.Platform,
				isForgePublishCommand(process.argv),
			);
			await prepareNativeDependencies(platform as NodeJS.Platform, arch);
			if (platform === "darwin") {
				const wakeBuild = spawnSync(process.execPath, [path.resolve("scripts/build-wake-helper.mjs"), "--arch", arch], { stdio: "inherit" });
				if (wakeBuild.error) throw wakeBuild.error;
				if (wakeBuild.status !== 0) throw new Error("macOS wake helper build failed");
			}
		},
		packageAfterPrune: async (_forgeConfig, buildPath) => {
			const nativeModule = path.join(
				buildPath,
				"node_modules",
				"better-sqlite3",
				"build",
				"Release",
				"better_sqlite3.node",
			);
			if (!existsSync(nativeModule)) {
				throw new Error("Packaged app is missing the better-sqlite3 native runtime");
			}
		},
		// Assert the native resource survived Electron Packager's copy/asar/sign
		// pipeline. A source build succeeding is not enough: a missing extraResource
		// would otherwise publish an app that silently fell back to machine tmux.
		postPackage: async (_forgeConfig, packageResult) => {
			if (packageResult.platform !== "darwin" && packageResult.platform !== "linux") return;
			for (const outputPath of packageResult.outputPaths) {
				let resourcesPath = path.join(outputPath, "resources");
				if (packageResult.platform === "darwin") {
					const appBundle = readdirSync(outputPath).find((entry) => entry.endsWith(".app"));
					if (!appBundle) throw new Error(`packaged macOS app bundle missing from ${outputPath}`);
					resourcesPath = path.join(outputPath, appBundle, "Contents", "Resources");
					if (!existsSync(path.join(resourcesPath, "wake-helper", "ao-wake"))) throw new Error(`packaged macOS wake helper missing from ${resourcesPath}`);
				}
				const binary = path.join(resourcesPath, "tmux", "bin", "tmux");
				if (!existsSync(binary)) throw new Error(`packaged tmux missing from ${binary}`);
				const version = spawnSync(binary, ["-V"], { encoding: "utf8" });
				if (version.status !== 0 || version.stdout.trim() !== `tmux ${BUNDLED_TMUX_VERSION}`) {
					throw new Error(`packaged tmux failed verification at ${binary}: ${version.stderr || version.stdout}`);
				}
				const socket = path.join(tmpdir(), `ao-tmux-smoke-${process.pid}.sock`);
				const smoke = spawnSync(binary, ["-S", socket, "-f", "/dev/null", "new-session", "-d", "true"], {
					encoding: "utf8",
					timeout: 5_000,
				});
				try {
					if (smoke.error || smoke.status !== 0) {
						const detail = smoke.error?.message || smoke.stderr || smoke.stdout;
						throw new Error(`packaged tmux could not create a session at ${binary}: ${detail}`);
					}
				} finally {
					spawnSync(binary, ["-S", socket, "kill-server"], { stdio: "ignore" });
					rmSync(socket, { force: true });
				}
			}
		},
		// The dmg container is NOT signed, notarized or stapled by any maker
		// (neither Forge's maker-dmg nor app-builder-lib's dmg target does it), and
		// the .app's own stapled ticket does not propagate through an unsealed
		// container. So seal it here, after the maker has produced it, reusing the
		// same credentials packagerConfig already consumes (#3267 decision 3).
		// The .app inside was already signed + notarized + stapled by
		// packagerConfig above, before any maker ran; nothing here touches it.
		//
		// Then PROVE the seal. sealDmg exiting 0 only says three commands ran on
		// this machine; it does not say Gatekeeper accepts the published bytes with
		// a stapled ticket, or that a bundled nested executable (the Intel ACP
		// Node, #3879) actually runs. verify-mac-artifact.sh is the canonical gate
		// for both (#3288 workstreams 1 and 2; #3879 for the nested-Node check),
		// and #3267 decision 3 step 4 asks for exactly this check on the dmg. Run
		// only when sealDmg actually sealed: an unsigned local or desktop-testing
		// build has nothing to verify and must keep producing its dmg.
		postMake: async (_forgeConfig, makeResults) => {
			for (const result of makeResults) {
				if (result.platform !== "darwin") continue;
				for (const artifact of result.artifacts) {
					if (artifact.endsWith(".dmg")) {
						if (await sealDmg(artifact)) await verifyDmg(artifact);
					}
				}
			}
			return makeResults;
		},
	},
	rebuildConfig: {},
	makers: [
		// Windows installer: NSIS via electron-builder (see makers/maker-nsis.ts).
		// Replaces Squirrel.Windows, which only does per-user installs with no
		// custom install dir or proper uninstaller (issue #401).
		new MakerNSIS(
			{
				appId: "dev.agent-orchestrator.desktop",
				// The name shown in the installer, the updater, the shortcuts, and
				// Windows Apps. It matches the app's display name in main.ts.
				productName: "Orchestrator.inc",
				// Match the packaged binary name so the Start menu shortcut targets
				// the real "agent-orchestrator.exe" (not "Agent Orchestrator.exe").
				executableName: EXECUTABLE_NAME,
				icon: "assets/icon.ico",
				// Keep the installer filename unchanged. electron-builder names it
				// after productName by default.
				nsis: { artifactName: "Agent Orchestrator Setup ${version}.${ext}" },
			},
			["win32"],
		),
		// macOS FIRST-INSTALL artifact: a dmg has no
		// user-driven extraction step, so a third-party unzip tool can no longer
		// break the signature seal on the way in (see makers/maker-dmg.ts, #3267).
		new MakerDMG(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: "Agent Orchestrator",
			},
			["darwin"],
		),
		// Linux fetch-and-run artifact for `ao start`: a single self-contained
		// AppImage the Go bootstrapper downloads and runs directly (see
		// makers/maker-appimage.ts). The deb/rpm makers below stay for users who
		// prefer a system package.
		new MakerAppImage(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: "Agent Orchestrator",
				icon: "assets/icon.png",
				protocols: [AUTH_PROTOCOL],
			},
			["linux"],
		),
		{
			name: "@electron-forge/maker-deb",
			config: {
				options: {
					// Must match packagerConfig.executableName, or the deb maker
					// looks for the package name and fails with "could not find
					// the Electron app binary". (Both are "agent-orchestrator".)
					bin: EXECUTABLE_NAME,
					icon: "assets/icon.png",
					maintainer: "Agent Orchestrator",
					homepage: "https://github.com/aoagents/agent-orchestrator",
					mimeType: [AUTH_PROTOCOL_MIME_TYPE],
				},
			},
		},
		{
			name: "@electron-forge/maker-rpm",
			config: {
				options: {
					icon: "assets/icon.png",
					// rpmbuild rejects a spec with an empty License field.
					license: "MIT",
					homepage: "https://github.com/aoagents/agent-orchestrator",
					mimeType: [AUTH_PROTOCOL_MIME_TYPE],
				},
			},
		},
	],
	publishers: [
		{
			name: "@electron-forge/publisher-github",
			// Release target is build-time overridable so a fork run publishes to the
			// fork without a source edit. AO_RELEASE_REPO is "owner/repo"; it defaults
			// to the production target. The dev/test loop sets
			// AO_RELEASE_REPO=harshitsinghbhandari/agent-orchestrator (spec §1.1, §8).
			// Note: aoagents/agent-orchestrator and AgentWrapper/agent-orchestrator
			// are prior homes and intentionally NOT the default; releases land on
			// Untrivial-ai.
			config: {
				repository: parseReleaseRepo(process.env.AO_RELEASE_REPO),
				prerelease: process.env.AO_RELEASE_PRERELEASE === "true",
				draft: false,
				// Ask GitHub to compose the body from the PRs merged since the last
				// release. Without it the publisher creates the release with an empty
				// body and release notes tell users nothing about what changed.
				generateReleaseNotes: true,
			},
		},
	],
	plugins: [
		new AutoUnpackNativesPlugin({}),
		new VitePlugin({
			build: [
				{ entry: "src/main.ts", config: "vite.main.config.ts", target: "main" },
				{ entry: "src/preload.ts", config: "vite.preload.config.ts", target: "preload" },
				{ entry: "src/annotate-preload.ts", config: "vite.preload.config.ts", target: "preload" },
			],
			renderer: [{ name: "main_window", config: "vite.renderer.config.ts" }],
		}),
		// Harden the packaged Electron binary. Flipped at package time, so dev
		// (electron-forge start) and Playwright e2e, which launch the unfused
		// node_modules/electron binary, are unaffected.
		new FusesPlugin({
			version: FuseVersion.V1,
			// The app never re-execs itself as Node (no ELECTRON_RUN_AS_NODE /
			// fork / utilityProcess); acp-runtime ships its own Node binary.
			[FuseV1Options.RunAsNode]: false,
			// Left on: with this fuse off Electron unsets NODE_OPTIONS from its own
			// environment at startup, so the daemon, agents and terminals it spawns
			// would silently lose a user's NODE_OPTIONS (Windows system env,
			// launchctl setenv). Disabling it needs its own change.
			[FuseV1Options.EnableNodeOptionsEnvironmentVariable]: true,
			[FuseV1Options.EnableNodeCliInspectArguments]: false,
			// Enforced on macOS and Windows; Electron does not validate ASAR
			// integrity on Linux.
			[FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
			[FuseV1Options.OnlyLoadAppFromAsar]: true,
			[FuseV1Options.GrantFileProtocolExtraPrivileges]: false,
		}),
	],
};

export default config;
