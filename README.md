# launchd-cli

The `launchd` command: `login`, `logout` and `deploy`. Plan: Task 19 in `~/Documents/src/plans/launchd/week1-demo/plan.md`.

## Install

The install scripts download the newest binary from the GitHub releases of this repo. An anonymous download works only when the repo is public.

- macOS and Linux: `curl -fsSL https://raw.githubusercontent.com/waywho/launchd-cli/main/install.sh | sh`. It puts `launchd` in `~/.local/bin`.
- Windows (PowerShell): `irm https://raw.githubusercontent.com/waywho/launchd-cli/main/install.ps1 | iex`. It puts `launchd.exe` in `%LOCALAPPDATA%\Programs\launchd` and adds that folder to the user `Path`.

## Commands

- `launchd login` opens the browser at `auth.launchd.eu`. It stores the Forgejo token in the Git credential store for `git.launchd.eu`, and the `launchd` token in `<user config folder>/launchd/config.json` (mode `600`). It also writes `ai-instructions.md` in that folder. When Git has no credential helper, it sets the `store` helper for `git.launchd.eu` only.
- `launchd logout` deletes both tokens.
- `launchd deploy [--name <app name>] [--no-browser]` runs in the app folder:
  1. On the first deploy, it checks that the app name is free. The default name is the folder name, in lowercase, with `-` for other characters. It adds the Git remote `launchd` and leaves other remotes alone.
  2. It commits all files that `.gitignore` allows, but never a `.env` file. It stops when an earlier commit holds a `.env` file.
  3. It sends the `.env` values, except the names in `env_rules`, and prints only the names. On the first deploy, `deployer` has no app record before the push, so the values arrive after the first build started. Run `launchd deploy` again to use them.
  4. It pushes to `main`, waits for the build, and prints the app address or "Build failed" with the last lines of the build log. When the app is live, it opens `https://launchd.eu/apps/<app name>`.

## Development

- Test: `go test ./...`. The tests use a fake `launchd` API and local bare Git repos. No test calls the real server.
- Lint: `gofmt -l .` (no output) and `go vet ./...`.
- Build: `./build.sh` puts the five binaries in `dist/`.
- Release: push a tag such as `v0.1.0`. The workflow `release.yml` tests, builds and attaches the binaries to a GitHub release.
