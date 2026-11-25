package httpapi

import (
	"context"
	"errors"

	"scrumboy/internal/integration"
	"scrumboy/internal/integration/githubwebhook"
)

var ErrGitHubAlreadyConfigured = errors.New("GitHub App is already configured")
var ErrGitHubActivationUnavailable = errors.New("GitHub App activation is unavailable")

func (s *Server) githubSnapshot() (integration.Provider, string, *githubwebhook.Service) {
	s.githubMu.RLock()
	defer s.githubMu.RUnlock()
	return s.githubProvider, s.githubAppSlug, s.githubWebhook
}

func (s *Server) ActivateGitHub(provider integration.Provider, slug string, receiver *githubwebhook.Service) error {
	if s.mode != "full" || provider == nil || receiver == nil || slug == "" {
		return ErrGitHubActivationUnavailable
	}
	s.githubMu.Lock()
	defer s.githubMu.Unlock()
	if s.githubClosed {
		return ErrGitHubActivationUnavailable
	}
	if s.githubProvider != nil || s.githubWebhook != nil {
		return ErrGitHubAlreadyConfigured
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.githubProvider = provider
	s.githubAppSlug = slug
	s.githubWebhook = receiver
	s.githubWebhookCancel = cancel
	go receiver.Run(ctx)
	return nil
}
