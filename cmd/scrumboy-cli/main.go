// Command scrumboy-cli is a command-line client for a Scrumboy server.
//
// It talks to the documented MCP HTTP tool interface (API.md) using an sb_
// API token, and shares no code with the server.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// version is overridden at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const progName = "scrumboy-cli"

// app holds the process-level dependencies so commands are testable.
type app struct {
	out, err io.Writer
	in       io.Reader
	getenv   func(string) string
	isTTY    bool
}

func defaultApp() *app {
	return &app{
		out:    os.Stdout,
		err:    os.Stderr,
		in:     os.Stdin,
		getenv: os.Getenv,
		isTTY:  term.IsTerminal(int(os.Stdin.Fd())),
	}
}

func main() {
	os.Exit(run(os.Args[1:], defaultApp()))
}

// usageError marks a mistake in how the command was invoked (exit code 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return usageError{fmt.Sprintf(format, args...)}
}

var errHelp = errors.New("help requested")

type handler func(a *app, args []string) error

var commands = map[string]handler{}

func init() {
	commands["auth"] = cmdAuth
	commands["project"] = cmdProject
	commands["board"] = cmdBoard
	commands["todo"] = cmdTodo
	commands["sprint"] = cmdSprint
	commands["tag"] = cmdTag
	commands["member"] = cmdMember
	commands["column"] = cmdColumn
	commands["priority"] = cmdPriority
	commands["tool"] = cmdTool
	commands["version"] = func(a *app, _ []string) error {
		fmt.Fprintf(a.out, "%s %s\n", progName, version)
		return nil
	}
}

func run(args []string, a *app) int {
	if len(args) == 0 {
		fmt.Fprint(a.err, rootUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(a.out, rootUsage)
		return 0
	case "--version", "-version":
		args = []string{"version"}
	}
	h, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(a.err, "%s: unknown command %q\n\n%s", progName, args[0], rootUsage)
		return 2
	}
	err := h(a, args[1:])
	switch {
	case err == nil, errors.Is(err, errHelp):
		return 0
	case errors.As(err, new(usageError)):
		fmt.Fprintf(a.err, "%s: %v\nRun '%s %s -h' for usage.\n", progName, err, progName, args[0])
		return 2
	default:
		fmt.Fprintf(a.err, "%s: %v\n", progName, err)
		return 1
	}
}

const rootUsage = `scrumboy-cli - command-line client for Scrumboy

Usage:
  scrumboy-cli <command> [subcommand] [flags]

Commands:
  auth       login, logout, status
  project    list, create, update, delete
  board      show a project's board
  todo       list, get, create, update, move, delete, search, archive,
             restore, links, link, unlink
  sprint     list, active, create, activate, close
  tag        list a project's tags
  member     list a project's members
  column     list a project's workflow columns
  priority   list a project's priority tiers
  tool       call any server tool directly (escape hatch for scripting)
  version    print the version

Global flags (accepted after the command name, by every command):
  --server URL    Scrumboy server URL   (env SCRUMBOY_URL)
  --token TOKEN   sb_ API token         (env SCRUMBOY_TOKEN; prefer 'auth login')
  -p, --project   default project slug  (env SCRUMBOY_PROJECT)
  --config PATH   config file           (env SCRUMBOY_CLI_CONFIG)
  --json          print machine-readable JSON instead of tables

Settings are resolved flag > environment > config file.
Todos are addressed as 'slug/12', 'slug#12', or just '12' with -p.
Run 'scrumboy-cli <command> -h' for details.
`

// commonFlags are bound on every command's FlagSet.
type commonFlags struct {
	server, token, project, config string
	json                           bool
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.server, "server", "", "Scrumboy server URL")
	fs.StringVar(&c.token, "token", "", "sb_ API token")
	fs.StringVar(&c.project, "project", "", "project slug")
	fs.StringVar(&c.project, "p", "", "project slug (shorthand)")
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.BoolVar(&c.json, "json", false, "print JSON")
}

// cmdCtx is a parsed invocation: resolved settings plus positional args.
type cmdCtx struct {
	*app
	s    settings
	file fileConfig
	json bool
	pos  []string
}

// parse runs a FlagSet over args, allowing flags before, between and after
// positional arguments (the stdlib flag package stops at the first positional).
func (a *app) parse(name, usage string, args []string, bind func(*flag.FlagSet)) (*cmdCtx, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	var cf commonFlags
	cf.bind(fs)
	if bind != nil {
		bind(fs)
	}
	fs.Usage = func() {
		fmt.Fprintf(a.err, "Usage: %s %s\n\nFlags:\n", progName, usage)
		fs.PrintDefaults()
	}

	// Everything after a bare "--" is positional. Split it off first: the
	// flag package swallows the first "--" itself, which would let later
	// tokens be re-parsed as flags.
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, usageError{"invalid flags"}
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	pos = append(pos, tail...)

	s, file, err := resolveSettings(cf.server, cf.token, cf.project, cf.config, a.getenv)
	if err != nil {
		return nil, err
	}
	return &cmdCtx{app: a, s: s, file: file, json: cf.json, pos: pos}, nil
}

// client returns an authenticated client or an actionable error.
func (c *cmdCtx) client() (*Client, error) {
	if c.s.Server == "" || c.s.Token == "" {
		return nil, fmt.Errorf("not logged in: run '%s auth login' or set %s and %s", progName, envServer, envToken)
	}
	server, err := normalizeServer(c.s.Server)
	if err != nil {
		return nil, err
	}
	if w := insecureWarning(server); w != "" {
		fmt.Fprintln(c.err, w)
	}
	return NewClient(server, c.s.Token, progName+"/"+version), nil
}

// call runs one tool call with a signal-aware context.
func (c *cmdCtx) call(tool string, input any) (*Result, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return cl.Call(ctx, tool, input)
}

// requireProject returns the project slug from -p/env/config.
func (c *cmdCtx) requireProject() (string, error) {
	if c.s.Project == "" {
		return "", usagef("no project: pass -p <slug> or set %s", envProject)
	}
	return c.s.Project, nil
}

func (c *cmdCtx) wantPos(n int, what string) error {
	if len(c.pos) != n {
		return usagef("expected %s", what)
	}
	return nil
}

// printJSON writes v as indented JSON.
func (c *cmdCtx) printJSON(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	// RawMessage values are re-indented as-is, preserving key order and
	// large integers.
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := c.out.Write(buf.Bytes())
	return err
}

func (c *cmdCtx) table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

// confirm gates destructive actions: --yes, an interactive prompt on a
// terminal, or a refusal when neither is available.
func (c *cmdCtx) confirm(yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !c.isTTY {
		return usagef("refusing to %s without --yes", prompt)
	}
	fmt.Fprintf(c.err, "%s? [y/N] ", prompt)
	line, _ := bufio.NewReader(c.in).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		return errors.New("aborted")
	}
	return nil
}
