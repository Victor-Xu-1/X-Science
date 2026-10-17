[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$source = Join-Path $PSScriptRoot 'source-dev-supervisor.ps1'
. (Join-Path $PSScriptRoot 'source-dev-owned-stdin.ps1')
$tempParent = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).ProviderPath.TrimEnd('\')
$fixture = New-Item -ItemType Directory -Path (Join-Path $tempParent ('x-science-owner fixture-' + [guid]::NewGuid()))
$fixturePath = $fixture.FullName
$createdAt = $fixture.CreationTimeUtc.Ticks
$token = [guid]::NewGuid().ToString('N')
$childPath = Join-Path $fixturePath 'child.ps1'
$ownerPath = Join-Path $fixturePath 'owner.ps1'
$markerPath = Join-Path $fixturePath 'child.json'
$owner = $null
$child = $null
$childVerified = $false

# The dummy models the old sleep-only holder until the real launcher establishes
# its ownership protocol. Neither fixture starts WSL, services or Scheduled Tasks.
$childScript = @'
param([string]$Marker, [string]$Token)
$ErrorActionPreference = 'Stop'
$self = [Diagnostics.Process]::GetCurrentProcess()
$record = @{ pid=$PID; ticks=$self.StartTime.ToUniversalTime().Ticks; token=$Token }
[IO.File]::WriteAllText(($Marker + '.tmp'), ($record | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
[IO.File]::Move(($Marker + '.tmp'), $Marker)
$start = [Console]::ReadLine()
if ($start -ceq 'XSCIENCE_KEEPALIVE_V1_START') {
  while ($null -ne [Console]::ReadLine()) {}
  [IO.File]::WriteAllText(($Marker + '.eof'), $Token)
  exit 0
}
# Explicit legacy control: old source has no parent-owned input channel.
while ($true) { [Threading.Thread]::Sleep(100) }
'@
$ownerScript = @'
param([string]$Source, [string]$ChildPath, [string]$Marker, [string]$Token)
$ErrorActionPreference = 'Stop'
. $Source -TestMode
function Get-WslKeepaliveArguments {
  param($Distro, $User, $KeepalivePath)
  @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $ChildPath,
    '-Marker', $Marker, '-Token', $Token)
}
function Join-Path {
  param([string]$Path, [string]$ChildPath)
  if ($Path -ceq $env:SystemRoot -and $ChildPath -ceq 'System32\wsl.exe') {
    return [IO.Path]::Combine($PSHOME, 'powershell.exe')
  }
  Microsoft.PowerShell.Management\Join-Path -Path $Path -ChildPath $ChildPath
}
$process = Start-WslKeepalive
$null = $process.Handle
[IO.File]::WriteAllText(($Marker + '.owner.tmp'), $Token)
[IO.File]::Move(($Marker + '.owner.tmp'), ($Marker + '.owner'))
$process.WaitForExit()
'@
[IO.File]::WriteAllText($childPath, $childScript, [Text.UTF8Encoding]::new($false))
[IO.File]::WriteAllText($ownerPath, $ownerScript, [Text.UTF8Encoding]::new($false))

try {
  $ownerArguments = ConvertTo-OwnedProcessArguments @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
    '-File', $ownerPath, '-Source', $source, '-ChildPath', $childPath,
    '-Marker', $markerPath, '-Token', $token)
  $owner = Start-Process -FilePath (Join-Path $PSHOME 'powershell.exe') -WindowStyle Hidden -PassThru `
    -ArgumentList $ownerArguments
  $null = $owner.Handle
  $deadline = [DateTime]::UtcNow.AddSeconds(10)
  while (-not ((Test-Path -LiteralPath $markerPath) -and (Test-Path -LiteralPath ($markerPath + '.owner')))) {
    if ($owner.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw 'Owned fixture did not become ready.' }
    [Threading.Thread]::Sleep(25)
  }
  $record = Get-Content -LiteralPath $markerPath -Raw | ConvertFrom-Json
  if ($record.token -cne $token -or (Get-Content -LiteralPath ($markerPath + '.owner') -Raw) -cne $token) {
    throw 'Fixture identity marker changed; unknown child preserved.'
  }
  $child = [Diagnostics.Process]::GetProcessById([int]$record.pid)
  $null = $child.Handle
  $metadata = Get-CimInstance Win32_Process -Filter ('ProcessId=' + $child.Id)
  if ($child.StartTime.ToUniversalTime().Ticks -ne $record.ticks -or
      $child.StartTime.ToUniversalTime() -lt $owner.StartTime.ToUniversalTime() -or
      $metadata.ParentProcessId -ne $owner.Id -or
      -not $metadata.CommandLine.Contains($childPath) -or -not $metadata.CommandLine.Contains($token)) {
    throw 'Fixture child identity changed; unknown process preserved.'
  }
  $childVerified = $true
  # Kill the retained owner directly: its finally cannot perform cleanup.
  $owner.Kill()
  if (-not $owner.WaitForExit(5000)) { throw 'Owned fixture parent did not stop.' }
  if (-not $child.WaitForExit(5000)) { throw 'Owned keepalive child survived abrupt owner exit.' }
  if ($child.ExitCode -ne 0 -or -not (Test-Path -LiteralPath ($markerPath + '.eof')) -or
      (Get-Content -LiteralPath ($markerPath + '.eof') -Raw) -cne $token) {
    throw 'Child exit did not prove ownership-channel EOF.'
  }
  'PASS: actual source launcher child exits on owner death without finally; no WSL/services/tasks used'
} finally {
  if ($null -ne $owner) {
    if (-not $owner.HasExited) { $owner.Kill(); [void]$owner.WaitForExit(5000) }
    $owner.Dispose()
  }
  if ($null -ne $child) {
    if ($childVerified -and -not $child.HasExited) { $child.Kill(); [void]$child.WaitForExit(5000) }
    $child.Dispose()
  }
  $current = Get-Item -LiteralPath $fixturePath -Force
  $resolved = (Resolve-Path -LiteralPath $fixturePath).ProviderPath
  if (-not $current.PSIsContainer -or $current.CreationTimeUtc.Ticks -ne $createdAt -or
      ($current.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
      $current.FullName -cne $fixturePath -or $resolved -cne $fixturePath -or
      [IO.Path]::GetDirectoryName($resolved) -cne $tempParent) {
    throw 'Replaced fixture directory retained.'
  }
  Remove-Item -LiteralPath $resolved -Recurse -Force
}
