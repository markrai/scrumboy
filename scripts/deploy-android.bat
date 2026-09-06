@echo off
setlocal EnableExtensions DisableDelayedExpansion
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "REPO_ROOT=%%~fI"
cd /d "%REPO_ROOT%"

echo.
echo ========================================
echo   Scrumboy - Deploy Android (adb)
echo ========================================
echo.

REM Resolve Android SDK early so adb and Gradle both see it. Prefer env vars;
REM fall back to the default Windows Studio install locations. Do not write
REM android/local.properties.
set "ANDROID_SDK_CANDIDATE="
if defined ANDROID_HOME if exist "%ANDROID_HOME%\" set "ANDROID_SDK_CANDIDATE=%ANDROID_HOME%"
if not defined ANDROID_SDK_CANDIDATE if defined ANDROID_SDK_ROOT if exist "%ANDROID_SDK_ROOT%\" set "ANDROID_SDK_CANDIDATE=%ANDROID_SDK_ROOT%"
if not defined ANDROID_SDK_CANDIDATE if defined LOCALAPPDATA if exist "%LOCALAPPDATA%\Android\Sdk\" set "ANDROID_SDK_CANDIDATE=%LOCALAPPDATA%\Android\Sdk"
if not defined ANDROID_SDK_CANDIDATE if defined USERPROFILE if exist "%USERPROFILE%\AppData\Local\Android\Sdk\" set "ANDROID_SDK_CANDIDATE=%USERPROFILE%\AppData\Local\Android\Sdk"

if not defined ANDROID_SDK_CANDIDATE (
  echo ERROR: Android SDK location not found.
  echo Set ANDROID_HOME ^(or ANDROID_SDK_ROOT^) to your SDK directory, or install the
  echo Android SDK under %%LOCALAPPDATA%%\Android\Sdk.
  exit /b 1
)

set "ANDROID_HOME=%ANDROID_SDK_CANDIDATE%"
set "ANDROID_SDK_ROOT=%ANDROID_HOME%"

if exist "%ANDROID_HOME%\platform-tools\adb.exe" (
  set "PATH=%ANDROID_HOME%\platform-tools;%PATH%"
)

echo Using Android SDK: %ANDROID_HOME%
echo.

where adb >nul 2>&1
if errorlevel 1 (
  echo ERROR: adb not found on PATH.
  echo Install Android SDK Platform-Tools under "%ANDROID_HOME%\platform-tools",
  echo or ensure adb.exe is otherwise available on PATH.
  exit /b 1
)

where npm.cmd >nul 2>&1
if errorlevel 1 (
  echo ERROR: npm not found on PATH.
  exit /b 1
)

echo Checking for an attached Android device...
adb get-state 1>nul 2>&1
if errorlevel 1 (
  echo ERROR: no Android device/emulator is ready for adb.
  echo Connect a device with USB debugging enabled, or start an emulator, then re-run.
  echo.
  adb devices -l
  exit /b 1
)

echo Connected device:
adb devices -l
echo.

if not exist "%REPO_ROOT%\mobile\capacitor\android\gradlew.bat" (
  echo ERROR: missing mobile\capacitor\android\gradlew.bat
  exit /b 1
)

if not exist "%REPO_ROOT%\mobile\capacitor\node_modules\@capacitor\cli" (
  echo Capacitor dependencies missing or incomplete - running npm ci...
  call npm.cmd --prefix "%REPO_ROOT%\mobile\capacitor" ci
  if errorlevel 1 (
    echo ERROR: npm ci failed in mobile\capacitor
    exit /b 1
  )
  if not exist "%REPO_ROOT%\mobile\capacitor\node_modules\@capacitor\cli" (
    echo ERROR: @capacitor/cli still missing after npm ci
    exit /b 1
  )
)

if not exist "%REPO_ROOT%\internal\httpapi\web\node_modules" (
  echo Web dependencies missing - running npm ci...
  call npm.cmd --prefix "%REPO_ROOT%\internal\httpapi\web" ci
  if errorlevel 1 (
    echo ERROR: npm ci failed in internal\httpapi\web
    exit /b 1
  )
)

REM Read the app version from internal/version/version.go for the Capacitor www artifact.
set "SCRUMBOY_VERSION="
for /f "tokens=4 delims= " %%V in ('findstr /R /C:"^const Version = " "%REPO_ROOT%\internal\version\version.go"') do (
  set "SCRUMBOY_VERSION=%%~V"
)
if not defined SCRUMBOY_VERSION (
  echo ERROR: could not read const Version from internal\version\version.go
  exit /b 1
)

echo [1/5] Building web sources...
call npm.cmd --prefix "%REPO_ROOT%\internal\httpapi\web" run build
if errorlevel 1 (
  echo ERROR: web build failed.
  exit /b 1
)

echo.
echo [2/5] Typechecking Capacitor shell...
call npm.cmd --prefix "%REPO_ROOT%\mobile\capacitor" run typecheck:shell
if errorlevel 1 (
  echo ERROR: Capacitor shell typecheck failed.
  exit /b 1
)

echo.
echo [3/5] Building Capacitor www artifact ^(version %SCRUMBOY_VERSION%^)...
call npm.cmd --prefix "%REPO_ROOT%\internal\httpapi\web" run build:capacitor-web -- --version %SCRUMBOY_VERSION%
if errorlevel 1 (
  echo ERROR: Capacitor www artifact build failed.
  exit /b 1
)

if not exist "%REPO_ROOT%\mobile\capacitor\www\index.html" (
  echo ERROR: mobile\capacitor\www\index.html was not generated.
  exit /b 1
)

echo.
echo [4/5] Syncing Capacitor Android project...
REM cap sync generates capacitor-cordova-android-plugins ^(including cordova.variables.gradle^)
REM and copies www into the Android assets tree. Without this, Gradle cannot configure :app.
call npm.cmd --prefix "%REPO_ROOT%\mobile\capacitor" run cap:sync
if errorlevel 1 (
  echo ERROR: cap sync android failed.
  exit /b 1
)

if not exist "%REPO_ROOT%\mobile\capacitor\android\capacitor-cordova-android-plugins\cordova.variables.gradle" (
  echo ERROR: cap sync did not create capacitor-cordova-android-plugins\cordova.variables.gradle
  exit /b 1
)

if not defined ANDROID_HOME (
  echo ERROR: ANDROID_HOME is unset before Gradle installDebug.
  exit /b 1
)
if not exist "%ANDROID_HOME%\" (
  echo ERROR: ANDROID_HOME "%ANDROID_HOME%" no longer exists before Gradle installDebug.
  exit /b 1
)

echo.
echo [5/5] Building and installing debug APK via Gradle/adb...
cd /d "%REPO_ROOT%\mobile\capacitor\android"
call ".\gradlew.bat" installDebug
if errorlevel 1 (
  echo.
  echo ERROR: gradlew installDebug failed.
  exit /b 1
)

echo.
echo Launching com.markrai.scrumboy/.MainActivity...
adb shell am start -n com.markrai.scrumboy/.MainActivity
if errorlevel 1 (
  echo ERROR: failed to launch the app on the device.
  exit /b 1
)

echo.
echo Deploy complete.
exit /b 0
