# launchd-cli

The `golaunchd` command: `login`, `logout` and `deploy`. Plan: Task 19 in `~/Documents/src/plans/launchd/week1-demo/plan.md`.

The command is not called `launchd`, because macOS has its own `/sbin/launchd`, and `/sbin` comes before `~/.local/bin` in the `PATH`.

## Install

The install scripts download the newest binary from the GitHub releases of this repo. An anonymous download works only when the repo is public.

- macOS and Linux: `curl -fsSL https://raw.githubusercontent.com/waywho/launchd-cli/main/install.sh | sh`. It puts `golaunchd` in `~/.local/bin`. The user does no manual step:
  - When the user can write to `/usr/local/bin`, the script adds a link to `golaunchd` there.
  - Else it adds `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshenv`. On Linux, it also adds the line to `~/.profile` and `~/.bashrc` when they exist. It adds the line only once and never uses `sudo`.
  - Then it checks that a new `zsh -c` (or `sh -c`) finds `golaunchd`, and prints the next step.
- Windows (PowerShell): `irm https://raw.githubusercontent.com/waywho/launchd-cli/main/install.ps1 | iex`. It puts `golaunchd.exe` in `%LOCALAPPDATA%\Programs\golaunchd` and adds that folder to the user `Path`. Then it checks that a new PowerShell process finds `golaunchd`, and prints the next step.

## Commands

- `golaunchd login` opens the browser at `auth.launchd.eu`. It stores the Forgejo token in the Git credential store for `git.launchd.eu`, and the `launchd` token in `<user config folder>/launchd/config.json` (mode `600`). It also writes `ai-instructions.md` in that folder. When Git has no credential helper, it sets the `store` helper for `git.launchd.eu` only.
- `golaunchd logout` deletes both tokens.
- `golaunchd deploy [--name <app name>] [--no-browser]` runs in the app folder:
  1. On the first deploy, it checks that the app name is free. The default name is the folder name, in lowercase, with `-` for other characters. It adds the Git remote `launchd` and leaves other remotes alone.
  2. It commits all files that `.gitignore` allows, but never a `.env` file. It stops when an earlier commit holds a `.env` file.
  3. It sends the `.env` values, except the names in `env_rules`, and prints only the names. When `deployer` has no app record before the push, the values arrive after the build started. Run `golaunchd deploy` again to use them.
  4. It pushes to `main`, waits for the build, and prints the app address or "Build failed" with the last lines of the build log. When the app is live, it opens `https://launchd.eu/apps/<app name>`.

## Development

- Test: `go test ./...`. The tests use a fake `launchd` API and local bare Git repos. No test calls the real server.
- Installer test: `sh test/install_test.sh`. It builds `golaunchd` and runs `install.sh` with a temp `HOME`. `GOLAUNCHD_DOWNLOAD_URL` sets the download address, and `GOLAUNCHD_LINK_DIR` sets the link folder.
- Lint: `gofmt -l .` (no output) and `go vet ./...`.
- Build: `./build.sh` puts the five binaries in `dist/`.
- Release: push a tag such as `v0.1.0`. The workflow `release.yml` tests, builds and attaches the binaries to a GitHub release.
