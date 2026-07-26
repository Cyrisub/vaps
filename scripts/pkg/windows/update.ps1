#Requires -Version 5.1
param(
    [string]$Channel,
    [string]$Version,
    [switch]$Restart
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$InstallDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$Conf = Join-Path $InstallDir 'vaps-install.conf'
$StartScript = Join-Path $InstallDir 'start.ps1'
$Bin = Join-Path $InstallDir 'vaps.exe'

function Write-Log([string]$Message) {
    Write-Output "[update] $Message"
}

function Normalize-Tag([string]$Tag) {
    if ($Tag.StartsWith('v')) { return $Tag }
    return "v$Tag"
}

function Get-ArchiveName([string]$Tag, [string]$Goos, [string]$Arch) {
    $version = $Tag.TrimStart('v')
    return "vaps-$version-$Goos-$Arch.zip"
}

function Get-DownloadUrl([string]$Repo, [string]$Tag, [string]$Goos, [string]$Arch) {
    $asset = Get-ArchiveName $Tag $Goos $Arch
    return "https://github.com/$Repo/releases/download/$Tag/$asset"
}

function Get-WebLatestTag([string]$Repo) {
    $response = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -MaximumRedirection 5 -Headers @{ 'User-Agent' = 'vaps-installer' }
    $tag = $response.BaseResponse.ResponseUri.Segments[-1].TrimEnd('/')
    if ([string]::IsNullOrWhiteSpace($tag) -or $tag -eq 'releases') {
        throw 'could not resolve latest release tag'
    }
    return $tag
}

function Get-HtmlPreReleaseTag([string]$Html) {
    $tag = $null
    $pendingWarning = $false
    foreach ($line in ($Html -split '>')) {
        if ($line -match 'releases/tag/([^"]+)') {
            $tag = $Matches[1]
            $pendingWarning = $false
        } elseif ($line -match 'Label--warning') {
            $pendingWarning = $true
        } elseif ($line -match '^Pre-release' -and $tag -and $pendingWarning) {
            return $tag
        } else {
            $pendingWarning = $false
        }
    }
    return $null
}

function Get-WebPreReleaseTag([string]$Repo) {
    $response = Invoke-WebRequest -Uri "https://github.com/$Repo/releases" -Headers @{ 'User-Agent' = 'vaps-installer' }
    return (Get-HtmlPreReleaseTag $response.Content)
}

function Resolve-Tag([string]$Repo, [string]$Channel, [string]$Pinned) {
    if ($Pinned) {
        return (Normalize-Tag $Pinned)
    }

    if ($Channel -eq 'release') {
        return (Get-WebLatestTag $Repo)
    }

    $tag = Get-WebPreReleaseTag $Repo
    if (-not $tag) {
        throw 'no pre-release found; pass -Version TAG to install a specific release'
    }
    return $tag
}

function Read-VapsVersionInfo([string]$BinPath) {
    $line = & $BinPath --version
    if ($line -match '^vaps ([^ ]+) \((.+)\)$') {
        return @{
            Version = $Matches[1]
            Channel = $Matches[2]
        }
    }
    throw "could not parse version output: $line"
}

function Write-InstallConf {
    @(
        '# Install metadata for update.ps1.',
        "GITHUB_REPO=`"$GITHUB_REPO`"",
        "ARCH=`"$ARCH`"",
        "GOOS=`"windows`"",
        "INSTALL_DIR=`"$InstallDir`""
    ) | Set-Content -Path $Conf -Encoding UTF8
}

function Import-InstallConf {
    if (-not (Test-Path $Conf)) {
        throw "missing $Conf"
    }
    Get-Content -Path $Conf | ForEach-Object {
        if ($_ -match '^\s*#' -or $_ -match '^\s*$') { return }
        $name, $value = $_ -split '=', 2
        Set-Variable -Name $name.Trim() -Value $value.Trim().Trim('"') -Scope Script
    }
    if (-not (Get-Variable -Name GITHUB_REPO -Scope Script -ErrorAction SilentlyContinue)) { throw 'missing GITHUB_REPO in vaps-install.conf' }
    if (-not (Get-Variable -Name ARCH -Scope Script -ErrorAction SilentlyContinue)) { throw 'missing ARCH in vaps-install.conf' }
    $recorded = ''
    $match = Select-String -Path $Conf -Pattern '^INSTALL_DIR="(?<dir>.+)"$' -ErrorAction SilentlyContinue
    if ($match) {
        $recorded = $match.Matches[0].Groups['dir'].Value
    }
    if ($recorded -ne $InstallDir) {
        Write-InstallConf
    }
}

function Install-PackageContents([string]$ExtractedDir) {
    Copy-Item -Path (Join-Path $ExtractedDir 'vaps.exe') -Destination $Bin -Force
    Copy-Item -Path (Join-Path $ExtractedDir 'start.ps1') -Destination (Join-Path $InstallDir 'start.ps1') -Force
    Copy-Item -Path (Join-Path $ExtractedDir 'update.ps1') -Destination (Join-Path $InstallDir 'update.ps1') -Force

    $configPath = Join-Path $InstallDir 'config.toml'
    if (-not (Test-Path $configPath)) {
        Copy-Item -Path (Join-Path $ExtractedDir 'config.toml') -Destination $configPath -Force
    }
}

Import-InstallConf

if (-not (Test-Path $Bin)) { throw "missing vaps binary: $Bin" }

$info = Read-VapsVersionInfo $Bin
if (-not $PSBoundParameters.ContainsKey('Channel') -or [string]::IsNullOrWhiteSpace($Channel)) {
    $Channel = $info.Channel
}

if (-not $PSBoundParameters.ContainsKey('Version')) {
    $Version = ''
}

$tag = Resolve-Tag $GITHUB_REPO $Channel $Version
$downloadUrl = Get-DownloadUrl $GITHUB_REPO $tag 'windows' $ARCH
$assetName = Get-ArchiveName $tag 'windows' $ARCH
$currentTag = Normalize-Tag $info.Version

if ([string]::IsNullOrWhiteSpace($Version) -and $tag -eq $currentTag) {
    Write-Log "already on $tag; nothing to do"
    exit 0
}

$wasRunning = $false
try {
    & $StartScript -Action status | Out-Null
    $wasRunning = $true
    Write-Log 'stopping running vaps'
    & $StartScript -Action stop
} catch {
    $wasRunning = $false
}

Write-Log "updating to $tag ($assetName)"

$tmpdir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmpdir | Out-Null
try {
    $archive = Join-Path $tmpdir 'package.zip'
    Invoke-WebRequest -Uri $downloadUrl -OutFile $archive
    $extracted = Join-Path $tmpdir 'extracted'
    New-Item -ItemType Directory -Path $extracted | Out-Null
    Expand-Archive -Path $archive -DestinationPath $extracted -Force
    Install-PackageContents $extracted
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmpdir
}

Write-InstallConf

Write-Log "update complete: $tag"

if ($Restart -or $wasRunning) {
    & $StartScript -Action start
}
