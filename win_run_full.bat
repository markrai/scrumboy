@echo off
cd /d "%~dp0"

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
set USE_HTTPS=0
if exist "cert.pem" (
  if exist "key.pem" (
    set USE_HTTPS=1
    goto :show_urls
  )
)
where mkcert >nul 2>&1
if %ERRORLEVEL% neq 0 (
  echo mkcert not found - will use HTTP.
  echo To enable HTTPS for intranet: install mkcert, run mkcert -install, then:
  echo   mkcert -cert-file cert.pem -key-file key.pem 192.168.1.250 localhost
  echo.
  goto :show_urls
)
REM Write straight to cert.pem/key.pem. Wildcard rename breaks on Windows ^(see f.bat^).
echo Generating HTTPS certificates ^(or refreshing if one of cert.pem/key.pem is missing^)...
mkcert -cert-file cert.pem -key-file key.pem 192.168.1.250 localhost
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
  echo   Intranet: https://192.168.1.250:8080/
) else (
  echo Server URLs ^(HTTP^):
  echo   Local:    http://127.0.0.1:8080/
  echo   Intranet: http://192.168.1.250:8080/
)
echo.
echo Press Ctrl+C to stop the server.
echo.

REM ---- Configuration ----
set SCRUMBOY_MODE=full

REM 2FA encryption key from scrumboy.env (file is in .gitignore)
if exist scrumboy.env (
  for /f "usebackq delims=" %%K in ("scrumboy.env") do set SCRUMBOY_ENCRYPTION_KEY=%%K
) else (
  for /f "delims=" %%K in ('powershell -NoProfile -Command "[Convert]::ToBase64String((1..32 | ForEach-Object { [byte](Get-Random -Maximum 256^) }^)^)"') do set SCRUMBOY_ENCRYPTION_KEY=%%K
  echo %SCRUMBOY_ENCRYPTION_KEY%>scrumboy.env
  echo Created scrumboy.env with new key - 2FA will work.
)

go run ./cmd/scrumboy
