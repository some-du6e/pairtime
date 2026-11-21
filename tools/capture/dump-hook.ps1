# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dump-hook.ps1 RUN_DIR LABEL [ARG...]
#
# The PowerShell twin of dump-hook.sh, for Windows machines without Git Bash.
# It writes the same files in the same format: each call claims the next
# number under RUN_DIR\hooks\ and writes time, argv, env (names only, never
# values), entrypoint (the value of CLAUDE_CODE_ENTRYPOINT when it is short
# and plain, other when it isn't, and no file when it is unset: the only
# value of a variable kept), cwd, parents (pid, terminal and executable
# name, never command lines), stdin (the bytes, unchanged) and, last, flags
# (the harness's executable name and yes or no for a print flag,
# stream-json and --bare among its arguments, never the arguments), plus
# stderr if something here failed. On Windows it also writes console:
# attached or none.
#
# Register it as one command line, with the bare executable name first and
# the paths in double quotes, which Git Bash, PowerShell and cmd all run:
#
#   powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "<kit>\dump-hook.ps1" "<run-dir>" <label>
#
# Never as exec form with powershell.exe as the command: a harness that
# drops "args" would start a bare powershell.exe, which runs the hook's
# payload on stdin as commands.
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

# The executable names internal/platform/appclass.json gives a harness. The
# nearest parent with one of them is the harness flags describes.
$harnesses = @('claude', 'codex', 'cursor-agent', 'copilot', 'gemini', 'devin', 'opencode', 'cline')

# The options codex takes a value with before its subcommand, as in
# codex -c key=value exec or codex -p work exec: the root options in
# codex-rs/cli/src/main.rs, codex-rs/tui/src/cli.rs and codex-rs/utils/cli
# at d64451b. A value joined to its option with = needs no skipping. -c and
# -C differ, so they are compared with case. -i, --image isn't here: it
# takes every argument up to the next one that starts with -.
$codexValues = @('-c', '--config', '--enable', '--disable', '--remote', '--remote-auth-token-env', '-a', '--ask-for-approval', '-m', '--model', '--local-provider', '-p', '--profile', '-s', '--sandbox', '-C', '--cd', '--add-dir')

# Split-CommandLine splits a Windows command line into its arguments the
# way CommandLineToArgvW does: whitespace ends an argument outside double
# quotes, and backslashes count only before a quote. So a quoted argument
# that holds " -p " stays one argument and isn't taken for the flag.
function Split-CommandLine([string]$text) {
    $words = New-Object System.Collections.Generic.List[string]
    $word = New-Object System.Text.StringBuilder
    $inWord = $false
    $quoted = $false
    $slashes = 0
    foreach ($c in $text.ToCharArray()) {
        if ($c -eq [char]'\') {
            $slashes++
            $inWord = $true
            continue
        }
        if ($c -eq [char]'"') {
            # 2n backslashes before a quote are n backslashes, and the quote
            # opens or closes; 2n+1 are n backslashes and a literal quote.
            [void]$word.Append([char]'\', [int][Math]::Floor($slashes / 2))
            if ($slashes % 2 -eq 1) {
                [void]$word.Append($c)
            } else {
                $quoted = -not $quoted
            }
            $slashes = 0
            $inWord = $true
            continue
        }
        if ($slashes -gt 0) {
            [void]$word.Append([char]'\', $slashes)
            $slashes = 0
        }
        if (-not $quoted -and ($c -eq [char]' ' -or $c -eq [char]"`t")) {
            if ($inWord) {
                $words.Add($word.ToString())
                [void]$word.Clear()
                $inWord = $false
            }
            continue
        }
        [void]$word.Append($c)
        $inWord = $true
    }
    if ($slashes -gt 0) {
        [void]$word.Append([char]'\', $slashes)
    }
    if ($inWord) {
        $words.Add($word.ToString())
    }
    return ,$words.ToArray()
}

# Get-Answers returns the print, stream_json and bare lines of flags for a
# harness whose name, as appclass.json lists it, is $key, from its
# arguments, the executable first. With none to read, each answer is ?.
function Get-Answers([string]$key, [string[]]$words) {
    if ($null -eq $words -or $words.Count -eq 0) {
        return "print=?`nstream_json=?`nbare=?`n"
    }
    $print = 'no'
    $stream = 'no'
    $bare = 'no'
    $subcommand = $false
    $value = $false
    $images = $false
    $previous = ''
    for ($i = 1; $i -lt $words.Count; $i++) {
        $a = [string]$words[$i]
        if ($a -ceq '--') {
            break
        }
        if ($key -eq 'codex') {
            # codex has no print flag; its exec subcommand, or e, is the
            # first argument that is neither an option nor the value of one.
            # -i takes every argument after it up to the next one that starts
            # with -, as clap reads its num_args = 1.. (shared_options.rs).
            # Two argument lists read wrong, and no capture step runs codex
            # either way: clap also takes a lone - as an image, and it reads
            # an exec after a prompt, as in codex "hi" exec "fix it", as the
            # subcommand.
            if (-not $subcommand) {
                $operand = -not $a.StartsWith('-', [StringComparison]::Ordinal)
                if ($value) {
                    $value = $false
                } elseif (-not $operand) {
                    $images = ($a -ceq '-i') -or ($a -ceq '--image')
                    $value = (-not $images) -and ($codexValues -ccontains $a)
                } elseif (-not $images) {
                    $subcommand = $true
                    if ($a -ceq 'exec' -or $a -ceq 'e') {
                        $print = 'yes'
                    }
                }
            }
        } elseif ($a -ceq '-p' -or $a -ceq '--print' -or $a -ceq '--prompt' -or $a.StartsWith('--prompt=', [StringComparison]::Ordinal)) {
            $print = 'yes'
        }
        if ($a -ceq '--bare') {
            $bare = 'yes'
        }
        if ($a -ceq '--input-format=stream-json' -or $a -ceq '--output-format=stream-json') {
            $stream = 'yes'
        }
        if ($a -ceq 'stream-json' -and ($previous -ceq '--input-format' -or $previous -ceq '--output-format')) {
            $stream = 'yes'
        }
        $previous = $a
    }
    return "print=$print`nstream_json=$stream`nbare=$bare`n"
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
    # hooks that fire at the same moment never share a directory. A number
    # is taken only when its time file is really there: any other failure
    # ends the call at once, instead of trying (and making a directory for)
    # every number up to 999999 while the harness waits.
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
            if ([System.IO.File]::Exists((Join-Path $dir 'time'))) {
                continue
            }
            exit 0
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

# The one value of a variable kept: CLAUDE_CODE_ENTRYPOINT, the token Claude
# Code gives its hooks, such as cli or sdk-cli (E-cc-transcript-labels). A
# value that isn't 1 to 32 characters of a-z, 0-9, - and _ becomes other,
# and an unset one writes no file. -cnotmatch keeps capitals out, and \z,
# unlike $, doesn't match before a final line break.
try {
    $entrypoint = [Environment]::GetEnvironmentVariable('CLAUDE_CODE_ENTRYPOINT')
    if ($null -ne $entrypoint) {
        if ($entrypoint -cnotmatch '^[a-z0-9_-]{1,32}\z') {
            $entrypoint = 'other'
        }
        Write-Lines (Join-Path $call 'entrypoint') @($entrypoint)
    }
} catch {
    Write-Problem "entrypoint: $($_.Exception.Message)"
}

try {
    Write-Lines (Join-Path $call 'cwd') @([Environment]::CurrentDirectory)
} catch {
    Write-Problem "cwd: $($_.Exception.Message)"
}

$onWindows = $PSVersionTable.PSVersion.Major -lt 6 -or $IsWindows

# parents: pid, controlling terminal and executable name of each parent,
# nearest first, the same lines dump-hook.sh writes. Windows has no
# controlling terminals, so the terminal is ? there, and the console file
# says instead whether this hook has a console, which it shares with the
# harness unless the harness started it without one.
try {
    $chain = New-Object System.Collections.Generic.List[string]
    $byId = @{}
    if ($onWindows) {
        # One CIM query, then walk it. Name is the executable's file name.
        # CommandLine isn't read here: flags, at the end, reads the
        # harness's alone.
        foreach ($p in Get-CimInstance -ClassName Win32_Process -Property ProcessId, ParentProcessId, Name) {
            $byId[[int]$p.ProcessId] = @{ Parent = [int]$p.ParentProcessId; Tty = '?'; Name = [string]$p.Name }
        }
    } else {
        # PowerShell 7 on macOS or Linux: the ps listing dump-hook.sh reads.
        # comm comes last because it can hold spaces.
        foreach ($line in @(& ps -A -o pid= -o ppid= -o tty= -o comm= 2>$null)) {
            $m = [regex]::Match([string]$line, '^\s*(\d+)\s+(\d+)\s+(\S+)\s*(.*)$')
            if ($m.Success) {
                $name = $m.Groups[4].Value
                $byId[[int]$m.Groups[1].Value] = @{ Parent = [int]$m.Groups[2].Value; Tty = $m.Groups[3].Value
                    Name = $name.Substring($name.LastIndexOf('/') + 1) }
            }
        }
    }
    $id = 0
    if ($byId.ContainsKey([int]$PID)) {
        $id = $byId[[int]$PID].Parent
    }
    for ($i = 0; $i -lt 12 -and $id -gt 0; $i++) {
        $p = $byId[$id]
        if ($null -eq $p) {
            $chain.Add("$id ? ?")
            break
        }
        $chain.Add("$id $($p.Tty) $($p.Name)")
        if ($p.Parent -eq $id) {
            break
        }
        $id = $p.Parent
    }
    Write-Lines (Join-Path $call 'parents') $chain.ToArray()
} catch {
    Write-Problem "parents: $($_.Exception.Message)"
}

# Reading the console's title fails when there is no console. The title
# itself is never kept, since it can show a command line.
if ($onWindows) {
    try {
        $console = 'none'
        try {
            [void][Console]::Title
            $console = 'attached'
        } catch {
        }
        Write-Lines (Join-Path $call 'console') @($console)
    } catch {
        Write-Problem "console: $($_.Exception.Message)"
    }
}

# After the quick files, because it can block until the harness closes the
# pipe. Raw bytes, so the console code page can't change a character.
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

# flags comes after everything else, so a hook the harness cuts short while
# this reads a command line has written every other file (E-procs-cmdlines).
# The harness is the nearest parent with a name in $harnesses. Its command
# line is read into memory, from Win32_Process.CommandLine on Windows,
# /proc/PID/cmdline on Linux and ps on macOS, and Get-Answers keeps three
# answers from it: no argument is ever written. The file is put together
# first, written as flags.tmp and moved into place, so flags is whole or
# missing.
try {
    $flags = "name=?`nprint=?`nstream_json=?`nbare=?`n"
    foreach ($entry in @($chain)) {
        $parts = [string]$entry -split ' ', 3
        if ($parts.Count -lt 3) {
            continue
        }
        $key = $parts[2].ToLowerInvariant()
        if ($key.EndsWith('.exe', [StringComparison]::Ordinal)) {
            $key = $key.Substring(0, $key.Length - 4)
        }
        if ($harnesses -notcontains $key) {
            continue
        }
        $hid = [int]$parts[0]
        $words = $null
        try {
            if ($onWindows) {
                $cmd = [string](Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $hid" -Property CommandLine).CommandLine
                if ($cmd -ne '') {
                    $words = Split-CommandLine $cmd
                }
            } elseif (Test-Path -LiteralPath "/proc/$hid/cmdline") {
                # One argument per NUL. The file reports no length, so it is
                # read to its end rather than by its size.
                $buffer = New-Object System.IO.MemoryStream
                $file = [System.IO.File]::OpenRead("/proc/$hid/cmdline")
                try {
                    $file.CopyTo($buffer)
                } finally {
                    $file.Dispose()
                }
                $text = $utf8.GetString($buffer.ToArray()).TrimEnd([char]0)
                if ($text -ne '') {
                    $words = $text.Split([char]0)
                }
            } else {
                # macOS: ps joins the arguments with spaces.
                $text = ([string](& ps -o args= -p $hid 2>$null)).Trim()
                if ($text -ne '') {
                    $words = $text -split '\s+'
                }
            }
        } catch {
            Write-Problem "flags: $($_.Exception.Message)"
        }
        $flags = "name=$($parts[2])`n" + (Get-Answers $key $words)
        break
    }
    # WriteAllText creates the file before it writes, so it writes the
    # temporary name. Move never overwrites, and flags isn't there yet.
    $tmp = Join-Path $call 'flags.tmp'
    [System.IO.File]::WriteAllText($tmp, $flags, $utf8)
    [System.IO.File]::Move($tmp, (Join-Path $call 'flags'))
} catch {
    Write-Problem "flags: $($_.Exception.Message)"
}

exit 0
