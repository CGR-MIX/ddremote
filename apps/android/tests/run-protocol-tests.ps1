param(
    [string]$JavaHome = $env:JAVA_HOME,
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '../build/protocol-tests')
)
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$testRoot = (Resolve-Path $OutputDirectory).Path
$sourceRoot = (Resolve-Path (Join-Path $PSScriptRoot '../app/src/main/java/com/limelight/ddremote')).Path
$keystore = Join-Path $testRoot 'test-host.p12'
if (!(Test-Path -LiteralPath $keystore)) {
    & "$JavaHome/bin/keytool.exe" -genkeypair -keystore $keystore -storetype PKCS12 -storepass local-test-only -keypass local-test-only -alias host -keyalg RSA -keysize 2048 -validity 2 -dname 'CN=localhost' -ext 'SAN=ip:127.0.0.1,dns:localhost' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Test certificate creation failed' }
}
& "$JavaHome/bin/javac.exe" -encoding UTF-8 -d $testRoot "$sourceRoot/CompanionLink.java" "$sourceRoot/CompanionClient.java" (Join-Path $PSScriptRoot 'CompanionProtocolTest.java')
if ($LASTEXITCODE -ne 0) { throw 'Test compilation failed' }
& "$JavaHome/bin/java.exe" -cp $testRoot com.limelight.ddremote.CompanionProtocolTest $keystore
if ($LASTEXITCODE -ne 0) { throw 'Protocol tests failed' }
