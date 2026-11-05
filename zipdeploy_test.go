package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestZipAppName(t *testing.T) {
	tests := []struct {
		fileName string
		want     string
	}{
		{"leave-tracker.zip", "leave-tracker"},
		{"Leave Tracker (2).zip", "leave-tracker"},
		{"My_Tool.zip", "my-tool"},
		{"(1).zip", ""},
		{"___.zip", ""},
	}
	for _, tt := range tests {
		if got := zipAppName(tt.fileName); got != tt.want {
			t.Errorf("zipAppName(%q) = %q, want %q", tt.fileName, got, tt.want)
		}
	}
}

func TestFindZipTakesTheNewestRenamedDownload(t *testing.T) {
	downloads := t.TempDir()
	old := filepath.Join(downloads, "my-app.zip")
	newer := filepath.Join(downloads, "my-app (1).zip")
	other := filepath.Join(downloads, "other.zip")
	for _, f := range []string{old, other} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(newer, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findZip(downloads, "my-app.zip")
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Errorf("findZip returned %q, want %q", got, newer)
	}
}

func TestFindZipUsesAFullPath(t *testing.T) {
	full := filepath.Join(t.TempDir(), "my-app.zip")
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findZip("/nowhere", full)
	if err != nil {
		t.Fatal(err)
	}
	if got != full {
		t.Errorf("findZip returned %q, want %q", got, full)
	}
}

func TestFindZipNamesTheNextStepWhenMissing(t *testing.T) {
	downloads := t.TempDir()

	_, err := findZip(downloads, "missing.zip")
	if err == nil || !strings.Contains(err.Error(), "No file missing.zip is in Downloads") {
		t.Fatalf("got %v", err)
	}
}

func TestDeployZipPushesAFirstApp(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	downloads := t.TempDir()
	env.cli.Downloads = downloads
	zipPath := filepath.Join(downloads, "my-app.zip")
	writeZip(t, zipPath, map[string]string{
		"my-app/": "", "my-app/Dockerfile": "FROM ruby\n", "my-app/app.rb": "puts 1\n",
	})

	name, err := env.cli.DeployZip("my-app.zip", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}
	if name != "my-app" {
		t.Errorf("name = %q", name)
	}

	files := env.pushedFiles(t, "my-app")
	if !slices.Equal(files, []string{"Dockerfile", "app.rb"}) {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployZipRefusesAnEmptyName(t *testing.T) {
	env := newDeployEnv(t)
	downloads := t.TempDir()
	env.cli.Downloads = downloads
	zipPath := filepath.Join(downloads, "(1).zip")
	writeZip(t, zipPath, map[string]string{"app.rb": "puts 1\n"})

	_, err := env.cli.DeployZip("(1).zip", false)
	want := "The app name comes from the zip name, and the zip name has no letter or digit. Ask the user to rename the zip, then call deploy again with the new name."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if files := env.pushedFiles(t, "(1)"); len(files) != 0 {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployZipKeepsTheDeployWhenTheAppRecordIsLate(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	env.api.statuses = []string{"missing"}
	defer func(wait time.Duration) { appRecordWait = wait }(appRecordWait)
	appRecordWait = 10 * time.Millisecond
	env.cli.Downloads = t.TempDir()
	writeZip(t, filepath.Join(env.cli.Downloads, "my-app.zip"), map[string]string{
		"Dockerfile": "FROM ruby\n", ".env": "STRIPE_KEY=x\n",
	})

	name, err := env.cli.DeployZip("my-app.zip", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}
	if name != "my-app" {
		t.Errorf("name = %q", name)
	}
	if !strings.Contains(env.out.String(), "The secrets from .env are not sent, because Launchd does not have the app yet. Deploy the zip again with replace set to true to send them.") {
		t.Errorf("output:\n%s", env.out)
	}
}

func TestDeployZipAsksBeforeReplacing(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	env.api.taken["my-app"] = true
	downloads := t.TempDir()
	env.cli.Downloads = downloads
	zipPath := filepath.Join(downloads, "my-app.zip")
	writeZip(t, zipPath, map[string]string{"app.rb": "puts 1\n"})

	_, err := env.cli.DeployZip("my-app.zip", false)
	want := "Replace the live app my-app? Ask the user. If they say yes, call deploy again with replace set to true."
	if !errors.Is(err, errReplaceNeeded) || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if files := env.pushedFiles(t, "my-app"); len(files) != 0 {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployZipReplacesOnTopOfTheHistory(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	bare := filepath.Join(env.gitRoot, "tally", "my-app.git")

	downloads1 := t.TempDir()
	env.cli.Downloads = downloads1
	zipPath1 := filepath.Join(downloads1, "my-app.zip")
	writeZip(t, zipPath1, map[string]string{"app.rb": "puts 1\n"})

	_, err := env.cli.DeployZip("my-app.zip", false)
	if err != nil {
		t.Fatalf("first deploy: %v\n%s", err, env.out)
	}

	firstCommit := run(t, bare, "rev-parse", "main")

	env.api.taken["my-app"] = true
	downloads2 := t.TempDir()
	env.cli.Downloads = downloads2
	zipPath2 := filepath.Join(downloads2, "my-app (1).zip")
	writeZip(t, zipPath2, map[string]string{"app.rb": "puts 2\n"})
	env.api.statuses = []string{"deploying", "live"}
	env.out.Reset()

	_, err = env.cli.DeployZip("my-app (1).zip", true)
	if err != nil {
		t.Fatalf("replace deploy: %v\n%s", err, env.out)
	}

	secondCommit := run(t, bare, "rev-parse", "main")
	if secondCommit == firstCommit {
		t.Error("the replace deploy pushed nothing")
	}
	parents := run(t, bare, "rev-parse", secondCommit+"^@")
	if !strings.Contains(parents, firstCommit) {
		t.Errorf("new commit %s parent is not %s: %s", secondCommit, firstCommit, parents)
	}
}

func TestDeployZipSendsSecretsWhenTheAppRecordExists(t *testing.T) {
	env := newDeployEnv(t, "my-app")
	downloads := t.TempDir()
	env.cli.Downloads = downloads
	zipPath := filepath.Join(downloads, "my-app.zip")
	writeZip(t, zipPath, map[string]string{
		"my-app/": "", "my-app/Dockerfile": "FROM ruby\n", "my-app/.env": "STRIPE_KEY=x\n",
	})

	_, err := env.cli.DeployZip("my-app.zip", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if got, ok := env.api.secrets["STRIPE_KEY"]; !ok || got != "x" {
		t.Errorf("secrets sent: %v", env.api.secrets)
	}
	if files := env.pushedFiles(t, "my-app"); slices.Contains(files, ".env") {
		t.Errorf(".env was pushed: %v", files)
	}
}
