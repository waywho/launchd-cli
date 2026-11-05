#!/bin/sh
# Packs the binaries from build.sh into dist/launchd-darwin.mcpb and dist/launchd-windows.mcpb.
# Usage: ./pack-mcpb.sh [version]. When CODESIGN_IDENTITY is set, it signs and notarizes the macOS binary
# before the pack. Notarization also needs NOTARY_KEY_PATH, NOTARY_KEY_ID and NOTARY_ISSUER_ID.
set -eu
version=${1:-0.0.0}
universal=dist/golaunchd-darwin-universal

lipo -create -output "$universal" dist/golaunchd-darwin-arm64 dist/golaunchd-darwin-amd64

if [ -n "${CODESIGN_IDENTITY:-}" ]; then
  codesign --force --sign "$CODESIGN_IDENTITY" --options runtime --timestamp "$universal"
  codesign --verify --strict --verbose=2 "$universal"
  ditto -c -k --keepParent "$universal" dist/notarize.zip
  xcrun notarytool submit dist/notarize.zip --key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID" \
    --issuer "$NOTARY_ISSUER_ID" --wait --output-format json | tee dist/notarize.json
  jq -e '.status == "Accepted"' dist/notarize.json
fi

pack() {
  platform=$1
  binary=$2
  server_file=$3
  stage=dist/mcpb-$platform
  rm -rf "$stage"
  mkdir -p "$stage/server"
  cp "$binary" "$stage/server/$server_file"
  node -e '
    const fs = require("fs");
    const [template, out, version, platform, serverFile] = process.argv.slice(1);
    const manifest = JSON.parse(fs.readFileSync(template, "utf8"));
    manifest.version = version;
    manifest.server.entry_point = `server/${serverFile}`;
    manifest.compatibility.platforms = [platform];
    fs.writeFileSync(out, JSON.stringify(manifest, null, 2));
  ' mcpb/manifest.json "$stage/manifest.json" "$version" "$4" "$server_file"
  npx -y @anthropic-ai/mcpb@2.1.2 pack "$stage" "dist/launchd-$platform.mcpb"
}

pack darwin "$universal" golaunchd darwin
pack windows dist/golaunchd-windows-amd64.exe golaunchd.exe win32
ls -l dist/*.mcpb
