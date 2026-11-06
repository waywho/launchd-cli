# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.3] - 2026-11-06
### Changed
- The MCP tools give the app address, and the replace question no longer shows as a failure.
### Fixed
- A script from a zip with no execute bit gets the execute bit.

## [0.2.2] - 2026-11-06
### Changed
- `golaunchd deploy` opens the live app after a deploy, not the app page.

## [0.2.1] - 2026-11-06
### Changed
- The Claude desktop extension shows a longer description, the website, the privacy policy and a support address.

## [0.2.0] - 2026-11-05
### Added
- Add `AGENTS.md` with the rule that each PR description has a changelog heading.
- Deploy an app from a zip in Downloads. A zip that has the name of a live app deploys only after the user says yes to the replace.
- `golaunchd mcp` runs an MCP server over stdio with `deploy` and `status` tools
- Each release attaches the Claude desktop extension `launchd-darwin.mcpb` (signed and notarized) and `launchd-windows.mcpb`.
- The `release` job passes `released` and `tag` up as job outputs. The `mcpb` job runs only when `released` is `true`.
- The certificate import: a temp keychain in `$RUNNER_TEMP`, `security import -T /usr/bin/codesign`, then `set-key-partition-list`. `security find-identity` must show one "Developer ID Application" identity, because `codesign --sign` matches on that name.
- `pack-mcpb.sh` signs only when the workflow gives it `CODESIGN_IDENTITY`. It runs `codesign --options runtime --timestamp`, zips the binary with `ditto`, and runs `notarytool submit --wait`. It fails unless the JSON status is `Accepted`.
- The secrets go to steps only through `env`. No step prints them.
- The extension needs `golaunchd mcp` (Task 9, branch `mcp`). Merge that PR first, or the extension starts a binary with no `mcp` command.
- Not tested on GitHub: the signing and notarization run only on a merge with a label. The local `./build.sh && ./pack-mcpb.sh` makes both files, and `mcpb pack` checks the manifest against its schema.
### Changed
- Unpack a zip with size and path checks
- Write the Launchd skill after the builder says yes
- The README documents `golaunchd mcp` and the Claude desktop install.

## [0.1.0] - 2026-11-04
### Changed
- Release from a labelled PR with pr-to-release

[Unreleased]: https://github.com/waywho/launchd-cli/compare/v0.2.3...HEAD
[0.2.3]: https://github.com/waywho/launchd-cli/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/waywho/launchd-cli/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/waywho/launchd-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/waywho/launchd-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/waywho/launchd-cli/releases/tag/v0.1.0
