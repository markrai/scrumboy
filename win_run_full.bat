@echo off
setlocal DisableDelayedExpansion
for %%I in ("%~dp0.") do set "REPO_ROOT=%%~fI"
cd /d "%REPO_ROOT%"

echo.
echo ========================================
echo   Scrumboy (Full Mode)
echo ========================================
echo.
echo Data will be stored in ./data/app.db
echo Mode: Full (multi-project)
echo.

REM ---- Free port 8080 ----
echo Stopping any existing server on port 8080...
for /f "tokens=5" %%a in ('netstat -aon ^| findstr :8080 ^| findstr LISTENING') do (
  echo Killing process %%a...
  taskkill /F /PID %%a >nul 2>&1
)
timeout /t 1 /nobreak >nul

REM ---- Optional HTTPS (mkcert + cert.pem/key.pem) ----
REM SCRUMBOY_INTRANET_IP is optional: when set, include it as a mkcert SAN and print an intranet URL.
set USE_HTTPS=0
set "MKCERT_NAMES=localhost 127.0.0.1"
set "HAS_INTRANET_IP=0"
if defined SCRUMBOY_INTRANET_IP if not "%SCRUMBOY_INTRANET_IP%"=="" (
  set "MKCERT_NAMES=%SCRUMBOY_INTRANET_IP% localhost 127.0.0.1"
  set "HAS_INTRANET_IP=1"
)
if exist "cert.pem" (
  if exist "key.pem" (
    set USE_HTTPS=1
    if "%HAS_INTRANET_IP%"=="1" echo LAN HTTPS: existing certificate files are being reused. If this LAN IP is new, regenerate cert.pem/key.pem so the IP is included.
    goto :show_urls
  )
)
where mkcert >nul 2>&1
if %ERRORLEVEL% neq 0 (
  echo mkcert not found - will use HTTP.
  if "%HAS_INTRANET_IP%"=="1" (
    echo To enable HTTPS for intranet: install mkcert, run mkcert -install, then:
    echo   mkcert -cert-file cert.pem -key-file key.pem %SCRUMBOY_INTRANET_IP% localhost 127.0.0.1
  )
  echo.
  goto :show_urls
)
REM Write straight to cert.pem/key.pem. Wildcard rename breaks on Windows ^(see f.bat^).
echo Generating HTTPS certificates ^(or refreshing if one of cert.pem/key.pem is missing^)...
mkcert -cert-file cert.pem -key-file key.pem %MKCERT_NAMES%
if %ERRORLEVEL% neq 0 (
  echo WARNING: Certificate generation failed - will use HTTP
) else (
  if exist "cert.pem" if exist "key.pem" set USE_HTTPS=1
)
echo.

:show_urls
if %USE_HTTPS%==1 (
  echo Server URLs ^(HTTPS^):
  echo   Local:    https://127.0.0.1:8080/
  if "%HAS_INTRANET_IP%"=="1" echo   Intranet: https://%SCRUMBOY_INTRANET_IP%:8080/
) else (
  echo Server URLs ^(HTTP^):
  echo   Local:    http://127.0.0.1:8080/
  if "%HAS_INTRANET_IP%"=="1" echo   Intranet: http://%SCRUMBOY_INTRANET_IP%:8080/
)
if not "%HAS_INTRANET_IP%"=="1" echo LAN HTTPS: set SCRUMBOY_INTRANET_IP to this machine's LAN IP to include it in the certificate.
echo.
echo Press Ctrl+C to stop the server.
echo.

REM ---- Configuration ----
set "SCRUMBOY_MODE=full"

REM Resolve SCRUMBOY_ENCRYPTION_KEY with precedence:
REM process env var -> data/scrumboy.env -> legacy root scrumboy.env
set "SCRUMBOY_KEY_TMP=%TEMP%\scrumboy-key-%RANDOM%-%RANDOM%.tmp"
powershell -NoProfile -ExecutionPolicy Bypass -File "%REPO_ROOT%\scripts\resolve_scrumboy_encryption_key.ps1" -OutputPath "%SCRUMBOY_KEY_TMP%" -RepoRoot "%REPO_ROOT%"
if errorlevel 1 (
  if exist "%SCRUMBOY_KEY_TMP%" del "%SCRUMBOY_KEY_TMP%" >nul 2>&1
  exit /b 1
)

if not exist "%SCRUMBOY_KEY_TMP%" (
  echo ERROR: failed to resolve SCRUMBOY_ENCRYPTION_KEY.
  exit /b 1
)
set "SCRUMBOY_ENCRYPTION_KEY="
<"%SCRUMBOY_KEY_TMP%" set /p "SCRUMBOY_ENCRYPTION_KEY="
del "%SCRUMBOY_KEY_TMP%" >nul 2>&1

go run ./cmd/scrumboy
