[CmdletBinding()]
param(
  [switch]$TestMode,
  [ValidatePattern('^[A-Za-z0-9_.-]+$')]
  [string]$WslDistro = 'Ubuntu',
  [ValidatePattern('^[A-Za-z0-9_.-]+$')]
  [string]$WslUser = 'victor_1',
  [ValidatePattern('^/[A-Za-z0-9_./-]+$')]
  [string]$WslKeepalivePath = '/home/victor_1/.x-science-v0.1.0/scripts/dev/source-wsl-keepalive.sh'
)

$ErrorActionPreference = 'Stop'

function Get-WslKeepaliveArguments {
  param([string]$Distro, [string]$User, [string]$KeepalivePath)
  if ([string]::IsNullOrWhiteSpace($Distro) -or
      [string]::IsNullOrWhiteSpace($User) -or
      [string]::IsNullOrWhiteSpace($KeepalivePath) -or
      -not $KeepalivePath.StartsWith('/')) {
    throw 'WSL distro, user, and absolute keepalive path are required.'
  }
  return @(
    '-d', $Distro, '-u', 'root', '--exec', '/usr/bin/env',
    "X_SCIENCE_DEV_WSL_USER=$User", $KeepalivePath
  )
}

function Enter-SupervisorMutex {
  param([Parameter(Mandatory)]$Mutex)
  try {
    return $Mutex.WaitOne(0, $false)
  } catch {
    $exception = $_.Exception
    while ($null -ne $exception) {
      if ($exception -is [System.Threading.AbandonedMutexException]) {
        # .NET transfers ownership while reporting an abandoned mutex.
        return $true
      }
      $exception = $exception.InnerException
    }
    throw
  }
}

if ($TestMode) { return }

# Retain installed paths and singleton identity. Linux systemd is the only
# product-service restart owner; this process only owns one keepalive client.
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'XScience\source-dev'
$logPath = Join-Path $runtimeRoot 'watchdog.log'
$oldLogPath = "$logPath.1"
$maxLogBytes = 512KB
New-Item -ItemType Directory -Path $runtimeRoot -Force | Out-Null

function Write-SupervisorLog {
  param([string]$Code, [string]$Detail = '')
  if (Test-Path -LiteralPath $logPath) {
    $item = Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue
    if ($item -and $item.Length -ge $maxLogBytes) {
      Move-Item -LiteralPath $logPath -Destination $oldLogPath -Force
    }
  }
  $timestamp = (Get-Date).ToUniversalTime().ToString('o')
  $suffix = if ($Detail) { " $Detail" } else { '' }
  Add-Content -LiteralPath $logPath -Value "$timestamp $Code$suffix"
}

function Start-WslKeepalive {
  $arguments = Get-WslKeepaliveArguments -Distro $WslDistro -User $WslUser -KeepalivePath $WslKeepalivePath
  return Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\wsl.exe') `
    -ArgumentList $arguments -WindowStyle Hidden -PassThru
}

function Stop-WslKeepalive {
  param($Process)
  if ($null -eq $Process -or $Process.HasExited) { return }
  $Process.Kill()
  if (-not $Process.WaitForExit(5000)) {
    throw 'Owned keepalive did not exit within the cleanup budget.'
  }
}

$mutex = New-Object System.Threading.Mutex($false, 'Local\XScienceSourceDevWatchdog')
$mutexAcquired = Enter-SupervisorMutex $mutex
if (-not $mutexAcquired) {
  $mutex.Dispose()
  exit 0
}

$keepaliveProcess = $null
$exitCode = 1
$stage = 'start'
try {
  $keepaliveProcess = Start-WslKeepalive
  if ($null -eq $keepaliveProcess) { throw 'Keepalive did not return a process.' }
  # Retain the owned process handle so a quick exit still has a readable code.
  $null = $keepaliveProcess.Handle
  Write-SupervisorLog 'wsl_keepalive_started' "pid=$($keepaliveProcess.Id)"
  $stage = 'wait'
  $keepaliveProcess.WaitForExit()
  if ($null -eq $keepaliveProcess.ExitCode) { throw 'Keepalive exit status is unavailable.' }
  Write-SupervisorLog 'wsl_keepalive_exited' "exit=$($keepaliveProcess.ExitCode)"
  # Even zero is unexpected: the canonical helper should stay alive. Report
  # termination once, without an HTTP-driven restart or another launch loop.
  [Console]::Error.WriteLine('X-Science keepalive exited; inspect its stage/exit log before explicitly starting it again.')
  if ($keepaliveProcess.ExitCode -ne 0) { $exitCode = $keepaliveProcess.ExitCode }
} catch {
  Write-SupervisorLog "wsl_keepalive_${stage}_failed" $_.Exception.GetType().Name
  [Console]::Error.WriteLine('X-Science keepalive failed; inspect its stage/exit log before explicitly starting it again.')
} finally {
  # Best-effort normal unwind only; abrupt parent termination requires its own
  # owned-child settlement contract and is not solved by this finally block.
  try { Stop-WslKeepalive $keepaliveProcess } finally {
    if ($null -ne $keepaliveProcess) { $keepaliveProcess.Dispose() }
    $mutex.ReleaseMutex()
    $mutex.Dispose()
  }
}
exit $exitCode
