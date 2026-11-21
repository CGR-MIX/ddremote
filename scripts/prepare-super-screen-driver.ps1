[CmdletBinding()]
param([switch]$Install)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$driver = Join-Path $root 'research/upstream/virtual-display/driver-25.7.23/VirtualDisplayDriver'
$expected = @{
 'MttVDD.inf'='550d211fe481e74dfe3f9d724ed78be48b3a9113405965d683d9373e8d672f5d'
 'MttVDD.dll'='c9ca837f57a98fbd43bc416a7f535a95843626e7759eaf85cf0cd7ce334dbb05'
 'mttvdd.cat'='08a0093fc9b2e32b287a6f8a77ca4de0a31830d29fc33d2b13a918dc859468f6'
}
foreach ($name in $expected.Keys) {
 $path = Join-Path $driver $name
 if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected[$name]) { throw "Driver hash mismatch: $name" }
}
foreach ($name in @('MttVDD.dll','mttvdd.cat')) {
 $signature = Get-AuthenticodeSignature -LiteralPath (Join-Path $driver $name)
 if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'CN=SignPath Foundation') { throw "Driver signature is not the verified SignPath release: $name" }
}
Write-Output 'Verified: Virtual Display Driver 25.7.23, INF/DLL/CAT pinned hashes, valid SignPath signatures.'
Write-Output 'Install only stages the driver package in Windows DriverStore; it does not create a monitor, modify display layouts, import certificates, or change driver signing policy.'
if (-not $Install) { Write-Output 'Read-only validation complete. Installation was not requested.'; return }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not ([Security.Principal.WindowsPrincipal]::new($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
 throw 'Run this script in an administrator PowerShell with -Install; no elevation or driver install was performed.'
}
& "$env:WINDIR/System32/pnputil.exe" /add-driver (Join-Path $driver 'MttVDD.inf')
if ($LASTEXITCODE -notin @(0,3010)) { throw "Windows rejected staging this signed driver ($LASTEXITCODE). Driver signing policy was not changed." }
Write-Output 'Driver package staged. DD creates its own temporary SWD\DDRemote device only when a super-screen session starts.'
