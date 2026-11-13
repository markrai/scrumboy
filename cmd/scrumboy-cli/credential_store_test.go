package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func expiredSession(f *fakeOAuth, refresh string) fileConfig {
	return fileConfig{Server: f.srv.URL, OAuth: &oauthConfig{
		Server: f.srv.URL, ClientID: "cid-1", TokenEndpoint: f.srv.URL + "/oauth/token",
		RevokeEndpoint: f.srv.URL + "/oauth/revoke", Resource: f.srv.URL + "/mcp/rpc",
		AccessToken: "old", RefreshToken: refresh, ExpiresAt: time.Now().Add(-time.Minute)}}
}

func shortCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// TestRefreshLockIsNeverTakenFromALiveHolder: the lock used to be an O_EXCL
// file that any waiter deleted once it was 30s old (the token request alone may
// take 30s), and whose release deleted whatever lock file existed. A holder's
// lock must stay exclusive however old it is, and only the holder releases it.
func TestRefreshLockIsNeverTakenFromALiveHolder(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	unlockA, err := lockFile(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(cfg+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := lockFile(shortCtx(t), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquirer got the lock of a live holder (err=%v)", err)
	}
	unlockA()
	unlockC, err := lockFile(shortCtx(t), cfg)
	if err != nil {
		t.Fatalf("lock not available after the holder released it: %v", err)
	}
	unlockC()
}

// TestRefreshLockWaitIsInterruptible: Ctrl-C (a canceled context) stops a
// process that is waiting for another one's refresh.
func TestRefreshLockWaitIsInterruptible(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	unlock, err := lockFile(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := lockFile(ctx, cfg); !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

// TestHelperHoldRefreshLock is not a test: TestRefreshLockReleasedWhenHolderDies
// runs it in a child process that takes the lock and waits to be killed.
func TestHelperHoldRefreshLock(t *testing.T) {
	cfg := os.Getenv("SCRUMBOY_CLI_TEST_HOLD_LOCK")
	if cfg == "" {
		t.Skip("helper process only")
	}
	if _, err := lockFile(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Minute)
}

// TestRefreshLockReleasedWhenHolderDies: a process killed while holding the
// lock (no cleanup runs) must not block the next one, without any stale-lock
// heuristic.
func TestRefreshLockReleasedWhenHolderDies(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldRefreshLock$", "-test.count=1")
	cmd.Env = append(os.Environ(), "SCRUMBOY_CLI_TEST_HOLD_LOCK="+cfg)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper did not take the lock: %q %v", line, err)
	}
	if _, err := lockFile(shortCtx(t), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock held by another process was acquired (err=%v)", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := lockFile(ctx, cfg)
	if err != nil {
		t.Fatalf("lock of a killed process was not released: %v", err)
	}
	unlock()
}

// TestRefreshSurvivesConfigOpenDuringSave: on Windows, replacing a file that
// another process has open (a concurrent CLI reading its config, an antivirus
// scan) fails with "Access is denied". After a refresh that is fatal: the old
// refresh token is already spent, so losing the write loses the session.
func TestRefreshSurvivesConfigOpenDuringSave(t *testing.T) {
	f := newFakeOAuth(t)
	f.validAccess, f.validRefr = "x", "rt-1"
	h := oauthHarness(t, f)
	if err := saveConfig(h.cfgPath, expiredSession(f, "rt-1")); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(300 * time.Millisecond); reader.Close() }()

	if code := h.run("project", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	cfg, err := loadConfig(h.cfgPath)
	if err != nil || cfg.OAuth == nil || cfg.OAuth.RefreshToken != "rt-2" {
		t.Fatalf("rotated refresh token was not persisted: %+v err=%v", cfg.OAuth, err)
	}
	h2 := oauthHarness(t, f)
	h2.cfgPath = h.cfgPath
	if code := h2.run("project", "list", "--config", h.cfgPath); code != 0 {
		t.Fatalf("session unusable after the refresh: exit %d: %s", code, h2.err)
	}
}

// TestLogoutWaitsForRefreshAndRevokesCurrentTokens: logout used to act on the
// config it read at startup without the lock, so a refresh finishing in
// another process could write the session back after logout (with tokens
// logout never revoked).
func TestLogoutWaitsForRefreshAndRevokesCurrentTokens(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	if err := saveConfig(h.cfgPath, expiredSession(f, "rt-1")); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockFile(context.Background(), h.cfgPath) // a refresh in progress elsewhere
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var code int
	wg.Add(1)
	go func() { defer wg.Done(); code = h.run("auth", "logout") }()
	time.Sleep(200 * time.Millisecond)
	f.mu.Lock()
	early := len(f.revoked)
	f.mu.Unlock()
	if early != 0 {
		t.Fatal("logout did not wait for the refresh in progress")
	}
	rotated := expiredSession(f, "rt-2")
	rotated.OAuth.AccessToken = "at-2"
	if err := saveConfig(h.cfgPath, rotated); err != nil {
		t.Fatal(err)
	}
	unlock()
	wg.Wait()
	if code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.err)
	}
	if got := strings.Join(f.revoked, ","); got != "rt-2,at-2" {
		t.Fatalf("revoked %q, want the rotated tokens rt-2,at-2", got)
	}
	if cfg, _ := loadConfig(h.cfgPath); cfg.OAuth != nil || cfg.Token != "" {
		t.Fatalf("credentials not cleared: %+v", cfg)
	}
}

// TestLogoutReportsFailedRevocation: the docs promise logout revokes the
// session; when the server does not confirm, the local session is still
// removed but the user must be told it remains valid server-side.
func TestLogoutReportsFailedRevocation(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	cfg := expiredSession(f, "rt-1")
	cfg.OAuth.RevokeEndpoint = f.srv.URL + "/oauth/revoke-unavailable" // 404
	if err := saveConfig(h.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if code := h.run("auth", "logout"); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.err)
	}
	if !strings.Contains(h.err.String(), "did not confirm revoking") || strings.Contains(h.err.String(), "rt-1") {
		t.Fatalf("stderr = %q", h.err)
	}
	if got, _ := loadConfig(h.cfgPath); got.OAuth != nil {
		t.Fatalf("credentials not cleared: %+v", got)
	}
}

// TestLoginIsNotOverwrittenByAnOlderRefresh: a refresh that started before a
// login (it read the old session, then waits on the token endpoint) must not
// write the old session back over the login that completed meanwhile. Login
// saves under the refresh lock, so it lands after the refresh instead.
func TestLoginIsNotOverwrittenByAnOlderRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at-2","refresh_token":"rt-2","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(slow.Close)
	srv, _ := fakeServer(t, map[string]string{
		"me_get": `{"ok":true,"data":{"userId":1,"email":"a@b.c","name":"Ann"},"meta":{}}`,
	})

	h := newHarness(t, "", map[string]string{envToken: ""})
	h.app.in = strings.NewReader("sb_new\n")
	old := fileConfig{Server: slow.URL, Project: "demo", OAuth: &oauthConfig{
		Server: slow.URL, ClientID: "cid-1", TokenEndpoint: slow.URL + "/oauth/token", Resource: slow.URL + "/mcp/rpc",
		AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(-time.Minute)}}
	if err := saveConfig(h.cfgPath, old); err != nil {
		t.Fatal(err)
	}

	refresher := &cmdCtx{s: settings{ConfigPath: h.cfgPath, OAuth: old.OAuth}}
	refreshed := make(chan error, 1)
	go func() { _, _, err := refresher.refreshSession(context.Background()); refreshed <- err }()
	<-entered // the refresh holds the lock and has read the old session

	loggedIn := make(chan int, 1)
	go func() { loggedIn <- h.run("auth", "login", "--server", srv.URL, "--with-token") }()
	code := -1
	select {
	case code = <-loggedIn: // only without the lock: login saved mid-refresh
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-refreshed; err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if code == -1 {
		code = <-loggedIn
	}
	if code != 0 {
		t.Fatalf("login exit %d: %s", code, h.err)
	}
	cfg, err := loadConfig(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "sb_new" || cfg.Server != srv.URL || cfg.OAuth != nil {
		t.Fatalf("the completed login was overwritten: server=%s token=%q oauth=%+v", cfg.Server, cfg.Token, cfg.OAuth)
	}
	if cfg.Project != "demo" {
		t.Errorf("login dropped the saved default project: %+v", cfg)
	}
}
