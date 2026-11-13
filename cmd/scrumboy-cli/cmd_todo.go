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

const todoUsage = `todo <subcommand>

  list      list todos (filters: --column --tag --search --assignee --priority --sprint)
  get       show one todo
  create    create a todo
  update    change fields of a todo (only the flags you pass are sent)
  move      move a todo to another column
  delete    permanently delete a todo
  search    find todos by title (a lightweight lookup, not a board filter)
  archive   archive todos (maintainer)
  restore   restore archived todos (maintainer)
  links     show a todo's links
  link      link two todos
  unlink    remove a link

A todo is addressed as 'slug/12', 'slug#12', or '12' together with -p.
`

func cmdTodo(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, todoUsage)
		return usagef("missing todo subcommand")
	}
	rest := args[1:]
	switch args[0] {
	case "list", "ls":
		return todoList(a, rest)
	case "get", "show":
		return todoGet(a, rest)
	case "create", "add", "new":
		return todoCreate(a, rest)
	case "update", "edit":
		return todoUpdate(a, rest)
	case "move", "mv":
		return todoMove(a, rest)
	case "delete", "rm":
		return todoDelete(a, rest)
	case "search":
		return todoSearch(a, rest)
	case "archive":
		return todoArchive(a, "archive", "todos_archive", rest)
	case "restore":
		return todoArchive(a, "restore", "todos_restore", rest)
	case "links":
		return todoLinks(a, rest)
	case "link":
		return todoLink(a, rest)
	case "unlink":
		return todoUnlink(a, rest)
	case "-h", "--help", "help":
		fmt.Fprint(a.out, todoUsage)
		return nil
	}
	return usagef("unknown todo subcommand %q", args[0])
}

type todo struct {
	ProjectSlug      string         `json:"projectSlug"`
	LocalID          int64          `json:"localId"`
	Title            string         `json:"title"`
	Body             string         `json:"body"`
	ColumnKey        string         `json:"columnKey"`
	Tags             []string       `json:"tags"`
	EstimationPoints *int64         `json:"estimationPoints"`
	AssigneeUserID   *int64         `json:"assigneeUserId"`
	SprintID         *int64         `json:"sprintId"`
	PriorityKey      *string        `json:"priorityKey"`
	DueAt            *string        `json:"dueAt"`
	CreatedAt        string         `json:"createdAt"`
	UpdatedAt        string         `json:"updatedAt"`
	DoneAt           *string        `json:"doneAt"`
	ArchivedAt       *string        `json:"archivedAt"`
	CustomFields     map[string]any `json:"customFields"`

	raw json.RawMessage // the server's exact JSON, for lossless --json output
}

func (t *todo) UnmarshalJSON(b []byte) error {
	type plain todo // no methods, so no recursion
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*t = todo(p)
	t.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (t todo) ref() string { return fmt.Sprintf("%s/%d", t.ProjectSlug, t.LocalID) }

// stringSlice is a repeatable string flag.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// parseRef resolves 'slug/12', 'slug#12' or '12' (with a default project).
func parseRef(arg, defProject string) (string, int64, error) {
	slug, num := defProject, arg
	if i := strings.LastIndexAny(arg, "/#"); i >= 0 {
		slug, num = arg[:i], arg[i+1:]
	}
	id, err := strconv.ParseInt(num, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, usagef("invalid todo reference %q (want slug/12, slug#12, or 12 with -p)", arg)
	}
	if slug == "" {
		return "", 0, usagef("todo %q has no project: use slug/%d or pass -p", arg, id)
	}
	return slug, id, nil
}

// optStr renders an optional value for a table cell.
func optStr[T any](p *T) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func shortDate(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339, *s); err == nil {
		return t.Format("2006-01-02 15:04 MST")
	}
	return *s
}

func printTodoDetail(c *cmdCtx, t todo) {
	fmt.Fprintf(c.out, "%s  %s\n", t.ref(), t.Title)
	fmt.Fprintf(c.out, "Column:   %s\n", t.ColumnKey)
	fmt.Fprintf(c.out, "Priority: %s   Assignee: %s   Points: %s   Sprint: %s\n",
		optStr(t.PriorityKey), optStr(t.AssigneeUserID), optStr(t.EstimationPoints), optStr(t.SprintID))
	if len(t.Tags) > 0 {
		fmt.Fprintf(c.out, "Tags:     %s\n", strings.Join(t.Tags, ", "))
	}
	if t.DueAt != nil {
		fmt.Fprintf(c.out, "Due:      %s\n", shortDate(t.DueAt))
	}
	for k, v := range t.CustomFields {
		fmt.Fprintf(c.out, "Field:    %s = %v\n", k, v)
	}
	fmt.Fprintf(c.out, "Created:  %s   Updated: %s\n", shortDate(&t.CreatedAt), shortDate(&t.UpdatedAt))
	if t.ArchivedAt != nil {
		fmt.Fprintf(c.out, "Archived: %s\n", shortDate(t.ArchivedAt))
	}
	if strings.TrimSpace(t.Body) != "" {
		fmt.Fprintf(c.out, "\n%s\n", t.Body)
	}
}

// printTodoResult prints a single-todo mutation result.
func printTodoResult(c *cmdCtx, res *Result) error {
	if c.json {
		var wrap struct {
			Todo json.RawMessage `json:"todo"`
		}
		if err := res.Decode(&wrap); err != nil {
			return err
		}
		return c.printJSON(wrap.Todo)
	}
	var wrap struct {
		Todo todo `json:"todo"`
	}
	if err := res.Decode(&wrap); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s  %s  [%s]\n", wrap.Todo.ref(), wrap.Todo.Title, wrap.Todo.ColumnKey)
	return nil
}

// --- list / board ---------------------------------------------------------

type boardOpts struct {
	column, search, assignee, priority string
	tags                               stringSlice
	sprint                             int64
	limit                              int
	all                                bool
}

func (o *boardOpts) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.column, "column", "", "only this workflow column key")
	fs.Var(&o.tags, "tag", "filter by tag (repeatable, AND)")
	fs.StringVar(&o.search, "search", "", "text search")
	fs.StringVar(&o.assignee, "assignee", "", "me, unassigned, or a user id")
	fs.StringVar(&o.priority, "priority", "", "priority key, or **none**")
	fs.Int64Var(&o.sprint, "sprint", 0, "sprint id (see 'sprint list')")
	fs.IntVar(&o.limit, "limit", 20, "max todos per column per page (1-100)")
	fs.BoolVar(&o.all, "all", false, "follow pagination and fetch every todo")
}

type boardColumn struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	IsDone bool   `json:"isDone"`
	Items  []todo `json:"items"`
	Total  int    `json:"totalCount"`
}

func (o *boardOpts) input(slug string) map[string]any {
	in := map[string]any{"projectSlug": slug}
	if len(o.tags) > 0 {
		in["tags"] = []string(o.tags)
	}
	if o.search != "" {
		in["search"] = o.search
	}
	if o.assignee != "" {
		in["assignee"] = o.assignee
	}
	if o.priority != "" {
		in["priority"] = o.priority
	}
	if o.sprint > 0 {
		in["sprintId"] = o.sprint
	}
	if o.column != "" {
		in["columnKey"] = o.column
	}
	if o.limit > 0 {
		in["limit"] = o.limit
	}
	return in
}

type boardData struct {
	Columns []boardColumn `json:"columns"`
}

type boardMeta struct {
	NextCursorByColumn map[string]*string `json:"nextCursorByColumn"`
	TotalCountByColumn map[string]int     `json:"totalCountByColumn"`
}

func (c *cmdCtx) fetchBoard(slug string, o *boardOpts) ([]boardColumn, error) {
	res, err := c.call("board_get", o.input(slug))
	if err != nil {
		return nil, err
	}
	var data boardData
	var meta boardMeta
	if err := res.Decode(&data); err != nil {
		return nil, err
	}
	if err := res.DecodeMeta(&meta); err != nil {
		return nil, err
	}
	for i := range data.Columns {
		col := &data.Columns[i]
		col.Total = meta.TotalCountByColumn[col.Key]
		if !o.all {
			continue
		}
		// A board cursor is per column, so page each column on its own
		// (re-reading all columns with one cursor would repeat the others).
		next := meta.NextCursorByColumn[col.Key]
		seen := map[string]bool{}
		for next != nil {
			if seen[*next] { // any revisited cursor, not just the previous one, would page forever
				return nil, fmt.Errorf("server returned a repeating cursor for column %s", col.Key)
			}
			seen[*next] = true
			in := o.input(slug)
			in["columnKey"] = col.Key
			in["cursorByColumn"] = map[string]string{col.Key: *next}
			pres, err := c.call("board_get", in)
			if err != nil {
				return nil, err
			}
			var pd boardData
			var pm boardMeta
			if err := pres.Decode(&pd); err != nil {
				return nil, err
			}
			if err := pres.DecodeMeta(&pm); err != nil {
				return nil, err
			}
			for _, pc := range pd.Columns {
				if pc.Key == col.Key {
					col.Items = append(col.Items, pc.Items...)
				}
			}
			next = pm.NextCursorByColumn[col.Key]
		}
	}
	return data.Columns, nil
}

func todoList(a *app, args []string) error {
	var o boardOpts
	c, err := a.parse("todo list", "todo list [-p slug] [flags]", args, o.bind)
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
	cols, err := c.fetchBoard(slug, &o)
	if err != nil {
		return err
	}
	var all []todo
	for _, col := range cols {
		all = append(all, col.Items...)
	}
	if c.json {
		raws := make([]json.RawMessage, 0, len(all))
		for _, t := range all {
			raws = append(raws, t.raw)
		}
		return c.printJSON(raws)
	}
	rows := make([][]string, 0, len(all))
	for _, t := range all {
		rows = append(rows, []string{fmt.Sprintf("#%d", t.LocalID), t.ColumnKey, optStr(t.PriorityKey), optStr(t.AssigneeUserID), t.Title})
	}
	c.table([]string{"ID", "COLUMN", "PRIORITY", "ASSIGNEE", "TITLE"}, rows)
	return nil
}

func cmdBoard(a *app, args []string) error {
	if len(args) > 0 && args[0] == "show" {
		args = args[1:]
	}
	var o boardOpts
	c, err := a.parse("board", "board [show] [-p slug] [flags]", args, o.bind)
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
	cols, err := c.fetchBoard(slug, &o)
	if err != nil {
		return err
	}
	if c.json {
		out := make([]map[string]any, 0, len(cols))
		for _, col := range cols {
			items := make([]json.RawMessage, 0, len(col.Items))
			for _, t := range col.Items {
				items = append(items, t.raw)
			}
			out = append(out, map[string]any{"key": col.Key, "name": col.Name, "isDone": col.IsDone, "items": items})
		}
		return c.printJSON(out)
	}
	for i, col := range cols {
		if i > 0 {
			fmt.Fprintln(c.out)
		}
		shown := fmt.Sprintf("%d", len(col.Items))
		if col.Total > len(col.Items) {
			shown = fmt.Sprintf("%d of %d", len(col.Items), col.Total)
		}
		fmt.Fprintf(c.out, "%s (%s) - %s\n", col.Name, col.Key, shown)
		rows := make([][]string, 0, len(col.Items))
		for _, t := range col.Items {
			rows = append(rows, []string{fmt.Sprintf("  #%d", t.LocalID), optStr(t.PriorityKey), optStr(t.AssigneeUserID), t.Title})
		}
		c.table([]string{"  ID", "PRIORITY", "ASSIGNEE", "TITLE"}, rows)
	}
	return nil
}

// --- get / create / update / move / delete ---------------------------------

func todoGet(a *app, args []string) error {
	c, err := a.parse("todo get", "todo get <ref>", args, nil)
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "one todo reference"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	res, err := c.call("todos_get", map[string]any{"projectSlug": slug, "localId": id})
	if err != nil {
		return err
	}
	var wrap struct {
		Todo todo `json:"todo"`
	}
	if c.json {
		var raw struct {
			Todo json.RawMessage `json:"todo"`
		}
		if err := res.Decode(&raw); err != nil {
			return err
		}
		return c.printJSON(raw.Todo)
	}
	if err := res.Decode(&wrap); err != nil {
		return err
	}
	printTodoDetail(c, wrap.Todo)
	return nil
}

// fieldFlags are the todo attributes shared by create and update.
type fieldFlags struct {
	title, body, bodyFile, appendBody string
	tags                              stringSlice
	clearTags                         bool
	points                            string
	assignee, sprint, priority, due   string
	fields                            stringSlice
	column                            string
	fs                                *flag.FlagSet
}

func (f *fieldFlags) bind(fs *flag.FlagSet, forUpdate bool) {
	f.fs = fs
	if forUpdate {
		fs.StringVar(&f.title, "title", "", "new title")
		fs.StringVar(&f.appendBody, "append", "", "append text to the body")
		fs.BoolVar(&f.clearTags, "clear-tags", false, "remove all tags")
	} else {
		fs.StringVar(&f.column, "column", "", "workflow column key (default: first column)")
	}
	fs.StringVar(&f.body, "body", "", "body (markdown)")
	fs.StringVar(&f.bodyFile, "body-file", "", "read the body from a file ('-' for stdin)")
	fs.Var(&f.tags, "tag", "tag (repeatable; on update replaces all tags)")
	fs.StringVar(&f.points, "points", "", "estimation points, or 'none'")
	fs.StringVar(&f.assignee, "assignee", "", "assignee user id, 'me', or 'none'")
	fs.StringVar(&f.sprint, "sprint", "", "sprint id, or 'none'")
	fs.StringVar(&f.priority, "priority", "", "priority key, or 'none'")
	fs.StringVar(&f.due, "due", "", "due date (RFC 3339 or YYYY-MM-DD), or 'none'")
	fs.Var(&f.fields, "field", "custom field key=value (key:=JSON for numbers; key= clears) (repeatable)")
}

func (f *fieldFlags) isSet(name string) bool {
	set := false
	f.fs.Visit(func(fl *flag.Flag) {
		if fl.Name == name {
			set = true
		}
	})
	return set
}

// collect turns the flags that were actually passed into tool-input keys.
// For update the result is the patch; for create it is merged into the input.
func (f *fieldFlags) collect(c *cmdCtx) (map[string]any, error) {
	out := map[string]any{}
	if f.isSet("title") {
		out["title"] = f.title
	}
	if f.isSet("body") && f.isSet("body-file") {
		return nil, usagef("--body and --body-file are mutually exclusive")
	}
	if f.isSet("body") {
		out["body"] = f.body
	}
	if f.isSet("body-file") {
		var raw []byte
		var err error
		if f.bodyFile == "-" {
			raw, err = io.ReadAll(c.in)
		} else {
			raw, err = os.ReadFile(f.bodyFile)
		}
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		out["body"] = string(raw)
	}
	if f.isSet("append") {
		if f.isSet("body") || f.isSet("body-file") {
			return nil, usagef("--append cannot be combined with --body or --body-file")
		}
		out["bodyAppend"] = f.appendBody
	}
	if f.clearTags {
		if len(f.tags) > 0 {
			return nil, usagef("--clear-tags cannot be combined with --tag")
		}
		out["tags"] = []string{}
	} else if len(f.tags) > 0 {
		out["tags"] = []string(f.tags)
	}
	if f.isSet("points") {
		if isNone(f.points) {
			out["estimationPoints"] = nil
		} else {
			n, err := strconv.ParseInt(f.points, 10, 64)
			if err != nil {
				return nil, usagef("--points must be an integer or 'none'")
			}
			out["estimationPoints"] = n
		}
	}
	if f.isSet("assignee") {
		v, err := resolveAssignee(c, f.assignee)
		if err != nil {
			return nil, err
		}
		out["assigneeUserId"] = v
	}
	if f.isSet("sprint") {
		if isNone(f.sprint) {
			out["sprintId"] = nil
		} else {
			n, err := strconv.ParseInt(f.sprint, 10, 64)
			if err != nil || n <= 0 {
				return nil, usagef("--sprint must be a sprint id or 'none'")
			}
			out["sprintId"] = n
		}
	}
	if f.isSet("priority") {
		if strings.TrimSpace(f.priority) == "" {
			return nil, usagef("--priority needs a key or 'none'")
		}
		if isNone(f.priority) {
			out["priorityKey"] = nil
		} else {
			out["priorityKey"] = f.priority
		}
	}
	if f.isSet("due") {
		if isNone(f.due) {
			out["dueAt"] = nil
		} else {
			d, err := parseDue(f.due)
			if err != nil {
				return nil, err
			}
			out["dueAt"] = d
		}
	}
	if len(f.fields) > 0 {
		cf := map[string]any{}
		for _, raw := range f.fields {
			k, v, err := parseField(raw)
			if err != nil {
				return nil, err
			}
			cf[k] = v
		}
		out["customFields"] = cf
	}
	return out, nil
}

func isNone(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "null":
		return true
	}
	return false
}

func resolveAssignee(c *cmdCtx, v string) (any, error) {
	v = strings.TrimSpace(v)
	switch {
	case isNone(v):
		return nil, nil
	case v == "me":
		res, err := c.call("me_get", nil)
		if err != nil {
			return nil, err
		}
		var me meResp
		if err := res.Decode(&me); err != nil {
			return nil, err
		}
		return me.UserID, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return nil, usagef("--assignee must be a user id, 'me', or 'none'")
	}
	return n, nil
}

// parseDue accepts RFC 3339 or a bare date (midnight UTC).
func parseDue(s string) (string, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", usagef("--due must be RFC 3339 (2026-01-31T17:00:00Z) or YYYY-MM-DD, or 'none'")
}

// parseField parses 'key=value' (string), 'key:=JSON' (typed), and 'key='
// (clear, sent as null).
func parseField(s string) (string, any, error) {
	i := strings.Index(s, "=")
	if i <= 0 {
		return "", nil, usagef("invalid --field %q (want key=value)", s)
	}
	key, val := s[:i], s[i+1:]
	if strings.HasSuffix(key, ":") {
		key = strings.TrimSuffix(key, ":")
		var v any
		if err := json.Unmarshal([]byte(val), &v); err != nil {
			return "", nil, usagef("invalid JSON in --field %q", s)
		}
		return key, v, nil
	}
	if val == "" {
		return key, nil, nil
	}
	return key, val, nil
}

func todoCreate(a *app, args []string) error {
	var f fieldFlags
	c, err := a.parse("todo create", `todo create <title> [-p slug] [flags]`, args, func(fs *flag.FlagSet) { f.bind(fs, false) })
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a title (quote it)"); err != nil {
		return err
	}
	slug, err := c.requireProject()
	if err != nil {
		return err
	}
	in, err := f.collect(c)
	if err != nil {
		return err
	}
	in["projectSlug"] = slug
	in["title"] = c.pos[0]
	if f.column != "" {
		in["columnKey"] = f.column
	}
	res, err := c.call("todos_create", in)
	if err != nil {
		return err
	}
	return printTodoResult(c, res)
}

func todoUpdate(a *app, args []string) error {
	var f fieldFlags
	c, err := a.parse("todo update", "todo update <ref> [flags]", args, func(fs *flag.FlagSet) { f.bind(fs, true) })
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "one todo reference"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	patch, err := f.collect(c)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return usagef("nothing to update: pass at least one field flag")
	}
	res, err := c.call("todos_update", map[string]any{"projectSlug": slug, "localId": id, "patch": patch})
	if err != nil {
		return err
	}
	return printTodoResult(c, res)
}

func todoMove(a *app, args []string) error {
	var after, before int64
	c, err := a.parse("todo move", "todo move <ref> <column> [--after N] [--before N]", args, func(fs *flag.FlagSet) {
		fs.Int64Var(&after, "after", 0, "place after this todo's local id")
		fs.Int64Var(&before, "before", 0, "place before this todo's local id")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(2, "a todo reference and a column key"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	in := map[string]any{"projectSlug": slug, "localId": id, "toColumnKey": c.pos[1]}
	if after > 0 {
		in["afterLocalId"] = after
	}
	if before > 0 {
		in["beforeLocalId"] = before
	}
	res, err := c.call("todos_move", in)
	if err != nil {
		return err
	}
	return printTodoResult(c, res)
}

func todoDelete(a *app, args []string) error {
	var yes bool
	c, err := a.parse("todo delete", "todo delete <ref> [--yes]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "one todo reference"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	if err := c.confirm(yes, fmt.Sprintf("permanently delete %s/%d", slug, id)); err != nil {
		return err
	}
	res, err := c.call("todos_delete", map[string]any{"projectSlug": slug, "localId": id})
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(res.Data)
	}
	fmt.Fprintf(c.out, "Deleted %s/%d\n", slug, id)
	return nil
}

func todoSearch(a *app, args []string) error {
	var limit int
	c, err := a.parse("todo search", "todo search <query> [-p slug] [--limit N]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 20, "max results")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "a search query"); err != nil {
		return err
	}
	slug, err := c.requireProject()
	if err != nil {
		return err
	}
	res, err := c.call("todos_search", map[string]any{"projectSlug": slug, "query": c.pos[0], "limit": limit})
	if err != nil {
		return err
	}
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
		Items []todo `json:"items"`
	}
	if err := res.Decode(&out); err != nil {
		return err
	}
	rows := make([][]string, 0, len(out.Items))
	for _, t := range out.Items {
		rows = append(rows, []string{fmt.Sprintf("#%d", t.LocalID), t.Title})
	}
	c.table([]string{"ID", "TITLE"}, rows)
	return nil
}

func todoArchive(a *app, verb, tool string, args []string) error {
	c, err := a.parse("todo "+verb, "todo "+verb+" <ref>...", args, nil)
	if err != nil {
		return err
	}
	if len(c.pos) == 0 {
		return usagef("expected at least one todo reference")
	}
	var slug string
	ids := make([]int64, 0, len(c.pos))
	for _, p := range c.pos {
		s, id, err := parseRef(p, c.s.Project)
		if err != nil {
			return err
		}
		if slug != "" && s != slug {
			return usagef("all todos must be in the same project (%s vs %s)", slug, s)
		}
		slug = s
		ids = append(ids, id)
	}
	res, err := c.call(tool, map[string]any{"projectSlug": slug, "localIds": ids})
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(res.Data)
	}
	var out struct {
		Transitioned int `json:"transitionedCount"`
		Unchanged    int `json:"unchangedCount"`
	}
	if err := res.Decode(&out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%sd %d, unchanged %d\n", verb, out.Transitioned, out.Unchanged)
	return nil
}

// --- links ------------------------------------------------------------------

type linkItem struct {
	LocalID  int64  `json:"localId"`
	Title    string `json:"title"`
	LinkType string `json:"linkType"`
}

type linksResp struct {
	Outbound []linkItem `json:"outbound"`
	Inbound  []linkItem `json:"inbound"`
}

func printLinks(c *cmdCtx, res *Result) error {
	if c.json {
		return c.printJSON(res.Data)
	}
	var l linksResp
	if err := res.Decode(&l); err != nil {
		return err
	}
	rows := [][]string{}
	for _, x := range l.Outbound {
		rows = append(rows, []string{"->", x.LinkType, fmt.Sprintf("#%d", x.LocalID), x.Title})
	}
	for _, x := range l.Inbound {
		rows = append(rows, []string{"<-", x.LinkType, fmt.Sprintf("#%d", x.LocalID), x.Title})
	}
	c.table([]string{"DIR", "TYPE", "ID", "TITLE"}, rows)
	return nil
}

func todoLinks(a *app, args []string) error {
	c, err := a.parse("todo links", "todo links <ref>", args, nil)
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "one todo reference"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	res, err := c.call("todos_linksList", map[string]any{"projectSlug": slug, "localId": id})
	if err != nil {
		return err
	}
	return printLinks(c, res)
}

func todoLink(a *app, args []string) error {
	var typ string
	c, err := a.parse("todo link", "todo link <ref> <target> [--type relates_to|blocks|duplicates|parent]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&typ, "type", "relates_to", "link type; describes <ref> as the subject (<ref> blocks <target>)")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(2, "a todo reference and a target"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	tslug, tid, err := parseRef(c.pos[1], slug)
	if err != nil {
		return err
	}
	if tslug != slug {
		return usagef("links must stay within one project (%s vs %s)", slug, tslug)
	}
	res, err := c.call("todos_linkAdd", map[string]any{"projectSlug": slug, "localId": id, "targetLocalId": tid, "linkType": typ})
	if err != nil {
		return err
	}
	return printLinks(c, res)
}

func todoUnlink(a *app, args []string) error {
	c, err := a.parse("todo unlink", "todo unlink <ref> <target>", args, nil)
	if err != nil {
		return err
	}
	if err := c.wantPos(2, "a todo reference and a target"); err != nil {
		return err
	}
	slug, id, err := parseRef(c.pos[0], c.s.Project)
	if err != nil {
		return err
	}
	tslug, tid, err := parseRef(c.pos[1], slug)
	if err != nil {
		return err
	}
	if tslug != slug {
		return usagef("links must stay within one project (%s vs %s)", slug, tslug)
	}
	res, err := c.call("todos_linkRemove", map[string]any{"projectSlug": slug, "localId": id, "targetLocalId": tid})
	if err != nil {
		return err
	}
	return printLinks(c, res)
}
