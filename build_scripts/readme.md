# Build Scripts

[← Back to main readme](../readme.md)

This folder contains everything needed to build and package the Gemini Flat Panel Proxy,
for both Windows (installer) and Linux (systemd service).

## Table of Contents

- [Single Source of Truth: `release_version.json`](#single-source-of-truth-release_versionjson)
- [Windows](#windows)
- [Local Development (Hot Reload)](#local-development-hot-reload)
- [Linux](#linux)
- [GitHub Actions: `release-*.yml`](#github-actions-release-yml)
- [Cutting a release / beta](#cutting-a-release--beta)
- [Prerequisites](#prerequisites)

## Single Source of Truth: `release_version.json`

The version number used to be edited by hand directly in `versioninfo.json` (or, before
that, patched from the git tag by the old CI workflow). Now there's exactly one place to
edit, at the repo root:

```json
{ "proxyVersion": "0.9.8" }
```

Every build script below reads this file first and propagates it into `versioninfo.json`
(the four redundant `FixedFileInfo`/`StringFileInfo` version fields `goversioninfo`
needs), via one of two equivalent sync scripts:

- **`sync_versions.ps1`** (Windows/PowerShell) - used by `build_exe.bat` and
  `build_linux.ps1`.
- **`sync_versions.sh`** (bash/`sed`) - used by `build_linux.sh`, for the case where the
  script runs natively on a Linux host/CI runner where PowerShell isn't available.

You should never need to run either sync script directly - the build scripts call them
automatically. `versioninfo.json` is a generated file from here on - don't hand-edit it,
edit `release_version.json` instead.

(This project has no separate firmware component, so there's only ever the one version
number to keep in sync.)

## Windows

### `build_exe.bat`
Builds the plain `geminiflatpanel.exe` (no installer). Steps:
1. Sync the version from `release_version.json` (see above).
2. Build the Vue frontend (`npm install && npm run build`).
3. Read the `ProductVersion` back out of `versioninfo.json` for the Go build below.
4. Install/update `goversioninfo`.
5. Generate `resource.syso` (Windows icon/manifest) from `versioninfo.json` + `icon.ico`.
6. `go build` → `build/geminiflatpanel.exe` (deleting `resource.syso` afterward).

### `build_installer.bat`
Builds on top of `build_exe.bat`: runs it, then fills `installer.iss`'s version
placeholders and compiles the final Windows installer with Inno Setup 6
(`C:\Program Files (x86)\Inno Setup 6\ISCC.exe`, hardcoded path) →
`build/GeminiFlatPanelProxy-Setup-<version>.exe`.

### `installer.iss`
The Inno Setup template - force-kills a running `geminiflatpanel.exe` before both
install and uninstall, auto-detects and silently uninstalls a previous version, and
registers/removes Windows autostart. Also installs `Helper/Create-Driver.bat` +
`Helper/Create-AscomDriver.ps1` (the classic-ASCOM driver registration helper -
separate from this build pipeline).

## Local Development (Hot Reload)

```bash
cd frontend-vue
npm run dev     # Dev server: http://localhost:5173
```
> [!NOTE]
> The dev server proxies API requests to the running Go proxy.

## Linux

### `build_linux.sh`
A native build script, meant to run directly on a Linux host (relies on the host's own
default `GOOS`/`GOARCH` - running it via Git Bash on Windows will silently produce a
Windows binary, not a Linux one). Detects its own architecture (`uname -m`,
`x86_64`→`amd64`/`aarch64`→`arm64` - same mapping `install_linux.sh` uses) and names its
output `build/geminiflatpanel-linux-<amd64|arm64>`. This is what
[`release-linux.yml`](#github-actions-release-yml) actually runs, on real
`amd64`/`arm64` GitHub-hosted Linux runners, to produce the release binaries.

Needs nothing beyond Go and Node - this project has no cgo/libusb dependency on Linux at
all (see the script's own header comment for why).

### `build_linux.ps1`
Runs on Windows, cross-compiles for Linux (no installer, just raw binaries) - useful for
a local, ad-hoc Linux build without a real Linux host. Same version-sync and
frontend-build steps as `build_exe.bat`, then two plain `go build` passes with
`GOOS=linux`: `GOARCH=amd64` → `build/geminiflatpanel-linux-amd64`, and
`GOARCH=arm64` (Raspberry Pi 4/5, 64-bit) → `build/geminiflatpanel-linux-arm64`. No
cross-compile toolchain needed (no cgo requirement to satisfy).

### `install_linux.sh`
A one-line installer for end users (`curl ... | sudo bash`, see
[docs/installation_linux.md](../docs/installation_linux.md) for the full walkthrough):
downloads the latest GitHub release binary matching the host's architecture, installs it
to `/usr/local/bin`, adds the invoking user to the `dialout` group for serial port
access, and sets up a `systemd` service (`geminiflatpanel`). Expects release assets
named `geminiflatpanel-linux-<amd64|arm64>`, matching both `build_linux.ps1`'s and
`build_linux.sh`'s output naming.

Set `GFP_RELEASE_TAG` to install from a specific release instead of whatever's current
`latest`:
```bash
curl -sSL .../install_linux.sh | sudo GFP_RELEASE_TAG=v0.9.9-beta.1 bash
```
(As a `sudo` argument, not before the pipeline - `sudo` doesn't pass through the invoking
shell's environment variables otherwise.)

Set `GFP_UNINSTALL=1` (same argument-position rule) to remove everything the script
itself installed - service, binary - and exit, instead of installing:
```bash
curl -sSL .../install_linux.sh | sudo GFP_UNINSTALL=1 bash
```
Leaves the `dialout` group membership and `~/.config/GeminiFlatPanelProxy/` (saved
config/logs) alone on purpose - other things on the system may depend on the group, and
the config is worth keeping in case of a reinstall.

## GitHub Actions: `release-*.yml`

`release-windows.yml`/`release-linux.yml`/`release-all.yml` are **manual only**
(`workflow_dispatch`), also callable as reusable workflows (`workflow_call`). Each
takes an optional `release_tag` input (blank = whatever's current `latest`) -
**none of them creates a release**, they only upload assets to one that already exists
(create it by hand first: tag, release notes, mark as pre-release if it's a beta).

- **`release-windows.yml`** - `windows-latest` runner: Go, Node, Inno Setup via
  Chocolatey, `build_installer.bat`, uploads the installer.
- **`release-linux.yml`** - builds `geminiflatpanel-linux-amd64`/`-arm64` **natively**
  in parallel on real `ubuntu-24.04`/`ubuntu-24.04-arm` runners via `build_linux.sh`,
  uploads them plus `install_linux.sh`. Includes a CRLF line-ending sanity check on
  `install_linux.sh` right before upload.
- **`release-all.yml`** - runs both in order, Windows → Linux (`needs:` chain).

`gh release upload ... --clobber` throughout, so re-running any of these replaces
existing same-named assets on the target release - useful for fixing a bad upload, not
just adding new ones.

`release-daily.yml` is different - it runs on its own (nightly cron, `17 2 * * *`
UTC, or manual dispatch) and, unlike the three above, **does** create/update a
release itself: a single rolling `dev-build` prerelease, force-moved to a fresh
version-bump commit each time something actually changed since the last dev-build
(a no-op day - nothing to build/publish - if nothing did, unless dispatched with
`force: true`). Version strings look like `0.9.8-dev-build.a1b2c3d` (the short hash of
the last commit that touched the repo, not the date - keeps the version tied to real
content). Once published, it calls `release-windows.yml`/`release-linux.yml` itself
with `release_tag: dev-build` - nothing else to do by hand. Install a dev build with
`GFP_RELEASE_TAG=dev-build` (see [install_linux.sh](#linux) above, or the Windows
installer attached directly to the
[`dev-build` release](https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/tag/dev-build)).
Rebuilt/unreviewed by nature - fine for testing, not for anything you depend on.

## Cutting a release / beta

Ongoing development happens on `dev`; `main` only ever advances at release time, via
this exact sequence (in this order - merging `dev` into `main` *before* the version-bump
commit, not after, is what keeps the release tag reachable from `dev`'s own history
afterward, which `release-daily.yml`'s "since the last stable release" note generation
depends on):

1. Merge `dev` into `main`.
2. On `main`: bump `release_version.json`, commit as `Release vX.Y.Z` - a fixed anchor
   commit for the release, separate from whatever the last `dev` commit happened to be.
3. Create the GitHub release by hand: tag `vX.Y.Z` (pointing at that anchor commit),
   write notes - via the web UI or `gh release create vX.Y.Z --notes "..."` (add
   `--prerelease` for a beta). This is also what actually creates/pushes the tag.
4. From the Actions tab, run `release-all` (stable), or `release-windows`/
   `release-linux` individually (e.g. for a beta, same `release_tag: vX.Y.Z` on each).
5. Merge `main` back into `dev`, so `dev` picks up the anchor commit (and the now-
   reachable tag) and development continues from there.

## Prerequisites

| Tool | Used by | Notes |
|---|---|---|
| [Go](https://go.dev/) | all build scripts | `go build` for the proxy backend |
| [Node.js](https://nodejs.org/) / npm | all build scripts | Vue frontend build |
| [Inno Setup 6](https://jrsoftware.org/isinfo.php) | `build_installer.bat` | Hardcoded path: `C:\Program Files (x86)\Inno Setup 6\ISCC.exe` |
