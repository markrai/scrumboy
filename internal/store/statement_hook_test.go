package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const hookAtBegin = "\x00begin"

// statementHook runs one function on the calling goroutine immediately before a chosen statement
// reaches SQLite, which pins a concurrent commit to an exact point inside a store call without any
// production hook or sleep. hookAtBegin fires as a transaction opens: a deferred transaction holds no
// snapshot yet, so its first read must see whatever fn commits. hookAtFirstWrite fires after the
// transaction has read: its snapshot is stale once fn commits, and SQLite must refuse its write.
type statementHook struct {
	mu    sync.Mutex
	match func(query string) bool
	fn    func()
}

func hookAtStatement(fragment string) func(string) bool {
	return func(query string) bool { return strings.Contains(query, fragment) }
}

func hookAtFirstWrite(query string) bool {
	for _, keyword := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), keyword) {
			return true
		}
	}
	return false
}

// staleReadInterleavings are the two points where a concurrent commit can invalidate a check. The
// first catches a check made before the transaction opened, which would still pass. The second
// catches a check that is in the transaction but relied on a snapshot that has since gone stale.
func staleReadInterleavings() map[string]func(string) bool {
	return map[string]func(string) bool{
		"before its first read":  hookAtStatement(hookAtBegin),
		"before its first write": hookAtFirstWrite,
	}
}

func (h *statementHook) arm(match func(string) bool, fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.match, h.fn = match, fn
}

func (h *statementHook) fire(query string) {
	h.mu.Lock()
	var fn func()
	if h.fn != nil && h.match(query) {
		fn, h.fn = h.fn, nil
	}
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (h *statementHook) assertFired(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fn != nil {
		t.Fatal("the interleaving never happened: the armed statement did not run")
	}
}

type hookedConnector struct {
	driver driver.Driver
	dsn    string
	hook   *statementHook
}

func (c *hookedConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &hookedConn{Conn: conn, hook: c.hook}, nil
}

func (c *hookedConnector) Driver() driver.Driver { return c.driver }

type hookedConn struct {
	driver.Conn
	hook *statementHook
}

func (c *hookedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.hook.fire(hookAtBegin)
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *hookedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *hookedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.hook.fire(query)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *hookedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.hook.fire(query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

// hooked returns a second store on the fixture's database file, on its own connection. It has the
// production pool size, and f.db stays free to commit whatever the hook does.
func (f *serviceFixture) hooked(t *testing.T) (*Store, *statementHook) {
	t.Helper()
	var seq int
	var name, path string
	if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	hook := &statementHook{}
	hookedDB := sql.OpenDB(&hookedConnector{
		driver: f.db.Driver(),
		dsn:    fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path),
		hook:   hook,
	})
	hookedDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = hookedDB.Close() })
	return New(hookedDB, nil), hook
}
