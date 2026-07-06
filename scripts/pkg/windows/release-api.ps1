function Get-ReleaseHeaders {
    $headers = @{
        Accept = 'application/vnd.github+json'
        'User-Agent' = 'vaps-installer'
    }
    if ($env:GITHUB_TOKEN) {
        $headers.Authorization = "Bearer $($env:GITHUB_TOKEN)"
    } elseif ($env:GH_TOKEN) {
        $headers.Authorization = "Bearer $($env:GH_TOKEN)"
    }
    return $headers
}

function Normalize-ReleaseTag([string]$Tag) {
    if ($Tag.StartsWith('v')) { return $Tag }
    return "v$Tag"
}

function Get-ReleaseArchiveName([string]$Tag, [string]$Goos, [string]$Arch) {
    $version = $Tag.TrimStart('v')
    return "vaps-$version-$Goos-$Arch.zip"
}

function Get-ReleaseDownloadUrl([string]$Repo, [string]$Tag, [string]$Goos, [string]$Arch) {
    $asset = Get-ReleaseArchiveName $Tag $Goos $Arch
    return "https://github.com/$Repo/releases/download/$Tag/$asset"
}

function Get-WebLatestTag([string]$Repo) {
    $response = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -MaximumRedirection 5 -Headers @{ 'User-Agent' = 'vaps-installer' }
    return $response.BaseResponse.ResponseUri.Segments[-1].TrimEnd('/')
}

function Get-ReleaseTagFromJson([string]$Json) {
    if ($Json -match '"tag_name"\s*:\s*"([^"]+)"') {
        return $Matches[1]
    }
    return $null
}

function Test-ReleaseJsonBool([string]$Json, [string]$Field) {
    if ($Json -match "`"$Field`"\s*:\s*(true|false)") {
        return $Matches[1] -eq 'true'
    }
    return $false
}

function Get-FirstPreReleaseTag([string]$Repo) {
    $headers = Get-ReleaseHeaders
    $json = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=100" -Headers $headers
    foreach ($release in $json) {
        if ($release.prerelease -and -not $release.draft) {
            return $release.tag_name
        }
    }
    return $null
}

function Resolve-ReleaseTag([string]$Repo, [string]$Channel, [string]$Pinned) {
    if ($Pinned) {
        return (Normalize-ReleaseTag $Pinned)
    }

    $headers = Get-ReleaseHeaders
    if ($Channel -eq 'release') {
        try {
            $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
            if ($latest.draft) { throw 'latest release is a draft; nothing to install' }
            if ($latest.prerelease) { throw 'latest release is a pre-release; use --channel pre-release' }
            return $latest.tag_name
        } catch {
            return (Get-WebLatestTag $Repo)
        }
    }

    $tag = Get-FirstPreReleaseTag $Repo
    if (-not $tag) {
        throw 'no pre-release found; set GITHUB_TOKEN or pass -Version TAG'
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

function Get-ReleaseTagForVersion([string]$Version) {
    if ($Version.StartsWith('v')) { return $Version }
    return "v$Version"
}
