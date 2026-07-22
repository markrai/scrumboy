package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func TestAuthStatus_WebPushStatesAndReasons(t *testing.T) {
	otherPrivate, otherPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate mismatched VAPID key pair: %v", err)
	}
	_ = otherPrivate

	tests := []struct {
		name            string
		opts            Options
		wantState       string
		wantReason      any
		wantConfigured  bool
		forbiddenValues []string
	}{
		{
			name:            "enabled",
			opts:            Options{ScrumboyMode: "full", VAPIDPublicKey: " \t" + testVapidPub + "\r\n", VAPIDPrivateKey: "\n" + testVapidPriv + "  ", VAPIDSubscriber: "mailto:ops@example.com"},
			wantState:       pushStateEnabled,
			wantReason:      nil,
			wantConfigured:  true,
			forbiddenValues: []string{testVapidPub, testVapidPriv},
		},
		{
			name:       "not configured",
			opts:       Options{ScrumboyMode: "full"},
			wantState:  pushStateNotConfigured,
			wantReason: nil,
		},
		{
			name:            "invalid subscriber",
			opts:            Options{ScrumboyMode: "full", VAPIDPublicKey: testVapidPub, VAPIDPrivateKey: testVapidPriv, VAPIDSubscriber: "mailto:mailto:secret@example.com"},
			wantState:       pushStateInvalid,
			wantReason:      pushReasonInvalidSubscriber,
			forbiddenValues: []string{"secret@example.com", "mailto:mailto:"},
		},
		{
			name:            "invalid public key",
			opts:            Options{ScrumboyMode: "full", VAPIDPublicKey: "invalid-public-secret", VAPIDPrivateKey: testVapidPriv},
			wantState:       pushStateInvalid,
			wantReason:      pushReasonInvalidVAPIDPublicKey,
			forbiddenValues: []string{"invalid-public-secret"},
		},
		{
			name:            "invalid private key",
			opts:            Options{ScrumboyMode: "full", VAPIDPublicKey: testVapidPub, VAPIDPrivateKey: "invalid-private-secret"},
			wantState:       pushStateInvalid,
			wantReason:      pushReasonInvalidVAPIDPrivateKey,
			forbiddenValues: []string{"invalid-private-secret"},
		},
		{
			name:            "initialization unavailable",
			opts:            Options{ScrumboyMode: "full", VAPIDPublicKey: otherPublic, VAPIDPrivateKey: testVapidPriv},
			wantState:       pushStateUnavailable,
			wantReason:      pushReasonInitializationFailed,
			forbiddenValues: []string{otherPublic, testVapidPriv},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			tc.opts.Logger = log.New(&logs, "", 0)
			ts, _, cleanup := newTestHTTPServerWithOptions(t, tc.opts)
			defer cleanup()

			unauthenticated := map[string]any{}
			resp, body := doJSON(t, http.DefaultClient, http.MethodGet, ts.URL+"/api/auth/status", nil, &unauthenticated)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("unauthenticated status=%d body=%s", resp.StatusCode, body)
			}
			if _, ok := unauthenticated["push"]; ok {
				t.Fatalf("unauthenticated response exposed detailed push status: %s", body)
			}

			client := newCookieClient(t)
			bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")
			status := map[string]any{}
			resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &status)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("authenticated status=%d body=%s", resp.StatusCode, body)
			}
			push, ok := status["push"].(map[string]any)
			if !ok {
				t.Fatalf("authenticated response missing push object: %s", body)
			}
			if push["state"] != tc.wantState || push["reason"] != tc.wantReason {
				t.Fatalf("push=%+v, want state=%q reason=%#v", push, tc.wantState, tc.wantReason)
			}
			if status["pushConfigured"] != tc.wantConfigured {
				t.Fatalf("pushConfigured=%#v, want %v", status["pushConfigured"], tc.wantConfigured)
			}
			for _, forbidden := range tc.forbiddenValues {
				if bytes.Contains(body, []byte(forbidden)) || strings.Contains(logs.String(), forbidden) {
					t.Fatalf("configured value %q leaked in response or logs", forbidden)
				}
			}
			if tc.wantReason != nil && !strings.Contains(logs.String(), fmt.Sprint(tc.wantReason)) {
				t.Fatalf("sanitized startup log %q does not contain reason %q", logs.String(), tc.wantReason)
			}
		})
	}
}

func TestAuthStatus_WebPushDetailAvailableToEverySignedInRole(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		ScrumboyMode:    "full",
		VAPIDPublicKey:  testVapidPub,
		VAPIDPrivateKey: testVapidPriv,
	})
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")

	users := []struct {
		name     string
		email    string
		password string
		role     string
	}{
		{name: "Regular", email: "user@example.com", password: "password123", role: "user"},
		{name: "Admin", email: "admin@example.com", password: "password123", role: "admin"},
	}
	for _, user := range users {
		var created map[string]any
		resp, body := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
			"name": user.name, "email": user.email, "password": user.password,
		}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", user.role, resp.StatusCode, body)
		}
		if user.role == "admin" {
			resp, body = doJSON(t, ownerClient, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", int64(created["id"].(float64))), map[string]any{"role": "admin"}, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("promote admin status=%d body=%s", resp.StatusCode, body)
			}
		}
	}

	accounts := append(users, struct {
		name     string
		email    string
		password string
		role     string
	}{name: "Owner", email: "owner@example.com", password: "password123", role: "owner"})
	for _, account := range accounts {
		t.Run(account.role, func(t *testing.T) {
			client := newCookieClient(t)
			loginUserClient(t, client, ts.URL, account.email, account.password)
			var status map[string]any
			resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &status)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			encoded, _ := json.Marshal(status["push"])
			if string(encoded) != `{"reason":null,"state":"enabled"}` {
				t.Fatalf("role %s push status=%s", account.role, encoded)
			}
		})
	}
}
