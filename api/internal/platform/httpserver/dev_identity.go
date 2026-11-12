//go:build dev

package httpserver

import (
	"os"
	"strconv"
)

func DevPrincipalFromEnv() *Principal {
	raw := os.Getenv("MISE_DEV_USER")
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	name := os.Getenv("MISE_DEV_NAME")
	if name == "" {
		name = "dev"
	}
	return &Principal{ID: id, Name: name, Kind: "human"}
}
