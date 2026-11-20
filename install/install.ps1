# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Installs pairtime on Windows. pairtime counts the hours you spend building
# with AI: https://github.com/some-du6e/pairtime
#
#   irm https://github.com/some-du6e/pairtime/releases/download/<version>/install.ps1 | iex
#
# It stops at the first thing that's wrong, and until step 4 it changes
# nothing on this computer:
#
#   1. Downloads the release's manifest.json and checks that a pairtime
#      release key signed it (docs/spec/update-manifest.md).
#   2. Downloads the archive for this computer and checks it, and the
#      pairtime.exe inside it, against the manifest's SHA-256s.
#   3. Runs the new pairtime.exe's 'pairtime version' and gives up if that
#      fails or takes over 60 seconds.
#   4. Puts it at %LOCALAPPDATA%\pairtime\bin\pairtime.exe.
#   5. Runs 'pairtime install', which lists every change it would make and
#      asks before making any.
#
# It needs no administrator rights, and it sends nothing anywhere: its only
# requests are the downloads. It runs in Windows PowerShell 5.1, which
# Windows 10 and 11 come with, and in PowerShell 7.
#
# Options are environment variables, set before 'irm ... | iex', or
# parameters when the script is saved and run as a file:
#
#   $env:PAIRTIME_VERSION = 'V'          -Version V          install release V instead of this script's
#   $env:PAIRTIME_BASE_URL = 'URL'       -BaseUrl URL        download the release files from URL
#   $env:PAIRTIME_RELEASE_KEYS = 'FILE'  -ReleaseKeys FILE   trust the public keys in FILE instead of pairtime's
#   $env:PAIRTIME_ALLOW_UNSIGNED = '1'   -AllowUnsigned      install even if no trusted key signed the release
#   $env:PAIRTIME_NO_INSTALL = '1'       -NoInstall          stop after step 4
#                                        -Yes                answer yes for 'pairtime install'

# Everything runs inside this script block, so that pasting the script with
# irm | iex leaves no functions or variables behind in the session.
$installPairtime = {
  param(
    [string]$Version = $env:PAIRTIME_VERSION,
    [string]$BaseUrl = $env:PAIRTIME_BASE_URL,
    [string]$ReleaseKeys = $env:PAIRTIME_RELEASE_KEYS,
    [switch]$AllowUnsigned,
    [switch]$NoInstall,
    [switch]$Yes
  )
  Set-StrictMode -Version 2
  $ErrorActionPreference = 'Stop'

  # tools/release scripts fills in these two lines when release.yml
  # attaches this script to a release: the release's version, and the
  # release keys its binaries trust (internal/release/release-keys.txt).
  # The copy in the repository has neither, so it needs -Version and
  # -ReleaseKeys.
  $PinnedVersion = ''
  $PinnedKeys = @()

  $Repo = 'https://github.com/some-du6e/pairtime'
  $PayloadType = 'application/vnd.pairtime.update-manifest.v1+json'
  $PublicPrefix = 'pairtime-release-ed25519-public:'
  # How long the new binary's 'version' may take (DESIGN 3.12).
  $VersionTimeout = 60

  function Fail([string]$Message) {
    throw "install.ps1: $Message"
  }

  function Say([string]$Message) {
    Write-Host $Message
  }

  # The first Length characters of Text.
  function Get-Start([string]$Text, [int]$Length) {
    if ($Text.Length -le $Length) { return $Text }
    $Text.Substring(0, $Length)
  }

  # RFC 8032's Ed25519 verification (section 5.1.7). Windows PowerShell 5.1
  # has no Ed25519, and the downloaded binary can't vouch for itself, so the
  # signature is checked here with .NET's big integers. Only public values
  # go through it. Points are arrays in extended coordinates (X, Y, Z, T),
  # where x = X/Z, y = Y/Z and x*y = T/Z.
  $P = [bigint]::Pow(2, 255) - 19
  $L = [bigint]::Pow(2, 252) + [bigint]::Parse('27742317777372353535851937790883648493')
  $Bit255 = [bigint]::Pow(2, 255)

  # x mod p, from 0 to p - 1
  function ModP([bigint]$x) {
    $r = $x % $P
    if ($r.Sign -lt 0) { $r += $P }
    $r
  }

  function Invert([bigint]$x) {
    [bigint]::ModPow((ModP $x), $P - 2, $P)
  }

  $D = ModP ((Invert 121666) * -121665)
  $SqrtMinusOne = [bigint]::ModPow(2, [bigint]::Divide($P - 1, 4), $P)

  # A little-endian byte string as a number, the way RFC 8032 reads keys and
  # signatures. The extra zero byte keeps it from reading as negative.
  function ConvertTo-Integer([byte[]]$Bytes) {
    [bigint]::new([byte[]]($Bytes + [byte]0))
  }

  # The sum of two points, by the formula in RFC 8032 section 5.1.4, which
  # also doubles a point.
  function Add-Point($p1, $p2) {
    $a = ($p1[1] - $p1[0]) * ($p2[1] - $p2[0]) % $P
    $b = ($p1[1] + $p1[0]) * ($p2[1] + $p2[0]) % $P
    $c = $p1[3] * $p2[3] * 2 * $D % $P
    $k = $p1[2] * $p2[2] * 2 % $P
    $e = $b - $a
    $f = $k - $c
    $g = $k + $c
    $h = $b + $a
    , @(($e * $f % $P), ($g * $h % $P), ($f * $g % $P), ($e * $h % $P))
  }

  # s times a point, doubling and adding.
  function Get-Multiple([bigint]$s, $point) {
    $sum = @([bigint]0, [bigint]1, [bigint]1, [bigint]0)
    while ($s -gt 0) {
      if (-not $s.IsEven) { $sum = Add-Point $sum $point }
      $point = Add-Point $point $point
      $s = [bigint]::Divide($s, 2)
    }
    , $sum
  }

  # The point with this y whose x has this low bit (RFC 8032 section
  # 5.1.3), or $null if there is none.
  function Get-Point([bigint]$y, [int]$sign) {
    if ($y -ge $P) { return $null }
    $u = ModP ($y * $y - 1)
    $v = ModP ($D * $y * $y + 1)
    $x = [bigint]::ModPow((ModP ($u * (Invert $v))), [bigint]::Divide($P + 3, 8), $P)
    if ((ModP ($v * $x * $x - $u)) -ne 0) { $x = $x * $SqrtMinusOne % $P }
    if ((ModP ($v * $x * $x - $u)) -ne 0) { return $null }
    if ($x.IsZero -and $sign -eq 1) { return $null }
    if ([int]($x % 2) -ne $sign) { $x = $P - $x }
    , @($x, $y, [bigint]1, ($x * $y % $P))
  }

  # The point that 32 bytes encode: y, with x's low bit as bit 255.
  function ConvertFrom-PointBytes([byte[]]$Bytes) {
    $n = ConvertTo-Integer $Bytes
    Get-Point ($n % $Bit255) ([int][bigint]::Divide($n, $Bit255))
  }

  # The base point B: y = 4/5 and x even.
  $B = Get-Point (ModP ((Invert 5) * 4)) 0

  # Whether Signature, 64 bytes, is a valid signature of Message under Key,
  # a 32-byte public key. As Go does, it computes [S]B - [k]A and compares
  # its encoding with R.
  function Test-Ed25519([byte[]]$Key, [byte[]]$Message, [byte[]]$Signature) {
    if ($Key.Length -ne 32 -or $Signature.Length -ne 64) { return $false }
    $a = ConvertFrom-PointBytes $Key
    if ($null -eq $a) { return $false }
    $rBytes = [byte[]]$Signature[0..31]
    $s = ConvertTo-Integer ([byte[]]$Signature[32..63])
    if ($s -ge $L) { return $false }
    $sha512 = [System.Security.Cryptography.SHA512]::Create()
    $k = (ConvertTo-Integer ($sha512.ComputeHash([byte[]]($rBytes + $Key + $Message)))) % $L
    # -A has the opposite x.
    $minusA = @((ModP (-$a[0])), $a[1], $a[2], (ModP (-$a[3])))
    $r = Add-Point (Get-Multiple $s $B) (Get-Multiple $k $minusA)
    if ((ModP $r[2]) -eq 0) { return $false }
    $zInverse = Invert $r[2]
    $x = ModP ($r[0] * $zInverse)
    $y = ModP ($r[1] * $zInverse)
    ($y + ($x % 2) * $Bit255) -eq (ConvertTo-Integer $rBytes)
  }

  function Get-Sha256Hex([byte[]]$Bytes) {
    $sum = [System.Security.Cryptography.SHA256]::Create().ComputeHash($Bytes)
    -join ($sum | ForEach-Object { $_.ToString('x2') })
  }

  function Get-FileSha256([string]$Path) {
    (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
  }

  # Runs Exe with Arguments and empty standard input, and kills it after
  # Seconds. Returns whether it exited 0 in time, and what it printed.
  function Invoke-WithTimeout([string]$Exe, [string]$Arguments, [int]$Seconds) {
    $info = New-Object System.Diagnostics.ProcessStartInfo
    $info.FileName = $Exe
    $info.Arguments = $Arguments
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    try {
      $process = [System.Diagnostics.Process]::Start($info)
    } catch {
      return @{ Ok = $false; TimedOut = $false; Output = "it couldn't start: $($_.Exception.Message)" }
    }
    $process.StandardInput.Close()
    $stdout = $process.StandardOutput.ReadToEndAsync()
    $stderr = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit($Seconds * 1000)) {
      try { $process.Kill() } catch { }
      return @{ Ok = $false; TimedOut = $true; Output = '' }
    }
    # This one also waits for the output to be read.
    $process.WaitForExit()
    $output = $stdout.Result + $stderr.Result
    if ($process.ExitCode -ne 0) {
      return @{ Ok = $false; TimedOut = $false; Output = "it exited with status $($process.ExitCode): $output" }
    }
    @{ Ok = $true; TimedOut = $false; Output = $output }
  }

  function Get-Download([string]$Url, [string]$Path) {
    # Windows PowerShell 5.1 downloads slowly while it draws a progress bar.
    $ProgressPreference = 'SilentlyContinue'
    try {
      Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing -UserAgent $agent
    } catch {
      Fail "couldn't download ${Url}: $($_.Exception.Message)"
    }
  }

  # Whether the environment variable Name is 1. Anything but 1, 0 or unset
  # is refused, so a typo doesn't quietly mean no.
  function Test-Flag([string]$Name) {
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ($value -eq '1') { return $true }
    if ($value -and $value -ne '0') { Fail "set $Name to 1, or leave it unset" }
    $false
  }

  function Get-Arch {
    $names = @($env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE)
    # Windows on Arm runs x64 PowerShell under emulation, which
    # PROCESSOR_ARCHITECTURE doesn't tell; .NET 4.7.1 and later do.
    try { $names += [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
    if ($names -contains 'ARM64') { return 'arm64' }
    if ($names -contains 'AMD64' -or $names -contains 'X64') { return 'amd64' }
    if ($names -contains 'x86') { return '386' }
    Fail "pairtime has no build for this processor ($(($names | Where-Object { $_ }) -join ', '))"
  }

  # The trusted public keys, from -ReleaseKeys or from this script, by key id.
  function Read-ReleaseKeys {
    if ($ReleaseKeys) {
      if (-not (Test-Path -LiteralPath $ReleaseKeys -PathType Leaf)) { Fail "-ReleaseKeys: $ReleaseKeys isn't a file" }
      $lines = @(Get-Content -LiteralPath $ReleaseKeys)
      $from = $ReleaseKeys
    } else {
      $lines = $PinnedKeys
      $from = 'this script'
    }
    $keys = New-Object 'System.Collections.Generic.Dictionary[string,byte[]]'
    foreach ($line in $lines) {
      $line = $line.Trim()
      # Blank lines and lines starting with # don't count, as in
      # internal/release/release-keys.txt.
      if ($line -eq '' -or $line.StartsWith('#')) { continue }
      # Never print it: it's a secret.
      if ($line.StartsWith('pairtime-release-ed25519-secret:')) { Fail "$from holds a release secret key, not a public key. Keep it secret, and replace it if anyone else could have seen it." }
      if (-not $line.StartsWith($PublicPrefix)) { Fail "$from holds a line that isn't a release public key ($PublicPrefix and the key in base64)" }
      $raw = $null
      try { $raw = [Convert]::FromBase64String($line.Substring($PublicPrefix.Length)) } catch { }
      if ($null -eq $raw -or $raw.Length -ne 32) { Fail "$from holds a release public key that isn't 32 bytes in base64" }
      # A key's id is the first 8 bytes of its SHA-256, in hex.
      $keys[(Get-Sha256Hex $raw).Substring(0, 16)] = $raw
    }
    , $keys
  }

  # Checks manifest.json's bytes as docs/spec/update-manifest.md's Verifying
  # steps 1 to 4 say. Returns its payload, the id of the trusted key whose
  # signature verified or $null, and the ids it was signed with.
  function Open-Manifest([byte[]]$Data, $Keys) {
    $bad = "manifest.json isn't a pairtime update manifest in the form tools/release writes"
    if ($Data.Length -gt 1MB) { Fail "manifest.json is over 1 MiB, so it isn't a pairtime manifest" }
    $text = [System.Text.Encoding]::ASCII.GetString($Data)
    $signature = '\{"keyid":"[0-9A-Za-z._-]*","sig":"[A-Za-z0-9+/]*={0,2}"\}'
    $form = '^\{"payload":"([A-Za-z0-9+/]*={0,2})","payloadType":"' + [regex]::Escape($PayloadType) +
      '","signatures":\[(' + $signature + '(?:,' + $signature + ')*)\]\}\n\z'
    $match = [regex]::Match($text, $form)
    if (-not $match.Success) { Fail $bad }
    try { $payload = [Convert]::FromBase64String($match.Groups[1].Value) } catch { Fail $bad }

    # DSSE's pre-authentication encoding of the payload is what was signed.
    $head = [System.Text.Encoding]::ASCII.GetBytes("DSSEv1 $($PayloadType.Length) $PayloadType $($payload.Length) ")
    $signed = [byte[]]($head + $payload)

    # The key id only picks which key to try (DSSE); any one signature that
    # verifies under a trusted key is enough.
    $signer = $null
    $signedBy = @()
    foreach ($m in [regex]::Matches($match.Groups[2].Value, '\{"keyid":"([^"]*)","sig":"([^"]*)"\}')) {
      $id = $m.Groups[1].Value
      $signedBy += $id
      if (-not $Keys.ContainsKey($id)) { continue }
      $sig = $null
      try { $sig = [Convert]::FromBase64String($m.Groups[2].Value) } catch { }
      if ($null -ne $sig -and (Test-Ed25519 $Keys[$id] $signed $sig)) {
        $signer = $id
        break
      }
    }
    @{ Payload = $payload; Signer = $signer; SignedBy = $signedBy }
  }

  # Unpacks pairtime.exe, and nothing else, from the zip archive.
  function Expand-Binary([string]$Zip, [string]$Destination) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($Zip)
    try {
      $entries = @($archive.Entries | Where-Object { $_.FullName -ceq 'pairtime.exe' })
      if ($entries.Count -ne 1) { Fail "$(Split-Path -Leaf $Zip) has no pairtime.exe" }
      [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entries[0], $Destination, $false)
    } finally {
      $archive.Dispose()
    }
  }

  # Puts New at Target. Windows can't replace or delete a pairtime.exe that
  # is running, such as the agent, but it can rename it, so an old one
  # moves aside first and goes once nothing runs it.
  function Move-IntoPlace([string]$New, [string]$Target) {
    $old = $null
    if (Test-Path -LiteralPath $Target) {
      $dir = Split-Path -Parent $Target
      $old = Join-Path $dir 'pairtime.old.exe'
      Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
      if (Test-Path -LiteralPath $old) { $old = Join-Path $dir "pairtime.old.$PID.exe" }
      Move-Item -LiteralPath $Target -Destination $old
    }
    try {
      Move-Item -LiteralPath $New -Destination $Target
    } catch {
      if ($old) { Move-Item -LiteralPath $old -Destination $Target -ErrorAction SilentlyContinue }
      Fail "couldn't move pairtime to ${Target}: $($_.Exception.Message)"
    }
    if ($old) { Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue }
  }

  function Install-Pairtime {
    if ($PSVersionTable.PSVersion.Major -lt 5) { Fail 'pairtime needs Windows PowerShell 5.1 or PowerShell 7' }
    if ([System.Environment]::OSVersion.Platform -ne 'Win32NT') { Fail 'install.ps1 is for Windows; on macOS and Linux, use install.sh' }
    $allowUnsigned = $AllowUnsigned.IsPresent -or (Test-Flag 'PAIRTIME_ALLOW_UNSIGNED')
    $noInstall = $NoInstall.IsPresent -or (Test-Flag 'PAIRTIME_NO_INSTALL')

    # Assigning a parameter here makes a copy local to this function.
    if (-not $Version) { $Version = $PinnedVersion }
    if (-not $Version) { Fail "this copy of install.ps1 doesn't name a release; set `$env:PAIRTIME_VERSION first, such as `$env:PAIRTIME_VERSION = 'v0.1.0'" }
    if ($Version -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?\z') {
      Fail "$Version isn't a pairtime version, such as v0.1.0 or v0.1.0-alpha.1"
    }
    if (-not $BaseUrl) { $BaseUrl = "$Repo/releases/download/$Version" }
    $BaseUrl = $BaseUrl.TrimEnd('/')
    # https, or http on this computer, which tests serve from.
    if ($BaseUrl -cnotmatch '^(https://[A-Za-z0-9.-]+|http://(127\.0\.0\.1|localhost))(:[0-9]+)?(/[A-Za-z0-9._~%+-]*)*\z') {
      Fail 'the base URL must be an https URL, without user names, queries or spaces'
    }
    if (-not $env:LOCALAPPDATA) { Fail "LOCALAPPDATA isn't set, so there's no folder of yours to install into" }
    $agent = "pairtime-install.ps1/$Version (+$Repo)"
    # GitHub needs TLS 1.2, which older setups of Windows PowerShell 5.1
    # don't offer unless asked. This adds it to what's allowed.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $arch = Get-Arch
    $keys = Read-ReleaseKeys
    if ($keys.Count -eq 0 -and -not $allowUnsigned) {
      Fail "this script trusts no release key, so it can't check that pairtime $Version is pairtime's. Releases made before pairtime had a release key are unsigned, and their notes say so. To install one anyway, set `$env:PAIRTIME_ALLOW_UNSIGNED = '1' first: the download is then checked only against the SHA-256s in its own manifest."
    }

    $binDir = Join-Path $env:LOCALAPPDATA 'pairtime\bin'
    $target = Join-Path $binDir 'pairtime.exe'
    $work = Join-Path ([System.IO.Path]::GetTempPath()) ('pairtime-install-' + [guid]::NewGuid().ToString('N'))
    $new = $null
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
      Say "Installing pairtime $Version for windows/$arch."
      $manifestPath = Join-Path $work 'manifest.json'
      Get-Download "$BaseUrl/manifest.json" $manifestPath
      $opened = Open-Manifest ([System.IO.File]::ReadAllBytes($manifestPath)) $keys
      $trusted = @($keys.Keys) -join ', '
      if (-not $trusted) { $trusted = 'none' }
      $signedBy = $opened.SignedBy -join ', '
      if ($opened.Signer) {
        Say "manifest.json is signed by release key $($opened.Signer)."
      } elseif ($allowUnsigned) {
        Write-Warning "no key this script trusts signed manifest.json (signed by: $signedBy; trusted: $trusted). PAIRTIME_ALLOW_UNSIGNED is set, so the install goes on, but only the manifest's SHA-256s check the download: nothing shows the files are pairtime's."
      } else {
        Fail "manifest.json isn't signed by a key this script trusts (signed by: $signedBy; trusted: $trusted), so nothing was installed."
      }

      $manifest = [System.Text.Encoding]::UTF8.GetString($opened.Payload) | ConvertFrom-Json
      # The manifest must be the requested release's, so an older signed one
      # can't stand in for it.
      if ([string]$manifest.version -cne $Version) { Fail "manifest.json is for pairtime $($manifest.version), not $Version, so nothing was installed" }
      $artifacts = @($manifest.artifacts | Where-Object { $_.os -ceq 'windows' -and $_.arch -ceq $arch })
      if ($artifacts.Count -eq 0) { Fail "pairtime $Version has no build for windows/$arch" }
      if ($artifacts.Count -gt 1) { Fail "manifest.json lists windows/$arch more than once" }
      $artifact = $artifacts[0]
      foreach ($hash in @($artifact.sha256, $artifact.binary_sha256)) {
        if ([string]$hash -cnotmatch '^[0-9a-f]{64}\z') { Fail "manifest.json's SHA-256 for windows/$arch isn't 64 lowercase hex digits" }
      }
      if ([string]$artifact.size -cnotmatch '^[0-9]+\z') { Fail "manifest.json's size for windows/$arch isn't a number" }
      $name = "pairtime_$($Version.Substring(1))_windows_$arch.zip"
      $url = [string]$artifact.url
      $urlName = $url.Substring($url.LastIndexOf('/') + 1)
      if ($urlName -cne $name) { Fail "manifest.json's url for windows/$arch names $urlName, not $name" }

      $zip = Join-Path $work $name
      Get-Download "$BaseUrl/$name" $zip
      $got = (Get-Item -LiteralPath $zip).Length
      if ($got -ne [long]$artifact.size) { Fail "$name is $got bytes, but manifest.json says $($artifact.size), so it was changed or damaged on the way. Nothing was installed." }
      if ((Get-FileSha256 $zip) -cne $artifact.sha256) { Fail "$name doesn't have the SHA-256 manifest.json lists, so it was changed or damaged on the way. Nothing was installed." }
      $unpacked = Join-Path $work 'pairtime.exe'
      Expand-Binary $zip $unpacked
      if ((Get-FileSha256 $unpacked) -cne $artifact.binary_sha256) { Fail "the pairtime.exe in $name doesn't have the SHA-256 manifest.json lists. Nothing was installed." }
      Say "$name and the pairtime.exe in it match manifest.json."

      # The binary waits beside its place until it has shown it runs here.
      New-Item -ItemType Directory -Force -Path $binDir | Out-Null
      if (Test-Path -LiteralPath $target -PathType Container) { Fail "$target is a folder, so pairtime can't go there" }
      $new = Join-Path $binDir ".pairtime.new.$PID.exe"
      Copy-Item -LiteralPath $unpacked -Destination $new -Force
      if ((Get-FileSha256 $new) -cne $artifact.binary_sha256) { Fail "the copy in $binDir doesn't match manifest.json; is the disk full?" }
      $run = Invoke-WithTimeout $new 'version' $VersionTimeout
      if ($run.TimedOut) { Fail "the new pairtime didn't finish 'pairtime version' within $VersionTimeout seconds, so it wasn't installed. Any pairtime you had is still there." }
      if (-not $run.Ok) { Fail "the new pairtime doesn't run on this computer ('pairtime version': $(Get-Start $run.Output 300)), so it wasn't installed. Any pairtime you had is still there." }
      $first = ($run.Output -split "`r?`n")[0]
      if (-not $first.StartsWith("pairtime $Version ")) { Fail "the new binary says it's $(Get-Start $first 200), not pairtime $Version, so it wasn't installed" }
      Move-IntoPlace $new $target
      $new = $null
      Say "Installed pairtime $Version at $target."
    } finally {
      if ($new -and (Test-Path -LiteralPath $new)) { Remove-Item -LiteralPath $new -Force -ErrorAction SilentlyContinue }
      Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }

    if ($noInstall) {
      Say "To finish setting it up, run: & '$target' install"
    } elseif (@(& $target help) -cmatch '^  install ') {
      Say 'Running pairtime install. It lists every change it would make and asks before making any.'
      $installArgs = @('install')
      if ($Yes) { $installArgs += '--yes' }
      & $target @installArgs
      if ($LASTEXITCODE -ne 0) { Fail "'pairtime install' stopped with status $LASTEXITCODE. pairtime is at $target; run & '$target' install to try again." }
    } else {
      Say 'This build of pairtime has no install command yet, so setup stops here.'
    }

    if (-not (@($env:Path -split ';') -contains $binDir)) {
      Say "$binDir isn't on your PATH. To run pairtime by name, run this once, then open a new terminal:"
      Say "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$binDir', 'User')"
    }
  }

  if ($env:PAIRTIME_INSTALL_PS1_TEST -ne 'functions') {
    Install-Pairtime
  }
}

# Tests dot-source this file with PAIRTIME_INSTALL_PS1_TEST=functions to call
# its functions (install/e2e/test.ps1).
try {
  if ($env:PAIRTIME_INSTALL_PS1_TEST -eq 'functions') {
    . $installPairtime
  } else {
    & $installPairtime @args
  }
} finally {
  Remove-Variable -Name installPairtime -ErrorAction SilentlyContinue
}
