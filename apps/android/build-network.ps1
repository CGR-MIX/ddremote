param(
    [Parameter(Mandatory=$true)][string]$NdkRoot,
    [string]$StageRoot='D:\DSHworkspace\_toolchain\ddremote-android-build',
    [string[]]$Abis=@('arm64-v8a','armeabi-v7a','x86','x86_64')
)
$ErrorActionPreference='Stop'
$source=Join-Path $PSScriptRoot 'network'
$stage=Join-Path $StageRoot 'network'
New-Item -ItemType Directory -Force $stage | Out-Null
robocopy $source $stage /E /XD .git /NFL /NDL /NJH /NJS /NP | Out-Null
if ($LASTEXITCODE -ge 8) {throw 'Cannot stage network source'}
$env:GOCACHE=Join-Path $StageRoot 'go-cache'
$env:GOTMPDIR=Join-Path $StageRoot 'tmp'
$env:GOOS='android'
$env:CGO_ENABLED='1'
$toolchain=Join-Path $NdkRoot 'toolchains/llvm/prebuilt/windows-x86_64/bin'
$targets=@{
    'arm64-v8a'=@('arm64','aarch64-linux-android21-clang.cmd');
    'armeabi-v7a'=@('arm','armv7a-linux-androideabi21-clang.cmd');
    'x86'=@('386','i686-linux-android21-clang.cmd');
    'x86_64'=@('amd64','x86_64-linux-android21-clang.cmd')
}
Push-Location $stage
try {
    go mod tidy
    if ($LASTEXITCODE -ne 0) {throw 'Resolving Go dependencies failed'}
    Copy-Item -LiteralPath 'go.sum' -Destination (Join-Path $source 'go.sum') -Force
    Copy-Item -LiteralPath 'go.mod' -Destination (Join-Path $source 'go.mod') -Force
    foreach ($abi in $Abis) {
        $env:GOARCH=$targets[$abi][0]
        $env:CC=Join-Path $toolchain $targets[$abi][1]
        if ($abi -eq 'armeabi-v7a') {$env:GOARM='7'} else {Remove-Item Env:GOARM -ErrorAction SilentlyContinue}
        $out=Join-Path $stage "out/$abi/libddnetwork.so"
        New-Item -ItemType Directory -Force (Split-Path -Parent $out) | Out-Null
        go build -buildmode=c-shared -trimpath -ldflags '-s -w -extldflags=-Wl,-z,max-page-size=16384' -o $out .
        if ($LASTEXITCODE -ne 0) {throw "Network build failed for $abi"}
        $destination=Join-Path $PSScriptRoot "app/src/main/jniLibs/$abi"
        New-Item -ItemType Directory -Force $destination | Out-Null
        Copy-Item -LiteralPath $out -Destination $destination -Force
        Write-Output "Built embedded network: $abi"
    }
} finally {Pop-Location}
