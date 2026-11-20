# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dump-hook.ps1 RUN_DIR LABEL [ARG...]
#
# The PowerShell twin of dump-hook.sh, for Windows machines without Git Bash.
# It writes the same files in the same format: each call claims the next
# number under RUN_DIR\hooks\ and writes time, argv, env (names only, never
# values), cwd, parents (pid and executable name, never command lines) and
# stdin (the bytes, unchanged), plus stderr if something here failed.
#
# Register it in exec form, so no shell sits in between:
#
#   powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <kit>\dump-hook.ps1 <run-dir> <label>
#
# It prints nothing and always exits 0: harnesses feed a hook's stdout to the
# model on some events, and a non-zero exit can block a prompt or a tool call.
# Nothing is sent anywhere. Runs on Windows PowerShell 5.1 and PowerShell 7.
#
# The arrival time is stamped once PowerShell has started, which on an old
# laptop can be most of a second after the harness ran the hook. The order of
# the numbered calls is still the arrival order.

$ticks = [DateTime]::UtcNow.Ticks
$argv = @($args)
$utf8 = New-Object System.Text.UTF8Encoding -ArgumentList $false
$inv = [Globalization.CultureInfo]::InvariantCulture

function Write-Lines([string]$path, [string[]]$lines) {
    $text = New-Object System.Text.StringBuilder
    foreach ($line in $lines) {
        [void]$text.Append($line).Append("`n")
    }
    [System.IO.File]::WriteAllText($path, $text.ToString(), $utf8)
}

$call = $null

function Write-Problem([string]$what) {
    if ($null -ne $call) {
        [System.IO.File]::AppendAllText((Join-Path $call 'stderr'), "$what`n", $utf8)
    }
}

try {
    if ($argv.Count -lt 2 -or [string]::IsNullOrEmpty($argv[0])) {
        exit 0
    }
    $hooks = Join-Path $argv[0] 'hooks'
    [void][System.IO.Directory]::CreateDirectory($hooks)

    $unixNs = ([long]$ticks - [long]621355968000000000) * [long]100
    $utc = (New-Object DateTime -ArgumentList $ticks, ([DateTimeKind]::Utc)).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $inv)
    $timeText = 'unix_ns=' + $unixNs.ToString($inv) + "`nutc=$utc`nclock=dotnet-utcnow`n"
    $timeBytes = $utf8.GetBytes($timeText)

    # Claim the next number. Opening time with CreateNew is atomic, so two
    # hooks that fire at the same moment never share a directory.
    $n = @([System.IO.Directory]::GetFileSystemEntries($hooks)).Count
    while ($true) {
        $n++
        if ($n -gt 999999) {
            exit 0
        }
        $dir = Join-Path $hooks ('{0:D6}' -f $n)
        [void][System.IO.Directory]::CreateDirectory($dir)
        try {
            $f = [System.IO.File]::Open((Join-Path $dir 'time'), [System.IO.FileMode]::CreateNew)
        } catch {
            continue
        }
        try {
            $f.Write($timeBytes, 0, $timeBytes.Length)
        } finally {
            $f.Dispose()
        }
        $call = $dir
        break
    }
} catch {
    exit 0
}

try {
    Write-Lines (Join-Path $call 'argv') ([string[]]$argv)
} catch {
    Write-Problem "argv: $($_.Exception.Message)"
}

try {
    $names = New-Object System.Collections.Generic.List[string]
    foreach ($key in [Environment]::GetEnvironmentVariables().Keys) {
        $names.Add([string]$key)
    }
    $sorted = $names.ToArray()
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    Write-Lines (Join-Path $call 'env') $sorted
} catch {
    Write-Problem "env: $($_.Exception.Message)"
}

try {
    Write-Lines (Join-Path $call 'cwd') @([Environment]::CurrentDirectory)
} catch {
    Write-Problem "cwd: $($_.Exception.Message)"
}

try {
    $chain = New-Object System.Collections.Generic.List[string]
    if ($PSVersionTable.PSVersion.Major -lt 6 -or $IsWindows) {
        # Windows: one CIM query, then walk it. Name is the executable's
        # file name; CommandLine is never read.
        $byId = @{}
        foreach ($p in Get-CimInstance -ClassName Win32_Process -Property ProcessId, ParentProcessId, Name) {
            $byId[[int]$p.ProcessId] = $p
        }
        $id = [int]$byId[[int]$PID].ParentProcessId
        for ($i = 0; $i -lt 12 -and $id -gt 0; $i++) {
            $p = $byId[$id]
            if ($null -eq $p) {
                $chain.Add("$id ?")
                break
            }
            $chain.Add("$id $($p.Name)")
            $next = [int]$p.ParentProcessId
            if ($next -eq $id) {
                break
            }
            $id = $next
        }
    } else {
        # PowerShell 7 on macOS or Linux.
        $p = (Get-Process -Id $PID).Parent
        for ($i = 0; $i -lt 12 -and $null -ne $p; $i++) {
            $chain.Add("$($p.Id) $($p.ProcessName)")
            $p = $p.Parent
        }
    }
    Write-Lines (Join-Path $call 'parents') $chain.ToArray()
} catch {
    Write-Problem "parents: $($_.Exception.Message)"
}

# Last, because it can block until the harness closes the pipe. Raw bytes,
# so the console code page can't change a character.
try {
    $stdinPath = Join-Path $call 'stdin'
    if ([Console]::IsInputRedirected) {
        $in = [Console]::OpenStandardInput()
        $out = [System.IO.File]::Create($stdinPath)
        try {
            $in.CopyTo($out)
        } finally {
            $out.Dispose()
        }
    } else {
        [System.IO.File]::WriteAllBytes($stdinPath, [byte[]]@())
    }
} catch {
    Write-Problem "stdin: $($_.Exception.Message)"
}

exit 0
