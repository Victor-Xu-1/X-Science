# One parent-owned input writer is the lifetime of a source keepalive child.
# This is not a service restart authority or a deadline for scientific tasks.
function ConvertTo-OwnedProcessArguments {
  param([string[]]$ArgumentList)
  $encoded = foreach ($argument in $ArgumentList) {
    if ($argument.IndexOf([char]0) -ge 0) { throw 'Process arguments cannot contain NUL.' }
    if ($argument.Length -gt 0 -and $argument -notmatch '[\s"]') { $argument; continue }
    $quoted = [regex]::Replace($argument, '(\\*)"', '${1}${1}\"')
    $quoted = [regex]::Replace($quoted, '(\\+)$', '${1}${1}')
    '"' + $quoted + '"'
  }
  return $encoded -join ' '
}

function Stop-OwnedStdinProcess {
  param($Process)
  if ($null -eq $Process -or $Process.HasExited) { return }
  # Covers the Linux bootstrap's30s +5s termination grace and forwarding.
  # The healthy holding phase has no total-duration or inactivity deadline.
  $closeFailure = $null
  try { $Process.StandardInput.Close() } catch { $closeFailure = $_.Exception }
  if ($Process.WaitForExit(40000)) {
    if ($null -ne $closeFailure) { throw $closeFailure }
    return
  }
  # Retained identity only. Windows exit is not proof of Linux-side settlement.
  $Process.Kill()
  if (-not $Process.WaitForExit(5000)) {
    throw 'Owned keepalive client did not exit within its cleanup budget.'
  }
  throw 'Owned keepalive client required forced cleanup; Linux settlement is unverified.'
}

function Start-OwnedStdinProcess {
  param([Parameter(Mandatory)][string]$FilePath, [string[]]$ArgumentList)
  if (-not [IO.Path]::IsPathRooted($FilePath) -or $FilePath.IndexOf([char]0) -ge 0) {
    throw 'Owned process executable must be an absolute path.'
  }
  $process = New-Object System.Diagnostics.Process
  $process.StartInfo = [Diagnostics.ProcessStartInfo]::new()
  $process.StartInfo.FileName = $FilePath
  $process.StartInfo.Arguments = ConvertTo-OwnedProcessArguments $ArgumentList
  $process.StartInfo.UseShellExecute = $false
  $process.StartInfo.CreateNoWindow = $true
  $process.StartInfo.RedirectStandardInput = $true
  $started = $false
  try {
    if (-not $process.Start()) { throw 'Owned keepalive client did not start.' }
    $started = $true
    $null = $process.Handle
    # Write bytes directly: PS5.1 StreamWriter defaults may add BOM or CRLF.
    $start = [Text.UTF8Encoding]::new($false).GetBytes("XSCIENCE_KEEPALIVE_V1_START`n")
    $process.StandardInput.BaseStream.Write($start, 0, $start.Length)
    $process.StandardInput.BaseStream.Flush()
    return $process
  } catch {
    $startFailure = $_.Exception
    try {
      if ($started) { Stop-OwnedStdinProcess $process }
    } finally { $process.Dispose() }
    throw $startFailure
  }
}
