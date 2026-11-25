package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"scrumboy/internal/config"
	"scrumboy/internal/integration"
	"scrumboy/internal/integration/githubapp"
	"scrumboy/internal/integration/githubcredentials"
	"scrumboy/internal/integration/githubmanifest"
	"scrumboy/internal/integration/githubwebhook"
)

type githubStartup struct {
	provider integration.Provider
	appSlug string
	webhook *githubwebhook.Service
	manifest *githubmanifest.Service
}

type githubActivation func(integration.Provider, string, *githubwebhook.Service) error

func prepareGitHub(cfg config.Config, inbox githubwebhook.Inbox, logger *log.Logger, activate githubActivation, manifestClient *http.Client) (githubStartup, error) {
	var result githubStartup
	manual := cfg.GitHubAppID != "" || cfg.GitHubAppPrivateKeyFile != ""
	if manual {
		if cfg.GitHubAppID == "" || cfg.GitHubAppPrivateKeyFile == "" {
			return result, errors.New("GitHub App setup requires both SCRUMBOY_GITHUB_APP_ID and SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE")
		}
		appID, err := strconv.ParseInt(cfg.GitHubAppID, 10, 64)
		if err != nil || appID <= 0 {
			return result, errors.New("SCRUMBOY_GITHUB_APP_ID must be a positive integer")
		}
		key, err := os.ReadFile(cfg.GitHubAppPrivateKeyFile)
		if err != nil {
			return result, errors.New("cannot read GitHub App private key file")
		}
		provider, err := githubapp.New(appID, key, nil)
		if err != nil {
			return result, errors.New("invalid GitHub App private key")
		}
		result.provider = provider
		result.appSlug = cfg.GitHubAppSlug
		if cfg.GitHubWebhookSecretFile != "" || cfg.GitHubWebhookSecret != "" {
			if cfg.ScrumboyMode != "full" {
				return result, errors.New("GitHub webhook secret requires Full Mode")
			}
			secret, err := resolveGitHubWebhookSecret(cfg.GitHubWebhookSecret, cfg.GitHubWebhookSecretFile)
			if err != nil {
				return result, err
			}
			result.webhook, err = githubwebhook.New(secret, inbox, provider, logger)
			if err != nil {
				return result, errors.New("invalid GitHub webhook secret configuration")
			}
		}
		return result, nil
	}
	if cfg.ScrumboyMode != "full" {
		if cfg.GitHubWebhookSecret != "" || cfg.GitHubWebhookSecretFile != "" {
			return result, errors.New("GitHub webhook secret requires configured GitHub App and Full Mode")
		}
		return result, nil
	}
	store, err := githubcredentials.New(cfg.DataDir)
	if err != nil {
		return result, errors.New("instance data directory does not support GitHub credential storage")
	}
	saved, exists, err := store.Load()
	if err != nil {
		return result, errors.New("GitHub App credential bundle is invalid or insecure; restore the original protected bundle before restarting")
	}
	if exists {
		if cfg.GitHubWebhookSecret != "" || cfg.GitHubWebhookSecretFile != "" {
			return result, errors.New("remove manual GitHub webhook secret settings when using a guided GitHub App")
		}
		provider, err := githubapp.New(saved.AppID, saved.PrivateKey, nil)
		if err != nil {
			return result, errors.New("saved GitHub App private key is invalid; restore its original credential bundle")
		}
		receiver, err := githubwebhook.New([]byte(saved.WebhookSecret), inbox, provider, logger)
		if err != nil {
			return result, errors.New("saved GitHub webhook secret is invalid; restore its original credential bundle")
		}
		result.provider = provider
		result.appSlug = saved.Slug
		result.webhook = receiver
		return result, nil
	}
	if cfg.GitHubWebhookSecret != "" || cfg.GitHubWebhookSecretFile != "" {
		return result, errors.New("GitHub webhook secret requires configured GitHub App")
	}
	if !githubmanifest.ValidOrigin(cfg.PublicBaseURL) || activate == nil {
		return result, nil
	}
	result.manifest = githubmanifest.New(manifestClient, func(ctx context.Context, creds githubmanifest.Credentials) error {
		provider, err := githubapp.New(creds.AppID, creds.PrivateKey, nil)
		if err != nil {
			return errors.New("received GitHub App credentials were invalid")
		}
		receiver, err := githubwebhook.New([]byte(creds.WebhookSecret), inbox, provider, logger)
		if err != nil {
			return errors.New("received GitHub webhook credentials were invalid")
		}
		if err := store.Save(creds); err != nil {
			if errors.Is(err, githubcredentials.ErrAlreadyConfigured) {
				return err
			}
			return errors.New("could not persist GitHub App credentials securely")
		}
		if err := activate(provider, creds.Slug, receiver); err != nil {
			return fmt.Errorf("GitHub credentials were saved; restart Scrumboy to activate them: %w", err)
		}
		return nil
	})
	return result, nil
}
