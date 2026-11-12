package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mise.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	return db
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	for _, path := range []string{"", "   "} {
		if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("Open(%q) error = %v, want empty path error", path, err)
		}
	}
}

func TestOpenMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "mise.db")

	_, err := Open(path)
	if err == nil {
		t.Fatalf("Open(%q) succeeded, want an error", path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %q, want it to mention %q", err, path)
	}
}

func TestOpenPragmasOnEveryConnection(t *testing.T) {
	db := openTestDB(t)

	const connections = 4
	ctx := context.Background()

	conns := make([]*sql.Conn, 0, connections)
	for i := 0; i < connections; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		conns = append(conns, conn)
	}
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	for i, conn := range conns {
		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i, foreignKeys)
		}

		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("connection %d busy_timeout = %d, want 5000", i, busyTimeout)
		}

		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("connection %d journal_mode: %v", i, err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			t.Errorf("connection %d journal_mode = %q, want wal", i, journalMode)
		}
	}
}
