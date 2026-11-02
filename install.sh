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
curl -fsSL "${GOLAUNCHD_DOWNLOAD_URL:-https://github.com/waywho/launchd-cli/releases/latest/download/golaunchd-$os-$arch}" -o "$dir/golaunchd"
chmod +x "$dir/golaunchd"

add_path_line() {
  line='export PATH="$HOME/.local/bin:$PATH"'
  grep -qxF "$line" "$1" 2>/dev/null || printf '\n%s\n' "$line" >>"$1"
}

link_dir="${GOLAUNCHD_LINK_DIR:-/usr/local/bin}"
if [ -d "$link_dir" ] && [ -w "$link_dir" ]; then
  ln -sf "$dir/golaunchd" "$link_dir/golaunchd"
else
  add_path_line "${ZDOTDIR:-$HOME}/.zshenv"
  if [ "$os" = linux ]; then
    for file in "$HOME/.profile" "$HOME/.bashrc"; do
      [ -f "$file" ] && add_path_line "$file"
    done
  fi
fi

if command -v zsh >/dev/null; then check_shell="zsh"; else check_shell="sh"; fi
if "$check_shell" -c 'command -v golaunchd' >/dev/null 2>&1; then
  echo "golaunchd is installed. Next step: run golaunchd login"
else
  echo "golaunchd is installed. Next step: open a new terminal and run golaunchd login"
fi
