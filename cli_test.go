package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const launchdToken = "lt-123"

type fakeAPI struct {
	t             *testing.T
	srv           *httptest.Server
	challenge     string
	taken         map[string]bool
	statuses      []string
	logTail       string
	secrets       map[string]string
	onSecrets     func()
	tokenDeleted  bool
	availableAsks []string
	callbacks     chan *http.Response
	authorizeURL  string
	skill         string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, taken: map[string]bool{}, statuses: []string{"deploying", "live"}, callbacks: make(chan *http.Response, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		sum := sha256.Sum256([]byte(body.CodeVerifier))
		if body.Code != "code-1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"launchd_token": launchdToken, "forgejo_token": "fg-456", "username": "tally"})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+launchdToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("DELETE /api/cli/token", authed(func(w http.ResponseWriter, r *http.Request) {
		f.tokenDeleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/apps/available", authed(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		f.availableAsks = append(f.availableAsks, name)
		json.NewEncoder(w).Encode(map[string]any{"host": name + "-tally.apps.golaunchd.eu", "available": !f.taken[name]})
	}))
	mux.HandleFunc("GET /api/env-rules", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string][]string{"reserved": {"DATABASE_URL", "SECRET_KEY_BASE"}, "local_only": {"RAILS_ENV"}})
	}))
	mux.HandleFunc("PUT /api/apps/{name}/secrets", authed(func(w http.ResponseWriter, r *http.Request) {
		if f.statuses[0] == "missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Secrets map[string]string `json:"secrets"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.secrets = body.Secrets
		if f.onSecrets != nil {
			f.onSecrets()
		}
		json.NewEncoder(w).Encode(map[string][]string{"set": slices.Sorted(maps.Keys(body.Secrets)), "skipped": {}})
	}))
	mux.HandleFunc("GET /api/apps/{name}/status", authed(func(w http.ResponseWriter, r *http.Request) {
		status := f.statuses[0]
		if len(f.statuses) > 1 {
			f.statuses = f.statuses[1:]
		}
		switch status {
		case "missing":
			w.WriteHeader(http.StatusNotFound)
			return
		case "502":
			w.WriteHeader(http.StatusBadGateway)
			return
		case "503":
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status, "owner": "tally", "log_tail": f.logTail})
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// openLogin plays the browser: it signs in and follows the redirect to the CLI's callback.
func (f *fakeAPI) openLogin(address string) error {
	f.authorizeURL = address
	authorize, _ := url.Parse(address)
	query := authorize.Query()
	f.challenge = query.Get("code_challenge")
	callbackQuery := url.Values{"code": {"code-1"}, "state": {query.Get("state")}}
	if f.skill != "" {
		callbackQuery.Set("skill", f.skill)
	}
	callback := query.Get("redirect_uri") + "?" + callbackQuery.Encode()
	go func() {
		browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if response, err := browser.Get(callback); err == nil {
			response.Body.Close()
			f.callbacks <- response
		}
	}()
	return nil
}

func isolateGit(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	return home
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type deployEnv struct {
	api      *fakeAPI
	cli      *CLI
	out      *bytes.Buffer
	gitRoot  string
	appDir   string
	browsers []string
}

// newDeployEnv makes a logged-in CLI for the app folder "crm", with a bare repo for each given app name.
func newDeployEnv(t *testing.T, bareRepos ...string) *deployEnv {
	isolateGit(t)
	env := &deployEnv{api: newFakeAPI(t), out: &bytes.Buffer{}, gitRoot: t.TempDir()}
	env.appDir = filepath.Join(t.TempDir(), "crm")
	os.Mkdir(env.appDir, 0o755)
	for _, name := range bareRepos {
		run(t, env.gitRoot, "init", "-q", "--bare", "tally/"+name+".git")
	}
	env.cli = &CLI{
		API: env.api.srv.URL, Git: "file://" + env.gitRoot, Public: "https://launchd.eu",
		ConfigDir: t.TempDir(), Dir: env.appDir, In: strings.NewReader(""), Out: env.out,
		PollInterval: time.Millisecond, DeployWait: time.Minute,
		OpenBrowser: func(address string) error { env.browsers = append(env.browsers, address); return nil },
	}
	if err := env.cli.saveConfig(config{Username: "tally", Token: launchdToken}); err != nil {
		t.Fatal(err)
	}
	return env
}

func (env *deployEnv) pushedFiles(t *testing.T, name string) []string {
	out, _ := exec.Command("git", "-C", filepath.Join(env.gitRoot, "tally", name+".git"), "ls-tree", "-r", "--name-only", "main").Output()
	return strings.Fields(string(out))
}

func TestLoginSavesTokens(t *testing.T) {
	home := isolateGit(t)
	api := newFakeAPI(t)
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	credentials, _ := os.ReadFile(filepath.Join(home, ".git-credentials"))
	if strings.TrimSpace(string(credentials)) != "https://tally:fg-456@git.launchd.eu" {
		t.Errorf("credential store holds %q", credentials)
	}
	info, err := os.Stat(filepath.Join(cli.ConfigDir, "config.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v, mode %v", err, info.Mode().Perm())
	}
	saved, _ := cli.loadConfig()
	if saved != (config{Username: "tally", Token: launchdToken}) {
		t.Errorf("config holds %+v", saved)
	}
}

func TestLoginKeepsAnExistingCredentialHelper(t *testing.T) {
	home := isolateGit(t)
	store := filepath.Join(home, "other-store")
	run(t, home, "config", "--global", "credential.helper", "store --file "+store)
	api := newFakeAPI(t)
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	if credentials, _ := os.ReadFile(store); !strings.Contains(string(credentials), "fg-456@git.launchd.eu") {
		t.Errorf("existing helper got %q", credentials)
	}
	if _, err := os.Stat(filepath.Join(home, ".git-credentials")); err == nil {
		t.Error("login added the store helper next to an existing helper")
	}
}

func TestLoginIgnoresTheGitRepoOfTheCurrentFolder(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	folder := t.TempDir()
	writeFiles(t, folder, map[string]string{".git": "gitdir: /nowhere"})
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Dir: folder, Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}
	if err := cli.Logout(); err != nil {
		t.Fatal(err)
	}
}

func TestLoginWritesAIInstructions(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	instructions, err := os.ReadFile(filepath.Join(cli.ConfigDir, "ai-instructions.md"))
	if err != nil || !strings.Contains(string(instructions), "golaunchd deploy") {
		t.Errorf("instructions file: %v, %q", err, instructions)
	}
}

func newLoginCLI(t *testing.T, api *fakeAPI) (*CLI, string) {
	claudeDir := t.TempDir()
	return &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), ClaudeDir: claudeDir, Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}, claudeDir
}

func TestLoginAsksForTheSkillWhenItIsMissing(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	cli, _ := newLoginCLI(t, api)

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	authorize, _ := url.Parse(api.authorizeURL)
	if skill := authorize.Query().Get("skill"); skill != "missing" {
		t.Errorf("authorize skill is %q", skill)
	}
}

func TestLoginSaysTheSkillIsPresent(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	cli, claudeDir := newLoginCLI(t, api)
	writeFiles(t, claudeDir, map[string]string{"skills/launchd/SKILL.md": "old"})

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	authorize, _ := url.Parse(api.authorizeURL)
	if skill := authorize.Query().Get("skill"); skill != "present" {
		t.Errorf("authorize skill is %q", skill)
	}
	if content, _ := os.ReadFile(filepath.Join(claudeDir, "skills", "launchd", "SKILL.md")); string(content) != "old" {
		t.Errorf("skill file holds %q", content)
	}
}

func TestLoginWritesTheSkillAfterAYes(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	api.skill = "yes"
	cli, claudeDir := newLoginCLI(t, api)

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(claudeDir, "skills", "launchd", "SKILL.md"))
	if err != nil || !strings.HasPrefix(string(content), "---\nname: launchd\n") {
		t.Errorf("skill file: %v, %q", err, content)
	}
}

func TestLoginWritesNoSkillAfterANo(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	api.skill = "no"
	cli, claudeDir := newLoginCLI(t, api)

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(claudeDir, "skills", "launchd", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("skill file exists: %v", err)
	}
}

func TestLoginSendsTheBrowserToTheLoggedInPage(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	out := &bytes.Buffer{}
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: out, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	select {
	case response := <-api.callbacks:
		if response.StatusCode != http.StatusFound || response.Header.Get("Location") != api.srv.URL+"/cli/logged-in" {
			t.Errorf("callback answered %d, Location %q", response.StatusCode, response.Header.Get("Location"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callback did not answer")
	}
	if want := "To let an AI tool run golaunchd, give it this file: " + filepath.Join(cli.ConfigDir, "ai-instructions.md"); !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not contain %q", out, want)
	}
}

func TestLoginUsesTheHyphenLoggedInPath(t *testing.T) {
	isolateGit(t)
	api := newFakeAPI(t)
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}

	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	select {
	case response := <-api.callbacks:
		if location := response.Header.Get("Location"); location != api.srv.URL+"/cli/logged-in" {
			t.Errorf("Location %q", location)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callback did not answer")
	}
}

func TestTextsNameTheGolaunchdCommand(t *testing.T) {
	oldName := regexp.MustCompile(`(^|[^o])launchd (login|logout|deploy)`)
	for _, text := range []string{usage, aiInstructions, errLoggedOut.Error()} {
		if oldName.MatchString(text) || !strings.Contains(text, "golaunchd ") {
			t.Errorf("text does not name golaunchd: %q", text)
		}
	}
}

func TestLogoutDeletesBothTokens(t *testing.T) {
	home := isolateGit(t)
	api := newFakeAPI(t)
	cli := &CLI{API: api.srv.URL, Git: "https://git.launchd.eu", ConfigDir: t.TempDir(), Out: &bytes.Buffer{}, OpenBrowser: api.openLogin}
	if err := cli.Login(); err != nil {
		t.Fatal(err)
	}

	if err := cli.Logout(); err != nil {
		t.Fatal(err)
	}

	if !api.tokenDeleted {
		t.Error("the launchd token was not deleted on the server")
	}
	if credentials, _ := os.ReadFile(filepath.Join(home, ".git-credentials")); strings.Contains(string(credentials), "fg-456") {
		t.Errorf("credential store still holds the Forgejo token: %q", credentials)
	}
	if _, err := os.Stat(filepath.Join(cli.ConfigDir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("config file still exists: %v", err)
	}
}

func TestDeployAddsRemoteAndKeepsOrigin(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})
	run(t, env.appDir, "init", "-q", "-b", "main")
	run(t, env.appDir, "remote", "add", "origin", "https://github.com/tally/crm.git")

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if origin := run(t, env.appDir, "remote", "get-url", "origin"); origin != "https://github.com/tally/crm.git" {
		t.Errorf("origin is %q", origin)
	}
	if remote := run(t, env.appDir, "remote", "get-url", "launchd"); remote != env.cli.Git+"/tally/crm.git" {
		t.Errorf("launchd remote is %q", remote)
	}
	if files := env.pushedFiles(t, "crm"); !slices.Equal(files, []string{"app.rb"}) {
		t.Errorf("pushed %v", files)
	}
	if !strings.Contains(env.out.String(), "https://crm-tally.apps.golaunchd.eu") {
		t.Errorf("output has no app address:\n%s", env.out)
	}
}

func TestDeployNeverPushesDotEnvAndFollowsGitignore(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{
		"app.rb": "puts 1", ".gitignore": "secret.txt\n!.env\n", "secret.txt": "x", ".env": "A=1", "config/.env": "B=2",
	})

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if files := env.pushedFiles(t, "crm"); !slices.Equal(files, []string{".gitignore", "app.rb"}) {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployStopsWhenHistoryHoldsDotEnv(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1", ".env": "A=1"})
	run(t, env.appDir, "init", "-q", "-b", "main")
	run(t, env.appDir, "add", "-A")
	run(t, env.appDir, "commit", "-q", "-m", "first")

	err := env.cli.Deploy("", true)

	if err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("got %v", err)
	}
	if files := env.pushedFiles(t, "crm"); len(files) != 0 {
		t.Errorf("pushed %v", files)
	}
}

func TestDeploySendsSecretsMinusEnvRulesAndPrintsNamesOnly(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1", ".env": strings.Join([]string{
		"# comment", "export STRIPE_KEY=sk_live_abc", `MAIL_FROM="hi@example.com"`, "PORT_HINT=3000 # dev",
		"DATABASE_URL=postgres://localhost/crm", "RAILS_ENV=development", "",
	}, "\n")})

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	want := map[string]string{"STRIPE_KEY": "sk_live_abc", "MAIL_FROM": "hi@example.com", "PORT_HINT": "3000"}
	if !maps.Equal(env.api.secrets, want) {
		t.Errorf("sent %v", env.api.secrets)
	}
	out := env.out.String()
	for _, name := range []string{"MAIL_FROM", "PORT_HINT", "STRIPE_KEY"} {
		if !strings.Contains(out, name) {
			t.Errorf("output does not name %s:\n%s", name, out)
		}
	}
	for _, value := range []string{"sk_live_abc", "hi@example.com", "postgres://localhost"} {
		if strings.Contains(out, value) {
			t.Errorf("output shows the value %q:\n%s", value, out)
		}
	}
}

func TestSecondDeployReusesRemoteAndSendsSecretsBeforePush(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})
	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}
	bare := filepath.Join(env.gitRoot, "tally", "crm.git")
	firstPush := run(t, bare, "rev-parse", "main")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 2", ".env": "STRIPE_KEY=x"})
	env.api.taken["crm"] = true
	env.api.statuses = []string{"live", "deploying", "live"}
	var atSecrets string
	env.api.onSecrets = func() { atSecrets = run(t, bare, "rev-parse", "main") }

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if atSecrets != firstPush {
		t.Error("the secrets arrived after the push")
	}
	if run(t, bare, "rev-parse", "main") == firstPush {
		t.Error("the second deploy pushed nothing")
	}
	if env.api.statuses[0] != "live" || len(env.api.statuses) != 1 {
		t.Errorf("the deploy ended before the new build: %v", env.api.statuses)
	}
}

func TestDeployAfterAFailedFirstPushSendsSecretsAfterTheBuild(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1", ".env": "STRIPE_KEY=x"})
	run(t, env.appDir, "init", "-q", "-b", "main")
	run(t, env.appDir, "remote", "add", "launchd", env.cli.Git+"/tally/crm.git")
	env.api.taken["crm"] = true
	env.api.statuses = []string{"missing", "deploying", "live"}

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if !maps.Equal(env.api.secrets, map[string]string{"STRIPE_KEY": "x"}) {
		t.Errorf("sent %v\n%s", env.api.secrets, env.out)
	}
	if files := env.pushedFiles(t, "crm"); !slices.Equal(files, []string{"app.rb"}) {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployAsksForAnotherNameWhenTaken(t *testing.T) {
	env := newDeployEnv(t, "crm2")
	env.api.taken["crm"] = true
	env.cli.Interactive = true
	env.cli.In = strings.NewReader("crm2\n")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if !slices.Equal(env.api.availableAsks, []string{"crm", "crm2"}) {
		t.Errorf("asked for %v", env.api.availableAsks)
	}
	if files := env.pushedFiles(t, "crm2"); !slices.Equal(files, []string{"app.rb"}) {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployWithoutTerminalNamesTheNameFlag(t *testing.T) {
	env := newDeployEnv(t, "crm")
	env.api.taken["crm"] = true
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	err := env.cli.Deploy("", true)

	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("got %v", err)
	}
	if files := env.pushedFiles(t, "crm"); len(files) != 0 {
		t.Errorf("pushed %v", files)
	}
}

func TestDeployMakesTheFolderNameAValidAppName(t *testing.T) {
	env := newDeployEnv(t, "my-tool")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	if err := env.cli.Deploy(" My_Tool ", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if !slices.Equal(env.api.availableAsks, []string{"my-tool"}) {
		t.Errorf("asked for %v", env.api.availableAsks)
	}
}

func TestDeployPrintsDeployFailedWithTheReason(t *testing.T) {
	env := newDeployEnv(t, "crm")
	env.api.statuses = []string{"deploying", "failed"}
	env.api.logTail = "The app does not start."
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	err := env.cli.Deploy("", false)

	if err == nil || !strings.Contains(env.out.String(), "Deploy failed") || strings.Contains(env.out.String(), "Build failed") ||
		!strings.Contains(env.out.String(), "The app does not start.") {
		t.Fatalf("got %v\n%s", err, env.out)
	}
	if len(env.browsers) != 0 {
		t.Errorf("opened %v", env.browsers)
	}
}

func TestDeployKeepsWaitingWhileLaunchdRestarts(t *testing.T) {
	env := newDeployEnv(t, "crm")
	env.api.statuses = []string{"502", "503", "deploying", "live"}
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}
	if !strings.Contains(env.out.String(), "Your app is live at") {
		t.Errorf("output does not say the app is live:\n%s", env.out)
	}
}

func TestDeployOpensTheLiveAppAndPrintsTheAppPage(t *testing.T) {
	env := newDeployEnv(t, "crm")
	env.api.statuses = []string{"missing", "deploying", "live"}
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	if err := env.cli.Deploy("", false); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if !slices.Equal(env.browsers, []string{"https://crm-tally.apps.golaunchd.eu"}) {
		t.Errorf("opened %v", env.browsers)
	}
	if !strings.Contains(env.out.String(), "Invite people on the app page: https://launchd.eu/apps/crm") {
		t.Errorf("output does not name the app page:\n%s", env.out)
	}
}

func TestDeployWithNoBrowserOpensNothing(t *testing.T) {
	env := newDeployEnv(t, "crm")
	writeFiles(t, env.appDir, map[string]string{"app.rb": "puts 1"})

	if err := env.cli.Deploy("", true); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}

	if len(env.browsers) != 0 {
		t.Errorf("opened %v", env.browsers)
	}
}
