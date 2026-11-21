param(
    [Parameter(Mandatory=$true)][string]$JavaHome,
    [Parameter(Mandatory=$true)][string]$BuildTools,
    [Parameter(Mandatory=$true)][string]$UnsignedApk,
    [Parameter(Mandatory=$true)][string]$Keystore,
    [Parameter(Mandatory=$true)][string]$PasswordFile,
    [Parameter(Mandatory=$true)][string]$OutputApk
)
$ErrorActionPreference = 'Stop'
$env:JAVA_HOME=$JavaHome
$output=[IO.Path]::GetFullPath($OutputApk)
New-Item -ItemType Directory -Force ([IO.Path]::GetDirectoryName($output)) | Out-Null
$aligned="$output.aligned"
& "$BuildTools/zipalign.exe" -P 16 -f 4 $UnsignedApk $aligned
if ($LASTEXITCODE -ne 0) { throw 'zipalign failed' }
& "$BuildTools/apksigner.bat" sign --ks $Keystore --ks-key-alias ddremote --ks-pass "file:$PasswordFile" --out $output $aligned
if ($LASTEXITCODE -ne 0) { throw 'APK signing failed' }
& "$BuildTools/apksigner.bat" verify --verbose --print-certs $output
if ($LASTEXITCODE -ne 0) { throw 'APK signature verification failed' }
& "$BuildTools/zipalign.exe" -c -P 16 4 $output
if ($LASTEXITCODE -ne 0) { throw 'Signed APK alignment verification failed' }
Remove-Item -LiteralPath $aligned
Get-FileHash -LiteralPath $output -Algorithm SHA256 | ConvertTo-Json
