package httpapi

import (
	"errors"
	"net/http"
	"time"

	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
)

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" || s.oidcService == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	returnTo := oidc.SanitizeReturnTo(r.URL.Query().Get("return_to"))

	redirectURL, err := s.oidcService.LoginRedirectURL(r.Context(), returnTo)
	if err != nil {
		s.logger.Printf("oidc: discovery/login error: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" || s.oidcService == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	result, errCode := s.oidcService.HandleCallback(r.Context(), r)
	if errCode != "" {
		if errCode != "provider" {
			s.logger.Printf("oidc: callback error: %s", errCode)
		}
		http.Redirect(w, r, "/?oidc_error="+errCode, http.StatusFound)
		return
	}

	ctx := r.Context()
	if result.Purpose != oidc.FlowLogin {
		s.handleSensitiveOIDCCallback(w, r, result)
		return
	}

	u, err := s.store.GetUserByOIDCIdentity(ctx, result.Issuer, result.Subject)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Printf("oidc: get user by identity: %v", err)
			http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
			return
		}

		// New identity: create user. Pass configured issuer so the store
		// only grants owner when issuer matches (plan section I).
		configuredIssuer := s.oidcService.Config().IssuerCanonical
		u, err = s.store.CreateUserOIDC(ctx, configuredIssuer, result.Issuer, result.Subject, result.Email, result.Name)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				// Do not identify or attach identities by email. The response is
				// intentionally generic while still directing the legitimate user.
				http.Redirect(w, r, "/?oidc_error=link_required", http.StatusFound)
				return
			} else {
				s.logger.Printf("oidc: create user: %v", err)
				http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
				return
			}
		}
	} else if err := s.store.UpdateOIDCIdentityEmailAtLogin(ctx, u.ID, result.Issuer, result.Subject, result.Email); err != nil {
		s.logger.Printf("oidc: update linked identity metadata: %v", err)
		http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
		return
	}

	if err := s.store.AssignUnownedDurableProjectsToUser(ctx, u.ID); err != nil {
		s.logger.Printf("oidc: assign unowned projects: %v", err)
	}

	token, expiresAt, err := s.store.CreateSession(ctx, u.ID, 30*24*time.Hour)
	if err != nil {
		s.logger.Printf("oidc: create session: %v", err)
		http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
		return
	}
	setSessionCookie(w, r, token, expiresAt)
	http.Redirect(w, r, result.ReturnTo, http.StatusFound)
}

func (s *Server) handleSensitiveOIDCCallback(w http.ResponseWriter, r *http.Request, result *oidc.CallbackResult) {
	sessionToken := sessionTokenFromRequest(r)
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok || userID != result.UserID || !result.SessionMatches(sessionToken) {
		http.Redirect(w, r, "/?oidc_error=session_changed", http.StatusFound)
		return
	}
	switch result.Purpose {
	case oidc.FlowSetPassword:
		linked, err := s.store.GetUserByOIDCIdentity(ctx, result.Issuer, result.Subject)
		if err != nil || linked.ID != userID {
			http.Redirect(w, r, "/?oidc_error=identity_mismatch", http.StatusFound)
			return
		}
		grant, expires, err := s.store.CreateFirstPasswordGrant(ctx, userID, sessionToken, 5*time.Minute)
		if err != nil {
			s.logger.Printf("oidc: create first-password authorization: %v", err)
			http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
			return
		}
		setFirstPasswordGrantCookie(w, r, grant, expires)
		http.Redirect(w, r, result.ReturnTo, http.StatusFound)
	case oidc.FlowLink:
		if err := s.store.LinkOIDCIdentityExplicit(ctx, userID, result.Issuer, result.Subject, result.Email); err != nil {
			if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrValidation) {
				http.Redirect(w, r, "/?oidc_error=link_rejected", http.StatusFound)
				return
			}
			s.logger.Printf("oidc: explicit identity link failed: %v", err)
			http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
			return
		}
		http.Redirect(w, r, result.ReturnTo, http.StatusFound)
	default:
		http.Redirect(w, r, "/?oidc_error=state_invalid", http.StatusFound)
	}
}
