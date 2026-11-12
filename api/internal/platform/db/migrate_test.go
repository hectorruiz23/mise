package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"mise/api/migrations"
)

type migrationRow struct {
	version   int
	appliedAt int64
	checksum  string
}

func readMigrationRows(t *testing.T, db *sql.DB) []migrationRow {
	t.Helper()

	rows, err := db.Query(
		`SELECT version, applied_at, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()

	var got []migrationRow
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.version, &row.appliedAt, &row.checksum); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}

	return got
}

func countRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()

	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

func TestMigrateAppliesEveryFile(t *testing.T) {
	db := openTestDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	if len(files) == 0 {
		t.Fatal("no embedded migrations found")
	}

	got := readMigrationRows(t, db)
	if len(got) != len(files) {
		t.Fatalf("schema_migrations rows = %d, want one per file (%d)",
			len(got), len(files))
	}

	for _, row := range got {
		var name string
		for _, file := range files {
			prefix, _, _ := strings.Cut(file, "_")
			version, err := strconv.Atoi(prefix)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			if version == row.version {
				name = file
				break
			}
		}
		if name == "" {
			t.Fatalf("version %d does not match any embedded file", row.version)
		}

		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sum := sha256.Sum256(body)
		if want := hex.EncodeToString(sum[:]); row.checksum != want {
			t.Errorf("%s checksum = %s, want %s", name, row.checksum, want)
		}
		if row.appliedAt <= 0 {
			t.Errorf("%s applied_at = %d, want a Unix timestamp", name, row.appliedAt)
		}
	}

	for _, table := range []string{
		"comments", "deliveries", "idempotency_records", "leases",
		"projects", "sessions", "signup_requests", "task_movements",
		"tasks", "users",
	} {
		if n := countRows(t, db,
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '`+table+`'`); n != 1 {
			t.Errorf("table %s present %d times, want 1", table, n)
		}
	}
}

func TestMigrateTwiceIsNoOp(t *testing.T) {
	db := openTestDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	before := readMigrationRows(t, db)
	if len(before) == 0 {
		t.Fatal("first migrate recorded nothing")
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	after := readMigrationRows(t, db)

	if !slices.Equal(before, after) {
		t.Fatalf("second migrate changed rows: before %v, after %v", before, after)
	}
}

func TestMigrateDetectsChangedMigration(t *testing.T) {
	db := openTestDB(t)

	original := fstest.MapFS{
		"0001_init.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE widget (id INTEGER);"),
		},
	}
	if err := migrate(db, original); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	changed := fstest.MapFS{
		"0001_init.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE widget (id INTEGER, label TEXT);"),
		},
	}
	if err := migrate(db, changed); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("migrate changed file error = %v, want a checksum error", err)
	}

	if n := countRows(t, db, `SELECT count(*) FROM pragma_table_info('widget')`); n != 1 {
		t.Fatalf("widget columns = %d, want 1 (schema must be untouched)", n)
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	db := openTestDB(t)

	files := fstest.MapFS{
		"0001_init.sql": &fstest.MapFile{Data: []byte("CREATE TABLE widget (id INTEGER);")},
	}
	if err := migrate(db, files); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO schema_migrations (version, applied_at, checksum) VALUES (999, 0, 'x')`); err != nil {
		t.Fatalf("insert unknown version: %v", err)
	}

	if err := migrate(db, files); err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("migrate with unknown version error = %v, want a newer schema error", err)
	}
}

func TestMigrateRejectsBadFileNames(t *testing.T) {
	valid := []byte("CREATE TABLE widget (id INTEGER);")

	tests := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"0001_a.sql": &fstest.MapFile{Data: valid},
				"0001_b.sql": &fstest.MapFile{Data: valid},
			},
			want: "duplicate",
		},
		{
			name:  "no separator",
			files: fstest.MapFS{"init.sql": &fstest.MapFile{Data: valid}},
			want:  "unparseable",
		},
		{
			name:  "non numeric version",
			files: fstest.MapFS{"v1_init.sql": &fstest.MapFile{Data: valid}},
			want:  "unparseable",
		},
		{
			name:  "zero version",
			files: fstest.MapFS{"0000_init.sql": &fstest.MapFile{Data: valid}},
			want:  "unparseable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			err := migrate(db, tt.files)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("migrate error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestMigrateRollsBackFailedFile(t *testing.T) {
	db := openTestDB(t)

	files := fstest.MapFS{
		"0001_init.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE first (id INTEGER);\nINSERT INTO missing VALUES (1);"),
		},
	}
	if err := migrate(db, files); err == nil {
		t.Fatal("migrate succeeded, want an error")
	}

	if n := countRows(t, db,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'first'`); n != 0 {
		t.Errorf("table first exists after a failed migration")
	}
	if n := countRows(t, db, `SELECT count(*) FROM schema_migrations`); n != 0 {
		t.Errorf("schema_migrations rows = %d, want 0", n)
	}
}
