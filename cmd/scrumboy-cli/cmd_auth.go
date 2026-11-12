package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"
)

const authUsage = `auth <login|logout|status>

  login    save a server URL and sb_ API token (verified with me_get)
  logout   remove the saved token
  status   show who the current credentials authenticate as

Create a token in the web UI or with POST /api/me/tokens (see API.md).
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
	var withToken bool
	c, err := a.parse("auth login", "auth login [--server URL] [--with-token]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&withToken, "with-token", false, "read the token from stdin (keeps it out of argv and shell history)")
	})
	if err != nil {
		return err
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
	if token == "" {
		if !c.isTTY {
			return usagef("no token: use --with-token (stdin) or set %s", envToken)
		}
		fmt.Fprint(c.err, "API token (sb_...): ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(c.err)
		if err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		token = strings.TrimSpace(string(raw))
	}
	server, err = normalizeServer(server)
	if err != nil {
		return usagef("%v", err)
	}
	if warn := insecureWarning(server); warn != "" {
		fmt.Fprintln(c.err, warn)
	}

	c.s.Server, c.s.Token = server, token
	res, err := c.call("me_get", nil)
	if err != nil {
		return fmt.Errorf("could not verify credentials: %w", err)
	}
	var me meResp
	if err := res.Decode(&me); err != nil {
		return err
	}

	cfg := c.file
	cfg.Server, cfg.Token = server, token
	if err := saveConfig(c.s.ConfigPath, cfg); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(me)
	}
	fmt.Fprintf(c.out, "Logged in to %s as %s <%s>\nSaved to %s\n", server, me.Name, me.Email, c.s.ConfigPath)
	return nil
}

func authLogout(a *app, args []string) error {
	c, err := a.parse("auth logout", "auth logout", args, nil)
	if err != nil {
		return err
	}
	cfg := c.file
	if cfg.Token == "" {
		fmt.Fprintln(c.out, "Not logged in (no saved token).")
		return nil
	}
	cfg.Token = ""
	if err := saveConfig(c.s.ConfigPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Removed saved token from %s\n", c.s.ConfigPath)
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
	fmt.Fprintf(c.out, "Config:  %s\n", c.s.ConfigPath)
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
