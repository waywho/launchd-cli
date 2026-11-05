package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errReplaceNeeded = errors.New("replace needed")

func zipAppName(fileName string) string {
	name := strings.TrimSuffix(fileName, ".zip")
	if i := strings.LastIndex(name, "("); i >= 0 && strings.HasSuffix(name, ")") {
		digits := name[i+1 : len(name)-1]
		if isDigits(digits) {
			name = strings.TrimSpace(name[:i])
		}
	}
	return appName(name)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func findZip(downloads, zipName string) (string, error) {
	if strings.ContainsAny(zipName, string(os.PathSeparator)+`/\`) {
		return zipName, nil
	}
	base := strings.TrimSuffix(zipName, ".zip")
	entries, err := os.ReadDir(downloads)
	if err != nil {
		return "", err
	}
	var newest string
	var newestTime int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == zipName || matchesRename(name, base) {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			if mod := info.ModTime().UnixNano(); mod > newestTime {
				newest, newestTime = name, mod
			}
		}
	}
	if newest == "" {
		return "", fmt.Errorf("No file %s is in Downloads. Ask the user where the file is, then call deploy again with the full path.", zipName)
	}
	return filepath.Join(downloads, newest), nil
}

func matchesRename(name, base string) bool {
	if !strings.HasPrefix(name, base) || !strings.HasSuffix(name, ".zip") {
		return false
	}
	middle := name[len(base) : len(name)-4]
	if len(middle) < 2 || middle[0] != ' ' || middle[1] != '(' || middle[len(middle)-1] != ')' {
		return false
	}
	return isDigits(middle[2 : len(middle)-1])
}

func (c *CLI) DeployZip(zipName string, replace bool) (string, error) {
	zipPath, err := findZip(c.Downloads, zipName)
	if err != nil {
		return "", err
	}
	name := zipAppName(filepath.Base(zipPath))
	if name == "" {
		return "", fmt.Errorf("The app name must have a letter or a digit. Run golaunchd deploy --name <another name>.")
	}

	cfg, err := c.loadConfig()
	if err != nil {
		return "", errLoggedOut
	}

	_, available, err := c.checkName(cfg.Token, name)
	if err != nil {
		return "", err
	}
	if !available && !replace {
		return "", fmt.Errorf("Replace the live app %s? Ask the user. If they say yes, call deploy again with replace set to true.: %w", name, errReplaceNeeded)
	}

	var rules map[string][]string
	if _, err := c.request(http.MethodGet, "/api/env-rules", cfg.Token, nil, &rules); err != nil {
		return "", err
	}

	workDir, err := os.MkdirTemp("", "launchd-deploy-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workDir)

	if err := unpackZip(zipPath, workDir); err != nil {
		return "", err
	}

	dir := c.Dir
	c.Dir = workDir
	defer func() { c.Dir = dir }()

	secrets, err := c.readSecrets(rules)
	if err != nil {
		return "", err
	}

	if _, err := c.git("init", "-q", "-b", "main"); err != nil {
		return "", err
	}

	firstDeploy := available

	if !firstDeploy {
		if _, err := c.git("remote", "add", "launchd", c.remoteURL(cfg.Username, name)); err != nil {
			return "", err
		}
		if _, err := c.git("fetch", "-q", "launchd", "main"); err != nil {
			return "", err
		}
		if _, err := c.git("reset", "--soft", "FETCH_HEAD"); err != nil {
			return "", err
		}
		if err := c.sendSecrets(cfg.Token, name, secrets); err != nil {
			return "", err
		}
	}

	if err := c.commitAndPush(cfg, name, firstDeploy); err != nil {
		return "", err
	}

	if firstDeploy && len(secrets) > 0 {
		start := time.Now()
		for time.Since(start) < 30*time.Second {
			code, statErr := c.request(http.MethodGet, "/api/apps/"+url.PathEscape(name)+"/status", cfg.Token, nil, nil)
			if code != http.StatusNotFound && !errors.Is(statErr, errNotFound) {
				break
			}
			time.Sleep(time.Second)
		}
		if err := c.sendSecrets(cfg.Token, name, secrets); err != nil {
			return "", err
		}
		fmt.Fprintln(c.Out, "The first build started before Launchd had the secrets. Deploy again to use them.")
	}

	return name, nil
}

func (c *CLI) Status(name string) (appStatus, error) {
	cfg, err := c.loadConfig()
	if err != nil {
		return appStatus{}, errLoggedOut
	}
	var status appStatus
	if _, err := c.request(http.MethodGet, "/api/apps/"+url.PathEscape(name)+"/status", cfg.Token, nil, &status); err != nil {
		return status, err
	}
	return status, nil
}
