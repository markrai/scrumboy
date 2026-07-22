package httpapi

import (
	"crypto/elliptic"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math/big"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
)

const (
	pushStateEnabled       = "enabled"
	pushStateNotConfigured = "not_configured"
	pushStateInvalid       = "invalid"
	pushStateUnavailable   = "unavailable"

	pushReasonInvalidSubscriber      = "invalid_subscriber"
	pushReasonInvalidVAPIDPublicKey  = "invalid_vapid_public_key"
	pushReasonInvalidVAPIDPrivateKey = "invalid_vapid_private_key"
	pushReasonInitializationFailed   = "initialization_failed"
)

type pushStatus struct {
	State  string  `json:"state"`
	Reason *string `json:"reason"`
}

type preparedWebPushConfiguration struct {
	publicKey  string
	privateKey string
	subscriber string
	status     pushStatus
}

func newPushStatus(state, reason string) pushStatus {
	status := pushStatus{State: state}
	if reason != "" {
		status.Reason = &reason
	}
	return status
}

func prepareWebPushConfiguration(mode, publicKey, privateKey, subscriber string) preparedWebPushConfiguration {
	prepared := preparedWebPushConfiguration{
		publicKey:  strings.TrimSpace(publicKey),
		privateKey: strings.TrimSpace(privateKey),
	}
	if prepared.publicKey == "" && prepared.privateKey == "" {
		prepared.status = newPushStatus(pushStateNotConfigured, "")
		return prepared
	}

	publicBytes, err := validVAPIDPublicKey(prepared.publicKey)
	if err != nil {
		prepared.status = newPushStatus(pushStateInvalid, pushReasonInvalidVAPIDPublicKey)
		return prepared
	}
	privateBytes, err := validVAPIDPrivateKey(prepared.privateKey)
	if err != nil {
		prepared.status = newPushStatus(pushStateInvalid, pushReasonInvalidVAPIDPrivateKey)
		return prepared
	}

	curve := elliptic.P256()
	privateX, privateY := curve.ScalarBaseMult(privateBytes)
	derivedPublic := elliptic.Marshal(curve, privateX, privateY)
	if subtle.ConstantTimeCompare(publicBytes, derivedPublic) != 1 {
		prepared.status = newPushStatus(pushStateUnavailable, pushReasonInitializationFailed)
		return prepared
	}

	prepared.subscriber, err = prepareWebPushSubscriber(subscriber)
	if err != nil {
		prepared.status = newPushStatus(pushStateInvalid, pushReasonInvalidSubscriber)
		return prepared
	}

	normalizedMode := strings.TrimSpace(mode)
	if normalizedMode != "full" && normalizedMode != "anonymous" {
		normalizedMode = "full"
	}
	if normalizedMode != "full" {
		prepared.status = newPushStatus(pushStateUnavailable, "")
		return prepared
	}
	prepared.status = newPushStatus(pushStateEnabled, "")
	return prepared
}

func prepareWebPushSubscriber(raw string) (string, error) {
	subscriber := strings.TrimSpace(raw)
	if subscriber == "" {
		return "scrumboy@localhost", nil
	}
	if strings.IndexFunc(subscriber, unicode.IsControl) >= 0 {
		return "", errors.New("subscriber contains control characters")
	}

	if hasCaseInsensitivePrefix(subscriber, "mailto:") {
		subscriber = subscriber[len("mailto:"):]
		if hasCaseInsensitivePrefix(subscriber, "mailto:") {
			return "", errors.New("nested mailto prefix")
		}
		return exactMailbox(subscriber)
	}

	if parsed, err := url.Parse(subscriber); err == nil && parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" {
			return "", errors.New("subscriber is not an unambiguous HTTPS URI")
		}
		parsed.Scheme = "https"
		return parsed.String(), nil
	}

	return exactMailbox(subscriber)
}

func hasCaseInsensitivePrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix)
}

func exactMailbox(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid mailbox")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", errors.New("invalid mailbox")
	}
	return value, nil
}

func decodeVAPIDKey(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func validVAPIDPublicKey(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("missing public key")
	}
	decoded, err := decodeVAPIDKey(value)
	if err != nil || len(decoded) != 65 {
		return nil, errors.New("invalid public key")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), decoded)
	if x == nil || y == nil {
		return nil, errors.New("invalid public key")
	}
	return decoded, nil
}

func validVAPIDPrivateKey(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("missing private key")
	}
	decoded, err := decodeVAPIDKey(value)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("invalid private key")
	}
	d := new(big.Int).SetBytes(decoded)
	if d.Sign() <= 0 || d.Cmp(elliptic.P256().Params().N) >= 0 {
		return nil, errors.New("invalid private key")
	}
	return decoded, nil
}

func PushConfigured(mode, publicKey, privateKey string) bool {
	normalizedMode := strings.TrimSpace(mode)
	if normalizedMode != "full" && normalizedMode != "anonymous" {
		normalizedMode = "full"
	}
	return normalizedMode == "full" && hasCompleteVAPIDKeys(publicKey, privateKey)
}

func hasCompleteVAPIDKeys(publicKey, privateKey string) bool {
	return strings.TrimSpace(publicKey) != "" && strings.TrimSpace(privateKey) != ""
}
