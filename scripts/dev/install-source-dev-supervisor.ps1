[CmdletBinding()]
param(
  [switch]$Start,
  [ValidatePattern('^[A-Za-z0-9_.-]+$')]
  [string]$WslDistro = 'Ubuntu',
  [ValidatePattern('^[A-Za-z0-9_.-]+$')]
  [string]$WslUser = 'victor_1',
  [ValidatePattern('^/[A-Za-z0-9_./-]+$')]
  [string]$WslKeepalivePath = '/home/victor_1/.x-science-v0.1.0/scripts/dev/source-wsl-keepalive.sh'
)

$ErrorActionPreference = 'Stop'
$taskName = 'X-Science Source Dev Supervisor'
$sourceScript = Join-Path $PSScriptRoot 'source-dev-supervisor.ps1'
$sourceLifetime = Join-Path $PSScriptRoot 'source-dev-owned-stdin.ps1'
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'XScience\source-dev'
$runtimeScript = Join-Path $runtimeRoot 'watchdog.ps1'
$runtimeLifetime = Join-Path $runtimeRoot 'source-dev-owned-stdin.ps1'

if (-not (Test-Path -LiteralPath $sourceScript)) {
  throw "Missing canonical supervisor: $sourceScript"
}
if (-not (Test-Path -LiteralPath $sourceLifetime)) {
  throw "Missing canonical lifetime helper: $sourceLifetime"
}

New-Item -ItemType Directory -Path $runtimeRoot -Force | Out-Null
Copy-Item -LiteralPath $sourceLifetime -Destination $runtimeLifetime -Force
Copy-Item -LiteralPath $sourceScript -Destination $runtimeScript -Force

$sourceHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $sourceScript).Hash
$runtimeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $runtimeScript).Hash
$sourceLifetimeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $sourceLifetime).Hash
$runtimeLifetimeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $runtimeLifetime).Hash
if ($sourceHash -ne $runtimeHash -or $sourceLifetimeHash -ne $runtimeLifetimeHash) {
  throw 'Installed supervisor and lifetime helper do not match canonical source.'
}

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument `
  "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$runtimeScript`" -WslDistro `"$WslDistro`" -WslUser `"$WslUser`" -WslKeepalivePath `"$WslKeepalivePath`""
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries `
  -DontStopIfGoingOnBatteries -StartWhenAvailable `
  -MultipleInstances IgnoreNew -ExecutionTimeLimit ([timespan]::Zero)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" `
  -LogonType Interactive -RunLevel Limited

Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger `
  -Settings $settings -Principal $principal -Force | Out-Null

if ($Start) {
  Start-ScheduledTask -TaskName $taskName
}

[pscustomobject]@{
  TaskName = $taskName
  RuntimeScript = $runtimeScript
  SHA256 = $runtimeHash
  LifetimeScript = $runtimeLifetime
  LifetimeSHA256 = $runtimeLifetimeHash
  WslDistro = $WslDistro
  WslUser = $WslUser
  WslKeepalivePath = $WslKeepalivePath
  Started = [bool]$Start
}
