#!/bin/sh
# Installs the golaunchd command on macOS or Linux: curl -fsSL <address of this file> | sh
set -eu

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "golaunchd: this computer type is not supported: $(uname -m)" >&2; exit 1 ;;
esac
case $os in
  darwin | linux) ;;
  *) echo "golaunchd: this system is not supported: $os. On Windows, use install.ps1." >&2; exit 1 ;;
esac

dir="$HOME/.local/bin"
mkdir -p "$dir"
curl -fsSL "https://github.com/waywho/launchd-cli/releases/latest/download/golaunchd-$os-$arch" -o "$dir/golaunchd"
chmod +x "$dir/golaunchd"
echo "Installed $dir/golaunchd"

case ":$PATH:" in
  *":$dir:"*) echo "Next step: run golaunchd login" ;;
  *) echo "Add this line to your shell profile, open a new terminal, then run golaunchd login:"
     echo "  export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac
