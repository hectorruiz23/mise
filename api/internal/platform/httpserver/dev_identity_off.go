//go:build !dev

package httpserver

func DevPrincipalFromEnv() *Principal { return nil }
