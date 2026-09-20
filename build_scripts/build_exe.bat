@echo off
setlocal EnableDelayedExpansion

REM Get script directory
cd /d "%~dp0"
set "SCRIPT_DIR=%CD%"
set "PROJECT_ROOT=%SCRIPT_DIR%\..\.."
set "PROXY_ROOT=%SCRIPT_DIR%\.."

echo --- Building Gemini Flat Panel Proxy EXE ---

REM 0. Cleanup previous build
if exist "..\build\geminiflatpanel.exe" (
    echo [0/6] Cleaning previous build...
    del "..\build\geminiflatpanel.exe"
)

REM 1. Sync version from release_version.json into versioninfo.json. Single source of
REM    truth: edit release_version.json only, everything else (this file's own
REM    ProductVersion read below, build_linux.sh, the release-*.yml workflows) picks it
REM    up from there via sync_versions.ps1/.sh - versioninfo.json is a generated file
REM    from here on, never hand-edited.
echo [1/6] Syncing version from release_version.json...
powershell -ExecutionPolicy Bypass -File "%SCRIPT_DIR%\sync_versions.ps1" -ProjectRoot "%PROXY_ROOT%"
if %ERRORLEVEL% NEQ 0 (
    echo Error syncing versions!
    exit /b 1
)

REM 2. Build Frontend
echo [2/6] Building Frontend...
pushd "%PROXY_ROOT%\frontend-vue"
call npm install
if %ERRORLEVEL% NEQ 0 (
    echo Error during npm install!
    popd
    exit /b 1
)
call npm run build
if %ERRORLEVEL% NEQ 0 (
    echo Error during npm run build!
    popd
    exit /b 1
)
popd

REM 3. Get Product Version for Go Build
set "VERSION_INFO=%PROXY_ROOT%\versioninfo.json"
echo [3/6] Reading ProductVersion from versioninfo.json...
for /f "usebackq delims=" %%I in (`powershell -Command "$json = Get-Content -Raw -Path '%VERSION_INFO%'; $obj = ConvertFrom-Json -InputObject $json; $obj.StringFileInfo.ProductVersion"`) do set "APP_VERSION=%%I"

if "%APP_VERSION%"=="" (
    echo Error: Could not extract ProductVersion!
    exit /b 1
)
echo App Version: %APP_VERSION%

REM 4. Prepare Go Environment
echo [4/6] Installing/Updating goversioninfo...
pushd "%PROXY_ROOT%"
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
popd

REM 5. Generate Resources
echo [5/6] Generating Windows Resources (Icon/Manifest)...
pushd "%PROXY_ROOT%"
"%USERPROFILE%\go\bin\goversioninfo.exe" -64 -o resource.syso versioninfo.json
if %ERRORLEVEL% NEQ 0 (
    echo Error generating resources!
    popd
    exit /b 1
)
popd

REM 6. Build EXE
echo [6/6] Compiling Go executable...
pushd "%PROXY_ROOT%"
if not exist "build" mkdir build
go build -ldflags="-H=windowsgui -X main.AppVersion=%APP_VERSION%" -o build/geminiflatpanel.exe .
if exist resource.syso del resource.syso
popd
echo --- Build Complete: build/geminiflatpanel.exe ---
endlocal
