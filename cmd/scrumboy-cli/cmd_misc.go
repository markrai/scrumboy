package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// listCmd builds a handler for a read-only `<noun> list` command that calls
// one tool with just the project slug and renders data.items as a table.
func listCmd(noun, tool string, headers []string, row func(m map[string]any) []string) handler {
	return func(a *app, args []string) error {
		if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
			args = args[1:]
		}
		c, err := a.parse(noun+" list", noun+" list [-p slug]", args, nil)
		if err != nil {
			return err
		}
		if err := c.wantPos(0, "no positional arguments"); err != nil {
			return err
		}
		slug, err := c.requireProject()
		if err != nil {
			return err
		}
		res, err := c.call(tool, map[string]any{"projectSlug": slug})
		if err != nil {
			return err
		}
		return printItems(c, res, headers, row)
	}
}

func printItems(c *cmdCtx, res *Result, headers []string, row func(map[string]any) []string) error {
	if c.json {
		var raw struct {
			Items json.RawMessage `json:"items"`
		}
		if err := res.Decode(&raw); err != nil {
			return err
		}
		return c.printJSON(raw.Items)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := res.Decode(&out); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Items))
	for _, it := range out.Items {
		rows = append(rows, row(it))
	}
	c.table(headers, rows)
	return nil
}

// str renders a JSON-decoded value for a table cell.
func str(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return "-"
	}
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		if t == "" {
			return "-"
		}
		return t
	}
	return fmt.Sprint(v)
}

// millisDate renders a Unix-millisecond field (sprint timestamps) as a date.
func millisDate(m map[string]any, key string) string {
	f, ok := m[key].(float64)
	if !ok {
		return "-"
	}
	return time.UnixMilli(int64(f)).UTC().Format("2006-01-02")
}

func cmdTag(a *app, args []string) error {
	return listCmd("tag", "tags_listProject", []string{"NAME", "COUNT", "COLOR"}, func(m map[string]any) []string {
		return []string{str(m, "name"), str(m, "count"), str(m, "color")}
	})(a, args)
}

func cmdMember(a *app, args []string) error {
	return listCmd("member", "members_list", []string{"USER", "NAME", "EMAIL", "ROLE"}, func(m map[string]any) []string {
		return []string{str(m, "userId"), str(m, "name"), str(m, "email"), str(m, "role")}
	})(a, args)
}

func cmdColumn(a *app, args []string) error {
	return listCmd("column", "workflow_list", []string{"KEY", "NAME", "DONE", "COLOR"}, func(m map[string]any) []string {
		return []string{str(m, "key"), str(m, "name"), str(m, "isDone"), str(m, "color")}
	})(a, args)
}

func cmdPriority(a *app, args []string) error {
	return listCmd("priority", "priorities_list", []string{"KEY", "NAME", "COLOR"}, func(m map[string]any) []string {
		return []string{str(m, "key"), str(m, "name"), str(m, "color")}
	})(a, args)
}

// --- project ----------------------------------------------------------------

const projectUsage = `project <list|create|update|delete>

  list                         projects you can see
  create <name>                create a project (you become maintainer)
  update <slug> [--name N] [--sprint-weeks 1|2]
  delete <slug> [--yes]        permanently delete a project
`

func cmdProject(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, projectUsage)
		return usagef("missing project subcommand")
	}
	rest := args[1:]
	switch args[0] {
	case "list", "ls":
		return projectList(a, rest)
	case "create", "new":
		return projectCreate(a, rest)
	case "update":
		return projectUpdate(a, rest)
	case "delete", "rm":
		return projectDelete(a, rest)
	case "-h", "--help", "help":
		fmt.Fprint(a.out, projectUsage)
		return nil
	}
	return usagef("unknown project subcommand %q", args[0])
}

func projectRow(m map[string]any) []string {
	return []string{str(m, "projectSlug"), str(m, "name"), str(m, "role")}
}

func projectList(a *app, args []string) error {
	c, err := a.parse("project list", "project list", args, nil)
	if err != nil {
		return err
	}
	if err := c.wantPos(0, "no positional arguments"); err != nil {
		return err
	}
	res, err := c.call("projects_list", nil)
	if err != nil {
		return err
	}
	return printItems(c, res, []string{"SLUG", "NAME", "ROLE"}, projectRow)
}

func printProject(c *cmdCtx, res *Result) error {
	if c.json {
		var raw struct {
			Project json.RawMessage `json:"project"`
		}
		if err := res.Decode(&raw); err != nil {
			return err
		}
		return c.printJSON(raw.Project)
	}
	var out struct {
		Project map[string]any `json:"project"`
	}
	if err := res.Decode(&out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s  %s\n", str(out.Project, "projectSlug"), str(out.Project, "name"))
	return nil
}

func projectCreate(a *app, args []string) error {
	c, err := a.parse("project create", "project create <name>", args, nil)
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a project name (quote it)"); err != nil {
		return err
	}
	res, err := c.call("projects_create", map[string]any{"name": c.pos[0]})
	if err != nil {
		return err
	}
	return printProject(c, res)
}

func projectUpdate(a *app, args []string) error {
	var name string
	var weeks int
	c, err := a.parse("project update", "project update <slug> [--name N] [--sprint-weeks 1|2]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "new name")
		fs.IntVar(&weeks, "sprint-weeks", 0, "default sprint length in weeks (1 or 2)")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a project slug"); err != nil {
		return err
	}
	patch := map[string]any{}
	if name != "" {
		patch["name"] = name
	}
	if weeks != 0 {
		patch["defaultSprintWeeks"] = weeks
	}
	if len(patch) == 0 {
		return usagef("nothing to update: pass --name or --sprint-weeks")
	}
	res, err := c.call("projects_update", map[string]any{"projectSlug": c.pos[0], "patch": patch})
	if err != nil {
		return err
	}
	return printProject(c, res)
}

func projectDelete(a *app, args []string) error {
	var yes bool
	c, err := a.parse("project delete", "project delete <slug> [--yes]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a project slug"); err != nil {
		return err
	}
	if err := c.confirm(yes, fmt.Sprintf("permanently delete project %s and all its todos", c.pos[0])); err != nil {
		return err
	}
	res, err := c.call("projects_delete", map[string]any{"projectSlug": c.pos[0]})
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(res.Data)
	}
	fmt.Fprintf(c.out, "Deleted project %s\n", c.pos[0])
	return nil
}

// --- sprint -----------------------------------------------------------------

const sprintUsage = `sprint <list|active|create|activate|close>

  list                                   all sprints (ids are used by other commands)
  active                                 the active sprint, if any
  create <name> --start D --end D        D is RFC 3339 or YYYY-MM-DD
  activate <sprint-id>
  close <sprint-id>
`

func cmdSprint(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, sprintUsage)
		return usagef("missing sprint subcommand")
	}
	rest := args[1:]
	switch args[0] {
	case "list", "ls":
		c, err := a.parse("sprint list", "sprint list [-p slug]", rest, nil)
		if err != nil {
			return err
		}
		if err := c.wantPos(0, "no positional arguments"); err != nil {
			return err
		}
		slug, err := c.requireProject()
		if err != nil {
			return err
		}
		res, err := c.call("sprints_list", map[string]any{"projectSlug": slug})
		if err != nil {
			return err
		}
		return printItems(c, res, []string{"ID", "NUMBER", "NAME", "STATE", "START", "END"}, func(m map[string]any) []string {
			return []string{str(m, "sprintId"), str(m, "number"), str(m, "name"), str(m, "state"), millisDate(m, "plannedStartAt"), millisDate(m, "plannedEndAt")}
		})
	case "active":
		c, err := a.parse("sprint active", "sprint active [-p slug]", rest, nil)
		if err != nil {
			return err
		}
		if err := c.wantPos(0, "no positional arguments"); err != nil {
			return err
		}
		slug, err := c.requireProject()
		if err != nil {
			return err
		}
		res, err := c.call("sprints_getActive", map[string]any{"projectSlug": slug})
		if err != nil {
			return err
		}
		return printSprint(c, res, "No active sprint.")
	case "create":
		var start, end string
		c, err := a.parse("sprint create", "sprint create <name> --start D --end D [-p slug]", rest, func(fs *flag.FlagSet) {
			fs.StringVar(&start, "start", "", "planned start (RFC 3339 or YYYY-MM-DD)")
			fs.StringVar(&end, "end", "", "planned end (RFC 3339 or YYYY-MM-DD)")
		})
		if err != nil {
			return err
		}
		if err := c.wantPos(1, "a sprint name"); err != nil {
			return err
		}
		slug, err := c.requireProject()
		if err != nil {
			return err
		}
		if start == "" || end == "" {
			return usagef("--start and --end are required")
		}
		s, err := parseDue(start)
		if err != nil {
			return usagef("--start: invalid date")
		}
		e, err := parseDue(end)
		if err != nil {
			return usagef("--end: invalid date")
		}
		res, err := c.call("sprints_create", map[string]any{"projectSlug": slug, "name": c.pos[0], "plannedStartAt": s, "plannedEndAt": e})
		if err != nil {
			return err
		}
		return printSprint(c, res, "")
	case "activate", "close":
		verb := args[0]
		c, err := a.parse("sprint "+verb, "sprint "+verb+" <sprint-id> [-p slug]", rest, nil)
		if err != nil {
			return err
		}
		if err := c.wantPos(1, "a sprint id"); err != nil {
			return err
		}
		id, err := strconv.ParseInt(c.pos[0], 10, 64)
		if err != nil || id <= 0 {
			return usagef("invalid sprint id %q (see 'sprint list')", c.pos[0])
		}
		slug, err := c.requireProject()
		if err != nil {
			return err
		}
		res, err := c.call("sprints_"+verb, map[string]any{"projectSlug": slug, "sprintId": id})
		if err != nil {
			return err
		}
		return printSprint(c, res, "")
	case "-h", "--help", "help":
		fmt.Fprint(a.out, sprintUsage)
		return nil
	}
	return usagef("unknown sprint subcommand %q", args[0])
}

func printSprint(c *cmdCtx, res *Result, none string) error {
	var out struct {
		Sprint map[string]any `json:"sprint"`
	}
	if err := res.Decode(&out); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(out.Sprint)
	}
	if out.Sprint == nil {
		fmt.Fprintln(c.out, none)
		return nil
	}
	fmt.Fprintf(c.out, "%s  %s  [%s]  %s -> %s\n", str(out.Sprint, "sprintId"), str(out.Sprint, "name"), str(out.Sprint, "state"),
		millisDate(out.Sprint, "plannedStartAt"), millisDate(out.Sprint, "plannedEndAt"))
	return nil
}

// --- tool (raw escape hatch) --------------------------------------------------

const toolUsage = `tool <name> [--input JSON | --input @file | --input -] [--envelope]

Call any server tool directly and print its data as JSON. Covers everything
the dedicated commands do not (webhooks, admin, custom fields, metrics, ...).
'tool system_getCapabilities' lists every tool the server implements.

  scrumboy-cli tool metrics_getBurndown --input '{"projectSlug":"demo"}'
`

func cmdTool(a *app, args []string) error {
	var input string
	var envelope bool
	c, err := a.parse("tool", "tool <name> [--input JSON]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&input, "input", "{}", "JSON input object, @file, or - for stdin")
		fs.BoolVar(&envelope, "envelope", false, "print {data, meta} instead of just data")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a tool name"); err != nil {
		return err
	}
	var raw []byte
	switch {
	case input == "-":
		raw, err = io.ReadAll(c.in)
	case strings.HasPrefix(input, "@"):
		raw, err = os.ReadFile(input[1:])
	default:
		raw = []byte(input)
	}
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		return usagef("--input must be a JSON object")
	}
	res, err := c.call(c.pos[0], in)
	if err != nil {
		return err
	}
	if envelope {
		return c.printJSON(map[string]json.RawMessage{"data": orNull(res.Data), "meta": orNull(res.Meta)})
	}
	return c.printJSON(res.Data)
}

func orNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}
