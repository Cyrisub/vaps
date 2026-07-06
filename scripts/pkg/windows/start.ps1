#Requires -Version 5.1
param(
    [ValidateSet('start', 'stop', 'status', 'restart', 'help')]
    [string]$Action = 'start'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$InstallDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$Bin = Join-Path $InstallDir 'vaps.exe'
$Config = Join-Path $InstallDir 'config.toml'
$PidFile = Join-Path $InstallDir 'vaps.pid'
$LogFile = Join-Path $InstallDir 'logs\startup.log'

function Show-Usage {
    Write-Output @'
Usage: start.ps1 [-Action start|stop|status|restart]

  start    Start vaps in the background
  stop     Stop the background vaps process
  status   Show whether vaps is running
  restart  Stop then start vaps
'@
}

function Test-Running {
    if (-not (Test-Path $PidFile)) { return $false }
    $pidValue = Get-Content -Path $PidFile -ErrorAction SilentlyContinue
    if (-not $pidValue) { return $false }
    return Get-Process -Id ([int]$pidValue) -ErrorAction SilentlyContinue
}

function Start-Server {
    if (Test-Running) {
        $pidValue = Get-Content -Path $PidFile
        Write-Output "vaps already running (pid $pidValue)"
        return
    }

    New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir 'logs') | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir 'data') | Out-Null

    $process = Start-Process -FilePath $Bin -ArgumentList @('--config', $Config) `
        -WorkingDirectory $InstallDir -RedirectStandardOutput $LogFile `
        -RedirectStandardError $LogFile -PassThru -WindowStyle Hidden
    Set-Content -Path $PidFile -Value $process.Id

    Start-Sleep -Milliseconds 200
    if (Test-Running) {
        Write-Output "vaps started (pid $($process.Id))"
        Write-Output "config: $Config"
        Write-Output "logs:   $(Join-Path $InstallDir 'logs\')"
    } else {
        Remove-Item -Force -ErrorAction SilentlyContinue $PidFile
        throw "vaps failed to start; see $LogFile"
    }
}

function Stop-Server {
    if (-not (Test-Running)) {
        Write-Output 'vaps is not running'
        Remove-Item -Force -ErrorAction SilentlyContinue $PidFile
        return
    }

    $pidValue = [int](Get-Content -Path $PidFile)
    Stop-Process -Id $pidValue -ErrorAction SilentlyContinue
    for ($i = 0; $i -lt 20; $i++) {
        if (-not (Get-Process -Id $pidValue -ErrorAction SilentlyContinue)) {
            Remove-Item -Force -ErrorAction SilentlyContinue $PidFile
            Write-Output 'vaps stopped'
            return
        }
        Start-Sleep -Milliseconds 500
    }

    Stop-Process -Id $pidValue -Force -ErrorAction SilentlyContinue
    Remove-Item -Force -ErrorAction SilentlyContinue $PidFile
    Write-Output 'vaps stopped'
}

switch ($Action) {
    'start' { Start-Server }
    'stop' { Stop-Server }
    'status' {
        if (Test-Running) {
            Write-Output "vaps running (pid $(Get-Content -Path $PidFile))"
        } else {
            Write-Output 'vaps not running'
            exit 1
        }
    }
    'restart' {
        Stop-Server
        Start-Server
    }
    'help' { Show-Usage }
    default { Show-Usage }
}
