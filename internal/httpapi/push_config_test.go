package httpapi

import (
	"encoding/base64"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func TestPrepareWebPushSubscriber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "", want: "scrumboy@localhost"},
		{name: "plain email", input: "ops@example.com", want: "ops@example.com"},
		{name: "mailto", input: "mailto:ops@example.com", want: "ops@example.com"},
		{name: "mixed case mailto", input: "MaIlTo:ops@example.com", want: "ops@example.com"},
		{name: "HTTPS URI", input: "HTTPS://example.com/contact?team=ops", want: "https://example.com/contact?team=ops"},
		{name: "nested mailto", input: "mailto:MAILTO:ops@example.com", wantErr: true},
		{name: "empty mailto", input: "mailto:", wantErr: true},
		{name: "display mailbox", input: "Ops <ops@example.com>", wantErr: true},
		{name: "HTTP URI", input: "http://example.com/contact", wantErr: true},
		{name: "HTTPS userinfo", input: "https://user@example.com/contact", wantErr: true},
		{name: "HTTPS fragment", input: "https://example.com/contact#ops", wantErr: true},
		{name: "ambiguous HTTPS", input: "https:example.com/contact", wantErr: true},
		{name: "control character", input: "ops@\nexample.com", wantErr: true},
		{name: "not a mailbox", input: "not an email", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := prepareWebPushSubscriber(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("prepareWebPushSubscriber(%q) = %q, want error", tc.input, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("prepareWebPushSubscriber(%q) = %q, %v; want %q, nil", tc.input, got, err, tc.want)
			}
			if strings.Contains(strings.ToLower(got), "mailto:mailto:") {
				t.Fatalf("prepared subscriber contains nested mailto: %q", got)
			}
		})
	}
}

func TestPrepareWebPushConfigurationStates(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	otherPrivate, otherPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate second VAPID key pair: %v", err)
	}
	_ = otherPrivate
	publicKeyBytes, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		t.Fatalf("decode generated public key: %v", err)
	}
	privateKeyBytes, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil {
		t.Fatalf("decode generated private key: %v", err)
	}
	paddedPublicKey := base64.URLEncoding.EncodeToString(publicKeyBytes)
	paddedPrivateKey := base64.URLEncoding.EncodeToString(privateKeyBytes)

	tests := []struct {
		name        string
		mode        string
		publicKey   string
		privateKey  string
		subscriber  string
		wantState   string
		wantReason  string
		wantSub     string
		wantPublic  string
		wantPrivate string
	}{
		{name: "not configured", mode: "full", wantState: pushStateNotConfigured},
		{name: "missing public key", mode: "full", privateKey: privateKey, wantState: pushStateInvalid, wantReason: pushReasonInvalidVAPIDPublicKey},
		{name: "invalid public key", mode: "full", publicKey: "invalid", privateKey: privateKey, wantState: pushStateInvalid, wantReason: pushReasonInvalidVAPIDPublicKey},
		{name: "missing private key", mode: "full", publicKey: publicKey, wantState: pushStateInvalid, wantReason: pushReasonInvalidVAPIDPrivateKey},
		{name: "invalid private key", mode: "full", publicKey: publicKey, privateKey: "invalid", wantState: pushStateInvalid, wantReason: pushReasonInvalidVAPIDPrivateKey},
		{name: "mismatched pair", mode: "full", publicKey: otherPublic, privateKey: privateKey, wantState: pushStateUnavailable, wantReason: pushReasonInitializationFailed},
		{name: "invalid subscriber", mode: "full", publicKey: publicKey, privateKey: privateKey, subscriber: "mailto:mailto:ops@example.com", wantState: pushStateInvalid, wantReason: pushReasonInvalidSubscriber},
		{name: "anonymous unavailable", mode: "anonymous", publicKey: publicKey, privateKey: privateKey, subscriber: "ops@example.com", wantState: pushStateUnavailable, wantSub: "ops@example.com"},
		{name: "enabled", mode: "full", publicKey: publicKey, privateKey: privateKey, subscriber: "mailto:ops@example.com", wantState: pushStateEnabled, wantSub: "ops@example.com"},
		{name: "enabled with surrounding key whitespace", mode: "full", publicKey: " \t" + publicKey + "\r\n", privateKey: "\n" + privateKey + "  ", subscriber: "ops@example.com", wantState: pushStateEnabled, wantSub: "ops@example.com", wantPublic: publicKey, wantPrivate: privateKey},
		{name: "enabled with padded URL-safe base64", mode: "full", publicKey: paddedPublicKey, privateKey: paddedPrivateKey, subscriber: "ops@example.com", wantState: pushStateEnabled, wantSub: "ops@example.com", wantPublic: paddedPublicKey, wantPrivate: paddedPrivateKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := prepareWebPushConfiguration(tc.mode, tc.publicKey, tc.privateKey, tc.subscriber)
			if got.status.State != tc.wantState || got.subscriber != tc.wantSub {
				t.Fatalf("prepareWebPushConfiguration() = sub %q status %+v; want sub %q state %q", got.subscriber, got.status, tc.wantSub, tc.wantState)
			}
			if tc.wantPublic != "" && got.publicKey != tc.wantPublic {
				t.Fatalf("normalized public key = %q, want %q", got.publicKey, tc.wantPublic)
			}
			if tc.wantPrivate != "" && got.privateKey != tc.wantPrivate {
				t.Fatalf("normalized private key = %q, want %q", got.privateKey, tc.wantPrivate)
			}
			if tc.wantReason == "" {
				if got.status.Reason != nil {
					t.Fatalf("reason = %q, want null", *got.status.Reason)
				}
			} else if got.status.Reason == nil || *got.status.Reason != tc.wantReason {
				t.Fatalf("reason = %v, want %q", got.status.Reason, tc.wantReason)
			}
		})
	}
}
