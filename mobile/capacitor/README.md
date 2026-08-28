# Scrumboy Capacitor shell

## Purpose

This workspace owns the thin Android Capacitor shell. Scrumboy's authoritative web source remains in `internal/httpapi/web`; the generated `www/` directory is an ignored, deterministic packaging artifact.

The WebView loads only signed application assets from `www`. C2 adds a packaged server selector and an app-local Android `ScrumboyTransport` plugin. The plugin owns the selected origin, authenticated cookie jar, REST, SSE, and acquired-resource networking. The WebView installs that runtime before importing the packaged `/app.js` entry.

Do not configure `server.url` for production. Do not load the user's Scrumboy server UI into the WebView.

## Prerequisites

- Node.js 22 or newer
- npm
- Android Studio and Android SDK API 36
- JDK 21

## Generate the web artifact

From the repository root:

```powershell
npm --prefix internal/httpapi/web run build
npm --prefix mobile/capacitor run typecheck:shell
npm --prefix internal/httpapi/web run build:capacitor-web -- --version <scrumboy-version>
```

Do not edit `mobile/capacitor/www/` by hand.

## Copy or sync Android

After generating `www/`:

```powershell
npm --prefix mobile/capacitor run cap:copy
npm --prefix mobile/capacitor run cap:sync
```

## Build Android

```powershell
cd mobile/capacitor/android
.\gradlew.bat test
.\gradlew.bat assembleDebug
```

The debug APK is generated under `android/app/build/outputs/apk/debug/` and must not be committed.

Debug builds may connect to an explicitly selected HTTP server for LAN development. Release builds require HTTPS. The production manifest does not globally enable cleartext traffic, and the transport does not bypass TLS validation.

## Run or open Android

With an existing emulator or device available:

```powershell
npm --prefix mobile/capacitor run android:run
npm --prefix mobile/capacitor run android:open
```

## C2 boundary and C3 handoff

C2 supports one selected Scrumboy server. Server selection is stored with Capacitor Preferences; session cookies remain native and are never exposed to JavaScript. Changing servers clears the native session, active streams, acquired resources, and user-scoped WebView state. Interactive OIDC remains disabled in the mobile runtime pending its dedicated native handoff phase.

C2 does not add app lifecycle/resume integration, Android back-button behavior, deep links, native OIDC, push, sharing/filesystem polish, native AI, multiple-server profiles, or iOS. Those remain later phases.
