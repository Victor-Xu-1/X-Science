[CmdletBinding()]
param([ValidateSet('All', 'SingleAuthority')][string]$Case = 'All')
$ErrorActionPreference = 'Stop'
$supervisor = Join-Path $PSScriptRoot 'source-dev-supervisor.ps1'
. (Join-Path $PSScriptRoot 'source-dev-owned-stdin.ps1')

# Real owned process/pipe, before installing unit OS-boundary fixtures.
$native = Start-OwnedStdinProcess -FilePath (Join-Path $PSHOME 'powershell.exe') `
  -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', '$null = [Console]::ReadLine(); exit 17')
try {
  if (-not $native.WaitForExit(5000) -or $native.ExitCode -ne 17) {
    throw 'Actual owned child exit code did not survive its retained handle.'
  }
} finally { Stop-OwnedStdinProcess $native; $native.Dispose() }
'PASS: actual short-lived owned PowerShell child and start protocol'

function Assert-Equal {
  param($Expected, $Actual, [string]$Message)
  if ($Expected -ne $Actual) { throw "$Message (expected=$Expected actual=$Actual)" }
}

Assert-Equal 'plain' (ConvertTo-OwnedProcessArguments @('plain')) 'plain argument stays literal'
Assert-Equal '"two words" ""' (ConvertTo-OwnedProcessArguments @('two words', '')) 'spaces and empty arguments retain boundaries'
Assert-Equal '"a\"b"' (ConvertTo-OwnedProcessArguments @('a"b')) 'embedded quote remains a literal quote'
Assert-Equal '"two words\\"' (ConvertTo-OwnedProcessArguments @('two words\')) 'quoted trailing slash is doubled'

# Execute the actual script entry. Only its OS boundaries are replaced: no WSL,
# HTTP request, service restart, or Scheduled Task operation may leave this test.
function New-HelperFixture {
  param([int]$ExitCode)
  $inputBuffer = [IO.MemoryStream]::new()
  $input = [pscustomobject]@{ BaseStream = $inputBuffer }
  $input | Add-Member ScriptMethod Close {
    $global:sourceSupervisorFixture.InputCloses++
    $global:sourceSupervisorFixture.Process.HasExited = $true
  }
  $process = [pscustomobject]@{
    Id = 1; Handle = 1; HasExited = $false; ExitCode = $ExitCode
    StartInfo = $null; StandardInput = $input
  }
  $process | Add-Member ScriptMethod Start {
    $fixture = $global:sourceSupervisorFixture
    $fixture.Starts++
    $fixture.Arguments = @($this.StartInfo.Arguments -split ' ')
    Assert-Equal $false $this.StartInfo.UseShellExecute 'owned input cannot use shell execution'
    Assert-Equal $true $this.StartInfo.CreateNoWindow 'helper must launch hidden'
    Assert-Equal $true $this.StartInfo.RedirectStandardInput 'writer must be owned before process start'
    Assert-Equal 'wsl.exe' ([IO.Path]::GetFileName($this.StartInfo.FileName)) 'only canonical helper entry is permitted'
    if ($fixture.Mode -eq 'start-error') { throw [InvalidOperationException]::new('controlled start failure') }
    return $true
  }
  $process | Add-Member ScriptMethod WaitForExit {
    param([int]$Milliseconds)
    if ($global:sourceSupervisorFixture.Mode -eq 'wait-error' -and $Milliseconds -eq 0) {
      throw [InvalidOperationException]::new('controlled wait failure')
    }
    $this.HasExited = $true
    if ($Milliseconds -gt 0) {
      Assert-Equal 40000 $Milliseconds 'normal unwind covers bounded bootstrap and forwarding'
      $global:sourceSupervisorFixture.CleanupWaits++
      return $true
    }
  }
  $process | Add-Member ScriptMethod Kill { $global:sourceSupervisorFixture.Kills++; $this.HasExited = $true }
  $process | Add-Member ScriptMethod Dispose {}
  return $process
}

function Start-Process {
  param($FilePath, $ArgumentList, $WindowStyle, [switch]$PassThru)
  throw 'Unowned Start-Process path must not be used by the source supervisor.'
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
  $global:sourceSupervisorFixture.PIDReacquires++
  throw 'cleanup must use the retained process object, never reacquire a PID'
}

function New-Object {
  param([string]$TypeName, [object[]]$ArgumentList)
  if ($TypeName -eq 'System.Diagnostics.Process') { return $global:sourceSupervisorFixture.Process }
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
    Kills = 0; CleanupWaits = 0; PIDReacquires = 0; InputCloses = 0
  }
  $savedLocalAppData = $env:LOCALAPPDATA
  $expectedTempRoot = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).ProviderPath.TrimEnd('\')
  $fixtureRoot = Join-Path $expectedTempRoot ('x-science-supervisor-' + [guid]::NewGuid())
  $createdDirectory = New-Item -ItemType Directory -Path $fixtureRoot
  $createdPath = $createdDirectory.FullName
  $createdAt = $createdDirectory.CreationTimeUtc.Ticks
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
      Kills = $global:sourceSupervisorFixture.Kills; CleanupWaits = $global:sourceSupervisorFixture.CleanupWaits
      PIDReacquires = $global:sourceSupervisorFixture.PIDReacquires
      InputCloses = $global:sourceSupervisorFixture.InputCloses
      StartBytes = [Text.UTF8Encoding]::new($false).GetString($global:sourceSupervisorFixture.Process.StandardInput.BaseStream.ToArray())
      Arguments = $global:sourceSupervisorFixture.Arguments
      Log = (Get-Content -Raw -LiteralPath (Join-Path $fixtureRoot 'XScience/source-dev/watchdog.log') -ErrorAction SilentlyContinue)
    }
    return $result
  } finally {
    $env:LOCALAPPDATA = $savedLocalAppData
    $global:sourceSupervisorFixture.Process.StandardInput.BaseStream.Dispose()
    # Resolve again before recursive deletion; retain any replaced or redirected
    # target rather than following it outside the freshly created fixture.
    $currentDirectory = Get-Item -LiteralPath $createdPath -Force
    $resolvedPath = (Resolve-Path -LiteralPath $createdPath).ProviderPath
    if (-not $currentDirectory.PSIsContainer -or
        ($currentDirectory.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $currentDirectory.CreationTimeUtc.Ticks -ne $createdAt -or
        $currentDirectory.FullName -cne $createdPath -or $resolvedPath -cne $createdPath -or
        [IO.Path]::GetDirectoryName($resolvedPath) -cne $expectedTempRoot) {
      throw 'Unexpected fixture cleanup identity; directory retained.'
    }
    Remove-Item -LiteralPath $resolvedPath -Recurse -Force
    Remove-Variable sourceSupervisorFixture -Scope Global
  }
}

$result = Invoke-SupervisorFixture
Assert-Equal 0 $result.Probes 'source launcher must never authorize service restart from HTTP health'
Assert-Equal 0 $result.RetrySleeps 'source launcher must not retry an exited helper'
Assert-Equal 1 $result.Starts 'one launcher invocation starts one helper'
Assert-Equal "XSCIENCE_KEEPALIVE_V1_START`n" $result.StartBytes 'protocol is exact UTF8 without BOM and uses LF'
Assert-Equal '-d|FixtureUbuntu|-u|root|--exec|/usr/bin/env|X_SCIENCE_DEV_WSL_USER=fixture_user|/fixture/keepalive.sh' `
  ($result.Arguments -join '|') 'actual launch must retain the explicit helper binding'
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

$result = Invoke-SupervisorFixture -Mode 'wait-error'
Assert-Equal 1 $result.ExitCode 'failed wait must return failure'
Assert-Equal 1 $result.Starts 'failed wait must not relaunch its helper'
Assert-Equal 0 $result.Kills 'normal unwind must prefer EOF, not kill a healthy owned holder'
Assert-Equal 1 $result.InputCloses 'normal unwind closes the sole parent writer'
Assert-Equal 1 $result.CleanupWaits 'normal unwind must join its owned process with a bounded wait'
Assert-Equal 0 $result.PIDReacquires 'normal unwind must never reacquire a process by PID'
Assert-Equal 1 $result.Releases 'failed wait must release the owned mutex'
Assert-Equal $null $result.Error 'successful cleanup must preserve the controlled failure exit'
Assert-Equal $true ($result.Log -match 'wsl_keepalive_wait_failed') 'failed wait must be observable'
'PASS: retained-object normal unwind after wait failure'

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
Assert-Equal $true ($installer.Contains('source-dev-owned-stdin.ps1')) 'installer retains the shared lifetime implementation'
Assert-Equal $true ($installer.IndexOf('Installed supervisor and lifetime helper') -lt $installer.IndexOf('Register-ScheduledTask')) `
  'both installed source hashes are verified before task registration'
$source = Get-Content -Raw -LiteralPath $supervisor
Assert-Equal $false ($source -match 'Restart-SourceServices|Invoke-WebRequest|Update-SupervisorState') 'retired restart authority must be deleted'
'PASS: installer and retired authority contract'
'source-dev-supervisor-test: ok (7 focused contracts; no WSL, HTTP, service, or task operation)'
