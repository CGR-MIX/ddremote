param([ValidateSet('sunshine','moonlight')][string]$Component)
$ErrorActionPreference='Stop'
$payloadDir=Join-Path $PSScriptRoot '../apps/windows/payload'
$source=Get-Content -LiteralPath (Join-Path $payloadDir "$Component.source.json") -Raw | ConvertFrom-Json
$partsDir=Join-Path $PSScriptRoot "../.tools/payload-parts/$Component"
New-Item -ItemType Directory -Force -Path $partsDir | Out-Null
$uri=$source.browser_download_url
$size=[long]$source.size
$chunk=512KB
$count=[int][Math]::Ceiling($size/$chunk)
0..($count-1) | ForEach-Object -ThrottleLimit 24 -Parallel {
 $index=$_; $start=[long]$index*$using:chunk; $end=[Math]::Min($using:size-1,$start+$using:chunk-1)
 $part=Join-Path $using:partsDir ('{0:d4}.part' -f $index); $expected=$end-$start+1
 if ((Test-Path -LiteralPath $part) -and (Get-Item -LiteralPath $part).Length -eq $expected){return}
 & curl.exe --silent --show-error --fail --location --retry 3 --connect-timeout 20 --max-time 300 --range "$start-$end" --output $part $using:uri
 if ($LASTEXITCODE -ne 0 -or (Get-Item -LiteralPath $part).Length -ne $expected){throw "Chunk $index failed"}
 Write-Output "Downloaded $index / $using:count"
}
$zip=Join-Path $payloadDir "$Component.parallel.zip"
$out=[IO.File]::Create($zip)
try{for($i=0;$i -lt $count;$i++){ $part=Join-Path $partsDir ('{0:d4}.part' -f $i);$inputStream=[IO.File]::OpenRead($part);try{$inputStream.CopyTo($out)}finally{$inputStream.Dispose()} }}finally{$out.Dispose()}
$hash=(Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
if("sha256:$hash" -ne $source.digest){throw "SHA256 mismatch: $hash"}
Write-Output "$Component verified $hash"
