# Scrumboy Capacitor shell

## Purpose

This workspace owns the thin Android Capacitor shell. Scrumboy's authoritative web source remains in `internal/httpapi/web`; the generated `www/` directory is an ignored, deterministic packaging artifact.

The WebView loads only signed application assets from `www`. C1 deliberately stops at a local “Mobile shell ready” screen. It does not select or contact a Scrumboy server, install a native transport, or start the product application.

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

## Run or open Android

With an existing emulator or device available:

```powershell
npm --prefix mobile/capacitor run android:run
npm --prefix mobile/capacitor run android:open
```

## C1 boundary and C2 handoff

C1 includes no server URL, server preferences, native HTTP/cookie session, REST, SSE, authenticated resource acquisition, custom plugin, deep link, push, AI, or iOS work. C2 will extend the local bootstrap to select a server, install the native runtime, and only then import the already-packaged `/app.js` entry.
