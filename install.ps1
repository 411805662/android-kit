# android-kit installer for Windows
# Usage:
#   irm https://cdn.jsdelivr.net/gh/411805662/android-kit@main/install.ps1 | iex

$ErrorActionPreference = "Stop"
$Repo = "411805662/android-kit"
$Version = if ($env:ANDROID_KIT_VERSION) { $env:ANDROID_KIT_VERSION } else { "latest" }
$Prefix = if ($env:ANDROID_KIT_PREFIX) { $env:ANDROID_KIT_PREFIX } else { Join-Path $env:LOCALAPPDATA "Programs\android-kit" }
$Aliases = if ($env:ANDROID_KIT_ALIASES) { $env:ANDROID_KIT_ALIASES } else { "none" }

$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { throw "64-bit Windows is required" }
$Asset = "android-kit_windows_$Arch.zip"
$Base = if ($Version -eq "latest") { "https://github.com/$Repo/releases/latest/download" } else { "https://github.com/$Repo/releases/download/$Version" }

$Tmp = Join-Path $env:TEMP ("android-kit-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Tmp | Out-Null

Write-Host "android-kit installer"
Write-Host "  version: $Version"
Write-Host "  prefix:  $Prefix"

$Zip = Join-Path $Tmp $Asset
Write-Host "-> downloading $Asset"
Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $Zip
Expand-Archive -Path $Zip -DestinationPath $Tmp -Force

New-Item -ItemType Directory -Path $Prefix -Force | Out-Null
Copy-Item (Join-Path $Tmp "android-kit.exe") (Join-Path $Prefix "android-kit.exe") -Force

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$Prefix*") {
  [Environment]::SetEnvironmentVariable("Path", "$UserPath;$Prefix", "User")
  Write-Host "added $Prefix to user PATH (restart terminal)"
}

& (Join-Path $Prefix "android-kit.exe") setup --yes --shell auto --aliases $Aliases
Remove-Item -Recurse -Force $Tmp

Write-Host ""
Write-Host "Done. Next:"
Write-Host "  ak project add C:\path\to\project --default"
Write-Host "  ak doctor"
