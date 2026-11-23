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
# It needs python for the HTTP server, install/e2e/serve.py.
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
  -ArgumentList @('-I', 'install/e2e/serve.py', "$port", "`"$Fixture`"") `
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
function Invoke-File([string]$Script, [string[]]$Parameters = @(), [hashtable]$Environment = @{}, [string]$Shell = 'powershell.exe') {
  Invoke-Child (@('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $Script) + $Parameters) $Environment $Shell
}

# Runs the README's one-liner, irm ... | iex, for the stamped script.
function Invoke-OneLiner([hashtable]$Environment, [string]$Shell = 'powershell.exe') {
  Invoke-Child @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', "irm $server/real/install.ps1 | iex") $Environment $Shell
}

$installed = 'pairtime pairtime\bin pairtime\bin\pairtime.exe'

# The shells the cases that check how PowerShell itself behaves run in:
# Windows PowerShell 5.1, and PowerShell 7 where the runner has it.
$shells = @('powershell.exe')
if (Get-Command pwsh -ErrorAction SilentlyContinue) { $shells += 'pwsh.exe' }

# The real binary's cases stop before pairtime install with -NoInstall,
# except the one just below, which runs the real pairtime install and
# uninstall (WP-IN2).
#
# The cases that need a script trusting no release key run the repository's
# copy, which pins none: the stamped copies pin whatever release-keys.txt
# holds, which stops being nothing once Karim commits the release key.

# A new empty folder in the scratch space, for a case's APPDATA or
# USERPROFILE.
function New-Dir([string]$Name) {
  (New-Item -ItemType Directory -Path (Join-Path $scratch ($Name + '-' + [guid]::NewGuid().ToString('N')))).FullName
}

# The HKCU Run value pairtime install writes for autostart, or $null.
function Get-RunValue {
  $run = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
  if ($null -eq $run) { return $null }
  try { $run.GetValue('pairtime') } finally { $run.Close() }
}

# Waits up to Seconds for Dir to hold nothing Get-Tree counts. pairtime
# uninstall can't delete the pairtime.exe it runs from, so a process that
# waits for it to exit does that a moment later.
function Wait-Empty([string]$Dir, [int]$Seconds) {
  $deadline = (Get-Date).AddSeconds($Seconds)
  while ((Get-Date) -lt $deadline) {
    try { if (-not (Get-Tree $Dir)) { return } } catch { }
    Start-Sleep -Milliseconds 250
  }
}

# Fails if a process still runs from Dir, as an agent that pairtime
# uninstall didn't stop would, and then stops it.
function Test-NoAgent([string]$Dir) {
  $left = @()
  foreach ($i in 1..50) {
    $left = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith("$Dir\", [StringComparison]::OrdinalIgnoreCase) })
    if ($left.Count -eq 0) { return }
    Start-Sleep -Milliseconds 200
  }
  Add-Failure "a process from $Dir still runs after pairtime uninstall: $(($left | ForEach-Object { "$($_.ProcessId) $($_.ExecutablePath)" }) -join '; ')"
  foreach ($p in $left) { Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }
}

# Checks that Line, a command the script printed, parses as PowerShell and
# names Want as one string, unchanged.
function Test-PrintedCommand([string]$What, [string]$Line, [string]$Want) {
  $errors = $null
  $ast = [System.Management.Automation.Language.Parser]::ParseInput($Line, [ref]$null, [ref]$errors)
  if ($errors.Count -ne 0) {
    Add-Failure "$What doesn't parse ($($errors[0].Message)): $Line"
    return
  }
  $strings = @($ast.FindAll({ $args[0] -is [System.Management.Automation.Language.StringConstantExpressionAst] }, $true) | ForEach-Object { $_.Value })
  if ($strings -cnotcontains $Want) { Add-Failure "$What doesn't name $Want as one string: $Line" }
}

# Runs a case and checks that it exits 0 (Want ok) or doesn't (Want
# fails), that its output matches Pattern, that LOCALAPPDATA then holds
# exactly Files, and that TEMP is empty. PowerShell wraps error and warning
# text at 120 columns, sometimes in the middle of a word, so the output and
# the pattern are compared with their whitespace taken out.
function Test-Case([string]$Name, [string]$Want, [string]$Pattern, [string]$Files, [hashtable]$Result) {
  $problems = @()
  if ($Want -eq 'ok' -and $Result.Code -ne 0) { $problems += "exited $($Result.Code), want 0" }
  if ($Want -eq 'fails' -and $Result.Code -eq 0) { $problems += 'exited 0, want a failure' }
  if (($Result.Output -replace '\s+', '') -cnotmatch ($Pattern -replace ' ', '')) { $problems += "its output doesn't match /$Pattern/" }
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
Test-Case 'irm | iex installs a release signed by a key it was given' ok 'Installed pairtime' $installed `
  (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'real.pub'); PAIRTIME_NO_INSTALL = '1' })
Test-Binary real

if (Get-Command pwsh -ErrorAction SilentlyContinue) {
  New-Case
  Test-Case 'PowerShell 7 runs the one-liner too' ok 'Installed pairtime' $installed `
    (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'real.pub'); PAIRTIME_NO_INSTALL = '1' } 'pwsh.exe')
  Test-Binary real
}

Add-Type -AssemblyName System.IO.Compression.FileSystem

# The real pairtime install and uninstall, through the script
# (docs/PLAN.md, WP-RL2): set up with -Yes in a new LOCALAPPDATA,
# APPDATA and USERPROFILE, then taken out with --delete-data, they must
# leave all three as they found them, the folders the script made
# included, with no Run value and no agent left. Windows PowerShell makes
# AppData\Roaming in whatever USERPROFILE it starts with, even to run
# nothing, and a real profile always has it, so the new one starts with
# it (measured on windows-latest, 2026-11-20).
New-Case
$roaming = New-Dir 'roaming'
$profileDir = New-Dir 'profile'
New-Item -ItemType Directory -Path (Join-Path $profileDir 'AppData\Roaming') | Out-Null
$profileBefore = Get-Tree $profileDir
$caseEnvironment = @{ APPDATA = $roaming; USERPROFILE = $profileDir }
$runBefore = Get-RunValue
$setUp = Invoke-File $stamped (Real real real.pub @('-Yes')) $caseEnvironment
$exe = Join-Path $script:appData 'pairtime\bin\pairtime.exe'
$takenOut = @{ Code = 1; Output = "there's no $exe to run pairtime uninstall with" }
if (Test-Path -LiteralPath $exe) { $takenOut = Invoke-Child @('uninstall', '--yes', '--delete-data') $caseEnvironment $exe }
Wait-Empty $script:appData 30
$code = $setUp.Code
if ($code -eq 0) { $code = $takenOut.Code }
Test-Case 'with -Yes in an empty profile, the script hands the binary to pairtime install, which sets pairtime up, and pairtime uninstall --yes --delete-data leaves it empty again' `
  ok 'Running pairtime install' '' @{ Code = $code; Output = $setUp.Output + $takenOut.Output }
$left = Get-Tree $roaming
if ($left) { Add-Failure "pairtime uninstall left [$left] in APPDATA" }
$left = Get-Tree $profileDir
if ($left -ne $profileBefore) { Add-Failure "USERPROFILE holds [$left] after pairtime uninstall, not the [$profileBefore] it started with" }
Test-NoAgent $script:appData
if ($null -eq $runBefore -and $null -ne (Get-RunValue)) { Add-Failure "pairtime uninstall left the Run value pairtime" }

New-Case
New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
Set-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -Value 'an old pairtime'
Test-Case 'a pairtime.exe already there is replaced, and a PATH without it gets a hint' ok "isn't on your PATH" $installed `
  (Invoke-File $stamped (Real real real.pub @('-NoInstall')))
Test-Binary real

New-Case
New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
$fakeZip = [System.IO.Compression.ZipFile]::OpenRead((Join-Path $Fixture "fake\pairtime_$($version.Substring(1))_windows_amd64.zip"))
try {
  [System.IO.Compression.ZipFileExtensions]::ExtractToFile($fakeZip.GetEntry('pairtime.exe'), (Join-Path $script:appData 'pairtime\bin\pairtime.exe'))
} finally {
  $fakeZip.Dispose()
}
$running = Start-Process -FilePath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -ArgumentList 'wait' -PassThru -WindowStyle Hidden
Test-Case 'a pairtime.exe that is running, as the agent would be, is moved aside and replaced' `
  ok 'Installed pairtime' "$installed pairtime\bin\pairtime.old.exe" (Invoke-File $stamped (Real real real.pub @('-NoInstall')))
Test-Binary real
Stop-Process -Id $running.Id -Force

New-Case
Test-Case 'by default a script that trusts no key refuses the release, before anything is downloaded' fails 'trusts no release key' '' `
  (Invoke-File $repoScript @('-Version', $version, '-BaseUrl', "$server/real"))

New-Case
Test-Case '-AllowUnsigned has a script that trusts no key install an unsigned release, with a warning' ok 'no key this script trusts signed manifest.json' $installed `
  (Invoke-File $repoScript @('-Version', $version, '-BaseUrl', "$server/real", '-AllowUnsigned', '-NoInstall'))
Test-Binary real

New-Case
$command = "`$env:PAIRTIME_ALLOW_UNSIGNED = '1'; irm $server/repo/install.ps1 | iex; " +
  "if (Test-Path Env:PAIRTIME_ALLOW_UNSIGNED) { 'PAIRTIME_ALLOW_UNSIGNED is still set'; exit 9 } else { 'PAIRTIME_ALLOW_UNSIGNED is gone' }"
Test-Case 'PAIRTIME_ALLOW_UNSIGNED has irm | iex of a script that trusts no key install an unsigned release, and is gone from the session after' `
  ok 'no key this script trusts signed manifest\.json[\s\S]*PAIRTIME_ALLOW_UNSIGNED is gone' $installed `
  (Invoke-Child @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', $command) @{ PAIRTIME_VERSION = $version; PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_NO_INSTALL = '1' })

New-Case
Test-Case 'PAIRTIME_ALLOW_UNSIGNED does not apply once the script trusts a key: a release no trusted key signed is refused' `
  fails "isn't signed by a key this script trusts.*Allowing unsigned installs doesn't change that" '' `
  (Invoke-OneLiner @{ PAIRTIME_BASE_URL = "$server/real"; PAIRTIME_RELEASE_KEYS = (Join-Path $keys 'other.pub'); PAIRTIME_ALLOW_UNSIGNED = '1'; PAIRTIME_NO_INSTALL = '1' })

New-Case
Test-Case '-AllowUnsigned does not apply once the script trusts a key either' `
  fails "isn't signed by a key this script trusts.*Allowing unsigned installs doesn't change that" '' `
  (Invoke-File $stamped (Real real other.pub @('-AllowUnsigned')))

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
Test-Case 'a signature whose base64 is not canonical is refused, as internal/release refuses it, and nothing is installed' `
  fails "manifest.json isn't a pairtime update manifest in the form tools/release writes" '' (Invoke-File $stamped (Real noncanonical-signature real.pub))

New-Case
Test-Case 'one good signature is enough, after one that fails under a trusted key''s id' `
  ok "manifest.json is signed by release key $(Get-KeyId (Join-Path $keys 'two.pub'))" $installed `
  (Invoke-File $stamped (Real two-signatures two.pub @('-NoInstall')))

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
Test-Case 'a build with an install command gets the staged binary to put in place, told which folders the script made' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub))
Test-Record "install --made $($script:appData)\pairtime --made $($script:appData)\pairtime\bin"
Test-Binary fake

New-Case
Test-Case '-Yes is passed on to pairtime install as --yes' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub @('-Yes')))
Test-Record "install --yes --made $($script:appData)\pairtime --made $($script:appData)\pairtime\bin"

New-Case
New-Item -ItemType Directory -Path (Join-Path $script:appData 'pairtime') | Out-Null
Test-Case 'a pairtime folder that was there is not named as made' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub))
Test-Record "install --made $($script:appData)\pairtime\bin"

New-Case
New-Item -ItemType Directory -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
Test-Case 'when pairtime\bin was there, pairtime install hears of no folder the script made' ok 'Running pairtime install' $installed (Invoke-File $stamped (Real fake fake.pub))
Test-Record 'install'

New-Case
Test-Case '-NoInstall stops once the binary is in place' ok 'To finish setting it up, run' $installed (Invoke-File $stamped (Real fake fake.pub @('-NoInstall')))
Test-Record ''

# A help that fails says nothing about pairtime install, and without it
# the binary would go in place with no consent screen. A native command's
# status throws in neither Windows PowerShell 5.1 nor PowerShell 7.
foreach ($shell in $shells) {
  New-Case
  Test-Case "in $shell, a build whose help fails is never put in place, and the folders made for it go again" `
    fails "the new pairtime's 'pairtime help' failed.*so it wasn't installed" '' `
    (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_HELP_EXIT = '5' } $shell)
  Test-Record ''

  New-Case
  New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
  Set-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -Value 'an old pairtime'
  Test-Case "in $shell, a build whose help fails leaves the pairtime.exe that was there as it was" fails 'Any pairtime you had is still there' $installed `
    (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_HELP_EXIT = '5' } $shell)
  if ((Get-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe')) -ne 'an old pairtime') { Add-Failure "in ${shell}, the old pairtime.exe was changed" }
  Test-Record ''
}

New-Case
Test-Case 'a pairtime install that stops before it puts pairtime in place leaves nothing, the folders made for it included' `
  fails "'pairtime install' stopped with status 4 before it put pairtime in place, so nothing was installed" '' `
  (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_INSTALL_EXIT = '4' })

New-Case
New-Item -ItemType Directory -Force -Path (Join-Path $script:appData 'pairtime\bin') | Out-Null
Set-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe') -Value 'an old pairtime'
Test-Case "a no at pairtime install's screen leaves the pairtime.exe that was there as it was" fails 'you said no, so nothing was installed' $installed `
  (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_INSTALL_EXIT = '3' })
if ((Get-Content -LiteralPath (Join-Path $script:appData 'pairtime\bin\pairtime.exe')) -ne 'an old pairtime') { Add-Failure 'the old pairtime.exe was changed' }

# A no changes nothing, so the same release already in place, from an
# earlier -NoInstall, was there before the rerun.
foreach ($shell in $shells) {
  New-Case
  $before = Invoke-File $stamped (Real fake fake.pub @('-NoInstall')) @{} $shell
  if ($before.Code -ne 0) { Add-Failure "in ${shell}, the -NoInstall run before the next case failed: $($before.Output)" }
  Test-Case "in $shell, a no on a rerun of the release already in place says nothing changed and how to set it up, not that install stopped" `
    fails "you said no, so nothing changed. The pairtime $([regex]::Escape($version)) at $([regex]::Escape((Join-Path $script:appData 'pairtime\bin\pairtime.exe'))) stays as it was; to set it up later, run: &" $installed `
    (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_INSTALL_EXIT = '3' } $shell)
  Test-Binary fake
}

New-Case
Test-Case 'a pairtime install that fails after putting pairtime in place says where it is and how to finish, with the folders made' `
  fails "'pairtime install' stopped with status 4. pairtime is at $([regex]::Escape((Join-Path $script:appData 'pairtime\bin\pairtime.exe'))); run & .*install --made" $installed `
  (Invoke-File $stamped (Real fake fake.pub) @{ PAIRTIME_FAKE_INSTALL_AFTER = '4' })

New-Case
$script:appData = (New-Item -ItemType Directory -Path (Join-Path $script:appData "D'Angelo")).FullName
$result = Invoke-File $stamped (Real real real.pub @('-NoInstall'))
Test-Case "the commands it prints are quoted for a LOCALAPPDATA with an apostrophe in it" ok 'To finish setting it up, run' $installed $result
$lines = @($result.Output -split "`r?`n")
$finish = @($lines | Where-Object { $_.StartsWith('To finish setting it up, run: ') })
$pathCommand = @($lines | Where-Object { $_.Contains('CreateSubKey') })
if ($finish.Count -ne 1 -or $pathCommand.Count -ne 1) {
  Add-Failure "found $($finish.Count) commands to finish with and $($pathCommand.Count) PATH commands, not one each"
} else {
  # A pairtime install run later finds the folders made, so it's told them.
  foreach ($want in @('pairtime\bin\pairtime.exe', 'pairtime', 'pairtime\bin')) {
    Test-PrintedCommand 'the command to finish with' $finish[0].Substring('To finish setting it up, run: '.Length) (Join-Path $script:appData $want)
  }
  Test-PrintedCommand 'the PATH command' $pathCommand[0].Trim() (Join-Path $script:appData 'pairtime\bin')
}

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
  (Invoke-File $repoScript (Real real real.pub @('-Version', $version, '-NoInstall')))

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

# Runs Body in a new powershell.exe, or Shell, with install.ps1's functions
# loaded. It must print ok.
function Test-Functions([string]$Name, [string]$Body, [string]$Shell = 'powershell.exe') {
  $file = Join-Path $scratch ('unit-' + [guid]::NewGuid().ToString('N') + '.ps1')
  $text = "`$ErrorActionPreference = 'Stop'`n`$env:PAIRTIME_INSTALL_PS1_TEST = 'functions'`n. '$repoScript'`n$Body"
  Set-Content -LiteralPath $file -Value $text
  New-Case
  $result = Invoke-File $file @() @{ VECTORS = $vectorFile } $Shell
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
  $other = FromHex ($rows[($n + 1) % $rows.Count].Split(' ')[1])
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
# The killed process has exited by the time Invoke-WithTimeout returns, so
# its exe can go at once, as the install's cleanup removes the new one.
$copy = Join-Path $env:TEMP 'ping-copy.exe'
Copy-Item -LiteralPath (Get-Command ping.exe).Source -Destination $copy
$slow = Invoke-WithTimeout $copy '-n 30 127.0.0.1' 2
if (-not $slow.TimedOut) { 'the copy of ping finished in 2 seconds?'; exit 1 }
Remove-Item -LiteralPath $copy
$quick = Invoke-WithTimeout 'cmd.exe' '/c echo hi' 10
if (-not $quick.Ok -or $quick.Output.Trim() -ne 'hi') { "a quick command: $($quick.Output)"; exit 1 }
$failing = Invoke-WithTimeout 'cmd.exe' '/c exit 7' 10
if ($failing.Ok -or $failing.Output -notmatch 'status 7') { "a failing command: $($failing.Output)"; exit 1 }
'ok'
'@

$elevation = @'
$type = Get-ElevationType
"TOKEN_ELEVATION_TYPE $type"
if ($type -lt 1 -or $type -gt 3) { 'Get-ElevationType gave no elevation type'; exit 1 }
'ok'
'@
Test-Functions 'Get-ElevationType reads the token through GetTokenInformation, in Windows PowerShell 5.1' $elevation
if (Get-Command pwsh -ErrorAction SilentlyContinue) {
  Test-Functions 'Get-ElevationType reads the token through GetTokenInformation, in PowerShell 7' $elevation 'pwsh.exe'
}

Test-Functions 'only the elevated half of a split token is refused, as pairtime hook refuses it' @'
foreach ($type in 0, 1, 3) { Assert-NotElevated $type }
try {
  Assert-NotElevated 2
  'the elevated half went on'
  exit 1
} catch {
  if ($_.Exception.Message -notmatch "don't run install\.ps1 as administrator") { "the refusal said: $($_.Exception.Message)"; exit 1 }
}
'ok'
'@

Test-Functions 'the PATH command adds the folder and keeps Path REG_EXPAND_SZ, its references unexpanded' @'
$key = 'Software\pairtime-e2e-' + [guid]::NewGuid().ToString('N') + '\Environment'
$dir = "C:\Users\D'Angelo\AppData\Local\pairtime\bin"
$stored = '%USERPROFILE%\AppData\Local\Microsoft\WindowsApps;%PAIRTIME_E2E_UNSET%\bin;'
try {
  $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($key)
  $k.SetValue('Path', $stored, 'ExpandString')
  $k.Close()
  Invoke-Expression (Get-PathCommand $dir $key)
  $k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($key)
  $kind = $k.GetValueKind('Path')
  $got = $k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
  $k.Close()
  if ($kind -ne 'ExpandString' -or $got -cne ($stored + $dir)) { "Path is $kind [$got], want ExpandString [$stored$dir]"; exit 1 }
  # With no Path yet, the folder is all of it.
  [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($key, $true).DeleteValue('Path')
  Invoke-Expression (Get-PathCommand $dir $key)
  $k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($key)
  $got = $k.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
  $k.Close()
  if ($got -cne $dir) { "a new Path is [$got], want [$dir]"; exit 1 }
} finally {
  [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($key.Substring(0, $key.LastIndexOf('\')), $false)
}
'ok'
'@

Test-Functions 'only canonical base64 is accepted, as internal/release accepts it' @'
$good = ConvertFrom-StrictBase64 'aGk='
if ($null -eq $good -or [System.Text.Encoding]::ASCII.GetString($good) -cne 'hi') { 'aGk= did not decode to hi'; exit 1 }
foreach ($bad in @('QR==', 'QQ', 'QQ===', 'aGk=aGk=', 'aG k=', "aGk=`n")) {
  if ($null -ne (ConvertFrom-StrictBase64 $bad)) { "accepted [$bad]"; exit 1 }
}
'ok'
'@

Test-Functions 'a path in a printed command reads back as one string, curly quotes too' @'
foreach ($text in @("C:\Users\D'Angelo\pairtime.exe", "it$([char]0x2019)s", "two''quotes", "$([char]0x2018)x$([char]0x201B)", '')) {
  $errors = $null
  $ast = [System.Management.Automation.Language.Parser]::ParseInput((ConvertTo-Quoted $text), [ref]$null, [ref]$errors)
  $value = $ast.EndBlock.Statements[0].PipelineElements[0].Expression.Value
  if ($errors.Count -ne 0 -or $value -cne $text) { "[$text] came back as [$value] from $(ConvertTo-Quoted $text)"; exit 1 }
}
'ok'
'@

Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
if ($script:failures -ne 0) {
  Write-Output "$($script:failures) checks failed"
  exit 1
}
Write-Output 'every check passed'
