package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"
)

const authUsage = `auth <login|logout|status>

  login    sign in: an sb_ API token (default) or OAuth in the browser (--oauth)
  logout   remove the saved credentials (revokes an OAuth session)
  status   show who the current credentials authenticate as

API token: create one in the web UI or with POST /api/me/tokens (see API.md),
then 'auth login --with-token'. OAuth: 'auth login --oauth' opens your browser
(PKCE, loopback redirect) and keeps the session fresh with a refresh token.
`

func cmdAuth(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, authUsage)
		return usagef("missing auth subcommand")
	}
	switch args[0] {
	case "login":
		return authLogin(a, args[1:])
	case "logout":
		return authLogout(a, args[1:])
	case "status":
		return authStatus(a, args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(a.out, authUsage)
		return nil
	}
	return usagef("unknown auth subcommand %q", args[0])
}

type meResp struct {
	UserID            int64  `json:"userId"`
	StableUserID      string `json:"stableUserId"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	IsService         bool   `json:"isService"`
	CanCreateProjects bool   `json:"canCreateProjects"`
}

func authLogin(a *app, args []string) error {
	var withToken, useOAuth, noBrowser bool
	c, err := a.parse("auth login", "auth login [--server URL] [--with-token | --oauth [--no-browser]]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&withToken, "with-token", false, "read the token from stdin (keeps it out of argv and shell history)")
		fs.BoolVar(&useOAuth, "oauth", false, "sign in with OAuth in your browser instead of an API token")
		fs.BoolVar(&noBrowser, "no-browser", false, "with --oauth: print the URL instead of opening a browser (paste the redirect back)")
	})
	if err != nil {
		return err
	}
	if useOAuth && withToken {
		return usagef("--oauth and --with-token are mutually exclusive")
	}
	if noBrowser && !useOAuth {
		return usagef("--no-browser only applies with --oauth")
	}

	server, token := c.s.Server, c.s.Token
	if withToken {
		raw, err := io.ReadAll(io.LimitReader(c.in, 4096))
		if err != nil {
			return fmt.Errorf("read token from stdin: %w", err)
		}
		token = strings.TrimSpace(string(raw))
	}
	if server == "" {
		if !c.isTTY {
			return usagef("no server: pass --server or set %s", envServer)
		}
		fmt.Fprint(c.err, "Scrumboy server URL: ")
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		server = strings.TrimSpace(line)
	}
	server, err = normalizeServer(server)
	if err != nil {
		return usagef("%v", err)
	}
	if warn := insecureWarning(server); warn != "" {
		fmt.Fprintln(c.err, warn)
	}

	var newToken string
	var newOAuth *oauthConfig
	c.s.Server, c.s.Token, c.s.OAuth = server, "", nil
	if useOAuth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		oc, err := oauthLogin(ctx, c, server, noBrowser)
		if err != nil {
			return err
		}
		c.s.OAuth = oc
		newOAuth = oc
	} else {
		if token == "" {
			if !c.isTTY {
				return usagef("no credentials: use --oauth, --with-token (stdin) or set %s", envToken)
			}
			fmt.Fprint(c.err, "API token (sb_...): ")
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(c.err)
			if err != nil {
				return fmt.Errorf("read token: %w", err)
			}
			token = strings.TrimSpace(string(raw))
		}
		c.s.Token = token
		newToken = token
	}

	res, err := c.call("me_get", nil)
	if err != nil {
		return fmt.Errorf("could not verify credentials: %w", err)
	}
	var me meResp
	if err := res.Decode(&me); err != nil {
		return err
	}

	if err := saveLogin(c.s.ConfigPath, server, newToken, newOAuth); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(me)
	}
	fmt.Fprintf(c.out, "Logged in to %s as %s <%s>\nSaved to %s\n", server, me.Name, me.Email, c.s.ConfigPath)
	return nil
}

// saveLogin persists a completed login under the refresh lock, which is taken
// only now, not across the interactive part. A refresh that another process
// started earlier finishes first; saving without the lock would let it write
// the old session back over this login. The config is re-read under the lock
// so the other settings it holds are kept as they are now.
func saveLogin(path, server, token string, oc *oauthConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	unlock, err := lockFile(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := loadConfig(path)
	if err != nil {
		return err // never overwrite a config we could not read
	}
	cfg.Server, cfg.Token, cfg.OAuth = server, token, oc
	return saveConfig(path, cfg)
}

func authLogout(a *app, args []string) error {
	c, err := a.parse("auth logout", "auth logout", args, nil)
	if err != nil {
		return err
	}
	if c.file.Token == "" && c.file.OAuth == nil {
		fmt.Fprintln(c.out, "Not logged in (no saved credentials).")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Hold the refresh lock and act on the config as it is now: a refresh in
	// flight in another process may have rotated the tokens since we read
	// them, and must not write the session back after it is removed.
	unlock, err := lockFile(ctx, c.s.ConfigPath)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := loadConfig(c.s.ConfigPath)
	if err != nil {
		return err
	}
	var revokeErr error
	if cfg.OAuth != nil {
		server, _ := normalizeServer(cfg.Server)
		revokeErr = revokeOAuth(ctx, server, *cfg.OAuth) // the local session is removed regardless
	}
	cfg.Token, cfg.OAuth = "", nil
	if err := saveConfig(c.s.ConfigPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Removed saved credentials from %s\n", c.s.ConfigPath)
	if revokeErr != nil {
		fmt.Fprintf(c.err, "warning: the server did not confirm revoking the OAuth session (%v); it stays valid until it expires\n", revokeErr)
	}
	return nil
}

func authStatus(a *app, args []string) error {
	c, err := a.parse("auth status", "auth status", args, nil)
	if err != nil {
		return err
	}
	res, err := c.call("me_get", nil)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(res.Data)
	}
	var me meResp
	if err := res.Decode(&me); err != nil {
		return err
	}
	kind := "user"
	if me.IsService {
		kind = "service account"
	}
	fmt.Fprintf(c.out, "Server:  %s\nUser:    %s <%s> (%s, id %d)\n", c.s.Server, me.Name, me.Email, kind, me.UserID)
	method := "API token"
	switch {
	case c.s.OAuth != nil:
		method = "OAuth session"
		if !c.s.OAuth.ExpiresAt.IsZero() {
			method += " (access token expires " + c.s.OAuth.ExpiresAt.Local().Format("15:04:05") + "; refreshed automatically)"
		}
	case !strings.HasPrefix(c.s.Token, "sb_"):
		method = "OAuth bearer token"
	}
	fmt.Fprintf(c.out, "Auth:    %s\nConfig:  %s\n", method, c.s.ConfigPath)
	return nil
}

// normalizeServer validates a server URL and strips any trailing slash.
func normalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("server URL is empty")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid server URL %q", s)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// insecureWarning flags tokens that would travel over cleartext HTTP to a
// non-local host.
func insecureWarning(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return ""
	}
	return "warning: " + server + " uses plain http; your API token will be sent unencrypted"
}
