#!/bin/sh
# Runs install.sh with a temp HOME and a file:// download: sh test/install_test.sh
set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/golaunchd" "$repo"

base_path=/usr/bin:/bin:/usr/sbin:/sbin
line='export PATH="$HOME/.local/bin:$PATH"'
if [ "$(uname -s)" = Darwin ]; then
  check_shell=zsh
  fallback_option=-c
else
  check_shell="sh"
  # Plain sh -c reads no startup file on Linux, so only a login shell reads ~/.profile.
  fallback_option=-lc
fi

fail() { echo "FAIL: $*" >&2; exit 1; }

install() {
  env -u ZDOTDIR HOME="$1" PATH="$2" GOLAUNCHD_DOWNLOAD_URL="file://$tmp/golaunchd" GOLAUNCHD_LINK_DIR="$3" \
    sh "$repo/install.sh"
}

home="$tmp/link/home"
link_dir="$tmp/link/bin"
mkdir -p "$home" "$link_dir"
output=$(install "$home" "$link_dir:$base_path" "$link_dir") || fail "link: install.sh failed: $output"
env -u ZDOTDIR HOME="$home" PATH="$link_dir:$base_path" "$check_shell" -c 'command -v golaunchd' >/dev/null \
  || fail "link: $check_shell -c does not find golaunchd"
case $output in *"golaunchd is installed. Next step: run golaunchd login"*) ;; *) fail "link: no success line: $output" ;; esac
echo "ok: link folder"

home="$tmp/fallback/home"
mkdir -p "$home"
[ "$(uname -s)" = Darwin ] || : >"$home/.profile"
install "$home" "$base_path" "$tmp/fallback/missing" >/dev/null || fail "fallback: first install.sh run failed"
output=$(install "$home" "$base_path" "$tmp/fallback/missing") || fail "fallback: second install.sh run failed: $output"
env -u ZDOTDIR HOME="$home" PATH="$base_path" "$check_shell" "$fallback_option" 'command -v golaunchd' >/dev/null \
  || fail "fallback: $check_shell $fallback_option does not find golaunchd"
for file in .zshenv .profile; do
  [ -f "$home/$file" ] || continue
  count=$(grep -cxF "$line" "$home/$file" || true)
  [ "$count" = 1 ] || fail "fallback: $file has $count PATH lines, not 1"
done
echo "ok: ~/.zshenv fallback"
