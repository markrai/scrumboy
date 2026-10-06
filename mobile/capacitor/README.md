# Scrumboy Capacitor shell

**Build, run, device connection, and release signing:** [docs/android.md](../../docs/android.md).

This file is the Capacitor **shell implementation** note (runtime boundary, native plugins, OIDC handoff). Do not treat it as the Android build guide.

## Purpose

This workspace owns the thin Android Capacitor shell. Scrumboy's authoritative web source remains in `internal/httpapi/web`; the generated `www/` directory is an ignored, deterministic packaging artifact.

The WebView loads only packaged application assets from `www`. The shell adds a packaged server selector and an app-local Android `ScrumboyTransport` plugin. The plugin owns the selected origin, authenticated cookie jar, REST, SSE, and acquired-resource networking. The WebView installs that runtime before importing the packaged `/app.js` entry.

Do not configure `server.url` for production. Do not load the user's Scrumboy server UI into the WebView.

Debug builds may connect to an explicitly selected HTTP server for LAN development. Release builds require HTTPS. The production manifest does not globally enable cleartext traffic, and the transport does not bypass TLS validation.

## Copy vs sync

After regenerating `www/` (see [docs/android.md](../../docs/android.md)), refresh Android assets with `npm --prefix mobile/capacitor run cap:copy` (`cap copy android`). That copies into gitignored `android/app/src/main/assets/public/`.

Use `npm --prefix mobile/capacitor run cap:sync` (`cap sync android`) only when Capacitor plugins or native Capacitor dependencies change. Sync also updates native wiring such as `android/app/capacitor.build.gradle` (marked do-not-edit) and can dirty committed files. Do not run it as a routine after every UI change.

Do not edit `mobile/capacitor/www/` by hand.

## Launcher icons

Android launcher icons (`ic_launcher`, `ic_launcher_round`, and adaptive `ic_launcher_foreground`) are derived from the PWA source `internal/httpapi/web/icon-512.png`. The generator also samples that artwork's canvas gray into `@color/ic_launcher_background` for the adaptive icon and splash icon-disk only. The full-screen cold-start splash background is the separate `@color/splash_screen_background` (PWA/app `#000000`). After changing the PWA icon, regenerate from the repository root:

```powershell
powershell -File mobile/capacitor/scripts/generate-android-icons.ps1
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

Android `allowBackup` is disabled; Capacitor Preferences (`CapacitorStorage`), the native cookie jar (`scrumboy_transport_cookies_v1`), the Dashboard widget metadata (`scrumboy_dashboard_widget_v1`), and the widget snapshot file (`scrumboy_dashboard_widget_snapshot.json`) are excluded from cloud backup and device-to-device transfer rules.

## Native on-device capabilities

The shell installs native speech input/output and local text-generation plugins and advertises them to the packaged web app. On capable English devices this is what enables **AI VoiceFlow**; otherwise the product stays on Basic VoiceFlow. Product behavior and gating: [docs/voiceflow.md](../../docs/voiceflow.md), [docs/enhanced-voiceflow.md](../../docs/enhanced-voiceflow.md).

## C2 / later-phase boundary

C2 supports one selected Scrumboy server. Server selection is stored with Capacitor Preferences; session cookies remain native and are never exposed to JavaScript. Changing servers clears the native session, active streams, acquired resources, and user-scoped WebView state.

Shipped in this shell: server selector, native transport, native OIDC handoff, on-device speech I/O, local text generation, and the **Scrumboy Dashboard** home-screen widget (snapshot-backed `AppWidgetProvider`; internal `MainActivity` extras, not generic public deep links).

Android back-button behavior (C3.1), push, generic deep links beyond the OIDC callback, sharing/filesystem polish, multiple-server profiles, and iOS remain later phases.
