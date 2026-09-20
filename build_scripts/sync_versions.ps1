# Reads release_version.json and syncs the single version number everywhere else it's
# needed: versioninfo.json's 4 redundant fields (FixedFileInfo.FileVersion/ProductVersion
# numeric parts, StringFileInfo.FileVersion/ProductVersion strings) that goversioninfo
# requires.
#
# Single source of truth: edit release_version.json only, this script (called from
# build_exe.bat) keeps versioninfo.json in sync automatically. This project has no
# firmware component, so there's only ever the one version number to sync (a project that
# also ships firmware would typically propagate a second, independent version into that
# firmware's own config header).
param(
    [Parameter(Mandatory = $true)][string]$ProjectRoot
)
$ErrorActionPreference = "Stop"

# Writes text without a BOM and without appending an extra trailing newline, matching what
# Set-Content/-Encoding UTF8 does NOT do in Windows PowerShell 5.1 (it both adds a BOM and can
# append a newline) - keeps the diff to just the actual version-number change.
$Utf8NoBom = New-Object System.Text.UTF8Encoding $false
function Write-TextExact([string]$Path, [string]$Content) {
    [System.IO.File]::WriteAllText($Path, $Content, $Utf8NoBom)
}

$releaseVersionFile = Join-Path $ProjectRoot "release_version.json"
if (-not (Test-Path $releaseVersionFile)) {
    throw "release_version.json not found at $releaseVersionFile"
}

$rel = Get-Content -Raw -Path $releaseVersionFile | ConvertFrom-Json
$proxyVer = $rel.proxyVersion
if ([string]::IsNullOrWhiteSpace($proxyVer)) {
    throw "release_version.json must set proxyVersion"
}

# --- versioninfo.json ---
# Targeted regex replace (not parse+re-serialize) to avoid ConvertTo-Json reformatting the
# file. Safe here because "Major"/"Minor"/"Patch"/"Build" only ever appear under
# FixedFileInfo's two version objects, both of which should always hold the same value - and
# the FileVersion/ProductVersion regexes only match the *string* form (StringFileInfo's), not
# the object form (FixedFileInfo's), because they require a quote immediately after the colon.
#
# FixedFileInfo's four fields are plain integers (PE resource format), so a prerelease suffix
# (e.g. "0.9.9-beta.1") has to be stripped before splitting on '.' - only the numeric core
# goes into Major/Minor/Patch/Build. The *string* fields below (FileVersion/ProductVersion)
# keep the full $proxyVer, suffix and all.
$core = $proxyVer.Split('-')[0]
$parts = $core.Split('.')
$major = [int]$parts[0]
$minor = [int]$parts[1]
$patch = if ($parts.Length -ge 3) { [int]$parts[2] } else { 0 }
$build = if ($parts.Length -ge 4) { [int]$parts[3] } else { 0 }

$viPath = Join-Path $ProjectRoot "versioninfo.json"
$vi = Get-Content -Raw -Path $viPath
$vi = $vi -replace '"Major":\s*\d+', "`"Major`": $major"
$vi = $vi -replace '"Minor":\s*\d+', "`"Minor`": $minor"
$vi = $vi -replace '"Patch":\s*\d+', "`"Patch`": $patch"
$vi = $vi -replace '"Build":\s*\d+', "`"Build`": $build"
$vi = $vi -replace '"FileVersion":\s*"[^"]*"', "`"FileVersion`": `"$proxyVer`""
$vi = $vi -replace '"ProductVersion":\s*"[^"]*"', "`"ProductVersion`": `"$proxyVer`""
Write-TextExact $viPath $vi
Write-Host "versioninfo.json set to $proxyVer"
