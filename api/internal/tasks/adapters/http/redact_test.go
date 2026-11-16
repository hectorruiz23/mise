package http

import (
	"strings"
	"testing"
)

func TestRedactedTextHidesPrivatePaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "absolute path between backticks",
			in:   "`VAULT_DEFAULT` apunta a `/home/hector/Obsidian/Hermes/03 Estudio/Temas/Inglés hablado — B2+ hacia C1.md`: su ruta y el título",
			want: "`VAULT_DEFAULT` apunta a `<path>`: su ruta y el título",
		},
		{
			name: "home directory alone",
			in:   "42 commits con `/home/hector` en 357 versiones de fichero",
			want: "42 commits con `<path>` en 357 versiones de fichero",
		},
		{
			name: "tilde path ends at punctuation",
			in:   "el real se va a `~/.config/refire/voicekit.toml`, fuera del repo",
			want: "el real se va a `<path>`, fuera del repo",
		},
		{
			name: "tilde path keeps the prose after it",
			in:   "si ~/Projects/english se renombra también (arrastraría voicekit)",
			want: "si <path> se renombra también (arrastraría voicekit)",
		},
		{
			name: "tilde path inside a command keeps the command",
			in:   "  cd ~/Projects/refire && cat prompt.txt | codex exec --sandbox read-only",
			want: "  cd <path> && cat prompt.txt | codex exec --sandbox read-only",
		},
		{
			name: "several paths in one paragraph",
			in:   "lee ~/Projects/portfolio/GUIA_IDENTIDAD.md §3 y ~/.hermes/scripts/retro.py",
			want: "lee <path> §3 y <path>",
		},
		{
			name: "macOS home",
			in:   "vive en /Users/anna/Projects/x",
			want: "vive en <path>",
		},
		{
			name: "approximation mark is not a path",
			in:   "mueve el ancho útil ~15 px y la guarda de la línea ~154",
			want: "mueve el ancho útil ~15 px y la guarda de la línea ~154",
		},
		{
			name: "grep pattern for /home/ is not a path",
			in:   "`git grep -In '/home/' -- .` devuelve 0 líneas",
			want: "`git grep -In '/home/' -- .` devuelve 0 líneas",
		},
		{
			name: "text without paths is untouched",
			in:   "sin rutas aquí",
			want: "sin rutas aquí",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactedText(tc.in); got != tc.want {
				t.Fatalf("redactedText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedactedTextLeavesEmptyAlone(t *testing.T) {
	if got := redactedText(""); got != "" {
		t.Fatalf("redactedText(\"\") = %q, want empty", got)
	}
	if got := redactedOptional(nil); got != nil {
		t.Fatalf("redactedOptional(nil) = %v, want nil", got)
	}
}

func TestRedactedDetailCoversEveryProse(t *testing.T) {
	notes := "en `/home/hector/Projects/mise` está el repo"
	acceptance := "corre `~/.local/bin/hermes` y comprueba"
	reason := "movida desde ~/Projects/mise-mockups"

	detail := redactedDetail(detailJSON{
		Task:      taskJSON{Key: "MI-1", Title: "t", Notes: &notes, Acceptance: &acceptance},
		Movements: []movementJSON{{TaskKey: "MI-1", ToState: "ready", Reason: &reason}},
		Deliveries: []deliveryJSON{{
			ID:       1,
			TaskKey:  "MI-1",
			Summary:  "hecho en /home/hector/tmp",
			Evidence: []string{"`ls ~/Projects/mise`", "salida limpia"},
		}},
		Comments: []commentJSON{{ID: 1, TaskKey: "MI-1", Body: "mira /home/hector/x"}},
	})

	prose := map[string]string{
		"notes":      *detail.Task.Notes,
		"acceptance": *detail.Task.Acceptance,
		"movement":   *detail.Movements[0].Reason,
		"summary":    detail.Deliveries[0].Summary,
		"evidence":   detail.Deliveries[0].Evidence[0],
		"comment":    detail.Comments[0].Body,
	}

	for name, value := range prose {
		if strings.Contains(value, "/home/hector") || strings.Contains(value, "~/") {
			t.Fatalf("%s still carries a private path: %q", name, value)
		}
	}
	if detail.Deliveries[0].Evidence[1] != "salida limpia" {
		t.Fatalf("evidence without a path changed: %q", detail.Deliveries[0].Evidence[1])
	}
	if detail.Task.Key != "MI-1" || detail.Deliveries[0].ID != 1 {
		t.Fatalf("redaction lost identity fields: %+v", detail)
	}
}
