package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"mise/api/migrations"
)

const createSchemaMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	applied_at INTEGER NOT NULL,
	checksum   TEXT    NOT NULL
)`

type migration struct {
	version  int
	name     string
	checksum string
	body     string
}

func Migrate(db *sql.DB) error {
	return migrate(db, migrations.FS)
}

func CurrentVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func migrate(db *sql.DB, fsys fs.FS) error {
	all, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	if _, err := db.Exec(createSchemaMigrations); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Close()

	applied, err := readApplied(ctx, conn)
	if err != nil {
		return err
	}

	known := make(map[int]migration, len(all))
	for _, m := range all {
		known[m.version] = m
	}

	for _, a := range applied {
		m, ok := known[a.version]
		if !ok {
			return fmt.Errorf(
				"migrate: database schema version %d is unknown to this binary (newer schema)",
				a.version)
		}
		if m.checksum != a.checksum {
			return fmt.Errorf(
				"migrate: checksum mismatch for %s: database has %s, file has %s",
				m.name, a.checksum, m.checksum)
		}
	}

	for _, m := range all {
		if slices.ContainsFunc(applied, func(a appliedMigration) bool {
			return a.version == m.version
		}) {
			continue
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			return err
		}
	}

	return nil
}

type appliedMigration struct {
	version  int
	checksum string
}

func readApplied(ctx context.Context, conn *sql.Conn) ([]appliedMigration, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	defer rows.Close()

	var applied []appliedMigration
	for rows.Next() {
		var a appliedMigration
		if err := rows.Scan(&a.version, &a.checksum); err != nil {
			return nil, fmt.Errorf("migrate: scan schema_migrations: %w", err)
		}
		applied = append(applied, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}

	return applied, nil
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}

	seen := make(map[int]string)
	var all []migration

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		name := entry.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok || prefix == "" {
			return nil, fmt.Errorf("migrate: migration %q has an unparseable version", name)
		}
		for _, r := range prefix {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("migrate: migration %q has an unparseable version", name)
			}
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrate: migration %q has an unparseable version", name)
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("migrate: duplicate version %d in %q and %q",
				version, previous, name)
		}
		seen[version] = name

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", name, err)
		}
		sum := sha256.Sum256(body)

		all = append(all, migration{
			version:  version,
			name:     name,
			checksum: hex.EncodeToString(sum[:]),
			body:     string(body),
		})
	}

	slices.SortFunc(all, func(a, b migration) int {
		return a.version - b.version
	})

	return all, nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, m migration) (err error) {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("migrate: begin %s: %w", m.name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, m.body); err != nil {
		return fmt.Errorf("migrate: apply %s: %w", m.name, err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at, checksum) VALUES (?, ?, ?)`,
		m.version, time.Now().Unix(), m.checksum); err != nil {
		return fmt.Errorf("migrate: record %s: %w", m.name, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migrate: commit %s: %w", m.name, err)
	}

	committed = true
	return nil
}
