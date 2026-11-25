package main

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestResolveGitHubWebhookSecret(t *testing.T) {
    path := filepath.Join(t.TempDir(), "secret")
    if err := os.WriteFile(path, []byte("  file-secret-value  \n"), 0600); err != nil {
        t.Fatal(err)
    }
    cases := []struct {
        name string
        inline string
        file string
        want string
        wantError bool
    }{
        {name: "unset"},
        {name: "env", inline: "  inline-secret  ", want: "inline-secret"},
        {name: "file", file: path, want: "file-secret-value"},
        {name: "conflict", inline: "hidden-value", file: path, wantError: true},
        {name: "missing file", file: path + "-missing", wantError: true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            secret, err := resolveGitHubWebhookSecret(tc.inline, tc.file)
            if (err != nil) != tc.wantError {
                t.Fatalf("error state = %v; want error = %v", err, tc.wantError)
            }
            if err != nil {
                if strings.Contains(err.Error(), tc.inline) && tc.inline != "" {
                    t.Fatal("configuration error exposed inline secret")
                }
                return
            }
            if string(secret) != tc.want {
                t.Fatalf("resolved secret does not match expected value")
            }
        })
    }
}
