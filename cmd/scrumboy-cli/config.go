package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	envServer  = "SCRUMBOY_URL"
	envToken   = "SCRUMBOY_TOKEN"
	envProject = "SCRUMBOY_PROJECT"
	envConfig  = "SCRUMBOY_CLI_CONFIG"
)

// fileConfig is what `auth login` persists.
type fileConfig struct {
	Server  string `json:"server,omitempty"`
	Token   string `json:"token,omitempty"`
	Project string `json:"project,omitempty"`
	// OAuth is an OAuth session from 'auth login --oauth'; it takes the place
	// of Token and refreshes itself.
	OAuth *oauthConfig `json:"oauth,omitempty"`
}

// settings are the resolved values for one invocation.
// Precedence, highest first: flag, environment variable, config file.
type settings struct {
	Server     string
	Token      string
	Project    string
	ConfigPath string
	// OAuth is set only when the credentials come from the config file's OAuth
	// session (an explicit flag/env token always wins).
	OAuth *oauthConfig
}

func defaultConfigPath(getenv func(string) string) (string, error) {
	if p := getenv(envConfig); p != "" {
		return p, nil
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "scrumboy", "config.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "scrumboy", "config.json"), nil
}

func loadConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// saveConfig writes the config atomically with owner-only permissions, since
// it holds an API token.
func saveConfig(path string, cfg fileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := restrictToOwner(tmp); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmp.Name(), path)
}

// replaceFile renames from over to, retrying briefly where the platform can
// refuse while another process has the target open. Losing this write after a
// refresh would lose the rotated refresh token, whose predecessor is spent.
func replaceFile(from, to string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || !renameRetryable(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sameServer compares two server URLs after normalization.
func sameServer(a, b string) bool {
	na, err1 := normalizeServer(a)
	nb, err2 := normalizeServer(b)
	return err1 == nil && err2 == nil && na == nb
}

func resolveSettings(flagServer, flagToken, flagProject, flagConfig string, getenv func(string) string) (settings, fileConfig, error) {
	path := flagConfig
	if path == "" {
		var err error
		if path, err = defaultConfigPath(getenv); err != nil {
			return settings{}, fileConfig{}, err
		}
	}
	file, err := loadConfig(path)
	if err != nil {
		return settings{}, file, err
	}
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	st := settings{
		Server:     pick(flagServer, getenv(envServer), file.Server),
		Token:      pick(flagToken, getenv(envToken)),
		Project:    pick(flagProject, getenv(envProject), file.Project),
		ConfigPath: path,
	}
	if st.Token == "" {
		// Saved credentials belong to the server they were issued by: never
		// send them to a different one chosen by --server or the environment.
		switch {
		case file.OAuth != nil && file.OAuth.AccessToken != "" && sameServer(st.Server, file.OAuth.Server):
			st.OAuth = file.OAuth
		case file.Token != "" && sameServer(st.Server, file.Server):
			st.Token = file.Token
		}
	}
	return st, file, nil
}
