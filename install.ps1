# Installs the launchd command on Windows: irm <address of this file> | iex
$ErrorActionPreference = 'Stop'

$dir = Join-Path $env:LOCALAPPDATA 'Programs\launchd'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'launchd.exe'
Invoke-WebRequest -UseBasicParsing 'https://github.com/waywho/launchd-cli/releases/latest/download/launchd-windows-amd64.exe' -OutFile $exe
Write-Host "Installed $exe"

$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($path -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'User')
  Write-Host 'Open a new terminal, then run launchd login'
} else {
  Write-Host 'Next step: run launchd login'
}
