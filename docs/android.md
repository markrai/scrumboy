# Android app

Scrumboy's Android application is a native client for a **running Scrumboy server**. It does not start, embed, or replace the server.

It is implemented with [Capacitor](https://capacitorjs.com/). The packaged WebView loads application assets from a generated `www/` tree. A first-run server selector asks for the origin of your Scrumboy instance; after a successful probe, you sign in on that server as usual.

Capacitor shell internals (runtime boundary, native transport, OIDC handoff sequence) live in [mobile/capacitor/README.md](../mobile/capacitor/README.md). This page is the build-and-use guide.

## Repository layout

| Path | Role |
|------|------|
| `mobile/capacitor/` | Capacitor workspace (shell TypeScript, npm scripts, Capacitor config) |
| `mobile/capacitor/android/` | Native Android Gradle project |
| `internal/httpapi/web/` | Authoritative web UI source packaged into the Android client |
| `mobile/capacitor/www/` | Generated web payload (`webDir`). Gitignored; do not edit by hand |
| `mobile/capacitor/.generated/` | Generated shell bundle consumed by the web-payload script. Gitignored |
| `mobile/capacitor/android/app/src/main/assets/public/` | Destination of `cap copy` / `cap sync`. Gitignored |

Application id: `com.markrai.scrumboy`. `minSdk` 26, `compileSdk` / `targetSdk` 36.

## Prerequisites

Required by this repository (not merely observed on one machine):

- **Node.js**
  - Capacitor workspace: `>=22.0.0` (`mobile/capacitor/package.json`)
  - Web UI build: `^20.19.0`, `^22.13.0`, or `>=24.0.0` (`internal/httpapi/web/package.json`)
  - Building the Android client needs **both**, so use **Node.js `^22.13.0` or `>=24.0.0`**. CI uses Node 24; that is the version the workflows run, not a unique requirement.
- **npm** with lockfile support. Both workspaces pin `"packageManager": "npm@11.6.1"` (the version used to maintain the lockfiles).
- **JDK 21** (`sourceCompatibility` / `targetCompatibility` / Kotlin `jvmTarget` are 21)
- **Android SDK** with **API 36** (project `compileSdk` / `targetSdk`)
- **Android Studio** to open the native project and deploy to a device or emulator. This repository does not pin a minimum Android Studio version.
- A running **Scrumboy server** to connect to (see [README.md](../README.md) Quick Start)

Gradle itself is provided by the wrapper under `mobile/capacitor/android/` (`gradlew` / `gradlew.bat`). Do not install a global Capacitor CLI; use the npm scripts below (`@capacitor/cli` is a local `devDependency`).

## Install dependencies

From the repository root, install from the lockfiles:

```bash
npm --prefix internal/httpapi/web ci
npm --prefix mobile/capacitor ci
```

CI uses the same `npm ci` pair.

## Build the Capacitor web payload

The native shell does not load your server's HTML. It packages a copy of the Scrumboy web UI plus the mobile bootstrap into `mobile/capacitor/www/`.

From the repository root:

```bash
npm --prefix internal/httpapi/web run build
npm --prefix mobile/capacitor run typecheck:shell
npm --prefix internal/httpapi/web run build:capacitor-web -- --version <scrumboy-version>
```

`build:capacitor-web` requires `--version` (a non-empty single-line existing Scrumboy version). Use the same string as `versionName` in `mobile/capacitor/android/app/build.gradle`. The script bundles the Capacitor shell (`mobile/capacitor/scripts/build-shell.mjs`), writes `mobile/capacitor/www/`, and verifies the artifact. You do not need a separate `build:shell` step.

Rerun this sequence after changing the web UI (`internal/httpapi/web`) or the Capacitor shell (`mobile/capacitor/shell`). Then copy or sync into Android (next section). Changing only Java/Kotlin under `mobile/capacitor/android/` does not require regenerating `www/`.

Do not edit `mobile/capacitor/www/` by hand.

## Copy or sync Capacitor assets

After `www/` is generated, copy it into the Android project.

**Preferred** after a web/shell payload rebuild (assets only):

```bash
npm --prefix mobile/capacitor run cap:copy
```

That runs `cap copy android`. It copies `www/` into the gitignored `android/app/src/main/assets/public/` tree.

**When Capacitor plugins or native Capacitor dependencies change**, update native wiring as well:

```bash
npm --prefix mobile/capacitor run cap:sync
```

That runs `cap sync android` (`copy` plus a native `update`). It regenerates files such as `android/app/capacitor.build.gradle` (marked do-not-edit). Do not run `cap:sync` as a routine after every UI change; it can dirty committed native project files.

## Build a debug APK

From `mobile/capacitor/android`:

```bash
./gradlew assembleDebug
```

On Windows, use `.\gradlew.bat` in place of `./gradlew`.

Debug APK output: `mobile/capacitor/android/app/build/outputs/apk/debug/` (gitignored; do not commit).

Debug builds are `FLAG_DEBUGGABLE` and overlay `usesCleartextTraffic=true`. They may connect to an explicitly selected **HTTP** origin for LAN development. Release builds do not.

## Run from Android Studio

1. Produce and copy the web payload (`build:capacitor-web` then `cap:copy`) so the native project has current assets.
2. Open the native project: `mobile/capacitor/android` (or `npm --prefix mobile/capacitor run android:open`).
3. Select an emulator or a device with USB debugging, then Run.

Alternative with an already available emulator or device:

```bash
npm --prefix mobile/capacitor run android:run
```

That runs `cap run android` (build + deploy). It is not a substitute for regenerating `www/` after UI/shell changes.

## Device and server connection

The app is a client. Configure the **server origin** on first launch (`Connect to your server`). The origin must be a host (optional port) with **no path, query, or fragment**. The client probes `/api/version` and `/api/auth/status` and rejects incompatible servers.

| Build | Transport |
|-------|-----------|
| **Debug** | HTTPS, or HTTP for LAN development |
| **Release** | **HTTPS required**. The production manifest does not enable cleartext. Native transport does not skip TLS validation. |

`http://localhost` / `http://127.0.0.1` on a **physical device** is the phone itself, not your development machine. Point the app at an address the device can reach (the host's LAN IP for a debug HTTP server, or a hostname with a valid certificate for HTTPS).

Self-hosted servers must be reachable from the device (LAN or public). App-level TLS is optional on the server; enable it with `SCRUMBOY_TLS_CERT` / `SCRUMBOY_TLS_KEY`, or terminate HTTPS at a reverse proxy. See [environment-variables.md](environment-variables.md#scrumboy_tls_cert--scrumboy_tls_key).

After connect, sign in with that server's normal auth (local account and/or SSO). Android SSO uses a native OIDC handoff (external browser / Custom Tab, then `com.markrai.scrumboy://oidc/callback`). Browser/PWA OIDC is unchanged. Details: [oidc.md](oidc.md#android-packaged-app-native-handoff), [authentication-api.md](authentication-api.md).

The selected origin is stored on device. Changing servers clears the native session.

## VoiceFlow and on-device capabilities

On Android, VoiceFlow can use native speech I/O. On supported English devices that report ready on-device speech **and** local text generation, the app may open **AI VoiceFlow** (on-device recognition and Gemini Nano planning, with confirmation before mutations). Otherwise it uses **Basic VoiceFlow**.

Availability is capability-gated at runtime, not a hard-coded phone list. Browser and PWA sessions stay on Basic VoiceFlow.

Canonical docs (do not duplicate them here):

- [voiceflow.md](voiceflow.md) — Basic VoiceFlow
- [enhanced-voiceflow.md](enhanced-voiceflow.md) — AI VoiceFlow, gating, speech settings

## Build a release App Bundle

From `mobile/capacitor/android`:

```bash
./gradlew bundleRelease
```

On Windows: `.\gradlew.bat bundleRelease`.

| Signing env vars | Result |
|------------------|--------|
| All four `SCRUMBOY_UPLOAD_*` variables set (non-empty) | Signed release bundle |
| Any missing or blank | Unsigned release validation build (intentional) |

Gradle logs which of those two modes it is using. An unsigned bundle is for local validation only; it is not a signed release artifact.

Output: `mobile/capacitor/android/app/build/outputs/bundle/release/`

## Release signing

Release signing is optional and driven only by these environment variables:

- `SCRUMBOY_UPLOAD_STORE_FILE`
- `SCRUMBOY_UPLOAD_STORE_PASSWORD`
- `SCRUMBOY_UPLOAD_KEY_ALIAS`
- `SCRUMBOY_UPLOAD_KEY_PASSWORD`

All four must be present and not blank/whitespace-only; the actual values are passed to Gradle signing unchanged. Point `SCRUMBOY_UPLOAD_STORE_FILE` at a keystore **outside** the repository. Keep keystores and signing credentials outside the repository. Do not commit passwords or keystore files. `*.jks` and `*.keystore` are gitignored under `mobile/capacitor/android/`.

A partial set does **not** enable signing; the release build stays unsigned.

## Useful validation

From the repository root (TypeScript shell):

```bash
npm --prefix mobile/capacitor run typecheck:shell
```

From `mobile/capacitor/android` (Windows: `.\gradlew.bat`):

```bash
./gradlew test
./gradlew lintRelease
./gradlew bundleRelease
```

`build:capacitor-web` already runs the web-artifact verifier. You can re-run it after `www/` exists with `npm --prefix internal/httpapi/web run verify:capacitor-web`.

## Output locations

| Artifact | Location |
|----------|----------|
| Capacitor web payload | `mobile/capacitor/www/` |
| Copied Android web assets | `mobile/capacitor/android/app/src/main/assets/public/` |
| Debug APK | `mobile/capacitor/android/app/build/outputs/apk/debug/` |
| Release App Bundle | `mobile/capacitor/android/app/build/outputs/bundle/release/` |

These paths are build outputs. Do not commit them.

## Troubleshooting

- **Wrong JDK.** The Android project compiles as Java 21. Point `JAVA_HOME` at JDK 21 if Gradle fails on bytecode or toolchain version.
- **Stale UI in the app.** Rebuild the web UI, rerun `build:capacitor-web`, then `cap:copy` (or Android Studio Run after that). `cap:sync` is not the routine refresh path.
- **Physical device cannot reach the server.** `localhost` is the device. Use a LAN IP or a DNS name the phone can resolve. Confirm the server process is listening on a reachable address (`BIND_ADDR`), not only `127.0.0.1`.
- **HTTP vs HTTPS.** Debug APKs may use HTTP. Release builds reject HTTP (`https_required`). Cleartext is not enabled in the main manifest.
- **TLS failure.** The transport does not disable certificate validation. Self-signed or hostname-mismatched certificates fail (`tls_failure`).
- **Missing Android SDK / platform 36.** Install SDK Platform 36. Android Studio usually writes gitignored `local.properties`; otherwise set `ANDROID_HOME` (or `ANDROID_SDK_ROOT`) to your SDK.
- **Incomplete signing variables.** If any of the four `SCRUMBOY_UPLOAD_*` values is missing or blank, Gradle leaves the release build unsigned.
- **Incompatible server.** The origin must be a Scrumboy instance that answers `/api/version` and `/api/auth/status`.

Contribution workflow (DCO, Go/web tests): [CONTRIBUTING.md](../CONTRIBUTING.md). The browser PWA is a separate install path: [pwa.md](pwa.md).
