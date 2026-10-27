package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"scrumboy/internal/crypto"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func newTestStoreWith2FA(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	key, err := crypto.DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("decode key: %v", err)
	}
	st := New(sqlDB, &StoreOptions{EncryptionKey: key})
	return st, func() { _ = sqlDB.Close() }
}

func TestCreateAndGetLogin2FAPending(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	token, expiresAt, err := st.CreateLogin2FAPending(ctx, u.ID, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiresAt")
	}

	got, attemptCount, err := st.GetUserByLogin2FAPendingToken(ctx, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("user id %d != %d", got.ID, u.ID)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt count %d != 0", attemptCount)
	}
}

func TestIncrementAttemptRevokesAfterMax(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token, _, err := st.CreateLogin2FAPending(ctx, u.ID, 0)
	if err != nil {
		t.Fatalf("CreateLogin2FAPending: %v", err)
	}

	for i := 0; i < 5; i++ {
		incErr := st.IncrementLogin2FAPendingAttempt(ctx, token)
		if incErr != nil {
			t.Fatalf("increment %d: %v", i, incErr)
		}
	}
	err = st.IncrementLogin2FAPendingAttempt(ctx, token)
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("expected ErrTooManyAttempts, got %v", err)
	}

	_, _, err = st.GetUserByLogin2FAPendingToken(ctx, token)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after revoke, got %v", err)
	}
}

func TestRecoveryCodesAndConsume(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	codes := GenerateRecoveryCodes(3)
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(codes))
	}
	for _, c := range codes {
		if len(c) != 9 {
			t.Fatalf("expected xxxx-xxxx format (9 chars), got %q len=%d", c, len(c))
		}
	}

	if err := st.AddRecoveryCodes(ctx, u.ID, codes); err != nil {
		t.Fatalf("add: %v", err)
	}

	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !consumed {
		t.Fatal("expected consumed=true")
	}

	consumed, _ = st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if consumed {
		t.Fatal("expected consumed=false on reuse")
	}
}

func TestRecoveryCodeConcurrentConsumptionIsSingleUse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "concurrent@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	const code = "ABCD-EFGH"
	if err := st.AddRecoveryCodes(ctx, u.ID, []string{code}); err != nil {
		t.Fatalf("AddRecoveryCodes: %v", err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, code)
			results <- consumed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ConsumeRecoveryCode: %v", err)
		}
	}
	successes := 0
	for consumed := range results {
		if consumed {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent consumptions = %d, want 1", successes)
	}
}

func TestUserIsTwoFactorActive(t *testing.T) {
	u := User{TwoFactorEnabled: true}
	if !u.IsTwoFactorActive() {
		t.Fatal("expected true when enabled")
	}
	u.TwoFactorEnabled = false
	if u.IsTwoFactorActive() {
		t.Fatal("expected false when disabled")
	}
}

func seedTwoFactorEnrollment(t *testing.T, st *Store, userID int64) string {
	t.Helper()
	secretEnc, err := st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("EncryptTOTPSecret: %v", err)
	}
	token, _, err := st.CreateTwoFactorEnrollment(context.Background(), userID, secretEnc, 0)
	if err != nil {
		t.Fatalf("CreateTwoFactorEnrollment: %v", err)
	}
	return token
}

func recoveryCodeCount(t *testing.T, st *Store, userID int64) (total, unused int) {
	t.Helper()
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ?`, userID).Scan(&total); err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&unused); err != nil {
		t.Fatalf("count unused recovery codes: %v", err)
	}
	return total, unused
}

func userTwoFactorEnabled(t *testing.T, st *Store, userID int64) bool {
	t.Helper()
	var enabled int
	if err := st.db.QueryRow(`SELECT two_factor_enabled FROM users WHERE id = ?`, userID).Scan(&enabled); err != nil {
		t.Fatalf("two_factor_enabled: %v", err)
	}
	return enabled == 1
}

func enrollmentExists(t *testing.T, st *Store, userID int64) bool {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM two_factor_enrollments WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	return n > 0
}

func recoveryCodeHashes(t *testing.T, st *Store, userID int64) []string {
	t.Helper()
	rows, err := st.db.Query(`SELECT code_hash FROM user_recovery_codes WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("list recovery hashes: %v", err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan recovery hash: %v", err)
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recovery hashes: %v", err)
	}
	return hashes
}

func recoveryCodeIDs(t *testing.T, st *Store, userID int64) []int64 {
	t.Helper()
	rows, err := st.db.Query(`SELECT id FROM user_recovery_codes WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("list recovery ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan recovery id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recovery ids: %v", err)
	}
	return ids
}

func containsRecoveryID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func matchRecoveryProof(t *testing.T, st *Store, userID int64, code string) RecoveryCodeProof {
	t.Helper()
	id, hash, err := st.MatchRecoveryCodeProof(context.Background(), userID, code)
	if err != nil || id == 0 || hash == "" {
		t.Fatalf("MatchRecoveryCodeProof: id=%d hash empty=%v err=%v", id, hash == "", err)
	}
	return RecoveryCodeProof{ID: id, Hash: hash}
}

func hashesMatchCodeSet(hashes, codes []string) bool {
	if len(hashes) != len(codes) {
		return false
	}
	used := make([]bool, len(hashes))
	for _, code := range codes {
		normalized := normalizeRecoveryCode(code)
		matched := false
		for i, hash := range hashes {
			if used[i] {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte(normalized)) == nil {
				used[i] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func TestCompleteTwoFactorEnrollmentIsAtomic(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "enable@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	codes := GenerateRecoveryCodes(RecoveryCodeCount)

	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, codes); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	if !userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("expected 2FA enabled")
	}
	if enrollmentExists(t, st, u.ID) {
		t.Fatal("expected enrollment consumed")
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes total=%d unused=%d, want %d", total, unused, RecoveryCodeCount)
	}
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), codes) {
		t.Fatal("stored hashes do not match issued recovery codes")
	}

	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay err = %v, want ErrNotFound", err)
	}
	total, unused = recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("replay mutated recovery codes total=%d unused=%d", total, unused)
	}
}

func TestCompleteTwoFactorEnrollmentRollsBackWhenInsertFails(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "rollback-enable@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	if _, err := st.db.Exec(`CREATE TRIGGER fail_recovery_insert BEFORE INSERT ON user_recovery_codes BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	err = st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount))
	if err == nil {
		t.Fatal("expected insert failure")
	}
	if userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("2FA enabled after rollback")
	}
	if !enrollmentExists(t, st, u.ID) {
		t.Fatal("enrollment consumed after rollback")
	}
	total, _ := recoveryCodeCount(t, st, u.ID)
	if total != 0 {
		t.Fatalf("recovery codes after rollback = %d, want 0", total)
	}
}

func TestCompleteTwoFactorEnrollmentHashesBeforeCommit(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "hash-enable@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	err = st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount-1))
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("short code set err = %v, want ErrValidation", err)
	}
	if userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("2FA enabled after hashing failure")
	}
	if !enrollmentExists(t, st, u.ID) {
		t.Fatal("enrollment consumed after hashing failure")
	}
}

func TestCompleteTwoFactorEnrollmentRejectsAlreadyEnabled(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "already@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	secretEnc, err := st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("EncryptTOTPSecret: %v", err)
	}
	if err := st.SetUserTwoFactor(ctx, u.ID, secretEnc); err != nil {
		t.Fatalf("SetUserTwoFactor: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	err = st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("already enabled err = %v, want ErrConflict", err)
	}
	if !userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("2FA disabled after conflict")
	}
	if !enrollmentExists(t, st, u.ID) {
		t.Fatal("enrollment consumed after already-enabled conflict")
	}
	total, _ := recoveryCodeCount(t, st, u.ID)
	if total != 0 {
		t.Fatalf("conflict inserted recovery codes = %d", total)
	}
}

func TestCompleteTwoFactorEnrollmentConcurrentSameToken(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "concurrent-enable@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount))
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent enable err = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent enrollments = %d, want 1", successes)
	}
	if !userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("expected 2FA enabled after concurrent enroll")
	}
	if enrollmentExists(t, st, u.ID) {
		t.Fatal("enrollment remained after concurrent enroll")
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes after concurrent enroll total=%d unused=%d", total, unused)
	}
}

func TestReplaceRecoveryCodesIsAtomicAndRequiresActive2FA(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "regen@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}

	replacement := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, replacement, RecoveryCodeProof{}); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	hashes := recoveryCodeHashes(t, st, u.ID)
	if hashesMatchCodeSet(hashes, original) {
		t.Fatal("original recovery codes remained after replace")
	}
	if !hashesMatchCodeSet(hashes, replacement) {
		t.Fatal("stored hashes do not match replacement set")
	}

	if err := st.ClearUserTwoFactor(ctx, u.ID); err != nil {
		t.Fatalf("ClearUserTwoFactor: %v", err)
	}
	err = st.ReplaceRecoveryCodes(ctx, u.ID, GenerateRecoveryCodes(RecoveryCodeCount), RecoveryCodeProof{})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("replace without 2FA err = %v, want ErrValidation", err)
	}
}

func TestReplaceRecoveryCodesConsumesProofAndRollsBack(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "regen-proof@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	proof := matchRecoveryProof(t, st, u.ID, original[0])

	if _, err := st.db.Exec(`CREATE TRIGGER fail_regen_insert BEFORE INSERT ON user_recovery_codes BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	err = st.ReplaceRecoveryCodes(ctx, u.ID, GenerateRecoveryCodes(RecoveryCodeCount), proof)
	if err == nil {
		t.Fatal("expected insert failure")
	}
	if !userTwoFactorEnabled(t, st, u.ID) {
		t.Fatal("2FA disabled after regen rollback")
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes after regen rollback total=%d unused=%d", total, unused)
	}
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), original) {
		t.Fatal("original recovery codes mutated after regen rollback")
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, original[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode after rollback: %v", err)
	}
	if !consumed {
		t.Fatal("proof recovery code was consumed despite rollback")
	}
}

func TestReplaceRecoveryCodesConcurrentRecoveryProofIsSingleUse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "regen-concurrent@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	proof := matchRecoveryProof(t, st, u.ID, original[0])

	sets := [][]string{GenerateRecoveryCodes(RecoveryCodeCount), GenerateRecoveryCodes(RecoveryCodeCount)}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- st.ReplaceRecoveryCodes(ctx, u.ID, sets[i], proof)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("concurrent regen err = %v, want ErrUnauthorized", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent regen = %d, want 1", successes)
	}
	hashes := recoveryCodeHashes(t, st, u.ID)
	matched := 0
	for _, set := range sets {
		if hashesMatchCodeSet(hashes, set) {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("leftover recovery hashes matched %d replacement sets, want 1 complete set", matched)
	}
	if hashesMatchCodeSet(hashes, original) {
		t.Fatal("original recovery codes mixed into replacement set")
	}
}

func TestReplaceRecoveryCodesConcurrentTOTPDoesNotMixSets(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "regen-totp-concurrent@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, GenerateRecoveryCodes(RecoveryCodeCount)); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}

	sets := [][]string{GenerateRecoveryCodes(RecoveryCodeCount), GenerateRecoveryCodes(RecoveryCodeCount)}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- st.ReplaceRecoveryCodes(ctx, u.ID, sets[i], RecoveryCodeProof{})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent TOTP regen: %v", err)
		}
	}
	hashes := recoveryCodeHashes(t, st, u.ID)
	matched := 0
	for _, set := range sets {
		if hashesMatchCodeSet(hashes, set) {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("leftover recovery hashes matched %d replacement sets, want 1 complete set", matched)
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes after concurrent TOTP regen total=%d unused=%d", total, unused)
	}
}

func TestReplaceRecoveryCodesRejectsStaleProofAfterRowIDReuse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "stale-id-reuse@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}

	proofA := matchRecoveryProof(t, st, u.ID, original[0])
	proofB := matchRecoveryProof(t, st, u.ID, original[1])
	if proofA.ID == proofB.ID {
		t.Fatal("matched proofs share a row id")
	}
	if proofA.Hash == proofB.Hash {
		t.Fatal("matched proofs share a hash")
	}

	firstSet := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, firstSet, proofA); err != nil {
		t.Fatalf("first ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryCodeIDs(t, st, u.ID)
	if !containsRecoveryID(ids, proofB.ID) {
		t.Fatalf("replacement ids %v did not reuse stale proof id %d", ids, proofB.ID)
	}

	secondSet := GenerateRecoveryCodes(RecoveryCodeCount)
	err = st.ReplaceRecoveryCodes(ctx, u.ID, secondSet, proofB)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale proof after id reuse err = %v, want ErrUnauthorized", err)
	}

	hashes := recoveryCodeHashes(t, st, u.ID)
	if !hashesMatchCodeSet(hashes, firstSet) {
		t.Fatal("first replacement set was not preserved")
	}
	if hashesMatchCodeSet(hashes, secondSet) {
		t.Fatal("stale proof committed a second replacement set")
	}
	if hashesMatchCodeSet(hashes, original) {
		t.Fatal("original recovery codes were restored")
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes after stale proof total=%d unused=%d", total, unused)
	}
}

func TestReplaceRecoveryCodesRejectsStaleProofAfterTOTPReplace(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "stale-totp-replace@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}

	stale := matchRecoveryProof(t, st, u.ID, original[1])
	totpSet := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, totpSet, RecoveryCodeProof{}); err != nil {
		t.Fatalf("TOTP ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryCodeIDs(t, st, u.ID)
	if !containsRecoveryID(ids, stale.ID) {
		t.Fatalf("TOTP replacement ids %v did not reuse stale proof id %d", ids, stale.ID)
	}

	rejectedSet := GenerateRecoveryCodes(RecoveryCodeCount)
	err = st.ReplaceRecoveryCodes(ctx, u.ID, rejectedSet, stale)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale proof after TOTP replace err = %v, want ErrUnauthorized", err)
	}

	hashes := recoveryCodeHashes(t, st, u.ID)
	if !hashesMatchCodeSet(hashes, totpSet) {
		t.Fatal("TOTP replacement set was not preserved")
	}
	if hashesMatchCodeSet(hashes, rejectedSet) {
		t.Fatal("stale proof committed a second replacement set")
	}
	total, unused := recoveryCodeCount(t, st, u.ID)
	if total != RecoveryCodeCount || unused != RecoveryCodeCount {
		t.Fatalf("recovery codes after stale TOTP-path proof total=%d unused=%d", total, unused)
	}
}

func TestReplaceRecoveryCodesRejectsIDOnlyProof(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "id-only-proof@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	proof := matchRecoveryProof(t, st, u.ID, original[0])
	err = st.ReplaceRecoveryCodes(ctx, u.ID, GenerateRecoveryCodes(RecoveryCodeCount), RecoveryCodeProof{ID: proof.ID})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("id-only proof err = %v, want ErrUnauthorized", err)
	}
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), original) {
		t.Fatal("id-only proof mutated recovery codes")
	}
}

func TestConsumeRecoveryCodeProofRejectsStaleProofAfterRowIDReuse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "consume-stale-id@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	stale := matchRecoveryProof(t, st, u.ID, original[1])
	current := matchRecoveryProof(t, st, u.ID, original[0])
	replacement := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, replacement, current); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryCodeIDs(t, st, u.ID)
	if !containsRecoveryID(ids, stale.ID) {
		t.Fatalf("replacement ids %v did not reuse stale proof id %d", ids, stale.ID)
	}

	consumed, err := st.ConsumeRecoveryCodeProof(ctx, u.ID, stale)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCodeProof stale: %v", err)
	}
	if consumed {
		t.Fatal("stale proof consumed a replacement recovery row")
	}
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), replacement) {
		t.Fatal("stale proof mutated replacement recovery codes")
	}
	ok, err := st.ConsumeRecoveryCode(ctx, u.ID, replacement[0])
	if err != nil || !ok {
		t.Fatalf("replacement plaintext consumed=%v err=%v", ok, err)
	}
}

func TestConsumeRecoveryCodeRejectsPlaintextAfterRegeneration(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "consume-old-plaintext@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	original := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, original); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	current := matchRecoveryProof(t, st, u.ID, original[0])
	replacement := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, replacement, current); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}

	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, original[1])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode old plaintext: %v", err)
	}
	if consumed {
		t.Fatal("generic helper consumed a replacement row using old plaintext")
	}
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), replacement) {
		t.Fatal("old plaintext mutated replacement recovery codes")
	}
}

func TestConsumeRecoveryCodeProofConcurrentSameProof(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "consume-proof-concurrent@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	codes := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, codes); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			consumed, err := st.ConsumeRecoveryCodeProof(ctx, u.ID, proof)
			results <- consumed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ConsumeRecoveryCodeProof: %v", err)
		}
	}
	successes := 0
	for consumed := range results {
		if consumed {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent proof consumptions = %d, want 1", successes)
	}
}

func TestConsumeRecoveryCodeProofRejectsIncompleteProofWithoutError(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "consume-incomplete@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	codes := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(ctx, u.ID, token, codes); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	proof := matchRecoveryProof(t, st, u.ID, codes[0])
	consumed, err := st.ConsumeRecoveryCodeProof(ctx, u.ID, RecoveryCodeProof{ID: proof.ID})
	if err != nil {
		t.Fatalf("incomplete proof should not surface as a store error: %v", err)
	}
	if consumed {
		t.Fatal("id-only proof consumed a recovery code")
	}
	ok, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !ok {
		t.Fatalf("id-only proof consumed the code: consumed=%v err=%v", ok, err)
	}
}

func enrollLogin2FAUser(t *testing.T, st *Store, email string) (User, []string) {
	t.Helper()
	u, err := st.BootstrapUser(context.Background(), email, "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	codes := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(context.Background(), u.ID, token, codes); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	return u, codes
}

func readyLogin2FAPending(t *testing.T, st *Store, userID int64) string {
	t.Helper()
	token, _, err := st.CreateLogin2FAPending(context.Background(), userID, 0)
	if err != nil {
		t.Fatalf("CreateLogin2FAPending: %v", err)
	}
	if err := st.IncrementLogin2FAPendingAttempt(context.Background(), token); err != nil {
		t.Fatalf("IncrementLogin2FAPendingAttempt: %v", err)
	}
	return token
}

func sessionCountForUser(t *testing.T, st *Store, userID int64) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestCompleteLogin2FAConsumesProofAndCreatesOneSession(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-once@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	session, expires, err := st.CompleteLogin2FA(ctx, pending, u.ID, proof, time.Hour)
	if err != nil {
		t.Fatalf("CompleteLogin2FA: %v", err)
	}
	if session == "" || expires.IsZero() {
		t.Fatal("expected session token and expiry")
	}
	got, err := st.GetUserBySessionToken(ctx, session)
	if err != nil || got.ID != u.ID {
		t.Fatalf("session user=%+v err=%v", got, err)
	}
	if sessionCountForUser(t, st, u.ID) != 1 {
		t.Fatalf("sessions=%d, want 1", sessionCountForUser(t, st, u.ID))
	}
	if _, _, err := st.GetUserByLogin2FAPendingToken(ctx, pending); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending remained usable: %v", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || consumed {
		t.Fatalf("proof reused: consumed=%v err=%v", consumed, err)
	}

	_, _, err = st.CompleteLogin2FA(ctx, pending, u.ID, proof, time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay err=%v, want ErrNotFound", err)
	}
	if sessionCountForUser(t, st, u.ID) != 1 {
		t.Fatalf("replay created another session: %d", sessionCountForUser(t, st, u.ID))
	}
}

func TestCompleteLogin2FATOTPDoesNotConsumeRecoveryCodes(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-totp@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)

	if _, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, RecoveryCodeProof{}, time.Hour); err != nil {
		t.Fatalf("CompleteLogin2FA TOTP: %v", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("TOTP completion consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FASessionInsertFailureRollsBackProof(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-session-fail@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	if _, err := st.db.Exec(`CREATE TRIGGER fail_login_session BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	_, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, proof, time.Hour)
	if err == nil {
		t.Fatal("expected session insert failure")
	}
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNotFound) {
		t.Fatalf("insert failure classified as caller error: %v", err)
	}
	if sessionCountForUser(t, st, u.ID) != 0 {
		t.Fatal("failed completion created a session")
	}
	if _, _, err := st.GetUserByLogin2FAPendingToken(ctx, pending); err != nil {
		t.Fatalf("pending was not restored after rollback: %v", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("session failure consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FAExpiredOrReplacedPendingDoesNotConsume(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-expired@x.com")
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	expired := readyLogin2FAPending(t, st, u.ID)
	if _, err := st.db.Exec(`UPDATE login_2fa_pending SET expires_at = 0 WHERE token_hash = ?`, hashToken(expired)); err != nil {
		t.Fatalf("expire pending: %v", err)
	}
	_, _, err := st.CompleteLogin2FA(ctx, expired, u.ID, proof, time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired pending err=%v, want ErrNotFound", err)
	}

	stale := readyLogin2FAPending(t, st, u.ID)
	_ = readyLogin2FAPending(t, st, u.ID) // latest-wins replacement
	_, _, err = st.CompleteLogin2FA(ctx, stale, u.ID, proof, time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced pending err=%v, want ErrNotFound", err)
	}
	if sessionCountForUser(t, st, u.ID) != 0 {
		t.Fatal("invalid pending created a session")
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("invalid pending consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FARejectsStaleProofAfterRowIDReuse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, original := enrollLogin2FAUser(t, st, "complete-stale@x.com")
	stale := matchRecoveryProof(t, st, u.ID, original[1])
	current := matchRecoveryProof(t, st, u.ID, original[0])
	replacement := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, replacement, current); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryCodeIDs(t, st, u.ID)
	if !containsRecoveryID(ids, stale.ID) {
		t.Fatalf("replacement ids %v did not reuse stale proof id %d", ids, stale.ID)
	}

	pending := readyLogin2FAPending(t, st, u.ID)
	_, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, stale, time.Hour)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale proof err=%v, want ErrUnauthorized", err)
	}
	if sessionCountForUser(t, st, u.ID) != 0 {
		t.Fatal("stale proof created a session")
	}
	if _, _, err := st.GetUserByLogin2FAPendingToken(ctx, pending); err != nil {
		t.Fatalf("pending was consumed by stale proof: %v", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, replacement[0])
	if err != nil || !consumed {
		t.Fatalf("stale proof consumed replacement: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FARequiresIncrementedAttempt(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-no-increment@x.com")
	pending, _, err := st.CreateLogin2FAPending(ctx, u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	proof := matchRecoveryProof(t, st, u.ID, codes[0])
	_, _, err = st.CompleteLogin2FA(ctx, pending, u.ID, proof, time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("zero-attempt pending err=%v, want ErrNotFound", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("zero-attempt completion consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FAConcurrentSameProofCreatesOneSession(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-concurrent@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, proof, time.Hour)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, notFound, other int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrNotFound):
			notFound++
		default:
			other++
			t.Errorf("unexpected concurrent error: %v", err)
		}
	}
	if succeeded != 1 || notFound != 1 || other != 0 {
		t.Fatalf("concurrent results succeeded=%d notFound=%d other=%d, want 1/1/0", succeeded, notFound, other)
	}
	if sessionCountForUser(t, st, u.ID) != 1 {
		t.Fatalf("concurrent sessions=%d, want 1", sessionCountForUser(t, st, u.ID))
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || consumed {
		t.Fatalf("losing concurrent callback consumed or left proof unused: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FAConcurrentTOTPCreatesOneSession(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-concurrent-totp@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, RecoveryCodeProof{}, time.Hour)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, notFound int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrNotFound):
			notFound++
		default:
			t.Fatalf("unexpected concurrent TOTP error: %v", err)
		}
	}
	if succeeded != 1 || notFound != 1 {
		t.Fatalf("concurrent TOTP succeeded=%d notFound=%d, want 1/1", succeeded, notFound)
	}
	if sessionCountForUser(t, st, u.ID) != 1 {
		t.Fatalf("concurrent TOTP sessions=%d, want 1", sessionCountForUser(t, st, u.ID))
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("TOTP concurrent completion consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FAAllowsAdditionalCreateSession(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, _ := enrollLogin2FAUser(t, st, "complete-multidevice@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	if _, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, RecoveryCodeProof{}, time.Hour); err != nil {
		t.Fatalf("CompleteLogin2FA: %v", err)
	}
	if _, _, err := st.CreateSession(ctx, u.ID, time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sessionCountForUser(t, st, u.ID) != 2 {
		t.Fatalf("multi-device sessions=%d, want 2", sessionCountForUser(t, st, u.ID))
	}
}

func TestCompleteLogin2FAIncompleteProofLeavesPending(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, codes := enrollLogin2FAUser(t, st, "complete-incomplete@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	_, _, err := st.CompleteLogin2FA(ctx, pending, u.ID, RecoveryCodeProof{ID: proof.ID}, time.Hour)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("incomplete proof err=%v, want ErrUnauthorized", err)
	}
	if sessionCountForUser(t, st, u.ID) != 0 {
		t.Fatal("incomplete proof created a session")
	}
	if _, _, err := st.GetUserByLogin2FAPendingToken(ctx, pending); err != nil {
		t.Fatalf("incomplete proof consumed pending: %v", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("incomplete proof consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestCompleteLogin2FADefaultSessionTTL(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, _ := enrollLogin2FAUser(t, st, "complete-ttl@x.com")
	pending := readyLogin2FAPending(t, st, u.ID)
	_, expires, err := st.CompleteLogin2FA(context.Background(), pending, u.ID, RecoveryCodeProof{}, 0)
	if err != nil {
		t.Fatalf("CompleteLogin2FA: %v", err)
	}
	delta := time.Until(expires)
	if delta < 29*24*time.Hour || delta > 31*24*time.Hour {
		t.Fatalf("session TTL = %v, want ~30 days", delta)
	}
}
