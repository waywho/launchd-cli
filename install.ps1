# Installs the golaunchd command on Windows: irm <address of this file> | iex
$ErrorActionPreference = 'Stop'

$dir = Join-Path $env:LOCALAPPDATA 'Programs\golaunchd'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'golaunchd.exe'
Invoke-WebRequest -UseBasicParsing 'https://github.com/waywho/launchd-cli/releases/latest/download/golaunchd-windows-amd64.exe' -OutFile $exe

$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($path -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'User')
}

$machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$env:Path = "$machinePath;$userPath"
$found = powershell -NoProfile -Command 'if (Get-Command golaunchd -ErrorAction SilentlyContinue) { "yes" }'
if ($found -eq 'yes') {
  Write-Host 'golaunchd is installed. Next step: run golaunchd login'
} else {
  Write-Host 'golaunchd is installed. Next step: open a new terminal and run golaunchd login'
}
