package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	dotEnvFiles   = ":(glob)**/.env"
	deployTimeout = 20 * time.Minute
)

const aiInstructions = `# Launchd

Launchd puts a web app live at its own address. Only the people that the builder invites can open it.
Run these commands in a terminal:

- ` + "`golaunchd deploy`" + `: run it in the app folder. It deploys the app and prints its address.
  - ` + "`--name <app name>`" + ` sets the app name. The default is the folder name. Use it when the name is taken.
  - ` + "`--no-browser`" + ` does not open the app page in the browser.
- ` + "`golaunchd login`" + `: run it when a command says that the login ended.
- ` + "`golaunchd logout`" + `: logs this computer out.

` + "`golaunchd deploy`" + ` sends the values in ` + "`.env`" + ` as secrets, and never pushes the ` + "`.env`" + ` file.
When the build fails, it prints "Build failed" and the last lines of the build log.
Invites and secrets are on https://launchd.eu/apps.
`

var (
	errLoggedOut   = errors.New("You are not logged in, or your login ended. Run golaunchd login.")
	errBuildFailed = errors.New("The build failed.")
	errNotFound    = errors.New("not found")
	notAppNameChar = regexp.MustCompile(`[^a-z0-9]+`)
	httpClient     = &http.Client{Timeout: 30 * time.Second}
)

type CLI struct {
	API, Git, Public, ConfigDir, Dir string
	In                               io.Reader
	Out                              io.Writer
	Interactive                      bool
	OpenBrowser                      func(string) error
	PollInterval, DeployWait         time.Duration
}

type config struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

type appStatus struct {
	Status  string `json:"status"`
	LogTail string `json:"log_tail"`
}

func (c *CLI) Login() error {
	verifier, state := randomString(), randomString()
	challenge := sha256.Sum256([]byte(verifier))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	codes := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" || r.URL.Query().Get("state") != state {
			http.Error(w, "This login link is not valid. Run golaunchd login again.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "You are logged in. Go back to the terminal.")
		select {
		case codes <- r.URL.Query().Get("code"):
		default:
		}
	})}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())

	address := c.API + "/cli/authorize?" + url.Values{
		"redirect_uri":   {"http://" + listener.Addr().String() + "/callback"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"state":          {state},
	}.Encode()
	fmt.Fprintf(c.Out, "Log in to Launchd in your browser. If the browser does not open, open this address:\n%s\n", address)
	c.OpenBrowser(address)

	var code string
	select {
	case code = <-codes:
	case <-time.After(10 * time.Minute):
		return errors.New("The login did not finish in 10 minutes. Run golaunchd login again.")
	}
	var tokens struct {
		LaunchdToken string `json:"launchd_token"`
		ForgejoToken string `json:"forgejo_token"`
		Username     string `json:"username"`
	}
	if _, err := c.request(http.MethodPost, "/api/cli/token", "", map[string]string{"code": code, "code_verifier": verifier}, &tokens); err != nil {
		return err
	}
	if err := c.storeGitToken(tokens.Username, tokens.ForgejoToken); err != nil {
		return err
	}
	if err := c.saveConfig(config{Username: tokens.Username, Token: tokens.LaunchdToken}); err != nil {
		return err
	}
	instructions := filepath.Join(c.ConfigDir, "ai-instructions.md")
	if err := os.WriteFile(instructions, []byte(aiInstructions), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "You are logged in as %s.\nInstructions for AI tools: %s\n", tokens.Username, instructions)
	return nil
}

func (c *CLI) Logout() error {
	cfg, err := c.loadConfig()
	if err != nil {
		fmt.Fprintln(c.Out, "You are not logged in.")
		return nil
	}
	if _, err := c.request(http.MethodDelete, "/api/cli/token", cfg.Token, nil, nil); err != nil && !errors.Is(err, errLoggedOut) && !errors.Is(err, errNotFound) {
		return err
	}
	if _, err := gitOutsideRepo(c.credential(cfg.Username, ""), "credential", "reject"); err != nil {
		return err
	}
	if err := os.Remove(c.configPath()); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "You are logged out.")
	return nil
}

func (c *CLI) Deploy(name string, noBrowser bool) error {
	cfg, err := c.loadConfig()
	if err != nil {
		return errLoggedOut
	}
	if _, err := os.Stat(filepath.Join(c.Dir, ".git")); err != nil {
		if _, err := c.git("init", "-q", "-b", "main"); err != nil {
			return err
		}
	}

	var host string
	remote, err := c.git("remote", "get-url", "launchd")
	firstDeploy := err != nil
	if firstDeploy {
		if name == "" {
			name = filepath.Base(c.Dir)
		}
		if name, host, err = c.pickName(cfg.Token, name); err != nil {
			return err
		}
	} else {
		remoteName := strings.TrimSuffix(path.Base(remote), ".git")
		if name != "" && appName(name) != remoteName {
			fmt.Fprintf(c.Out, "This folder deploys as %s, the name in its launchd remote.\n", remoteName)
		}
		name = remoteName
		if host, _, err = c.checkName(cfg.Token, name); err != nil {
			return err
		}
	}

	var rules map[string][]string
	if _, err := c.request(http.MethodGet, "/api/env_rules", cfg.Token, nil, &rules); err != nil {
		return err
	}
	secrets, err := c.readSecrets(rules)
	if err != nil {
		return err
	}
	// deployer creates the app record only after the first push, so the first deploy sends the secrets at the end.
	// A first push that failed after the remote was added also leaves no app record.
	secretsAfterBuild := firstDeploy
	if !firstDeploy {
		err := c.sendSecrets(cfg.Token, name, secrets)
		secretsAfterBuild = errors.Is(err, errNotFound)
		if err != nil && !secretsAfterBuild {
			return err
		}
	}

	if err := c.commit(cfg.Username); err != nil {
		return err
	}
	if firstDeploy {
		if _, err := c.git("remote", "add", "launchd", c.remoteURL(cfg.Username, name)); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.Out, "Sending %s to Launchd.\n", name)
	if _, err := c.git("push", "-q", "launchd", "HEAD:refs/heads/main"); err != nil {
		return fmt.Errorf("Git cannot push the app. If the error is about the login, run golaunchd login.\n%w", err)
	}

	fmt.Fprintln(c.Out, "Building. This can take some minutes.")
	status, err := c.waitForDeploy(cfg.Token, name)
	if err != nil {
		return err
	}
	if secretsAfterBuild && len(secrets) > 0 {
		if err := c.sendSecrets(cfg.Token, name, secrets); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "The first build started before Launchd had the secrets. Run golaunchd deploy again to use them.")
	}
	if status.Status == "failed" {
		fmt.Fprintf(c.Out, "Build failed. The last lines of the build log:\n%s\n", status.LogTail)
		return errBuildFailed
	}

	fmt.Fprintf(c.Out, "Your app is live at https://%s\n", host)
	if !noBrowser {
		page := c.Public + "/apps/" + url.PathEscape(name)
		fmt.Fprintf(c.Out, "Invite people on the app page: %s\n", page)
		c.OpenBrowser(page)
	}
	return nil
}

func (c *CLI) pickName(token, name string) (string, string, error) {
	answers := bufio.NewReader(c.In)
	for {
		name = appName(name)
		problem := "The app name must have a letter or a digit."
		if name != "" {
			host, available, err := c.checkName(token, name)
			if err != nil || available {
				return name, host, err
			}
			problem = fmt.Sprintf("The app name %s is taken.", name)
		}
		if !c.Interactive {
			return "", "", fmt.Errorf("%s Run golaunchd deploy --name <another name>.", problem)
		}
		fmt.Fprintf(c.Out, "%s Type another app name: ", problem)
		answer, err := answers.ReadString('\n')
		if err != nil && answer == "" {
			return "", "", fmt.Errorf("%s Run golaunchd deploy --name <another name>.", problem)
		}
		name = answer
	}
}

func (c *CLI) checkName(token, name string) (string, bool, error) {
	var answer struct {
		Host      string `json:"host"`
		Available bool   `json:"available"`
	}
	_, err := c.request(http.MethodGet, "/api/apps/available?name="+url.QueryEscape(name), token, nil, &answer)
	return answer.Host, answer.Available, err
}

// appName gives the name that Forgejo and the app host both accept: "My_Tool" becomes "my-tool".
func appName(name string) string {
	return strings.Trim(notAppNameChar.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func (c *CLI) readSecrets(rules map[string][]string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(c.Dir, ".env"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	secrets := map[string]string{}
	var skipped []string
	// shortcut: no multi-line values and no escapes, add them when a builder's .env needs them.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		name, value, found := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if value != "" && (value[0] == '"' || value[0] == '\'') {
			if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
				value = value[1 : end+1]
			}
		} else if comment := strings.Index(value, " #"); comment >= 0 {
			value = strings.TrimSpace(value[:comment])
		}
		if slices.Contains(rules["reserved"], name) || slices.Contains(rules["local_only"], name) {
			skipped = append(skipped, name)
			continue
		}
		secrets[name] = value
	}
	if len(skipped) > 0 {
		fmt.Fprintf(c.Out, "Not sent from .env (Launchd sets these, or they are for your computer only): %s\n", strings.Join(skipped, ", "))
	}
	return secrets, nil
}

func (c *CLI) sendSecrets(token, name string, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	var answer struct {
		Set []string `json:"set"`
	}
	if _, err := c.request(http.MethodPut, "/api/apps/"+url.PathEscape(name)+"/secrets", token, map[string]any{"secrets": secrets}, &answer); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "Secrets sent from .env: %s\n", strings.Join(answer.Set, ", "))
	return nil
}

func (c *CLI) commit(username string) error {
	if _, err := c.git("rev-parse", "-q", "--verify", "HEAD"); err == nil {
		if found, _ := c.git("rev-list", "-1", "HEAD", "--", dotEnvFiles); found != "" {
			return errors.New("A commit in this folder holds a .env file, and Launchd never pushes .env files. Remove the .env file from the Git history, then deploy again.")
		}
	}
	if _, err := c.git("add", "-A"); err != nil {
		return err
	}
	if _, err := c.git("rm", "-r", "-q", "--cached", "--ignore-unmatch", "--", dotEnvFiles); err != nil {
		return err
	}
	if files, _ := c.git("ls-files"); files == "" {
		return errors.New("This folder has no files to deploy.")
	}
	args := []string{"commit", "-q", "--allow-empty", "-m", "Deploy with Launchd"}
	if _, err := c.git("var", "GIT_AUTHOR_IDENT"); err != nil {
		args = append([]string{"-c", "user.name=" + username, "-c", "user.email=" + username + "@users.noreply.launchd.eu"}, args...)
	}
	_, err := c.git(args...)
	return err
}

func (c *CLI) waitForDeploy(token, name string) (appStatus, error) {
	start := time.Now()
	sawDeploying := false
	for time.Since(start) < deployTimeout {
		var status appStatus
		_, err := c.request(http.MethodGet, "/api/apps/"+url.PathEscape(name)+"/status", token, nil, &status)
		switch {
		case errors.Is(err, errNotFound):
		case err != nil:
			return status, err
		case status.Status == "deploying":
			sawDeploying = true
		// Until deployer starts the new deploy, the status is the one of the last deploy.
		case sawDeploying || time.Since(start) >= c.DeployWait:
			return status, nil
		}
		time.Sleep(c.PollInterval)
	}
	return appStatus{}, fmt.Errorf("The deploy did not finish in %v. Look at the app page on %s.", deployTimeout, c.Public)
}

func (c *CLI) storeGitToken(username, token string) error {
	if _, err := gitOutsideRepo("", "config", "--get-urlmatch", "credential.helper", c.Git); err != nil {
		if _, err := gitOutsideRepo("", "config", "--global", "credential."+c.Git+".helper", "store"); err != nil {
			return err
		}
	}
	_, err := gitOutsideRepo(c.credential(username, token), "credential", "approve")
	return err
}

func (c *CLI) credential(username, token string) string {
	input := "url=" + c.Git + "\nusername=" + username + "\n"
	if token != "" {
		input += "password=" + token + "\n"
	}
	return input + "\n"
}

func (c *CLI) remoteURL(username, name string) string {
	remote, _ := url.Parse(c.Git)
	if remote.Scheme == "https" {
		remote.User = url.User(username)
	}
	return remote.JoinPath(username, name+".git").String()
}

func (c *CLI) request(method, path, token string, body, out any) (int, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, c.API+path, payload)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("Launchd cannot be reached: %w", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return response.StatusCode, errLoggedOut
	case response.StatusCode == http.StatusNotFound:
		return response.StatusCode, fmt.Errorf("Launchd answered %s to %s %s: %w", response.Status, method, path, errNotFound)
	case response.StatusCode >= 300:
		return response.StatusCode, fmt.Errorf("Launchd answered %s to %s %s. Try again.", response.Status, method, path)
	}
	if out != nil {
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			return response.StatusCode, fmt.Errorf("The answer from Launchd is not valid JSON: %w", err)
		}
	}
	return response.StatusCode, nil
}

func (c *CLI) git(args ...string) (string, error) { return runGit(c.Dir, "", args...) }

// gitOutsideRepo runs Git where no repo's own settings, or a broken repo, can change the credential helper.
func gitOutsideRepo(input string, args ...string) (string, error) {
	return runGit(os.TempDir(), input, args...)
}

func runGit(dir, input string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	command.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *CLI) configPath() string { return filepath.Join(c.ConfigDir, "config.json") }

func (c *CLI) saveConfig(cfg config) error {
	if err := os.MkdirAll(c.ConfigDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.configPath(), data, 0o600); err != nil {
		return err
	}
	return os.Chmod(c.configPath(), 0o600)
}

func (c *CLI) loadConfig() (config, error) {
	var cfg config
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(data, &cfg)
}

func randomString() string {
	random := make([]byte, 32)
	rand.Read(random)
	return base64.RawURLEncoding.EncodeToString(random)
}
