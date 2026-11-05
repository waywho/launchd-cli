package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const usage = `Usage:
  golaunchd login                                     Log in to Launchd in the browser.
  golaunchd logout                                    Log this computer out.
  golaunchd deploy [--name <app name>] [--no-browser] Deploy the app in this folder.
  golaunchd mcp                                       Run the MCP server for AI tools.
`

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	stdin, err := os.Stdin.Stat()
	downloads := filepath.Join(home, "Downloads")

	cli := &CLI{
		API: "https://auth.launchd.eu", Git: "https://git.launchd.eu", Public: "https://launchd.eu",
		ConfigDir: filepath.Join(configDir, "launchd"), ClaudeDir: claudeDir, Dir: dir, Downloads: downloads,
		In: os.Stdin, Out: os.Stdout, Interactive: err == nil && stdin.Mode()&os.ModeCharDevice != 0,
		OpenBrowser: openBrowser, PollInterval: 3 * time.Second, DeployWait: time.Minute,
	}
	if err := execute(cli, os.Args[1:]); err != nil {
		if !errors.Is(err, errBuildFailed) && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func execute(cli *CLI, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "login":
			return cli.Login()
		case "logout":
			return cli.Logout()
		case "deploy":
			flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
			name := flags.String("name", "", "the app name (default: the folder name)")
			noBrowser := flags.Bool("no-browser", false, "do not open the app page in the browser")
			if err := flags.Parse(args[1:]); err != nil {
				return err
			}
			return cli.Deploy(*name, *noBrowser)
		case "mcp":
			return runMCPServer(cli)
		}
	}
	fmt.Fprint(os.Stderr, usage)
	return flag.ErrHelp
}

func openBrowser(address string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", address).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Start()
	default:
		return exec.Command("xdg-open", address).Start()
	}
}
