# Install the haiven CLI on Windows from a GitHub release.
#
#   irm <haiven>/install.ps1 | iex
#
# Environment:
#   HAIVEN_VERSION        release to install (default: latest)
#   HAIVEN_INSTALL_DIR    install directory (default: %LOCALAPPDATA%\haiven\bin)
#   HAIVEN_DOWNLOAD_BASE  release mirror (default: GitHub releases)
#   HAIVEN_NO_PATH=1      do not add the install directory to the user PATH
#
# The archive is verified against the release's checksums.txt before anything
# is installed. Linux and macOS: use install.sh.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'haiven-dev/haiven-cli'
$version = $env:HAIVEN_VERSION
$base = if ($env:HAIVEN_DOWNLOAD_BASE) { $env:HAIVEN_DOWNLOAD_BASE } else { "https://github.com/$repo/releases/download" }
$installDir = if ($env:HAIVEN_INSTALL_DIR) { $env:HAIVEN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'haiven\bin' }

function Fail([string]$message) {
    # throw, not exit: the documented irm | iex invocation runs in the
    # caller's session, and exit would close that interactive process.
    # An unhandled exception still makes -File return nonzero.
    throw "haiven install: $message"
}

switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
}

if (-not $version) {
    try {
        $release = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$repo/releases/latest"
        $version = $release.tag_name
    } catch {
        Fail 'could not resolve the latest release; set HAIVEN_VERSION'
    }
}
$version = $version.TrimStart('v')
if ($version -notmatch '^[0-9A-Za-z.+-]+$') { Fail "invalid version: $version" }

$asset = "haiven_${version}_windows_${arch}.zip"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("haiven-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $sums = Join-Path $tmp 'checksums.txt'
    $archive = Join-Path $tmp $asset
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$version/checksums.txt" -OutFile $sums
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$version/$asset" -OutFile $archive
    } catch {
        Fail "could not download haiven $version ($asset): $($_.Exception.Message)"
    }

    $want = $null
    foreach ($line in Get-Content $sums) {
        $fields = $line -split '\s+'
        if ($fields.Count -ge 2 -and ($fields[1] -eq $asset -or $fields[1] -eq "*$asset")) {
            $want = $fields[0].ToLowerInvariant()
            break
        }
    }
    if (-not $want) { Fail "checksums.txt lists no $asset" }
    $got = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($got -ne $want) { Fail "checksum mismatch for $asset; nothing was installed" }

    $extract = Join-Path $tmp 'x'
    Expand-Archive -Path $archive -DestinationPath $extract
    $bin = Get-ChildItem -Path $extract -Recurse -File -Filter 'haiven.exe' | Select-Object -First 1
    if (-not $bin) { Fail "$asset contains no haiven.exe" }

    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    Copy-Item -Force -Path $bin.FullName -Destination (Join-Path $installDir 'haiven.exe')
} finally {
    Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}

Write-Output "installed haiven $version to $(Join-Path $installDir 'haiven.exe')"
if ($env:HAIVEN_NO_PATH -ne '1') {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = if ($userPath) { $userPath -split ';' } else { @() }
    if ($entries -notcontains $installDir) {
        $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Output "added $installDir to your user PATH; open a new terminal to use haiven"
    }
}
