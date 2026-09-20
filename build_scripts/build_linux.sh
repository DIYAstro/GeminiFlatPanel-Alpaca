#!/bin/bash
set -e

# Native build, meant to run directly on a Linux host/CI runner (uses the host's own
# default GOOS/GOARCH rather than setting them explicitly - running this via Git Bash on
# Windows will silently produce a Windows binary, not a Linux one; use build_linux.ps1
# from Windows instead).
#
# This needs no C compiler, no cgo, and no libusb: this project has no Linux-specific
# file that talks to hardware directly over libusb (some USB-serial devices need that to
# work around reset-on-connect quirks in the kernel's own tty driver, but this one
# doesn't), and the only package that would pull in cgo/D-Bus dependencies
# (internal/systray) is entirely `//go:build windows` and never compiled into a Linux
# binary at all. A plain `go build` is all this needs.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Detect the host's own architecture - same mapping install_linux.sh uses - so the output
# is named the same way build_linux.ps1's cross-compiled binaries are
# (geminiflatpanel-linux-amd64/-arm64). Matters for anything that consumes this output
# directly by that name, e.g. the release-linux.yml GitHub Action.
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH_TAG="amd64"
        ;;
    aarch64)
        ARCH_TAG="arm64"
        ;;
    *)
        echo "Error: Unsupported architecture '$ARCH'."
        echo "Supported: x86_64 (amd64), aarch64 (arm64)"
        exit 1
        ;;
esac
OUTPUT_BINARY="geminiflatpanel-linux-$ARCH_TAG"

echo "--- Building Gemini Flat Panel Proxy (Linux, $ARCH_TAG) ---"

# 0. Cleanup previous build
if [ -f "$PROJECT_ROOT/build/$OUTPUT_BINARY" ]; then
    echo "[0/4] Cleaning previous build..."
    rm "$PROJECT_ROOT/build/$OUTPUT_BINARY"
fi

# 1. Sync version from release_version.json into versioninfo.json. Single source of
#    truth: edit release_version.json only, this script keeps versioninfo.json (and so
#    the version baked into this binary via -ldflags below) in sync automatically.
#    PowerShell isn't guaranteed to be available on a native Linux host, so this uses the
#    bash/sed equivalent sync_versions.sh rather than calling sync_versions.ps1.
#    Invoked as `bash sync_versions.sh` rather than `./sync_versions.sh` on purpose:
#    git's executable bit is easy to lose on a file that's ever edited from Windows (as
#    this repo's scripts have been), and unlike relying on that bit, this works
#    regardless of it.
echo "[1/4] Syncing version from release_version.json..."
bash "$SCRIPT_DIR/sync_versions.sh" "$PROJECT_ROOT"

# 2. Build Frontend
echo "[2/4] Building Frontend..."
cd "$PROJECT_ROOT/frontend-vue"
npm install
npm run build

# 3. Get Product Version for Go Build
# Needs a UTF-8 locale for `grep -P` to work correctly; on a real Linux distro (and GitHub's
# Ubuntu runners) that's normally the default, but if you ever see a "grep: -P supports only
# unibyte and UTF-8 locales" warning, set e.g. LC_ALL=C.UTF-8 before running this script.
echo "[3/4] Reading ProductVersion from versioninfo.json..."
cd "$PROJECT_ROOT"
APP_VERSION=$(grep -oP '"ProductVersion":\s*"\K[^"]+' versioninfo.json)
if [ -z "$APP_VERSION" ]; then
    echo "Error: Could not extract ProductVersion!"
    exit 1
fi
echo "App Version: $APP_VERSION"

# 4. Build Binary
echo "[4/4] Compiling Go executable..."
mkdir -p build
go build -ldflags="-X main.AppVersion=$APP_VERSION" -o "build/$OUTPUT_BINARY" .

echo "--- Build Complete: build/$OUTPUT_BINARY (v$APP_VERSION) ---"
