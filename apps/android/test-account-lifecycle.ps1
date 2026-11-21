param(
    [Parameter(Mandatory=$true)][string]$JavaHome,
    [string]$StageRoot='D:\DSHworkspace\_toolchain\ddremote-android-build'
)
$ErrorActionPreference='Stop'
$evidence=Join-Path $PSScriptRoot 'build-evidence'
$testOut=Join-Path $StageRoot 'session-tests'
New-Item -ItemType Directory -Force $evidence,$testOut | Out-Null
$env:GOCACHE=Join-Path $StageRoot 'go-cache'
$env:GOTMPDIR=Join-Path $StageRoot 'tmp'
Push-Location (Join-Path $PSScriptRoot 'network')
try {
    go test -count=1 -v ./... 2>&1 | Tee-Object -FilePath (Join-Path $evidence 'embedded-network-tests.txt')
    if ($LASTEXITCODE -ne 0) {throw 'Embedded network lifecycle tests failed'}
} finally {Pop-Location}
& "$JavaHome/bin/javac.exe" -encoding UTF-8 -d $testOut `
    (Join-Path $PSScriptRoot 'app/src/main/java/com/limelight/ddremote/SessionGate.java') `
    (Join-Path $PSScriptRoot 'tests/SessionGateTest.java')
if ($LASTEXITCODE -ne 0) {throw 'Compiling production session guard test failed'}
& "$JavaHome/bin/java.exe" -cp $testOut com.limelight.ddremote.SessionGateTest 2>&1 | Tee-Object -FilePath (Join-Path $evidence 'account-generation-tests.txt')
if ($LASTEXITCODE -ne 0) {throw 'Production session guard tests failed'}
