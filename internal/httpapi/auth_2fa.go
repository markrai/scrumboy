package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"scrumboy/internal/crypto"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/store"
)

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			xff = strings.TrimSpace(xff[:i])
		}
		if host, _, err := net.SplitHostPort(xff); err == nil {
			return host
		}
		return xff
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host != "" {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var in struct {
		TempToken string `json:"tempToken"`
		Code      string `json:"code"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if in.TempToken == "" || in.Code == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tempToken and code required", nil)
		return
	}

	ctx := s.requestContext(r)
	u, _, err := s.store.GetUserByLogin2FAPendingToken(ctx, in.TempToken)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired code", nil)
			return
		}
		writeStoreErr(w, err, true)
		return
	}

	ipKey := "ip:" + clientIP(r)
	emailKey := "email:" + ratelimit.NormalizeEmail(u.Email)
	if s.authRateLimit != nil && !s.authRateLimit.Allow(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}

	if err := s.store.IncrementLogin2FAPendingAttempt(ctx, in.TempToken); err != nil {
		if errors.Is(err, store.ErrTooManyAttempts) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "too many attempts; please sign in again", nil)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired code", nil)
			return
		}
		writeStoreErr(w, err, true)
		return
	}

	code := strings.TrimSpace(in.Code)

	// Verification order: recovery code first, then TOTP
	consumed, err := s.store.ConsumeRecoveryCode(ctx, u.ID, code)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if consumed {
		s.finishLogin2FA(w, r, ctx, in.TempToken, u)
		return
	}

	secret, err := s.store.GetUserTwoFactorSecret(ctx, u.ID)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if !crypto.ValidateTOTPCode(secret, code) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid code", nil)
		return
	}

	s.finishLogin2FA(w, r, ctx, in.TempToken, u)
}

func (s *Server) finishLogin2FA(w http.ResponseWriter, r *http.Request, ctx context.Context, tempToken string, u store.User) {
	_ = s.store.DeleteLogin2FAPendingToken(ctx, tempToken)
	if err := s.store.AssignUnownedDurableProjectsToUser(ctx, u.ID); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	token, expiresAt, err := s.store.CreateSession(ctx, u.ID, 30*24*time.Hour)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	setSessionCookie(w, r, token, expiresAt)
	writeJSON(w, http.StatusOK, userToJSON(u))
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	ipKey := "ip:" + clientIP(r)
	u, _ := s.store.GetUser(ctx, userID)
	emailKey := "email:" + ratelimit.NormalizeEmail(u.Email)
	if s.authRateLimit != nil && !s.authRateLimit.Allow(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}

	secret, otpauthURI, manualEntryKey, err := crypto.GenerateTOTPSecret(u.Email)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	encrypted, err := s.store.EncryptTOTPSecret([]byte(secret))
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}

	setupToken, _, err := s.store.CreateTwoFactorEnrollment(ctx, userID, encrypted, 10*time.Minute)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}

	qrCodeDataURL := ""
	if png, err := qrcode.Encode(otpauthURI, qrcode.Medium, 192); err == nil {
		qrCodeDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"setupToken":     setupToken,
		"otpauthUri":     otpauthURI,
		"manualEntryKey": manualEntryKey,
		"qrCodeDataUrl":  qrCodeDataURL,
	})
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	var in struct {
		SetupToken string `json:"setupToken"`
		Code       string `json:"code"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if in.SetupToken == "" || in.Code == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "setupToken and code required", nil)
		return
	}

	enrollUserID, secretEnc, err := s.store.GetTwoFactorEnrollmentByToken(ctx, in.SetupToken)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired setup; please start again", nil)
			return
		}
		writeStoreErr(w, err, true)
		return
	}
	if enrollUserID != userID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
		return
	}

	if err := s.store.IncrementEnrollmentAttempt(ctx, in.SetupToken); err != nil {
		if errors.Is(err, store.ErrTooManyAttempts) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "too many attempts; please start setup again", nil)
			return
		}
		writeStoreErr(w, err, true)
		return
	}

	plaintext, err := s.store.DecryptTOTPSecret(secretEnc)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if !crypto.ValidateTOTPCode(string(plaintext), strings.TrimSpace(in.Code)) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid code", nil)
		return
	}

	if err := s.store.SetUserTwoFactor(ctx, userID, secretEnc); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	_ = s.store.DeleteTwoFactorEnrollmentByToken(ctx, in.SetupToken)

	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	if err := s.store.AddRecoveryCodes(ctx, userID, codes); err != nil {
		writeStoreErr(w, err, true)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if in.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "password required", nil)
		return
	}

	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}
	if _, err := s.store.AuthenticateUser(ctx, u.Email, in.Password); err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid password", nil)
			return
		}
		writeStoreErr(w, err, true)
		return
	}

	if err := s.store.ClearUserTwoFactor(ctx, userID); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handle2FARecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	u, _ := s.store.GetUser(ctx, userID)
	ipKey := "ip:" + clientIP(r)
	emailKey := "email:" + ratelimit.NormalizeEmail(u.Email)
	if s.authRateLimit != nil && !s.authRateLimit.Allow(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}

	if err := s.store.DeleteRecoveryCodesByUser(ctx, userID); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	if err := s.store.AddRecoveryCodes(ctx, userID, codes); err != nil {
		writeStoreErr(w, err, true)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}
