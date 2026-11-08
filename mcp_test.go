package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	wantStatus := "Shows whether a Launchd app is building, live, or failed. When it failed, it shows the reason: the last lines of the build log, or why the app does not start."
	if byName["status"] != wantStatus {
		t.Errorf("status description: %q", byName["status"])
	}
}

func TestMCPToolArgumentsTellTheAIWhatToPass(t *testing.T) {
	env := newDeployEnv(t)
	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	type schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	schemas := map[string]schema{}
	for _, tool := range tools.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		var s schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		schemas[tool.Name] = s
	}

	want := []struct{ tool, arg, description string }{
		{"deploy", "zip_name", "The file name of the zip in Downloads, or the full path of the zip."},
		{"deploy", "replace", "Set to true only after the user says yes to replace the live app."},
		{"status", "name", "The app name that deploy gave."},
	}
	for _, w := range want {
		if got := schemas[w.tool].Properties[w.arg].Description; got != w.description {
			t.Errorf("%s %s description: %q", w.tool, w.arg, got)
		}
	}
	if !slices.Equal(schemas["deploy"].Required, []string{"zip_name"}) || !slices.Equal(schemas["status"].Required, []string{"name"}) {
		t.Errorf("required: deploy %v, status %v", schemas["deploy"].Required, schemas["status"].Required)
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

	if result.IsError {
		t.Error("the replace question shows as an error")
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

func TestMCPDeployGivesTheAppAddress(t *testing.T) {
	env := newDeployEnv(t, "my-app")
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

	if text := mcpText(result); !strings.Contains(text, "The app address is https://my-app-tally.apps.golaunchd.eu.") {
		t.Errorf("result has no address: %q", text)
	}
}

func TestMCPStatusGivesTheAppAddressWhenLive(t *testing.T) {
	env := newDeployEnv(t)
	env.api.statuses = []string{"live"}

	sess, cleanup := newMCPEnv(t, env.cli)
	defer cleanup()

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "status",
		Arguments: map[string]any{"name": "my-app"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if text := mcpText(result); !strings.Contains(text, "App my-app: live at https://my-app-tally.apps.golaunchd.eu.") {
		t.Errorf("result has no address: %q", text)
	}
}

func mcpText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String()
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

func TestMCPInfoReturnsThePage(t *testing.T) {
	pageContent := "# Launchd\n\nTest page content."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/llms.txt" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, pageContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	cli := &CLI{
		API: "https://auth.launchd.eu", Git: "https://git.launchd.eu", Public: srv.URL,
		ConfigDir: t.TempDir(), Dir: t.TempDir(), Out: out,
	}
	sess, cleanup := newMCPEnv(t, cli)
	defer cleanup()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	w.Close()
	os.Stdout = old
	var stdoutBuf bytes.Buffer
	io.Copy(&stdoutBuf, r)

	if stdoutBuf.Len() > 0 {
		t.Errorf("os.Stdout has %q", stdoutBuf.String())
	}
	text := mcpText(result)
	if text != pageContent {
		t.Errorf("got %q, want %q", text, pageContent)
	}
}
