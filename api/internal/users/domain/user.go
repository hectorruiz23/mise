package domain

import (
	"strings"
	"unicode/utf8"
)

type Kind string

const (
	Human Kind = "human"
	Agent Kind = "agent"
)

const EmailMaxLength = 320

const NameMaxLength = 200

type User struct {
	ID        int64
	Email     string
	Name      string
	Kind      Kind
	TokenHash string
	Active    bool
	CreatedAt int64
}

func (k Kind) Valid() bool {
	return k == Human || k == Agent
}

func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", invalidInput("email is required")
	}
	if !strings.Contains(email, "@") {
		return "", invalidInput("email must contain @")
	}
	if utf8.RuneCountInString(email) < 3 {
		return "", invalidInput("email is too short")
	}
	if utf8.RuneCountInString(email) > EmailMaxLength {
		return "", invalidInput("email must be at most 320 characters")
	}
	return email, nil
}

func NormalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalidInput("name is required")
	}
	if utf8.RuneCountInString(name) > NameMaxLength {
		return "", invalidInput("name must be at most 200 characters")
	}
	return name, nil
}

func NameFromEmail(email string) string {
	local := email
	if at := strings.IndexByte(email, '@'); at >= 0 {
		local = email[:at]
	}
	local = strings.TrimSpace(local)
	if local == "" {
		local = email
	}
	runes := []rune(local)
	if len(runes) > NameMaxLength {
		local = string(runes[:NameMaxLength])
	}
	return local
}

func NewUser(email, name string, kind Kind, tokenHash string, at int64) (User, error) {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	normalizedName, err := NormalizeName(name)
	if err != nil {
		return User{}, err
	}
	if !kind.Valid() {
		return User{}, invalidInput("unknown user kind")
	}
	if strings.TrimSpace(tokenHash) == "" {
		return User{}, invalidInput("token hash is required")
	}

	return User{
		Email:     normalizedEmail,
		Name:      normalizedName,
		Kind:      kind,
		TokenHash: tokenHash,
		Active:    true,
		CreatedAt: at,
	}, nil
}
