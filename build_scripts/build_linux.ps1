# Runs on Windows, cross-compiles for Linux (no installer, just raw binaries) - useful for
# a local, ad-hoc Linux build without needing a real Linux host. The actual GitHub release
# binaries are produced by the release-linux.yml GitHub Action instead, which builds
# natively on real Linux runners.
#
# No cross-compile toolchain is needed (no Zig, no extracted libusb sysroot): this project
# has no Linux-specific file that talks to hardware directly over libusb, and the only
# package that would pull in cgo/D-Bus dependencies (internal/systray) is entirely
# `//go:build windows` and never compiled into a Linux binary at all - so CGO_ENABLED
# doesn't even need setting, a plain `go build` with GOOS/GOARCH set cross-compiles
# cleanly out of the box.
$ErrorActionPreference = "Stop"

$ScriptDir = $PSScriptRoot
$ProjectRoot = Split-Path $ScriptDir -Parent

Write-Host "--- Building Gemini Flat Panel Proxy (Linux, cross-compiled from Windows) ---"

# 1. Sync version from release_version.json into versioninfo.json - same single source of
#    truth build_exe.bat uses.
Write-Host "[1/3] Syncing version from release_version.json..."
& (Join-Path $ScriptDir "sync_versions.ps1") -ProjectRoot $ProjectRoot

# 2. Build Frontend
Write-Host "[2/3] Building Frontend..."
Push-Location (Join-Path $ProjectRoot "frontend-vue")
try {
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
} finally {
    Pop-Location
}

# 3. Get Product Version, then cross-compile both architectures
$viPath = Join-Path $ProjectRoot "versioninfo.json"
$vi = Get-Content -Raw -Path $viPath | ConvertFrom-Json
$appVersion = $vi.StringFileInfo.ProductVersion
if ([string]::IsNullOrWhiteSpace($appVersion)) {
    throw "Could not extract ProductVersion from versioninfo.json"
}
Write-Host "App Version: $appVersion"

Push-Location $ProjectRoot
try {
    New-Item -ItemType Directory -Force -Path "build" | Out-Null

    Write-Host "[3/3] Compiling Go executables (linux/amd64, linux/arm64)..."
    $env:GOOS = "linux"

    $env:GOARCH = "amd64"
    go build -ldflags="-X main.AppVersion=$appVersion" -o "build/geminiflatpanel-linux-amd64" .
    if ($LASTEXITCODE -ne 0) { throw "go build (amd64) failed" }

    $env:GOARCH = "arm64"
    go build -ldflags="-X main.AppVersion=$appVersion" -o "build/geminiflatpanel-linux-arm64" .
    if ($LASTEXITCODE -ne 0) { throw "go build (arm64) failed" }
} finally {
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}

Write-Host "--- Build Complete: build/geminiflatpanel-linux-amd64, build/geminiflatpanel-linux-arm64 (v$appVersion) ---"
