[CmdletBinding()]
param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '../dist/source'),
    [ValidateSet('Sunshine','MoonlightQt','MoonlightQtDeps','MoonlightAndroid','Tailscale','Airhand')]
    [string[]]$Components = @('Sunshine','MoonlightQt','MoonlightQtDeps','MoonlightAndroid','Tailscale'),
    [switch]$SkipUpstream,
    [switch]$SkipDDSource,
    [switch]$IncludeGoModules,
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$')]
    [string]$SourceVersion,
    [string]$GoExecutable = 'go',
    [string]$PythonExecutable = 'python'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$workRoot = Join-Path $outputRoot 'checkouts'
$pins = @(
    @{ Name='Sunshine'; Repository='https://github.com/SunshineStream/Sunshine.git'; Release='v2026.914.233613'; Commit='63d35f702ee9e362e43263742981836ec0710384' },
    @{ Name='MoonlightQt'; Repository='https://github.com/moonlight-stream/moonlight-qt.git'; Release='v6.2.0'; Commit='de2467e433821664cdd2224aad8c89a625be1ad9' },
    @{ Name='MoonlightQtDeps'; Repository='https://github.com/moonlight-stream/moonlight-qt-deps.git'; Release='v19'; Commit='79621271459ebb945343f58d7acd6ad24d402c38' },
    @{ Name='MoonlightAndroid'; Repository='https://github.com/moonlight-stream/moonlight-android.git'; Release='v12.2'; Commit='b48494cb96bff23d8886c4775cc4f39a1075495d' },
    @{ Name='Tailscale'; Repository='https://github.com/tailscale/tailscale.git'; Release='v1.104.7'; Commit='cbc74fe48ef1b1649f69a4d9a602319427e25102' },
    @{ Name='Airhand'; Repository='https://github.com/sqlpxc/airhand.git'; Release='reference-only'; Commit='48d9ac8a13985f24409629028d6eb576dfc6e1b9' }
)

function Invoke-Checked {
    param([string]$File, [string[]]$Arguments)
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$File failed with exit code $LASTEXITCODE" }
}
function Write-ArchiveHash {
    param([string]$Archive)
    $hash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $([IO.Path]::GetFileName($Archive))" | Set-Content -LiteralPath "$Archive.sha256" -Encoding ascii
}

if (-not $SkipUpstream) {
    New-Item -ItemType Directory -Force -Path $workRoot | Out-Null
    foreach ($pin in $pins) {
        if ($pin.Name -notin $Components) { continue }
        $checkout = Join-Path $workRoot $pin.Name
        if (-not (Test-Path -LiteralPath (Join-Path $checkout '.git'))) {
            if ((Test-Path -LiteralPath $checkout) -and @(Get-ChildItem -LiteralPath $checkout -Force).Count) {
                throw "Refusing to initialize nonempty directory $checkout"
            }
            Invoke-Checked git @('init', $checkout)
            Invoke-Checked git @('-C', $checkout, 'config', 'core.longpaths', 'true')
            Invoke-Checked git @('-C', $checkout, 'remote', 'add', 'origin', $pin.Repository)
        }
        $origin = (& git -C $checkout remote get-url origin).Trim()
        if ($LASTEXITCODE -ne 0 -or $origin -ne $pin.Repository) { throw "Unexpected repository in $checkout" }
        $dirty = @(& git -C $checkout status --porcelain)
        if ($LASTEXITCODE -ne 0 -or $dirty.Count) { throw "Refusing to replace modified checkout $checkout" }
        Invoke-Checked git @('-C', $checkout, 'fetch', '--depth', '1', 'origin', $pin.Commit)
        Invoke-Checked git @('-C', $checkout, 'checkout', '--detach', $pin.Commit)
        $actual = (& git -C $checkout rev-parse HEAD).Trim()
        if ($actual -ne $pin.Commit) { throw "Commit verification failed for $($pin.Name)" }
        Invoke-Checked git @('-C', $checkout, '-c', 'core.longpaths=true', 'submodule', 'update', '--init', '--recursive', '--depth', '1', '--jobs', '4')
        $submodules = @(& git -C $checkout submodule status --recursive)
        if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate submodules for $($pin.Name)" }
        if (@($submodules | Where-Object { $_ -match '^[+-U]' }).Count) { throw "Incomplete or mismatched submodule in $($pin.Name)" }
        $manifest = [ordered]@{ name=$pin.Name; repository=$pin.Repository; release=$pin.Release; commit=$actual; submodules=$submodules }
        $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $outputRoot "$($pin.Name)-source.json") -Encoding utf8
        $archive = Join-Path $outputRoot "$($pin.Name)-$($pin.Commit.Substring(0,12))-source.tar.gz"
        Invoke-Checked $PythonExecutable @((Join-Path $PSScriptRoot 'archive-source.py'), '--root', $workRoot, '--output', $archive, $pin.Name)
        Write-ArchiveHash $archive
    }
}

if ($IncludeGoModules) {
    $goRoot = Join-Path $repoRoot 'apps/windows'
    $moduleRoot = Join-Path $outputRoot 'go-modules'
    New-Item -ItemType Directory -Force -Path $moduleRoot | Out-Null
    # The selected versions come from the shipped go.mod/go.sum, never @latest.
    $modules = @(& $GoExecutable -C $goRoot list -deps '-f={{with .Module}}{{if .Version}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./... | Where-Object { $_ } | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0) { throw 'go list failed' }
    $androidGoRoot = Join-Path $repoRoot 'apps/android/network'
    if (Test-Path -LiteralPath (Join-Path $androidGoRoot 'go.mod')) {
        $previousGoOS, $previousGoArch, $previousCgo = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
        try {
            $env:GOOS = 'android'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '1'
            $modules += @(& $GoExecutable -C $androidGoRoot list -deps '-f={{with .Module}}{{if .Version}}{{.Path}}@{{.Version}}{{end}}{{end}}' tailscale.com/tsnet | Where-Object { $_ })
            if ($LASTEXITCODE -ne 0) { throw 'Cannot list Android tsnet dependencies' }
        } finally {
            $env:GOOS = $previousGoOS; $env:GOARCH = $previousGoArch; $env:CGO_ENABLED = $previousCgo
        }
        $modules = @($modules | Sort-Object -Unique)
    }
    $moduleManifest = @()
    $moduleInputs = @()
    foreach ($module in $modules) {
        $json = (& $GoExecutable -C $goRoot mod download -json $module) -join "`n"
        if ($LASTEXITCODE -ne 0) { throw "go mod download failed: $module" }
        $download = $json | ConvertFrom-Json
        $safeName = ($module -replace '[^A-Za-z0-9._@-]', '_')
        $moduleDir = Join-Path $moduleRoot $safeName
        $moduleInputs += "go-modules/$safeName"
        New-Item -ItemType Directory -Force -Path $moduleDir | Out-Null
        foreach ($property in @('Zip','GoMod','Info')) {
            $file = $download.$property
            if (-not (Test-Path -LiteralPath $file)) { throw "Missing $property for $module" }
            Copy-Item -LiteralPath $file -Destination $moduleDir -Force
        }
        $moduleManifest += [ordered]@{ module=$module; sum=$download.Sum; goModSum=$download.GoModSum; files=@(Get-ChildItem -LiteralPath $moduleDir -File | ForEach-Object { @{ name=$_.Name; sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant() } }) }
    }
    $moduleManifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $outputRoot 'go-module-sources.json') -Encoding utf8
    $archive = Join-Path $outputRoot 'DDRemote-go-module-sources.tar.gz'
    Invoke-Checked $PythonExecutable (@((Join-Path $PSScriptRoot 'archive-source.py'), '--root', $outputRoot, '--output', $archive, 'go-module-sources.json') + $moduleInputs)
    Write-ArchiveHash $archive
}

if (-not $SkipDDSource) {
    # Deliberate allowlist: research/ (including all proprietary UU artifacts),
    # dist/, .tools/, local application state and signing keys are never input.
    $inputs = @('apps','scripts','docs','LICENSE','README.md','.gitignore') | Where-Object { Test-Path -LiteralPath (Join-Path $repoRoot $_) }
    $versionSource = Get-Content -LiteralPath (Join-Path $repoRoot 'apps/windows/main.go') -Raw
    if ($versionSource -notmatch '(?m)^const Version = "([0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?)"\s*$') {
        throw 'Cannot determine the DDRemote version from apps/windows/main.go'
    }
    $archiveVersion = if ($SourceVersion) { $SourceVersion } else { $Matches[1] }
    $archive = Join-Path $outputRoot "DDRemote-$archiveVersion-source.tar.gz"
    Invoke-Checked $PythonExecutable (@((Join-Path $PSScriptRoot 'archive-source.py'), '--root', $repoRoot, '--output', $archive, '--dd-source') + $inputs)
    Write-ArchiveHash $archive
}
Write-Output "Source archives and SHA-256 files are in $outputRoot"
Write-Output 'See docs/third-party.md for upstream binary dependency provenance and known source limitations.'
