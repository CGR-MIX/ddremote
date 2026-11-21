param(
    [Parameter(Mandatory=$true)][string]$JavaHome,
    [Parameter(Mandatory=$true)][string]$SdkRoot,
    [Parameter(Mandatory=$true)][string]$GradleExe,
    [Parameter(Mandatory=$true)][string]$StageRoot
)
$ErrorActionPreference = 'Stop'
if (!(Test-Path -LiteralPath "$JavaHome/bin/java.exe")) { throw 'JDK not found' }
if (!(Test-Path -LiteralPath $GradleExe)) { throw 'Gradle executable not found' }
if (!(Test-Path -LiteralPath "$SdkRoot/build-tools/37.0.0/apksigner.bat")) { throw 'Android Build Tools 37.0.0 not found' }
$stage = [IO.Path]::GetFullPath($StageRoot)
$source = [IO.Path]::GetFullPath($PSScriptRoot)
if ($stage -eq [IO.Path]::GetPathRoot($stage) -or $stage -eq $source) { throw 'Choose a dedicated staging directory' }
if ($stage -match '[^\x00-\x7f]') { throw 'Use an ASCII staging path for NDK compatibility' }
New-Item -ItemType Directory -Force "$stage/source","$stage/gradle-home","$stage/tmp","$stage/user-home","$stage/android-user-home" | Out-Null
robocopy $source "$stage/source" /E /XD .git .gradle build .cxx /NFL /NDL /NJH /NJS /NC /NS /NP
if ($LASTEXITCODE -ge 8) { throw 'Copying source failed' }
$sdk = [IO.Path]::GetFullPath($SdkRoot).Replace('\','/').Replace(':','\:')
Set-Content -LiteralPath "$stage/source/local.properties" -Encoding utf8 -Value "sdk.dir=$sdk"
$env:JAVA_HOME=$JavaHome
$env:ANDROID_HOME=$SdkRoot
$env:ANDROID_USER_HOME="$stage/android-user-home"
$env:GRADLE_USER_HOME="$stage/gradle-home"
$env:TEMP="$stage/tmp"
$env:TMP="$stage/tmp"
$jvm="-Xmx3072m -Dfile.encoding=UTF-8 -Djava.io.tmpdir=$($stage.Replace('\','/'))/tmp -Duser.home=$($stage.Replace('\','/'))/user-home"
& $GradleExe -p "$stage/source" "-Dorg.gradle.jvmargs=$jvm" :app:assembleNonRootRelease :app:lintNonRootRelease --no-daemon --console=plain *> "$stage/build.log"
$buildExit = $LASTEXITCODE
Get-Content -LiteralPath "$stage/build.log" -Tail 80
if ($buildExit -ne 0) { throw "Android build failed. Full log: $stage/build.log" }
Write-Output "Unsigned APK: $stage/source/app/build/outputs/apk/nonRoot/release/app-nonRoot-release-unsigned.apk"
