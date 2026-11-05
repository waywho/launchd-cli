package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newMCPEnv(t *testing.T, cli *CLI) (*mcp.ClientSession, func()) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "golaunchd"}, nil)
	registerMCPTools(server, cli)

	st, ct := mcp.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(context.Background(), st) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		sess.Close()
		<-serverDone
	}
	return sess, cleanup
}

func TestMCPListsDeployAndStatus(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]string{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool.Description
	}

	wantDeploy := "Use when the user wants to put an app live on Launchd. Launchd builds the app on its servers. Do not build or run the app on this computer."
	if byName["deploy"] != wantDeploy {
		t.Errorf("deploy description: %q", byName["deploy"])
	}
	wantStatus := "Shows whether a Launchd app is building, live, or failed, with the last lines of the build log when it failed."
	if byName["status"] != wantStatus {
		t.Errorf("status description: %q", byName["status"])
	}
}

func TestMCPDeployReturnsTheOutputAndWritesNothingToStdout(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	env.cli.Downloads = t.TempDir()
	writeZip(t, filepath.Join(env.cli.Downloads, "my-app.zip"), map[string]string{"app.rb": "puts 1\n"})

	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "deploy",
		Arguments: map[string]any{"zip_name": "my-app.zip"},
	})

	w.Close()
	os.Stdout = old
	var stdoutBuf bytes.Buffer
	io.Copy(&stdoutBuf, r)

	if err != nil {
		t.Fatal(err)
	}
	if stdoutBuf.Len() > 0 {
		t.Errorf("os.Stdout has %q", stdoutBuf.String())
	}
	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "The build started. Call status with the app name my-app") {
		t.Errorf("result does not say the build started: %q", text.String())
	}
}

func TestMCPDeployAsksBeforeReplacing(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	env.api.taken["my-app"] = true
	env.cli.Downloads = t.TempDir()
	writeZip(t, filepath.Join(env.cli.Downloads, "my-app.zip"), map[string]string{"app.rb": "puts 1\n"})

	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "deploy",
		Arguments: map[string]any{"zip_name": "my-app.zip"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error for replace")
	}
	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "Replace the live app my-app?") {
		t.Errorf("result does not ask about replace: %q", text.String())
	}
}

func TestMCPStatusGivesTheLogTailWhenFailed(t *testing.T) {
	env := newDeployEnv(t)
	env.api.statuses = []string{"failed"}
	env.api.logTail = "Gem::MissingSpecError: rails"

	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "status",
		Arguments: map[string]any{"name": "my-app"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "Gem::MissingSpecError: rails") {
		t.Errorf("result does not show log tail: %q", text.String())
	}
}

func TestMCPLogsInOnceWhenLoggedOut(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	os.Remove(env.cli.configPath())
	env.cli.OpenBrowser = env.api.openLogin
	env.cli.Downloads = t.TempDir()
	writeZip(t, filepath.Join(env.cli.Downloads, "my-app.zip"), map[string]string{"app.rb": "puts 1\n"})

	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "deploy",
		Arguments: map[string]any{"zip_name": "my-app.zip"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "The build started. Call status with the app name my-app") {
		t.Errorf("result after auto-login: %q", text.String())
	}
}
