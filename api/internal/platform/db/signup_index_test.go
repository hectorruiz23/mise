package db

import (
	"strings"
	"testing"
)

func TestSignupPendingEmailIndex(t *testing.T) {
	database := openTestDB(t)
	if err := Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var indexes int
	if err := database.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'signup_requests_pending_email'`).
		Scan(&indexes); err != nil {
		t.Fatalf("find pending email index: %v", err)
	}
	if indexes != 1 {
		t.Fatalf("signup_requests_pending_email indexes = %d, want 1", indexes)
	}

	if _, err := database.Exec(`
		INSERT INTO signup_requests (email, secret_hash, requested_at)
		VALUES (?, ?, ?)`, "human@example.com", humanHash, now); err != nil {
		t.Fatalf("first pending request: %v", err)
	}

	_, err := database.Exec(`
		INSERT INTO signup_requests (email, secret_hash, requested_at)
		VALUES (?, ?, ?)`, "human@example.com", agentHash, now)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second pending request error = %v, want UNIQUE constraint failed", err)
	}
}
