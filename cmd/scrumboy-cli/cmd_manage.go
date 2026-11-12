package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file holds the management commands (sprints, tags, members, workflow,
// priorities, custom fields, webhooks, dashboard, metrics, admin). They are
// thin: each maps positional args and flags onto one server tool, so they are
// declared as specs rather than hand-written handlers.

type flagDef struct {
	name, usage string
	kind        byte // 's' string, 'i' integer, 'b' bool, 'l' repeatable string
	def         string
}

// flagVals gives a spec's build function access to its parsed flags.
type flagVals struct {
	fs *flag.FlagSet
	s  map[string]*string
	i  map[string]*int64
	b  map[string]*bool
	l  map[string]*stringSlice
}

func (v *flagVals) Str(n string) string    { return *v.s[n] }
func (v *flagVals) Int(n string) int64     { return *v.i[n] }
func (v *flagVals) Bool(n string) bool     { return *v.b[n] }
func (v *flagVals) List(n string) []string { return []string(*v.l[n]) }
func (v *flagVals) Set(n string) (set bool) {
	v.fs.Visit(func(f *flag.Flag) {
		if f.Name == n {
			set = true
		}
	})
	return
}

type spec struct {
	usage       string
	pos         int  // exact positional count
	needProject bool // requires -p / SCRUMBOY_PROJECT
	flags       []flagDef
	// confirm, when set, makes the command destructive: it returns the prompt
	// and --yes is added.
	confirm func(c *cmdCtx, v *flagVals) string
	build   func(c *cmdCtx, v *flagVals) (tool string, in map[string]any, err error)
	// done formats the human-readable success line; --json prints the payload.
	done func(c *cmdCtx, res *Result) string
	// render overrides done for list-style output.
	render func(c *cmdCtx, res *Result) error
}

func (sp spec) run(a *app, name string, args []string) error {
	v := &flagVals{s: map[string]*string{}, i: map[string]*int64{}, b: map[string]*bool{}, l: map[string]*stringSlice{}}
	var yes bool
	c, err := a.parse(name, sp.usage, args, func(fs *flag.FlagSet) {
		v.fs = fs
		for _, f := range sp.flags {
			switch f.kind {
			case 's':
				v.s[f.name] = fs.String(f.name, f.def, f.usage)
			case 'i':
				v.i[f.name] = fs.Int64(f.name, 0, f.usage)
			case 'b':
				v.b[f.name] = fs.Bool(f.name, false, f.usage)
			case 'l':
				ss := &stringSlice{}
				v.l[f.name] = ss
				fs.Var(ss, f.name, f.usage)
			}
		}
		if sp.confirm != nil {
			fs.BoolVar(&yes, "yes", false, "do not ask for confirmation")
		}
	})
	if err != nil {
		return err
	}
	if sp.pos >= 0 {
		if err := c.wantPos(sp.pos, fmt.Sprintf("%d positional argument(s): %s", sp.pos, sp.usage)); err != nil {
			return err
		}
	}
	if sp.needProject {
		if _, err := c.requireProject(); err != nil {
			return err
		}
	}
	if sp.confirm != nil {
		prompt := sp.confirm(c, v)
		if sp.needProject {
			prompt += " in project " + c.s.Project // never act on an ambient project silently
		}
		if err := c.confirm(yes, prompt); err != nil {
			return err
		}
	}
	tool, in, err := sp.build(c, v)
	if err != nil {
		return err
	}
	if sp.needProject {
		in["projectSlug"] = c.s.Project
	}
	res, err := c.call(tool, in)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(res.Data)
	}
	switch {
	case sp.render != nil:
		return sp.render(c, res)
	case sp.done != nil:
		fmt.Fprintln(c.out, sp.done(c, res))
	default:
		return c.printJSON(res.Data)
	}
	return nil
}

// dispatch routes `<noun> <verb>` to a spec table.
func dispatch(a *app, noun string, args []string, usage string, specs map[string]spec) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, usage)
		return usagef("missing %s subcommand", noun)
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(a.out, usage)
		return nil
	}
	sp, ok := specs[args[0]]
	if !ok {
		return usagef("unknown %s subcommand %q", noun, args[0])
	}
	return sp.run(a, noun+" "+args[0], args[1:])
}

// --- argument helpers ---------------------------------------------------------

func posInt(c *cmdCtx, i int, what string) (int64, error) {
	n, err := strconv.ParseInt(c.pos[i], 10, 64)
	if err != nil || n <= 0 {
		return 0, usagef("invalid %s %q (want a positive integer)", what, c.pos[i])
	}
	return n, nil
}

// colorArg validates #RRGGBB.
func colorArg(s string) (string, error) {
	if len(s) == 7 && s[0] == '#' {
		if _, err := strconv.ParseUint(s[1:], 16, 32); err == nil {
			return strings.ToLower(s), nil
		}
	}
	return "", usagef("invalid color %q (want #RRGGBB)", s)
}

// dateMillis parses RFC 3339 or YYYY-MM-DD into Unix milliseconds (sprint fields).
func dateMillis(s string) (int64, error) {
	d, err := parseDue(s)
	if err != nil {
		return 0, usagef("invalid date %q (want RFC 3339 or YYYY-MM-DD)", s)
	}
	t, _ := time.Parse(time.RFC3339, d)
	return t.UnixMilli(), nil
}

// pageInput builds the optional limit/cursor of a paginated list tool.
func pageInput(v *flagVals) map[string]any {
	in := map[string]any{}
	if v.Set("limit") {
		in["limit"] = v.Int("limit")
	}
	if v.Str("cursor") != "" {
		in["cursor"] = v.Str("cursor")
	}
	return in
}

func itemsRender(headers []string, row func(map[string]any) []string) func(*cmdCtx, *Result) error {
	return func(c *cmdCtx, res *Result) error { return printItems(c, res, headers, row) }
}

func msg(format string, args ...any) func(*cmdCtx, *Result) string {
	s := fmt.Sprintf(format, args...)
	return func(*cmdCtx, *Result) string { return s }
}

var (
	fColor = flagDef{name: "color", usage: "color as #RRGGBB", kind: 's'}
	fName  = flagDef{name: "name", usage: "display name", kind: 's'}
)

// --- sprint (get/update/delete; the rest live in cmd_misc.go) -----------------

var sprintSpecs = map[string]spec{
	"get": {usage: "sprint get <sprint-id> [-p slug]", pos: 1, needProject: true,
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "sprint id")
			return "sprints_get", map[string]any{"sprintId": id}, err
		}},
	"update": {usage: "sprint update <sprint-id> [--name N] [--start D] [--end D] [-p slug]", pos: 1, needProject: true,
		flags: []flagDef{{"name", "new name", 's', ""}, {"start", "planned start (RFC 3339 or YYYY-MM-DD)", 's', ""}, {"end", "planned end (RFC 3339 or YYYY-MM-DD)", 's', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "sprint id")
			if err != nil {
				return "", nil, err
			}
			patch := map[string]any{}
			if v.Set("name") {
				patch["name"] = v.Str("name")
			}
			for flagName, field := range map[string]string{"start": "plannedStartAt", "end": "plannedEndAt"} {
				if v.Set(flagName) {
					ms, err := dateMillis(v.Str(flagName))
					if err != nil {
						return "", nil, err
					}
					patch[field] = ms
				}
			}
			if len(patch) == 0 {
				return "", nil, usagef("nothing to update: pass --name, --start or --end")
			}
			return "sprints_update", map[string]any{"sprintId": id, "patch": patch}, nil
		}},
	"delete": {usage: "sprint delete <sprint-id> [--yes] [-p slug]", pos: 1, needProject: true,
		confirm: func(c *cmdCtx, _ *flagVals) string { return "permanently delete sprint " + c.pos[0] },
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "sprint id")
			return "sprints_delete", map[string]any{"sprintId": id}, err
		},
		done: func(c *cmdCtx, _ *Result) string { return "Deleted sprint " + c.pos[0] }},
}

// --- tag ------------------------------------------------------------------------

const tagUsage = `tag <list|mine|color|delete>

  list [-p slug]                       the project's tags
  mine                                 your personal tag library
  color <name> <#RRGGBB|none> [-p]     set your own color for a personal label
  color --id N <#RRGGBB|none> [-p]     set a board-scoped tag's shared color (maintainer)
  color --mine --id N <#RRGGBB|none>   set a tag in your personal library
  delete --id N [--mine] [--yes] [-p]  delete a board-scoped (or, with --mine, personal) tag
`

func colorOrNull(s string) (any, error) {
	if isNone(s) {
		return nil, nil
	}
	return colorArg(s)
}

var tagSpecs = map[string]spec{
	"list": {usage: "tag list [-p slug]", pos: 0, needProject: true,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
			return "tags_listProject", map[string]any{}, nil
		},
		render: itemsRender([]string{"NAME", "COUNT", "COLOR", "ID"}, func(m map[string]any) []string {
			return []string{str(m, "name"), str(m, "count"), str(m, "color"), str(m, "tagId")}
		})},
	"mine": {usage: "tag mine", pos: 0,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
			return "tags_listMine", map[string]any{}, nil
		},
		render: itemsRender([]string{"ID", "NAME", "COLOR"}, func(m map[string]any) []string {
			return []string{str(m, "tagId"), str(m, "name"), str(m, "color")}
		})},
	"color": {usage: "tag color (<name> | --id N [--mine]) <#RRGGBB|none>", pos: -1,
		flags: []flagDef{{"id", "tag id", 'i', ""}, {"mine", "the tag is in your personal library", 'b', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			byID := v.Set("id")
			if v.Bool("mine") && !byID {
				return "", nil, usagef("--mine needs --id")
			}
			if byID && v.Int("id") <= 0 {
				return "", nil, usagef("--id must be a positive tag id")
			}
			if !v.Bool("mine") {
				if _, err := c.requireProject(); err != nil {
					return "", nil, err
				}
			}
			want := 2
			if byID {
				want = 1
			}
			if len(c.pos) != want {
				return "", nil, usagef("expected %d positional argument(s)", want)
			}
			color, err := colorOrNull(c.pos[want-1])
			if err != nil {
				return "", nil, err
			}
			switch {
			case byID && v.Bool("mine"):
				return "tags_updateMineColor", map[string]any{"tagId": v.Int("id"), "color": color}, nil
			case byID:
				return "tags_updateProjectColor", map[string]any{"projectSlug": c.s.Project, "tagId": v.Int("id"), "color": color}, nil
			}
			return "tags_updateProjectColor", map[string]any{"projectSlug": c.s.Project, "tagName": c.pos[0], "color": color}, nil
		},
		done: msg("Updated tag color")},
	"delete": {usage: "tag delete --id N [--mine] [--yes] [-p slug]", pos: 0,
		flags:   []flagDef{{"id", "tag id", 'i', ""}, {"mine", "delete from your personal library", 'b', ""}},
		confirm: func(_ *cmdCtx, v *flagVals) string { return fmt.Sprintf("permanently delete tag %d", v.Int("id")) },
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			if !v.Set("id") || v.Int("id") <= 0 {
				return "", nil, usagef("--id is required")
			}
			if v.Bool("mine") {
				return "tags_deleteMine", map[string]any{"tagId": v.Int("id")}, nil
			}
			if _, err := c.requireProject(); err != nil {
				return "", nil, err
			}
			return "tags_deleteProject", map[string]any{"projectSlug": c.s.Project, "tagId": v.Int("id")}, nil
		},
		done: msg("Deleted tag")},
}

func cmdTag(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...) // `tag -p demo` keeps working
	}
	return dispatch(a, "tag", args, tagUsage, tagSpecs)
}

// --- member -----------------------------------------------------------------------

const memberUsage = `member <list|available|add|role|remove>

  list                          project members
  available                     users who can be added (maintainer)
  add <user-id> --role R        add a user (maintainer|contributor|viewer)
  role <user-id> <role>         change a member's role
  remove <user-id> [--yes]      remove a member
`

var roleFlag = flagDef{name: "role", usage: "maintainer, contributor or viewer", kind: 's'}

var memberSpecs = map[string]spec{
	"list": {usage: "member list [-p slug]", needProject: true,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) { return "members_list", map[string]any{}, nil },
		render: itemsRender([]string{"USER", "NAME", "EMAIL", "ROLE"}, func(m map[string]any) []string {
			return []string{str(m, "userId"), str(m, "name"), str(m, "email"), str(m, "role")}
		})},
	"available": {usage: "member available [-p slug]", needProject: true,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
			return "members_listAvailable", map[string]any{}, nil
		},
		render: itemsRender([]string{"USER", "NAME", "EMAIL"}, func(m map[string]any) []string {
			return []string{str(m, "userId"), str(m, "name"), str(m, "email")}
		})},
	"add": {usage: "member add <user-id> --role R [-p slug]", pos: 1, needProject: true, flags: []flagDef{roleFlag},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "user id")
			if v.Str("role") == "" {
				return "", nil, usagef("--role is required")
			}
			return "members_add", map[string]any{"userId": id, "role": v.Str("role")}, err
		},
		done: func(c *cmdCtx, _ *Result) string { return "Added user " + c.pos[0] }},
	"role": {usage: "member role <user-id> <role> [-p slug]", pos: 2, needProject: true,
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "user id")
			return "members_updateRole", map[string]any{"userId": id, "role": c.pos[1]}, err
		},
		done: func(c *cmdCtx, _ *Result) string { return fmt.Sprintf("User %s is now %s", c.pos[0], c.pos[1]) }},
	"remove": {usage: "member remove <user-id> [--yes] [-p slug]", pos: 1, needProject: true,
		confirm: func(c *cmdCtx, _ *flagVals) string { return "remove user " + c.pos[0] + " from the project" },
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "user id")
			return "members_remove", map[string]any{"userId": id}, err
		},
		done: func(c *cmdCtx, _ *Result) string { return "Removed user " + c.pos[0] }},
}

func cmdMember(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	return dispatch(a, "member", args, memberUsage, memberSpecs)
}

// --- column / priority (same shape: list, add, update, delete) -----------------

func laneSpecs(noun, keyField, createTool, updateTool, deleteTool, listTool string) map[string]spec {
	return map[string]spec{
		"list": {usage: noun + " list [-p slug]", needProject: true,
			build: func(*cmdCtx, *flagVals) (string, map[string]any, error) { return listTool, map[string]any{}, nil },
			render: itemsRender([]string{"KEY", "NAME", "COLOR"}, func(m map[string]any) []string {
				return []string{str(m, "key"), str(m, "name"), str(m, "color")}
			})},
		"add": {usage: noun + " add <name> [-p slug]", pos: 1, needProject: true,
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				return createTool, map[string]any{"name": c.pos[0]}, nil
			},
			done: func(c *cmdCtx, _ *Result) string { return "Added " + noun + " " + c.pos[0] }},
		"update": {usage: noun + " update <key> --name N --color #RRGGBB [-p slug]", pos: 1, needProject: true,
			flags: []flagDef{fName, fColor},
			build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
				if v.Str("name") == "" || v.Str("color") == "" {
					return "", nil, usagef("--name and --color are both required (the server replaces both)")
				}
				col, err := colorArg(v.Str("color"))
				return updateTool, map[string]any{keyField: c.pos[0], "name": v.Str("name"), "color": col}, err
			},
			done: func(c *cmdCtx, _ *Result) string { return "Updated " + noun + " " + c.pos[0] }},
		"delete": {usage: noun + " delete <key> [--yes] [-p slug]", pos: 1, needProject: true,
			confirm: func(c *cmdCtx, _ *flagVals) string { return "delete " + noun + " " + c.pos[0] },
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				return deleteTool, map[string]any{keyField: c.pos[0]}, nil
			},
			done: func(c *cmdCtx, _ *Result) string { return "Deleted " + noun + " " + c.pos[0] }},
	}
}

var columnSpecs = laneSpecs("column", "columnKey", "workflow_create", "workflow_update", "workflow_delete", "workflow_list")
var prioritySpecs = laneSpecs("priority", "priorityKey", "priorities_create", "priorities_update", "priorities_delete", "priorities_list")

func cmdColumn(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	return dispatch(a, "column", args, "column <list|add|update|delete>\n\nManage a project's workflow columns (board lanes).\n", columnSpecs)
}

func cmdPriority(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	return dispatch(a, "priority", args, "priority <list|add|update|delete>\n\nManage a project's priority tiers.\n", prioritySpecs)
}

// --- field (custom fields) ------------------------------------------------------

const fieldUsage = `field <list|add|delete>

  list                                       custom field definitions
  add <name> --type text|url|number [--key]  define a field
  delete <key> [--yes]                       delete a field and every value under it

Set values with: todo create|update --field key=value
`

var fieldSpecs = map[string]spec{
	"list": {usage: "field list [-p slug]", needProject: true,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
			return "customFields_list", map[string]any{}, nil
		},
		render: itemsRender([]string{"KEY", "NAME", "TYPE"}, func(m map[string]any) []string {
			return []string{str(m, "key"), str(m, "name"), str(m, "type")}
		})},
	"add": {usage: "field add <name> --type text|url|number [--key K] [-p slug]", pos: 1, needProject: true,
		flags: []flagDef{{"type", "text, url or number", 's', ""}, {"key", "stable key (default: slug of the name)", 's', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			if v.Str("type") == "" {
				return "", nil, usagef("--type is required (text, url or number)")
			}
			in := map[string]any{"name": c.pos[0], "type": v.Str("type")}
			if v.Str("key") != "" {
				in["key"] = v.Str("key")
			}
			return "customFields_create", in, nil
		},
		done: func(c *cmdCtx, _ *Result) string { return "Added field " + c.pos[0] }},
	"delete": {usage: "field delete <key> [--yes] [-p slug]", pos: 1, needProject: true,
		confirm: func(c *cmdCtx, _ *flagVals) string {
			return "delete field " + c.pos[0] + " and every todo value under it"
		},
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			return "customFields_delete", map[string]any{"key": c.pos[0]}, nil
		},
		done: func(c *cmdCtx, _ *Result) string { return "Deleted field " + c.pos[0] }},
}

func cmdField(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	return dispatch(a, "field", args, fieldUsage, fieldSpecs)
}

// --- webhook --------------------------------------------------------------------------

const webhookUsage = `webhook <list|create|delete>

  list [--limit N] [--cursor C]
  create <url> --event E [--event E ...] [--secret S] [--service-account ID] [--durable]
         events: todo.created, ... or '*' (see docs/webhooks.md); --durable is todo.created only
  delete <webhook-id> [--yes]
`

var webhookSpecs = map[string]spec{
	"list": {usage: "webhook list [--limit N] [--cursor C] [-p slug]", needProject: true,
		flags: []flagDef{{"limit", "page size (max 100)", 'i', ""}, {"cursor", "pagination cursor", 's', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			in := map[string]any{}
			if v.Set("limit") {
				in["limit"] = v.Int("limit")
			}
			if v.Str("cursor") != "" {
				in["cursor"] = v.Str("cursor")
			}
			return "webhooks_list", in, nil
		},
		render: itemsRender([]string{"ID", "EVENTS", "OWNER", "DESTINATION"}, func(m map[string]any) []string {
			dest := str(m, "url")
			if dest == "-" {
				dest = str(m, "destinationPreview")
			}
			return []string{str(m, "webhookId"), fmt.Sprint(m["events"]), str(m, "ownerName"), dest}
		})},
	"create": {usage: "webhook create <url> --event E [...] [--secret S] [--service-account ID] [--durable] [-p slug]", pos: 1, needProject: true,
		flags: []flagDef{{"event", "event type or * (repeatable)", 'l', ""}, {"secret", "HMAC signing secret (visible in ps/history; prefer --secret-file)", 's', ""}, {"secret-file", "read the signing secret from a file ('-' for stdin)", 's', ""},
			{"service-account", "service-account user id to own it", 'i', ""}, {"durable", "durable delivery (todo.created only)", 'b', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			if len(v.List("event")) == 0 {
				return "", nil, usagef("at least one --event is required")
			}
			in := map[string]any{"url": c.pos[0], "events": v.List("event")}
			if v.Str("secret") != "" && v.Str("secret-file") != "" {
				return "", nil, usagef("--secret and --secret-file are mutually exclusive")
			}
			if v.Str("secret") != "" {
				in["secret"] = v.Str("secret")
			}
			if f := v.Str("secret-file"); f != "" {
				var raw []byte
				var err error
				if f == "-" {
					raw, err = io.ReadAll(io.LimitReader(c.in, 4096))
				} else {
					raw, err = os.ReadFile(f)
				}
				if err != nil {
					return "", nil, fmt.Errorf("read secret: %w", err)
				}
				if sec := strings.TrimRight(string(raw), "\r\n"); sec != "" {
					in["secret"] = sec
				}
			}
			if v.Set("service-account") {
				in["serviceAccountUserId"] = v.Int("service-account")
			}
			if v.Bool("durable") {
				in["deliveryMode"] = "durable"
			}
			return "webhooks_create", in, nil
		},
		done: msg("Webhook created")},
	"delete": {usage: "webhook delete <webhook-id> [--yes] [-p slug]", pos: 1, needProject: true,
		confirm: func(c *cmdCtx, _ *flagVals) string { return "delete webhook " + c.pos[0] },
		build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
			id, err := posInt(c, 0, "webhook id")
			return "webhooks_delete", map[string]any{"webhookId": id}, err
		},
		done: func(c *cmdCtx, _ *Result) string { return "Deleted webhook " + c.pos[0] }},
}

func cmdWebhook(a *app, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"list"}, args...)
	}
	return dispatch(a, "webhook", args, webhookUsage, webhookSpecs)
}

// --- dashboard / metrics ------------------------------------------------------------

const dashboardUsage = `dashboard <summary|todos>

  summary [--timezone TZ]               your cross-project workload
  todos [--limit N] [--cursor C] [--sort activity|board]
                                        todos assigned to you in every project
`

var dashboardSpecs = map[string]spec{
	"summary": {usage: "dashboard summary [--timezone TZ]",
		flags: []flagDef{{"timezone", "IANA timezone for week boundaries (default UTC)", 's', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			in := map[string]any{}
			if v.Str("timezone") != "" {
				in["timezone"] = v.Str("timezone")
			}
			return "dashboard_getSummary", in, nil
		}}, // no human format: the summary is deeply nested, so it prints as JSON
	"todos": {usage: "dashboard todos [--limit N] [--cursor C] [--sort activity|board]",
		flags: []flagDef{{"limit", "page size (max 100)", 'i', ""}, {"cursor", "pagination cursor", 's', ""}, {"sort", "activity (default) or board", 's', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			in := map[string]any{}
			if v.Set("limit") {
				in["limit"] = v.Int("limit")
			}
			if v.Str("cursor") != "" {
				in["cursor"] = v.Str("cursor")
			}
			if v.Str("sort") != "" {
				in["sort"] = v.Str("sort")
			}
			return "dashboard_listTodos", in, nil
		},
		render: itemsRender([]string{"TODO", "COLUMN", "PRIORITY", "TITLE"}, func(m map[string]any) []string {
			return []string{fmt.Sprintf("%s/%s", str(m, "projectSlug"), str(m, "localId")), str(m, "columnKey"), str(m, "priorityKey"), str(m, "title")}
		})},
}

func cmdDashboard(a *app, args []string) error {
	return dispatch(a, "dashboard", args, dashboardUsage, dashboardSpecs)
}

var metricsSpecs = map[string]spec{
	"burndown": {usage: "metrics burndown [--sprint ID] [-p slug]", needProject: true,
		flags: []flagDef{{"sprint", "scope to one sprint id", 'i', ""}},
		build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
			in := map[string]any{}
			if v.Set("sprint") {
				in["sprintId"] = v.Int("sprint")
			}
			return "metrics_getBurndown", in, nil
		},
		render: itemsPointsRender([]string{"DATE", "REMAINING", "SCOPE"}, "date", "remainingWork", "initialScope")},
	"backlog": {usage: "metrics backlog [-p slug]", needProject: true,
		build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
			return "metrics_getBacklogSize", map[string]any{}, nil
		},
		render: itemsPointsRender([]string{"DATE", "INCOMPLETE", "TOTAL"}, "date", "incompleteCount", "totalScope")},
}

// itemsPointsRender renders a data.points series as a table.
func itemsPointsRender(headers []string, keys ...string) func(*cmdCtx, *Result) error {
	return func(c *cmdCtx, res *Result) error {
		var out struct {
			Points []map[string]any `json:"points"`
		}
		if err := res.Decode(&out); err != nil {
			return err
		}
		rows := make([][]string, 0, len(out.Points))
		for _, p := range out.Points {
			row := make([]string, len(keys))
			for i, k := range keys {
				row[i] = str(p, k)
				if row[i] == "-" && k != "date" {
					row[i] = "0" // the server omits zero-valued series fields
				}
			}
			rows = append(rows, row)
		}
		c.table(headers, rows)
		return nil
	}
}

func cmdMetrics(a *app, args []string) error {
	return dispatch(a, "metrics", args, "metrics <burndown|backlog>\n", metricsSpecs)
}

// --- admin (system-level; owner/admin role) ---------------------------------------

const adminUsage = `admin <users|service-account|service-token> <subcommand>

  users list
  users role <user-id> <admin|user>            (owner)
  users delete <user-id> [--yes]               (owner)
  service-account list | create <name> | delete <user-id> [--yes]
  service-token list <account-user-id> | create <account-user-id> [--name N] | revoke <token-id> [--yes]

Service tokens are shown in full exactly once, by 'service-token create'.
`

var adminSpecs = map[string]map[string]spec{
	"users": {
		"list": {usage: "admin users list",
			build: func(*cmdCtx, *flagVals) (string, map[string]any, error) {
				return "admin_listUsers", map[string]any{}, nil
			},
			render: itemsRender([]string{"ID", "NAME", "EMAIL", "ROLE", "SERVICE"}, func(m map[string]any) []string {
				return []string{str(m, "userId"), str(m, "name"), str(m, "email"), str(m, "systemRole"), str(m, "isService")}
			})},
		"role": {usage: "admin users role <user-id> <admin|user>", pos: 2,
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "user id")
				return "admin_updateUserRole", map[string]any{"userId": id, "role": c.pos[1]}, err
			},
			done: func(c *cmdCtx, _ *Result) string { return fmt.Sprintf("User %s is now %s", c.pos[0], c.pos[1]) }},
		"delete": {usage: "admin users delete <user-id> [--yes]", pos: 1,
			confirm: func(c *cmdCtx, _ *flagVals) string { return "permanently delete user " + c.pos[0] },
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "user id")
				return "admin_deleteUser", map[string]any{"userId": id}, err
			},
			done: func(c *cmdCtx, _ *Result) string { return "Deleted user " + c.pos[0] }},
	},
	"service-account": {
		"list": {usage: "admin service-account list [--limit N] [--cursor C]",
			flags: []flagDef{{"limit", "page size (max 100)", 'i', ""}, {"cursor", "pagination cursor", 's', ""}},
			build: func(_ *cmdCtx, v *flagVals) (string, map[string]any, error) {
				return "admin_listServiceAccounts", pageInput(v), nil
			},
			render: itemsRender([]string{"ID", "NAME", "CREATED"}, func(m map[string]any) []string {
				return []string{str(m, "userId"), str(m, "name"), str(m, "createdAt")}
			})},
		"create": {usage: "admin service-account create <name>", pos: 1,
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				return "admin_createServiceAccount", map[string]any{"name": c.pos[0]}, nil
			}},
		"delete": {usage: "admin service-account delete <user-id> [--yes]", pos: 1,
			confirm: func(c *cmdCtx, _ *flagVals) string { return "delete service account " + c.pos[0] },
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "user id")
				return "admin_deleteServiceAccount", map[string]any{"userId": id}, err
			},
			done: func(c *cmdCtx, _ *Result) string { return "Deleted service account " + c.pos[0] }},
	},
	"service-token": {
		"list": {usage: "admin service-token list <account-user-id> [--limit N] [--cursor C]", pos: 1,
			flags: []flagDef{{"limit", "page size (max 100)", 'i', ""}, {"cursor", "pagination cursor", 's', ""}},
			build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "account user id")
				in := pageInput(v)
				in["userId"] = id
				return "admin_listServiceTokens", in, err
			},
			render: itemsRender([]string{"TOKEN", "NAME", "CREATED", "REVOKED"}, func(m map[string]any) []string {
				return []string{str(m, "tokenId"), str(m, "name"), str(m, "createdAt"), str(m, "revokedAt")}
			})},
		"create": {usage: "admin service-token create <account-user-id> [--name N]", pos: 1,
			flags: []flagDef{fName},
			build: func(c *cmdCtx, v *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "account user id")
				in := map[string]any{"userId": id}
				if v.Str("name") != "" {
					in["name"] = v.Str("name")
				}
				return "admin_createServiceToken", in, err
			}}, // prints the JSON payload: it contains the one-time token
		"revoke": {usage: "admin service-token revoke <token-id> [--yes]", pos: 1,
			confirm: func(c *cmdCtx, _ *flagVals) string { return "revoke service token " + c.pos[0] },
			build: func(c *cmdCtx, _ *flagVals) (string, map[string]any, error) {
				id, err := posInt(c, 0, "token id")
				return "admin_revokeServiceToken", map[string]any{"tokenId": id}, err
			},
			done: func(c *cmdCtx, _ *Result) string { return "Revoked token " + c.pos[0] }},
	},
}

func cmdAdmin(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, adminUsage)
		return usagef("missing admin subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(a.out, adminUsage)
		return nil
	}
	group, ok := adminSpecs[args[0]]
	if !ok {
		return usagef("unknown admin group %q", args[0])
	}
	return dispatch(a, "admin "+args[0], args[1:], adminUsage, group)
}
