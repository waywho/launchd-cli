# launchd-cli

The `golaunchd` command: `login`, `logout`, `deploy` and `mcp`. Plan: Task 19 in `~/Documents/src/plans/launchd/week1-demo/plan.md`.

The command is not called `launchd`, because macOS has its own `/sbin/launchd`, and `/sbin` comes before `~/.local/bin` in the `PATH`.

## Install

The install scripts download the newest binary from the GitHub releases of this repo.

- macOS and Linux: `curl -fsSL https://github.com/waywho/launchd-cli/releases/latest/download/install.sh | sh`. It puts `golaunchd` in `~/.local/bin`. The user does no manual step:
  - When the user can write to `/usr/local/bin`, the script adds a link to `golaunchd` there.
  - Else it adds `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshenv`. On Linux, it also adds the line to `~/.profile` and `~/.bashrc` when they exist. It adds the line only once and never uses `sudo`.
  - Then it checks that a new `zsh -c` (or `sh -c`) finds `golaunchd`, and prints the next step.
- Windows (PowerShell): `irm https://github.com/waywho/launchd-cli/releases/latest/download/install.ps1 | iex`. It puts `golaunchd.exe` in `%LOCALAPPDATA%\Programs\golaunchd` and adds that folder to the user `Path`. Then it checks that a new PowerShell process finds `golaunchd`, and prints the next step.
- Claude desktop on macOS: download https://github.com/waywho/launchd-cli/releases/latest/download/launchd-darwin.mcpb and open the file. Claude desktop installs the extension. Then tell Claude in a chat: put my app on Launchd. On Windows, the file is `launchd-windows.mcpb`.

## Release

Add the label `major`, `minor` or `patch` to a PR. Its merge makes the release.

## Commands

- `golaunchd login` opens the browser at `auth.launchd.eu`. It stores the Forgejo token in the Git credential store for `git.launchd.eu`, and the `launchd` token in `<user config folder>/launchd/config.json` (mode `600`). It also writes `ai-instructions.md` in that folder. When Git has no credential helper, it sets the `store` helper for `git.launchd.eu` only.
- `golaunchd logout` deletes both tokens.
- `golaunchd deploy [--name <app name>] [--no-browser]` runs in the app folder:
  1. On the first deploy, it checks that the app name is free. The default name is the folder name, in lowercase, with `-` for other characters. It adds the Git remote `launchd` and leaves other remotes alone.
  2. It commits all files that `.gitignore` allows, but never a `.env` file. It stops when an earlier commit holds a `.env` file.
  3. It sends the `.env` values, except the names in `env_rules`, and prints only the names. When `deployer` has no app record before the push, the values arrive after the build started. Run `golaunchd deploy` again to use them.
  4. It pushes to `main`, waits for the build, and prints the app address or "Deploy failed" with the reason: the last lines of the build log, or why the app does not start. When the app is live, it opens the app address, and prints the app page `https://launchd.eu/apps/<app name>` for invitations.
- `golaunchd mcp` runs an MCP server over stdio for AI tools, with two tools:
  - `deploy` takes a zip from Downloads, or a full path, and pushes it. When the app name is taken, it asks before a replace.
  - `status` shows whether an app is building, live or failed.

`golaunchd` has no command to invite people. To invite a person, open `https://launchd.eu/apps/<app name>`, type the email address, and click **Invite**. The person gets an email with the app address.

## Development

- Test: `go test ./...`. The tests use a fake `launchd` API and local bare Git repos. No test calls the real server.
- Installer test: `sh test/install_test.sh`. It builds `golaunchd` and runs `install.sh` with a temp `HOME`. `GOLAUNCHD_DOWNLOAD_URL` sets the download address, and `GOLAUNCHD_LINK_DIR` sets the link folder.
- Lint: `gofmt -l .` (no output) and `go vet ./...`.
- Build: `./build.sh` puts the five binaries in `dist/`.
- Claude desktop extension: `./pack-mcpb.sh [version]` runs after `./build.sh`. It makes one universal macOS binary with `lipo`, and packs `dist/launchd-darwin.mcpb` and `dist/launchd-windows.mcpb` from `mcpb/manifest.json`. The extension runs `golaunchd mcp`. On a local run, the macOS binary is not signed.
- Release: merge a labelled PR. The workflow `release.yml` makes the release, then attaches the binaries and both install scripts. Then the job `mcpb` signs and notarizes the macOS binary on `macos-latest`, and attaches both `.mcpb` files. It uses the secrets `MACOS_CERT_P12`, `MACOS_CERT_PASSWORD`, `NOTARY_KEY`, `NOTARY_KEY_ID` and `NOTARY_ISSUER_ID`.
