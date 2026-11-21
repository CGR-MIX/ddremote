[CmdletBinding()]
param([string]$GoExecutable = 'go', [switch]$SkipTests, [string]$OutputPath)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$source = Join-Path $root 'apps/windows'
$output = if ($OutputPath) { [IO.Path]::GetFullPath($OutputPath) } else { Join-Path $root 'dist/DDRemote-Windows-x64.exe' }
New-Item -ItemType Directory -Force -Path (Split-Path $output) | Out-Null
foreach ($component in @('sunshine', 'moonlight')) {
    if (-not (Test-Path -LiteralPath (Join-Path $source "payload/$component.zip"))) {
        throw "Missing payload/$component.zip. Run scripts/download-payload.ps1 with the pinned assets first."
    }
}
$oldOS, $oldArch, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    if (-not $SkipTests) {
        & $GoExecutable -C $source test ./... -count=1 -timeout=90s
        if ($LASTEXITCODE -ne 0) { throw 'Windows tests failed' }
    }
    & $GoExecutable -C $source build -trimpath -ldflags '-H=windowsgui -s -w' -o $output .
    if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
    $hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $([IO.Path]::GetFileName($output))" | Set-Content -LiteralPath "$output.sha256" -Encoding ascii
    Get-Item -LiteralPath $output | Select-Object FullName,Length
    Write-Output "SHA256: $hash"
} finally {
    $env:GOOS = $oldOS; $env:GOARCH = $oldArch; $env:CGO_ENABLED = $oldCGO
}
