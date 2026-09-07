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

Android launcher icons (`ic_launcher`, `ic_launcher_round`, and adaptive `ic_launcher_foreground`) are derived from the PWA source `internal/httpapi/web/icon-512.png`. After changing that file, regenerate the mipmaps from the repository root:

```powershell
powershell -File mobile/capacitor/scripts/generate-android-icons.ps1
```

Debug builds may connect to an explicitly selected HTTP server for LAN development. Release builds require HTTPS. The production manifest does not globally enable cleartext traffic, and the transport does not bypass TLS validation.

## Run or open Android

With an existing emulator or device available:

```powershell
npm --prefix mobile/capacitor run android:run
npm --prefix mobile/capacitor run android:open
```

## Native Android OIDC (C4)

Interactive SSO on Android uses an external browser / Custom Tab and the server's existing OIDC configuration. No second IdP Android client is required; the registered HTTPS redirect URL remains `SCRUMBOY_OIDC_REDIRECT_URL`.

Sequence:

1. Product auth UI calls `AppRuntime.startInteractiveOIDC(...)`.
2. The shell coordinator posts `POST /api/auth/oidc/mobile/start` through the selected-server native transport, keeps the S256 verifier in Capacitor Preferences, and opens the returned HTTPS authorization URL externally.
3. After IdP login, Scrumboy's ordinary HTTPS callback issues a short-lived one-time handoff and redirects to `com.markrai.scrumboy://oidc/callback` with only `code`+`state` (or `error`+`state`).
4. Warm returns use `appUrlOpen`; cold launches use `getLaunchUrl`. Both validate the pending selected-server binding and exchange through the same native transport cookie jar.
5. Successful exchange sets a normal `scrumboy_session` cookie. The custom callback never carries session credentials.

Logout still calls ordinary server logout, clears native session cookies, and retains the selected server. Browser/PWA OIDC is unchanged and does not use the mobile handoff. iOS native OIDC is not implemented.

Android `allowBackup` is disabled; Capacitor Preferences (`CapacitorStorage`) and the native cookie jar (`scrumboy_transport_cookies_v1`) are excluded from cloud backup and device-to-device transfer rules.

## C2 / later-phase boundary

C2 supports one selected Scrumboy server. Server selection is stored with Capacitor Preferences; session cookies remain native and are never exposed to JavaScript. Changing servers clears the native session, active streams, acquired resources, and user-scoped WebView state.

Android back-button behavior (C3.1), push, generic deep links beyond the OIDC callback, sharing/filesystem polish, native AI, multiple-server profiles, and iOS remain later phases.
