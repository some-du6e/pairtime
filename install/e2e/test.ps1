# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests install/install.ps1 against the releases install/e2e/fixture.sh
# builds (docs/PLAN.md, WP-RL2), served over HTTP on this computer. Each
# install runs in a new powershell.exe with a new, empty LOCALAPPDATA and
# TEMP and no PAIRTIME_ variables but the case's, and each case checks the
# exit status, the output, every file left in LOCALAPPDATA and that TEMP is
# empty again. It runs on Windows in Windows PowerShell 5.1, from the
# repository root:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File install/e2e/test.ps1 <fixture dir>
#
# It needs python for the HTTP server.
param([Parameter(Mandatory = $true)][string]$Fixture)
Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'

$Fixture = (Resolve-Path -LiteralPath $Fixture).Path
$repoScript = (Resolve-Path -LiteralPath 'install/install.ps1').Path
$stamped = Join-Path $Fixture 'real\install.ps1'
$version = (Get-Content -LiteralPath (Join-Path $Fixture 'version.txt')).Trim()
$keys = Join-Path $Fixture 'keys'
# RUNNER_TEMP has no 8.3 short names in it, unlike TEMP, so paths compare.
$base = $env:RUNNER_TEMP
if (-not $base) { $base = [System.IO.Path]::GetTempPath() }
$scratch = (New-Item -ItemType Directory -Path (Join-Path $base ('install-e2e-' + [guid]::NewGuid().ToString('N')))).FullName
$port = 47615
$server = "http://127.0.0.1:$port"
$script:failures = 0
$script:appData = $null
$script:tmp = $null
$script:record = $null

$python = (Get-Command python).Source
$serverProcess = Start-Process -FilePath $python -PassThru -NoNewWindow `
  -ArgumentList @('-I', '-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', "`"$Fixture`"") `
  -RedirectStandardOutput (Join-Path $scratch 'server.out') -RedirectStandardError (Join-Path $scratch 'server.err')
$ready = $false
foreach ($i in 1..100) {
  try {
    Invoke-WebRequest -Uri "$server/version.txt" -UseBasicParsing | Out-Null
    $ready = $true
    break
  } catch {
    Start-Sleep -Milliseconds 100
  }
}
if (-not $ready) { throw 'the test server never answered' }

function Add-Failure([string]$Message) {
  Write-Output "FAIL $Message"
  Write-Output "::error::install.ps1 e2e: $Message"
  $script:failures++
}

# A new, empty LOCALAPPDATA and TEMP for the next case, and the file that
# fakepairtime's install writes to.
function New-Case {
  $script:appData = (New-Item -ItemType Directory -Path (Join-Path $scratch ('home-' + [guid]::NewGuid().ToString('N')))).FullName
  $script:tmp = (New-Item -ItemType Directory -Path (Join-Path $scratch ('tmp-' + [guid]::NewGuid().ToString('N')))).FullName
  $script:record = "$($script:appData).record"
}

# Every path under Dir, relative and sorted, on one line. PowerShell keeps
# caches under LOCALAPPDATA\Microsoft, which aren't the installer's doing.
function Get-Tree([string]$Dir) {
  $paths = @(Get-ChildItem -LiteralPath $Dir -Recurse -Force | ForEach-Object { $_.FullName.Substring($Dir.Length + 1) } |
    Where-Object { $_ -ne 'Microsoft' -and -not $_.StartsWith('Microsoft\') } | Sort-Object)
  $paths -join ' '
}

function Format-Argument([string]$Text) {
  if ($Text -notmatch '[\s"]') { return $Text }
  '"' + ($Text -replace '"', '\"') + '"'
}

# Runs Shell with Arguments in the case's LOCALAPPDATA and TEMP, with the
# variables in Environment, and returns its exit code and output.
function Invoke-Child([string[]]$Arguments, [hashtable]$Environment = @{}, [string]$Shell = 'powershell.exe') {
  $info = New-Object System.Diagnostics.ProcessStartInfo
  $info.FileName = $Shell
  $info.Arguments = ($Arguments | ForEach-Object { Format-Argument $_ }) -join ' '
  $info.UseShellExecute = $false
  $info.RedirectStandardOutput = $true
  $info.RedirectStandardError = $true
  foreach ($name in @($info.EnvironmentVariables.Keys)) {
    if ($name -like 'PAIRTIME_*') { $info.EnvironmentVariables.Remove($name) }
  }
  $info.EnvironmentVariables['LOCALAPPDATA'] = $script:appData
  $info.EnvironmentVariables['TEMP'] = $script:tmp
  $info.EnvironmentVariables['TMP'] = $script:tmp
  $info.EnvironmentVariables['PAIRTIME_FAKE_RECORD'] = $script:record
  foreach ($name in $Environment.Keys) { $info.EnvironmentVariables[$name] = $Environment[$name] }
  $process = [System.Diagnostics.Process]::Start($info)
  $stdout = $process.StandardOutput.ReadToEndAsync()
  $stderr = $process.StandardError.ReadToEndAsync()
  $process.WaitForExit()
  @{ Code = $process.ExitCode; Output = $stdout.Result + $stderr.Result }
}

# Runs a script file with -File and the given parameters.
function Invoke-File([string]$Script, [string[]]$Parameters = @(), [hashtable]$Environment = @{}) {
  Invoke-Child (@('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $Script) + $Parameters) $Environment
}

# Runs the README's one-liner, irm ... | iex, for the stamped script.
function Invoke-OneLiner([hashtable]$Environment, [string]$Shell = 'powershell.exe') {
  Invoke-Child @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', "irm $server/real/install.ps1 | iex") $Environment $Shell
}

$installed = 'pairtime pairtime\bin pairtime\bin\pairtime.exe'

# Runs a case and checks that it exits 0 (Want ok) or doesn't (Want
# fails), that its output matches Pattern, that LOCALAPPDATA then holds
# exactly Files, and that TEMP is empty.
function Test-Case([string]$Name, [string]$Want, [string]$Pattern, [string]$Files, [hashtable]$Result) {
  $problems = @()
  if ($Want -eq 'ok' -and $Result.Code -ne 0) { $problems += "exited $($Result.Code), want 0" }
  if ($Want -eq 'fails' -and $Result.Code -eq 0) { $problems += 'exited 0, want a failure' }
  if ($Result.Output -cnotmatch $Pattern) { $problems += "its output doesn't match /$Pattern/" }
  $got = Get-Tree $script:appData
  if ($got -ne $Files) { $problems += "LOCALAPPDATA holds [$got], want [$Files]" }
  $left = Get-Tree $script:tmp
  if ($left) { $problems += "TEMP holds [$left], want nothing" }
  if ($problems.Count -eq 0) {
    Write-Output "ok   $Name"
  } else {
    Add-Failure "${Name}: $($problems -join '; ')"
    foreach ($line in ($Result.Output -split "`r?`n")) { Write-Output "     | $line" }
  }
}

function Get-BinarySha256([string]$Release) {
  $envelope = Get-Content -Raw -LiteralPath (Join-Path $Fixture "$Release\manifest.json") | ConvertFrom-Json
  $payload = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($envelope.payload)) | ConvertFrom-Json
  @($payload.artifacts | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' })[0].binary_sha256
}

# Checks that the case's pairtime.exe is Release's and runs.
function Test-Binary([string]$Release) {
  $exe = Join-Path $script:appData 'pairtime\bin\pairtime.exe'
  $want = Get-BinarySha256 $Release
  $got = $null
  if (Test-Path -LiteralPath $exe) { $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $exe).Hash.ToLowerInvariant() }
  if ($got -ne $want) {
    Add-Failure "the installed pairtime.exe has SHA-256 $got, not $Release's $want"
    return
  }
  $said = @(& $exe version)[0]
  if (-not $said.StartsWith("pairtime $version ")) { Add-Failure "the installed pairtime.exe says $said" }
}

# Checks the arguments fakepairtime's install wrote, the first line of its
# record. Want is empty if it shouldn't have run. (The second line, what its
# standard input was, is install/e2e/test.sh's to check: on Windows the
# console and NUL look alike to it.)
function Test-Record([string]$Want) {
  $got = ''
  if (Test-Path -LiteralPath $script:record) { $got = @(Get-Content -LiteralPath $script:record)[0] }
  if ($got -ne $Want) { Add-Failure "fakepairtime install was run as [$got], want [$Want]" }
}

function Get-KeyId([string]$File) {
  $line = @(Get-Content -LiteralPath $File | Where-Object { $_.StartsWith('pairtime-release-ed25519-public:') })[-1]
  $raw = [Convert]::FromBase64String($line.Substring(32))
  $sum = [System.Security.Cryptography.SHA256]::Create().ComputeHash($raw)
  -join ($sum[0..7] | ForEach-Object { $_.ToString('x2') })
}

function Real([string]$Release, [string]$Key, [string[]]$More = @()) {
  @('-BaseUrl', "$server/$Release", '-ReleaseKeys', (Join-Path $keys $Key)) + $More
}

Write-Output "== install.ps1, against the fixture for $version"

New-Case
Test-Case 'irm | iex installs a release signed by a key it was given, and stops before pairtime install, which this build lacks' `
  ok 'has no install command yet' $installed `
  (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'real.pub') })
Test-Binary real

if (Get-Command pwsh -ErrorAction SilentlyContinue) {
  New-Case
  Test-Case 'PowerShell 7 runs the one-liner too' ok 'Installed pairtime' $installed `
    (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'real.pub') } 'pwsh.exe')
  Test-Binary real
}

New-Case
New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
Set-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -Value 'an old pairtime'
Test-Case 'a pairtime.exe already there is replaced, and a PATH without it gets a hint' ok "isn't on your PATH" $installed `
  (Invoke-File $stamped (Real real real.pub))
Test-Binary real

New-Case
New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem
$fakeZip = [System.IO.Compression.ZipFile]::OpenRead((Join-Path $Fixture "fake\pairtime_$($version.Substring(1))_windows_amd64.zip"))
try {
  [System.IO.Compression.ZipFileExtensions]::ExtractToFile($fakeZip.GetEntry('pairtime.exe'), (Join-Path $script:appData 'pairtime\bin\pairtime.exe'))
} finally {
  $fakeZip.Dispose()
}
$running = Start-Process -FilePath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -ArgumentList 'wait' -PassThru -WindowStyle Hidden
Test-Case 'a pairtime.exe that is running, as the agent would be, is moved aside and replaced' `
  ok 'Installed pairtime' "$installed pairtime\bin\pairtime.old.exe" (Invoke-File $stamped (Real real real.pub))
Test-Binary real
Stop-Process -Id $running.Id -Force

New-Case
Test-Case 'by default an unsigned release is refused, before anything is downloaded' fails 'trusts no release key' '' `
  (Invoke-File $stamped @('-BaseUrl', "$server/real"))

New-Case
Test-Case '-AllowUnsigned installs an unsigned release, with a warning' ok 'no key this script trusts signed manifest.json' $installed `
  (Invoke-File $stamped @('-BaseUrl', "$server/real", '-AllowUnsigned'))
Test-Binary real

New-Case
Test-Case 'PAIRTIME_ALLOW_UNSIGNED=1 with irm | iex installs a release no trusted key signed, with a warning' `
  ok 'no key this script trusts signed manifest.json' $installed `
  (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'other.pub'); PAIRTIME_ALLOW_UNSIGNED = '1' })

foreach ($refused in @(
    @('other.pub', 'real', "a release signed by a key it doesn't trust"),
    @('real.pub', 'bad-signature', "another key's signature under the trusted key's id"),
    @('real.pub', 'bad-payload', 'a payload changed after signing'),
    @('real.pub', 'big-s', "a signature whose S isn't reduced mod L"))) {
  New-Case
  Test-Case "$($refused[2]) is refused, and nothing is installed" fails "isn't signed by a key this script trusts" '' `
    (Invoke-File $stamped (Real $refused[1] $refused[0]))
}

New-Case
Test-Case 'one good signature is enough, after one that fails under a trusted key''s id' `
  ok "manifest.json is signed by release key $(Get-KeyId (Join-Path $keys 'two.pub'))" $installed `
  (Invoke-File $stamped (Real two-signatures two.pub))

New-Case
Test-Case 'an archive with a wrong SHA-256 is refused, and nothing is installed' fails "doesn't have the SHA-256 manifest.json lists" '' `
  (Invoke-File $stamped (Real bad-archive real.pub))

New-Case
Test-Case 'a binary with a wrong SHA-256 is refused, and nothing is installed' `
  fails "the pairtime.exe in .* doesn't have the SHA-256 manifest.json lists" '' (Invoke-File $stamped (Real bad-binary bad-binary.pub))

New-Case
Test-Case 'a binary whose version fails is never put in place, and the folders made for it go again' fails "doesn't run on this computer" '' `
  (Invoke-File $stamped (Real broken broken.pub))

New-Case
Test-Case 'a signed manifest for another release is refused' fails "manifest.json is for pairtime $([regex]::Escape($version)), not v0.0.0-snapshot.999999999" '' `
  (Invoke-File $stamped (Real real real.pub @('-Version', 'v0.0.0-snapshot.999999999')))

New-Case
Test-Case 'a build with an install command gets handed over to' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub))
Test-Record 'install'

New-Case
Test-Case '-Yes is passed on to pairtime install as --yes' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub @('-Yes')))
Test-Record 'install --yes'

New-Case
Test-Case '-NoInstall stops once the binary is in place' ok 'To finish setting it up, run' $installed (Invoke-File $stamped (Real fake fake.pub @('-NoInstall')))
Test-Record ''

New-Case
Test-Case 'a failing pairtime install fails the script, and the binary stays' fails "'pairtime install' stopped with status 4" $installed `
  (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_INSTALL_EXIT = '4' })

New-Case
Test-Case "a base URL that isn't https or this computer is refused" fails 'must be an https URL' '' `
  (Invoke-File $stamped @('-BaseUrl', 'http://example.com/pairtime', '-ReleaseKeys', (Join-Path $keys 'real.pub')))

New-Case
Test-Case 'a PAIRTIME_NO_INSTALL that is neither 0 nor 1 is refused' fails 'set PAIRTIME_NO_INSTALL to 1, or leave it unset' '' `
  (Invoke-File $stamped (Real real real.pub) @{ PAIRTIME_NO_INSTALL = 'yes' })

New-Case
Test-Case "the repository's copy names no release, so it needs a version" fails "doesn't name a release" '' `
  (Invoke-File $repoScript (Real real real.pub))

New-Case
Test-Case "the repository's copy installs with -Version and -ReleaseKeys" ok 'Installed pairtime' $installed `
  (Invoke-File $repoScript (Real real real.pub @('-Version', $version)))

Write-Output "== install.ps1's functions"

# RFC 8032 section 7.1's vectors, read from internal/release's test.
$source = (Get-Content -Raw -LiteralPath 'internal/release/keys_test.go') -replace "`r`n", "`n"
$block = $source.Substring($source.IndexOf('var rfc8032 = '))
$block = $block.Substring(0, $block.IndexOf("`n}`n"))
$vectors = @([regex]::Matches($block, 'name:\s*"([^"]+)",\s*secret:\s*"[0-9a-f]*",\s*public:\s*"([0-9a-f]+)",\s*msg:\s*"([0-9a-f]*)",\s*sig:\s*"([0-9a-f]+)"') |
  ForEach-Object { '{0} {1} {2} {3}' -f $_.Groups[1].Value.Replace(' ', '_'), $_.Groups[2].Value, $_.Groups[3].Value, $_.Groups[4].Value })
if ($vectors.Count -ne 5) { Add-Failure "found $($vectors.Count) RFC 8032 vectors in internal/release/keys_test.go, not 5" }
$vectorFile = Join-Path $scratch 'vectors.txt'
# An empty message is written as -.
Set-Content -LiteralPath $vectorFile -Value ($vectors | ForEach-Object { $f = $_.Split(' '); if ($f[2] -eq '') { $f[2] = '-' }; $f -join ' ' })

# Runs Body in a new powershell.exe with install.ps1's functions loaded. It
# must print ok.
function Test-Functions([string]$Name, [string]$Body) {
  $file = Join-Path $scratch ('unit-' + [guid]::NewGuid().ToString('N') + '.ps1')
  $text = "`$ErrorActionPreference = 'Stop'`n`$env:PAIRTIME_INSTALL_PS1_TEST = 'functions'`n. '$repoScript'`n$Body"
  Set-Content -LiteralPath $file -Value $text
  New-Case
  $result = Invoke-File $file @() @{ VECTORS = $vectorFile }
  if ($result.Code -eq 0 -and $result.Output -cmatch '(?m)^ok\r?$') {
    Write-Output "ok   $Name"
  } else {
    Add-Failure $Name
    foreach ($line in ($result.Output -split "`r?`n")) { Write-Output "     | $line" }
  }
}

Test-Functions "RFC 8032's five vectors verify, and each fails with a bit of R or S, the message or the key changed" @'
function FromHex([string]$Hex) {
  if ($Hex -eq '-') { return , [byte[]]@() }
  , [byte[]]@(for ($i = 0; $i -lt $Hex.Length; $i += 2) { [Convert]::ToByte($Hex.Substring($i, 2), 16) })
}
$rows = @(Get-Content -LiteralPath $env:VECTORS)
for ($n = 0; $n -lt $rows.Count; $n++) {
  $f = $rows[$n].Split(' ')
  $key = FromHex $f[1]; $msg = FromHex $f[2]; $sig = FromHex $f[3]
  $other = FromHex $rows[($n + 1) % $rows.Count].Split(' ')[1]
  $r = [byte[]]$sig.Clone(); $r[0] = $r[0] -bxor 1
  $s = [byte[]]$sig.Clone(); $s[40] = $s[40] -bxor 0x10
  $longer = [byte[]]($msg + [byte]0)
  if (-not (Test-Ed25519 $key $msg $sig)) { "$($f[0]): the good signature is refused"; exit 1 }
  foreach ($case in @(@('R', $key, $msg, $r), @('S', $key, $msg, $s), @('message', $key, $longer, $sig), @('key', $other, $msg, $sig))) {
    if (Test-Ed25519 $case[1] $case[2] $case[3]) { "$($f[0]): a changed $($case[0]) is accepted"; exit 1 }
  }
}
'ok'
'@

Test-Functions 'a command that outlasts its time is killed, and one that does not keeps its status' @'
$started = Get-Date
$slow = Invoke-WithTimeout 'ping.exe' '-n 30 127.0.0.1' 2
if (-not $slow.TimedOut -or $slow.Ok) { 'ping -n 30 finished in 2 seconds?'; exit 1 }
if (((Get-Date) - $started).TotalSeconds -gt 15) { 'the kill took too long'; exit 1 }
$quick = Invoke-WithTimeout 'cmd.exe' '/c echo hi' 10
if (-not $quick.Ok -or $quick.Output.Trim() -ne 'hi') { "a quick command: $($quick.Output)"; exit 1 }
$failing = Invoke-WithTimeout 'cmd.exe' '/c exit 7' 10
if ($failing.Ok -or $failing.Output -notmatch 'status 7') { "a failing command: $($failing.Output)"; exit 1 }
'ok'
'@

Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
if ($script:failures -ne 0) {
  Write-Output "$($script:failures) checks failed"
  exit 1
}
Write-Output 'every check passed'
