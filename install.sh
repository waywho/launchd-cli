#!/bin/sh
# Installs the launchd command on macOS or Linux: curl -fsSL <address of this file> | sh
set -eu

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "launchd: this computer type is not supported: $(uname -m)" >&2; exit 1 ;;
esac
case $os in
  darwin | linux) ;;
  *) echo "launchd: this system is not supported: $os. On Windows, use install.ps1." >&2; exit 1 ;;
esac

dir="$HOME/.local/bin"
mkdir -p "$dir"
curl -fsSL "https://github.com/waywho/launchd-cli/releases/latest/download/launchd-$os-$arch" -o "$dir/launchd"
chmod +x "$dir/launchd"
echo "Installed $dir/launchd"

case ":$PATH:" in
  *":$dir:"*) echo "Next step: run launchd login" ;;
  *) echo "Add this line to your shell profile, open a new terminal, then run launchd login:"
     echo "  export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac
