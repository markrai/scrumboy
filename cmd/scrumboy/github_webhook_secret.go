package main

import (
    "errors"
    "os"
    "strings"
)

func resolveGitHubWebhookSecret(inline, file string) ([]byte, error) {
    if inline != "" && file != "" {
        return nil, errors.New("configure exactly one of SCRUMBOY_GITHUB_WEBHOOK_SECRET and SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE")
    }
    if file != "" {
        bytes, err := os.ReadFile(file)
        if err != nil {
            return nil, errors.New("cannot read GitHub webhook secret file")
        }
        return []byte(strings.TrimSpace(string(bytes))), nil
    }
    return []byte(strings.TrimSpace(inline)), nil
}
