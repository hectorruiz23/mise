package db

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"mise/api/migrations"
)

const (
	now       = 1700000000
	humanHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mise.db")
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	db.SetMaxOpenConns(1)

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	return db
}

func applySchema(t *testing.T, db *sql.DB) {
	t.Helper()

	schema, err := fs.ReadFile(migrations.FS, "0001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func execTask(t *testing.T, db *sql.DB, key, project, state, priority string, size, closedAt any) error {
	t.Helper()

	idem := "idem-" + key
	mustExec(t, db, `INSERT INTO idempotency_records
		(key, actor_id, request_digest, response_body, status_code, created_at)
		VALUES (?, 1, ?, '{}', 201, ?)`, idem, "digest-"+key, now)

	_, err := db.Exec(`INSERT INTO tasks
		(key, project_key, title, state, priority, size, created_by,
		 version, created_at, updated_at, closed_at, idempotency_key)
		VALUES (?, ?, 'Test task', ?, ?, ?, 1, 1, ?, ?, ?, ?)`,
		key, project, state, priority, size, now, now, closedAt, idem)
	return err
}

func TestSchema(t *testing.T) {
	db := newDB(t)
	applySchema(t, db)

	mustExec(t, db, `INSERT INTO users
		(id, email, name, kind, token_hash, active, created_at)
		VALUES (1, 'human@example.com', 'Human', 'human', ?, 1, ?)`, humanHash, now)
	mustExec(t, db, `INSERT INTO users
		(id, email, name, kind, token_hash, active, created_at)
		VALUES (2, 'agent@example.com', 'Agent', 'agent', ?, 1, ?)`, agentHash, now)

	t.Run("ten tables and no triggers", func(t *testing.T) {
		rows, err := db.Query(`SELECT name FROM sqlite_master
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
			ORDER BY name`)
		if err != nil {
			t.Fatalf("list tables: %v", err)
		}
		defer rows.Close()

		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan table: %v", err)
			}
			got = append(got, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("list tables: %v", err)
		}

		want := []string{
			"comments", "deliveries", "idempotency_records", "leases",
			"projects", "sessions", "signup_requests", "task_movements",
			"tasks", "users",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("tables = %v, want %v", got, want)
		}

		var triggers int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master
			WHERE type = 'trigger'`).Scan(&triggers); err != nil {
			t.Fatalf("count triggers: %v", err)
		}
		if triggers != 0 {
			t.Fatalf("triggers = %d, want 0", triggers)
		}
	})

	t.Run("seeded projects", func(t *testing.T) {
		rows, err := db.Query(`SELECT key, name, next_task_number
			FROM projects ORDER BY key`)
		if err != nil {
			t.Fatalf("list projects: %v", err)
		}
		defer rows.Close()

		type project struct {
			key, name string
			next      int
		}
		var got []project
		for rows.Next() {
			var p project
			if err := rows.Scan(&p.key, &p.name, &p.next); err != nil {
				t.Fatalf("scan project: %v", err)
			}
			got = append(got, p)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("list projects: %v", err)
		}

		want := []project{
			{"DE", "Despensa", 1},
			{"MI", "Mise", 1},
			{"PO", "Portfolio", 1},
		}
		if !slices.Equal(got, want) {
			t.Fatalf("projects = %v, want %v", got, want)
		}
	})

	t.Run("no cascades", func(t *testing.T) {
		for _, table := range []string{
			"comments", "deliveries", "idempotency_records", "leases",
			"projects", "sessions", "signup_requests", "task_movements",
			"tasks", "users",
		} {
			rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
			if err != nil {
				t.Fatalf("foreign keys of %s: %v", table, err)
			}
			for rows.Next() {
				var id, seq int
				var parent, from, to, onUpdate, onDelete, match string
				if err := rows.Scan(&id, &seq, &parent, &from, &to,
					&onUpdate, &onDelete, &match); err != nil {
					t.Fatalf("scan foreign key of %s: %v", table, err)
				}
				if onDelete != "NO ACTION" {
					t.Errorf("%s.%s ON DELETE %s, want NO ACTION",
						table, from, onDelete)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("foreign keys of %s: %v", table, err)
			}
			rows.Close()
		}
	})

	t.Run("valid task", func(t *testing.T) {
		if err := execTask(t, db, "MI-1", "MI", "ready", "normal", nil, nil); err != nil {
			t.Fatalf("insert valid task: %v", err)
		}
	})

	t.Run("state check", func(t *testing.T) {
		err := execTask(t, db, "MI-bad-state", "MI", "nope", "normal", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("invalid state error = %v, want CHECK constraint failed", err)
		}
	})

	t.Run("priority check", func(t *testing.T) {
		err := execTask(t, db, "MI-bad-priority", "MI", "ready", "urgent", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("invalid priority error = %v, want CHECK constraint failed", err)
		}
	})

	t.Run("size check", func(t *testing.T) {
		err := execTask(t, db, "MI-bad-size", "MI", "ready", "normal", "XL", nil)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("invalid size error = %v, want CHECK constraint failed", err)
		}
	})

	t.Run("done requires closed_at", func(t *testing.T) {
		err := execTask(t, db, "MI-done-no-close", "MI", "done", "normal", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("done without closed_at error = %v, want CHECK constraint failed", err)
		}
	})

	t.Run("closed_at only when done", func(t *testing.T) {
		err := execTask(t, db, "MI-open-with-close", "MI", "ready", "normal", nil, now)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("open task with closed_at error = %v, want CHECK constraint failed", err)
		}
	})

	t.Run("done with closed_at", func(t *testing.T) {
		if err := execTask(t, db, "MI-done", "MI", "done", "normal", nil, now); err != nil {
			t.Fatalf("insert done task: %v", err)
		}
	})

	t.Run("foreign key", func(t *testing.T) {
		err := execTask(t, db, "MI-bad-project", "ZZ", "ready", "normal", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("unknown project error = %v, want FOREIGN KEY constraint failed", err)
		}
	})

	t.Run("one live lease per task", func(t *testing.T) {
		if err := execTask(t, db, "MI-lease", "MI", "ready", "normal", nil, nil); err != nil {
			t.Fatalf("insert task for lease: %v", err)
		}

		mustExec(t, db, `INSERT INTO leases
			(task_key, agent_id, created_at, expires_at)
			VALUES ('MI-lease', 2, ?, ?)`, now, now+900)

		_, err := db.Exec(`INSERT INTO leases
			(task_key, agent_id, created_at, expires_at)
			VALUES ('MI-lease', 2, ?, ?)`, now, now+900)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			t.Fatalf("second live lease error = %v, want UNIQUE constraint failed", err)
		}

		mustExec(t, db, `UPDATE leases SET released_at = ?, closed_reason = 'released'
			WHERE task_key = 'MI-lease' AND released_at IS NULL`, now+60)

		if _, err := db.Exec(`INSERT INTO leases
			(task_key, agent_id, created_at, expires_at)
			VALUES ('MI-lease', 2, ?, ?)`, now+120, now+1020); err != nil {
			t.Fatalf("lease after release: %v", err)
		}
	})
}
