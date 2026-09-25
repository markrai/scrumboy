package store

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mobileTestProof(fill byte) string {
	return base64.RawURLEncoding.EncodeToString(bytesOf(fill, 32))
}

func bytesOf(fill byte, size int) []byte {
	value := make([]byte, size)
	for i := range value {
		value[i] = fill
	}
	return value
}

func createMobileOIDCTestGrant(t *testing.T, st *Store) (state, verifier, code string, userID int64) {
	t.Helper()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "mobile@example.com", "Password123!", "Mobile User")
	if err != nil {
		t.Fatal(err)
	}
	state = mobileTestProof('s')
	verifier = mobileTestProof('v')
	if err := st.CreateMobileOIDCFlow(ctx, state, s256Challenge(verifier), "/dashboard?view=mine", time.Minute); err != nil {
		t.Fatal(err)
	}
	code, _, _, err = st.CreateMobileOIDCHandoffGrant(ctx, state, user.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return state, verifier, code, user.ID
}

func TestMobileOIDCFlowPersistsOnlyHashedStateAndChallenge(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	state := mobileTestProof('s')
	verifier := mobileTestProof('v')
	challenge := s256Challenge(verifier)
	if err := st.CreateMobileOIDCFlow(ctx, state, challenge, "/dashboard", time.Minute); err != nil {
		t.Fatal(err)
	}

	var storedState, storedChallenge, method string
	if err := st.db.QueryRow(`SELECT state_hash, handoff_challenge, challenge_method FROM mobile_oidc_flows`).Scan(
		&storedState, &storedChallenge, &method,
	); err != nil {
		t.Fatal(err)
	}
	if storedState == state || storedState != hashToken(state) {
		t.Fatalf("raw state persisted or hash mismatch: %q", storedState)
	}
	if storedChallenge != challenge || storedChallenge == verifier || method != "S256" {
		t.Fatalf("app proof persistence mismatch: challenge=%q method=%q", storedChallenge, method)
	}

	// A fresh Store over the same database still recognizes the trusted marker.
	fresh := New(st.db, nil)
	flow, err := fresh.GetMobileOIDCFlow(ctx, state)
	if err != nil || flow.ReturnTo != "/dashboard" || flow.HandoffChallenge != challenge {
		t.Fatalf("fresh store did not restore mobile flow: flow=%+v err=%v", flow, err)
	}
}

func TestMobileOIDCWrongProofDoesNotConsumeGrant(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	state, verifier, code, _ := createMobileOIDCTestGrant(t, st)
	ctx := context.Background()
	if _, err := st.ExchangeMobileOIDCHandoff(ctx, code, mobileTestProof('x'), verifier, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong state err=%v", err)
	}
	if _, err := st.ExchangeMobileOIDCHandoff(ctx, code, state, mobileTestProof('x'), time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong verifier err=%v", err)
	}
	exchange, err := st.ExchangeMobileOIDCHandoff(ctx, code, state, verifier, time.Hour)
	if err != nil || exchange.SessionToken == "" || exchange.ReturnTo != "/dashboard?view=mine" {
		t.Fatalf("correct proof failed after rejected attempts: exchange=%+v err=%v", exchange, err)
	}
	if _, err := st.GetUserBySessionToken(ctx, exchange.SessionToken); err != nil {
		t.Fatalf("ordinary session was not created: %v", err)
	}
}

func TestMobileOIDCExpiredGrantFailsClosed(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	state, verifier, code, _ := createMobileOIDCTestGrant(t, st)
	if _, err := st.db.Exec(`UPDATE mobile_oidc_handoff_grants SET expires_at = ?`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExchangeMobileOIDCHandoff(context.Background(), code, state, verifier, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired grant err=%v", err)
	}
}

func TestMobileOIDCConcurrentExchangeCreatesExactlyOneSession(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	state, verifier, code, userID := createMobileOIDCTestGrant(t, st)

	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if exchange, err := st.ExchangeMobileOIDCHandoff(context.Background(), code, state, verifier, time.Hour); err == nil && exchange.SessionToken != "" {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful exchanges=%d, want 1", successes.Load())
	}
	var sessions int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("sessions created=%d, want 1", sessions)
	}
}
