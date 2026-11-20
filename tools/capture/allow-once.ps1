# SPDX-License-Identifier: AGPL-3.0-or-later
#
# allow-once.ps1 RUN_DIR LABEL [ARG...]
#
# The PowerShell twin of allow-once.sh, for Windows: Claude Code's
# PermissionRequest hook for a capture (SCRIPTS.md, Claude Code, step 22).
# It records every call with dump-hook.ps1, like every other hook. Then, the
# first time a payload names step 22's file, it waits 2 seconds and prints
# Claude Code's allow decision, so the capture holds one permission prompt
# that a hook answered, not the person (SM 1.2), and keeps that decision in
# RUN_DIR\allow-once. Every other call prints nothing, which leaves the
# prompt to the person. It always exits 0, and without arguments it exits
# before reading anything.
#
# Register it as one command line, like dump-hook.ps1:
#
#   powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "<kit>\allow-once.ps1" "<run-dir>" PermissionRequest
#
# Runs on Windows PowerShell 5.1 and PowerShell 7.

$file = 'hook-allowed.txt'
$decision = '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
$argv = @($args)
$utf8 = New-Object System.Text.UTF8Encoding -ArgumentList $false

# One argument as Windows splits a command line back into arguments:
# quoted, with backslashes doubled only before a quote or the end.
function Quote-Arg([string]$a) {
    return '"' + (($a -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}

try {
    if ($argv.Count -lt 2 -or [string]::IsNullOrEmpty($argv[0])) {
        exit 0
    }
    $run = $argv[0]
    [void][System.IO.Directory]::CreateDirectory($run)

    # Read the payload once, as bytes, so the dumper gets it unchanged and
    # the check below can read it after.
    $payload = [byte[]]@()
    if ([Console]::IsInputRedirected) {
        $in = [Console]::OpenStandardInput()
        $buf = New-Object System.IO.MemoryStream
        $in.CopyTo($buf)
        $payload = $buf.ToArray()
    }

    # Record the call with the dumper, run by this same PowerShell with the
    # payload on its stdin. It prints nothing.
    try {
        $dumper = Join-Path $PSScriptRoot 'dump-hook.ps1'
        $list = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $dumper) + $argv
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = (Get-Process -Id $PID).Path
        $psi.Arguments = (($list | ForEach-Object { Quote-Arg ([string]$_) }) -join ' ')
        $psi.UseShellExecute = $false
        $psi.RedirectStandardInput = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $p = [System.Diagnostics.Process]::Start($psi)
        $p.StandardInput.BaseStream.Write($payload, 0, $payload.Length)
        $p.StandardInput.Close()
        [void]$p.StandardOutput.ReadToEnd()
        [void]$p.StandardError.ReadToEnd()
        $p.WaitForExit()
    } catch {
    }

    # Only the call that creates allow-once answers: CreateNew is atomic, so
    # only one call ever can. The wait makes the hook's answer come seconds
    # after the request, as a person's would.
    $text = $utf8.GetString($payload)
    if ($text.Contains($file)) {
        $claimed = $false
        try {
            $f = [System.IO.File]::Open((Join-Path $run 'allow-once'), [System.IO.FileMode]::CreateNew)
            try {
                $bytes = $utf8.GetBytes($decision + "`n")
                $f.Write($bytes, 0, $bytes.Length)
            } finally {
                $f.Dispose()
            }
            $claimed = $true
        } catch {
        }
        if ($claimed) {
            Start-Sleep -Seconds 2
            [Console]::Out.Write($decision + "`n")
        }
    }
} catch {
}
exit 0
