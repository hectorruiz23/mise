package credential_test

import (
	"encoding/base64"
	"regexp"
	"testing"

	"mise/api/internal/platform/credential"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewTokenIs32RandomBytesInBase64URL(t *testing.T) {
	token, err := credential.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not unpadded base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(raw))
	}
	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43", len(token))
	}
}

func TestNewTokenAndSecretDiffer(t *testing.T) {
	token, err := credential.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	secret, err := credential.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}
	if token == secret {
		t.Fatal("two generated values are identical")
	}
}

func TestHashIsLowercaseSHA256Hex(t *testing.T) {
	got := credential.Hash("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("Hash(hello) = %q, want %q", got, want)
	}
	if !hex64.MatchString(got) {
		t.Fatalf("Hash(hello) = %q, want 64 lowercase hex characters", got)
	}
}
