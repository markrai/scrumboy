package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Options struct {
	BusyTimeout int
	JournalMode string
	Synchronous string
}

func Open(path string, opts Options) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}

	// Set busy_timeout and journal_mode in DSN to apply to ALL connections.
	// CRITICAL: busy_timeout prevents indefinite blocking on lock contention.
	// Using both _busy_timeout and _pragma=busy_timeout for defense in depth.
	// Performance pragmas for SQLite on NAS:
	// - journal_size_limit(64MB): Prevents unbounded WAL growth
	// - cache_size(-20000): 20MB page cache (negative = kilobytes)
	// - temp_store(MEMORY): Use RAM for temporary tables instead of disk
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=%d&_journal_mode=%s&_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)&_pragma=journal_mode(%s)&_pragma=synchronous(%s)&_pragma=journal_size_limit(67108864)&_pragma=cache_size(-20000)&_pragma=temp_store(MEMORY)",
		path,
		opts.BusyTimeout,
		opts.JournalMode,
		opts.BusyTimeout,
		opts.JournalMode,
		opts.Synchronous,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	// CRITICAL: SQLite does NOT scale with connection count.
	// WAL mode gives concurrent READS via file locking, not via connection pooling.
	// Setting MaxOpenConns too high causes:
	// - Cache thrashing (each connection has its own page cache)
	// - CPU contention on low-power hardware
	// - Blocked WAL checkpointing (many open readers)
	// - Progressive performance degradation over time
	// For SQLite on NAS/low-power hardware: 1-2 connections maximum.
	// Concurrency is handled by busy_timeout, not connection pools.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // Long-lived connections are good for SQLite (no reconnection churn)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	// Defensive PRAGMAs: Execute immediately after opening to ensure settings are applied.
	// This is defense in depth - DSN should handle this, but we verify here.
	_, err = db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", opts.BusyTimeout))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy_timeout pragma: %w", err)
	}

	_, err = db.Exec(fmt.Sprintf("PRAGMA journal_mode = %s", opts.JournalMode))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set journal_mode pragma: %w", err)
	}

	_, err = db.Exec(fmt.Sprintf("PRAGMA synchronous = %s", opts.Synchronous))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set synchronous pragma: %w", err)
	}

	// STARTUP ASSERTION: Verify busy_timeout is not 0.
	// This is a hard invariant - busy_timeout=0 causes indefinite blocking.
	var actualBusyTimeout int
	err = db.QueryRow("PRAGMA busy_timeout").Scan(&actualBusyTimeout)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("query busy_timeout: %w", err)
	}
	if actualBusyTimeout == 0 {
		_ = db.Close()
		return nil, fmt.Errorf("FATAL: busy_timeout is 0 (expected %d). SQLite will block indefinitely on lock contention", opts.BusyTimeout)
	}

	log.Printf("SQLite connection opened: busy_timeout=%dms journal_mode=%s synchronous=%s", actualBusyTimeout, opts.JournalMode, opts.Synchronous)

	return db, nil
}
