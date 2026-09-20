#!/bin/bash
# Bash equivalent of sync_versions.ps1, for use on a native Linux host/CI runner where
# PowerShell isn't guaranteed to be available (build_linux.sh calls this instead).
#
# Reads release_version.json and syncs the single version number into versioninfo.json's
# 4 redundant fields (FixedFileInfo.FileVersion/ProductVersion numeric parts,
# StringFileInfo.FileVersion/ProductVersion strings) that goversioninfo requires.
#
# Single source of truth: edit release_version.json only, this script (called from
# build_linux.sh) keeps versioninfo.json in sync automatically. This project has no
# firmware component, so there's only ever the one version number to sync (a project that
# also ships firmware would typically propagate a second, independent version into that
# firmware's own config header).
#
# Uses sed with POSIX basic regex only (no `grep -P`/PCRE) to avoid the locale-dependent
# failures PCRE mode can hit, and assumes GNU sed (`sed -i` without a backup-suffix argument),
# which ships on essentially every Linux distro including Raspberry Pi OS - this script
# targets Linux only, so no BSD-sed (`sed -i ''`) fallback is needed.
set -e

PROJECT_ROOT="${1:?Usage: sync_versions.sh <project_root>}"

RELEASE_VERSION_FILE="$PROJECT_ROOT/release_version.json"
if [ ! -f "$RELEASE_VERSION_FILE" ]; then
    echo "Error: release_version.json not found at $RELEASE_VERSION_FILE" >&2
    exit 1
fi

PROXY_VER=$(sed -n 's/.*"proxyVersion"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$RELEASE_VERSION_FILE")

if [ -z "$PROXY_VER" ]; then
    echo "Error: release_version.json must set proxyVersion" >&2
    exit 1
fi

# --- versioninfo.json ---
# Targeted sed replace (not a JSON parse+re-serialize) to avoid reformatting the file - keeps
# the diff to just the actual version-number change. Safe here because "Major"/"Minor"/"Patch"/
# "Build" only ever appear under FixedFileInfo's two version objects, both of which should
# always hold the same value - and the FileVersion/ProductVersion patterns only match the
# *string* form (StringFileInfo's, quote right after the colon), not the object form
# (FixedFileInfo's, which starts with "{").
#
# FixedFileInfo's four fields are plain integers (PE resource format), so a prerelease suffix
# (e.g. "0.9.9-beta.1") has to be stripped before splitting on '.' - only the numeric core
# goes into Major/Minor/Patch/Build. The *string* fields below (FileVersion/ProductVersion)
# keep the full $PROXY_VER, suffix and all.
PROXY_VER_CORE="${PROXY_VER%%-*}"
IFS='.' read -r MAJOR MINOR PATCH BUILD <<< "$PROXY_VER_CORE"
MAJOR=${MAJOR:-0}
MINOR=${MINOR:-0}
PATCH=${PATCH:-0}
BUILD=${BUILD:-0}

VI_PATH="$PROJECT_ROOT/versioninfo.json"
if [ ! -f "$VI_PATH" ]; then
    echo "Error: versioninfo.json not found at $VI_PATH" >&2
    exit 1
fi

sed -i \
    -e "s/\"Major\":[[:space:]]*[0-9]*/\"Major\": $MAJOR/" \
    -e "s/\"Minor\":[[:space:]]*[0-9]*/\"Minor\": $MINOR/" \
    -e "s/\"Patch\":[[:space:]]*[0-9]*/\"Patch\": $PATCH/" \
    -e "s/\"Build\":[[:space:]]*[0-9]*/\"Build\": $BUILD/" \
    -e "s/\"FileVersion\":[[:space:]]*\"[^\"]*\"/\"FileVersion\": \"$PROXY_VER\"/" \
    -e "s/\"ProductVersion\":[[:space:]]*\"[^\"]*\"/\"ProductVersion\": \"$PROXY_VER\"/" \
    "$VI_PATH"
echo "versioninfo.json set to $PROXY_VER"
