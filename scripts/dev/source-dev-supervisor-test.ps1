[CmdletBinding()]
param([ValidateSet('All', 'SingleAuthority')][string]$Case = 'All')
$ErrorActionPreference = 'Stop'
$supervisor = Join-Path $PSScriptRoot 'source-dev-supervisor.ps1'

function Assert-Equal {
  param($Expected, $Actual, [string]$Message)
  if ($Expected -ne $Actual) { throw "$Message (expected=$Expected actual=$Actual)" }
}

# Execute the actual script entry. Only its OS boundaries are replaced: no WSL,
# HTTP request, service restart, or Scheduled Task operation may leave this test.
function New-HelperFixture {
  param([int]$ExitCode)
  $process = [pscustomobject]@{ Id = 1; HasExited = $false; ExitCode = $ExitCode }
  $process | Add-Member ScriptMethod WaitForExit { $this.HasExited = $true }
  $process | Add-Member ScriptMethod Kill { $this.HasExited = $true }
  $process | Add-Member ScriptMethod Dispose {}
  return $process
}

function Start-Process {
  param($FilePath, $ArgumentList, $WindowStyle, [switch]$PassThru)
  $fixture = $global:sourceSupervisorFixture
  $fixture.Starts++
  $fixture.Arguments = @($ArgumentList)
  Assert-Equal 'Hidden' $WindowStyle 'helper must launch hidden'
  Assert-Equal $true $PassThru.IsPresent 'launcher must retain its process object'
  Assert-Equal 'wsl.exe' ([IO.Path]::GetFileName($FilePath)) 'only canonical helper entry is permitted'
  if ($fixture.Mode -eq 'start-error') { throw [InvalidOperationException]::new('controlled start failure') }
  if ($fixture.Mode -eq 'real-exit') {
    $fixture.Process = Microsoft.PowerShell.Management\Start-Process -FilePath (Join-Path $PSHOME 'powershell.exe') `
      -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', 'exit 17') -WindowStyle Hidden -PassThru
    # Keep the actual owned handle before the short process can exit.
    $null = $fixture.Process.Handle
  }
  return $fixture.Process
}

function Invoke-WebRequest {
  param($Uri, [switch]$UseBasicParsing, $TimeoutSec)
  $global:sourceSupervisorFixture.Probes++
  throw 'controlled unhealthy probe; no network request was sent'
}

function Start-Sleep {
  param($Seconds)
  $global:sourceSupervisorFixture.RetrySleeps++
  throw 'fixture stopped an obsolete polling/retry loop'
}

function Stop-Process {
  param($Id, [switch]$Force)
  Assert-Equal $global:sourceSupervisorFixture.Process.Id $Id 'only owned fixture may be stopped'
  $global:sourceSupervisorFixture.Process.Kill()
}

function New-Object {
  param([string]$TypeName, [object[]]$ArgumentList)
  if ($TypeName -ne 'System.Threading.Mutex') { throw "Unexpected constructor: $TypeName" }
  $mutex = [pscustomobject]@{}
  $mutex | Add-Member ScriptMethod WaitOne {
    param($Timeout, $ExitContext)
    return -not $global:sourceSupervisorFixture.MutexBusy
  }
  $mutex | Add-Member ScriptMethod ReleaseMutex { $global:sourceSupervisorFixture.Releases++ }
  $mutex | Add-Member ScriptMethod Dispose {}
  return $mutex
}

function Invoke-SupervisorFixture {
  param([string]$Mode = 'zero-exit', [int]$ExitCode = 0, [switch]$MutexBusy)
  $global:sourceSupervisorFixture = [pscustomobject]@{
    Mode = $Mode; Process = (New-HelperFixture $ExitCode); Starts = 0; Probes = 0
    RetrySleeps = 0; Releases = 0; MutexBusy = $MutexBusy.IsPresent; Arguments = @()
  }
  $savedLocalAppData = $env:LOCALAPPDATA
  $fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('x-science-supervisor-' + [guid]::NewGuid())
  $null = New-Item -ItemType Directory -Path $fixtureRoot
  try {
    $env:LOCALAPPDATA = $fixtureRoot
    $failure = $null
    $global:LASTEXITCODE = 0
    try { $null = & $supervisor -WslDistro 'FixtureUbuntu' -WslUser 'fixture_user' -WslKeepalivePath '/fixture/keepalive.sh' }
    catch { $failure = $_.Exception }
    $result = [pscustomobject]@{
      ExitCode = $LASTEXITCODE; Error = $failure
      Starts = $global:sourceSupervisorFixture.Starts; Probes = $global:sourceSupervisorFixture.Probes
      RetrySleeps = $global:sourceSupervisorFixture.RetrySleeps; Releases = $global:sourceSupervisorFixture.Releases
      Arguments = $global:sourceSupervisorFixture.Arguments
      Log = (Get-Content -Raw -LiteralPath (Join-Path $fixtureRoot 'XScience/source-dev/watchdog.log') -ErrorAction SilentlyContinue)
    }
    return $result
  } finally {
    $env:LOCALAPPDATA = $savedLocalAppData
    $process = $global:sourceSupervisorFixture.Process
    if ($process -is [Diagnostics.Process]) {
      if (-not $process.HasExited) { $process.Kill(); [void]$process.WaitForExit(5000) }
      $process.Dispose()
    }
    # Delete only this freshly created, exact test-owned directory.
    Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
    Remove-Variable sourceSupervisorFixture -Scope Global
  }
}

$result = Invoke-SupervisorFixture
Assert-Equal 0 $result.Probes 'source launcher must never authorize service restart from HTTP health'
Assert-Equal 0 $result.RetrySleeps 'source launcher must not retry an exited helper'
Assert-Equal 1 $result.Starts 'one launcher invocation starts one helper'
Assert-Equal 1 $result.ExitCode 'unexpected helper exit zero is not a live-service success'
Assert-Equal $null $result.Error 'helper failure must have a controlled exit status'
Assert-Equal 1 $result.Releases 'owned mutex must be released after helper exit'
Assert-Equal $true ($result.Log -match 'wsl_keepalive_exited.*exit=0') 'helper exit must be observable'
'PASS: single authority and unexpected zero exit'
if ($Case -eq 'SingleAuthority') { return }

$result = Invoke-SupervisorFixture -Mode 'start-error'
Assert-Equal 1 $result.Starts 'failed launch must not be retried'
Assert-Equal 1 $result.ExitCode 'failed launch must return failure'
Assert-Equal 1 $result.Releases 'failed launch must release its mutex'
Assert-Equal $true ($result.Log -match 'wsl_keepalive_start_failed') 'failed start must be observable'
Assert-Equal $false ($result.Log -match 'controlled start failure|/fixture/keepalive.sh') 'logs contain only safe stage and exception type'
'PASS: failed helper start'

$result = Invoke-SupervisorFixture -Mode 'real-exit'
Assert-Equal 17 $result.ExitCode 'actual owned child exit status must survive the real wait'
Assert-Equal 1 $result.Starts 'actual failed child is not restarted'
Assert-Equal 0 $result.Probes 'actual child exit does not authorize health probes'
Assert-Equal $true ($result.Log -match 'wsl_keepalive_exited.*exit=17') 'actual exit is logged'
'PASS: actual short-lived owned PowerShell child'

$result = Invoke-SupervisorFixture -MutexBusy
Assert-Equal 0 $result.Starts 'a competing launcher must not create another helper'
Assert-Equal 0 $result.Releases 'a competing launcher must not release another owner mutex'
Assert-Equal 0 $result.ExitCode 'existing owner is not an installation failure'
'PASS: mutex ownership'

. $supervisor -TestMode
$arguments = @(Get-WslKeepaliveArguments -Distro 'FixtureUbuntu' -User 'fixture_user' -KeepalivePath '/fixture/keepalive.sh')
Assert-Equal '-d|FixtureUbuntu|-u|root|--exec|/usr/bin/env|X_SCIENCE_DEV_WSL_USER=fixture_user|/fixture/keepalive.sh' `
  ($arguments -join '|') 'closed argv binds distro/user/helper without a shell string'
$rejected = $false
try { $null = Get-WslKeepaliveArguments -Distro Ubuntu -User fixture_user -KeepalivePath relative }
catch { $rejected = $true }
Assert-Equal $true $rejected 'relative helper path must fail closed'
$abandoned = [pscustomobject]@{}
$abandoned | Add-Member ScriptMethod WaitOne { throw [Threading.AbandonedMutexException]::new() }
Assert-Equal $true (Enter-SupervisorMutex $abandoned) 'abandoned mutex grants ownership'
'PASS: explicit binding and abandoned mutex'

$installer = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'install-source-dev-supervisor.ps1')
Assert-Equal $false ($installer -match '-RestartCount|-RestartInterval') 'installer must not add a second retry loop'
Assert-Equal $true ($installer.Contains('-AtLogOn')) 'explicit logon entry stays supported'
Assert-Equal $true ($installer.Contains('if ($Start)')) 'installation does not implicitly start the task'
$source = Get-Content -Raw -LiteralPath $supervisor
Assert-Equal $false ($source -match 'Restart-SourceServices|Invoke-WebRequest|Update-SupervisorState') 'retired restart authority must be deleted'
'PASS: installer and retired authority contract'
'source-dev-supervisor-test: ok (6 focused contracts; no WSL, HTTP, service, or task operation)'
