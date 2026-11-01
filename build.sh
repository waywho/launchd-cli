#!/bin/sh
# Builds the release binaries into dist/, with the names that install.sh and install.ps1 download.
set -eu
rm -rf dist
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  suffix=$([ "$os" = windows ] && echo .exe || true)
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "dist/launchd-$os-$arch$suffix" .
done
ls -l dist
