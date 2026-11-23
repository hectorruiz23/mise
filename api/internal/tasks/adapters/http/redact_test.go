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
			in:   "`VAULT_DEFAULT` apunta a `/home/someone/Notas/03 Estudio/Temas/Inglés hablado — B2+ hacia C1.md`: su ruta y el título",
			want: "`VAULT_DEFAULT` apunta a `<path>`: su ruta y el título",
		},
		{
			name: "home directory alone",
			in:   "42 commits con `/home/someone` en 357 versiones de fichero",
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

func TestRedactedDeliveryCoversEvidenceItems(t *testing.T) {
	label := "el repo está en /home/someone/Projects/mise"
	raw := "`cd ~/Projects/mise && go test ./...`"
	delivery := redactedDelivery(deliveryJSON{
		ID:       7,
		TaskKey:  "MI-7",
		Summary:  "hecho en /home/someone/tmp",
		Evidence: []string{raw},
		EvidenceItems: []evidenceItemJSON{
			{Label: &label, Raw: raw},
			{Label: nil, Raw: "salida limpia"},
		},
	})

	if strings.Contains(delivery.Summary, "/home/someone") {
		t.Fatalf("summary still carries a path: %q", delivery.Summary)
	}
	if delivery.EvidenceItems[0].Label == nil ||
		strings.Contains(*delivery.EvidenceItems[0].Label, "/home/someone") {
		t.Fatalf("label still carries a path: %v", delivery.EvidenceItems[0].Label)
	}
	if !strings.Contains(*delivery.EvidenceItems[0].Label, "<path>") {
		t.Fatalf("label carries no redaction mark: %q", *delivery.EvidenceItems[0].Label)
	}
	if strings.Contains(delivery.EvidenceItems[0].Raw, "~/Projects") {
		t.Fatalf("raw still carries a path: %q", delivery.EvidenceItems[0].Raw)
	}
	if delivery.Evidence[0] != delivery.EvidenceItems[0].Raw {
		t.Fatalf("evidence and evidence_items diverged: %q vs %q",
			delivery.Evidence[0], delivery.EvidenceItems[0].Raw)
	}
	if delivery.EvidenceItems[1].Label != nil || delivery.EvidenceItems[1].Raw != "salida limpia" {
		t.Fatalf("clean entry changed: %+v", delivery.EvidenceItems[1])
	}
	if delivery.ID != 7 || delivery.TaskKey != "MI-7" {
		t.Fatalf("redaction lost identity fields: %+v", delivery)
	}
}

func TestRedactedDetailCoversEveryProse(t *testing.T) {
	notes := "en `/home/someone/Projects/mise` está el repo"
	acceptance := "corre `~/.local/bin/hermes` y comprueba"
	reason := "movida desde ~/Projects/mise-mockups"

	detail := redactedDetail(detailJSON{
		Task:      taskJSON{Key: "MI-1", Title: "t", Notes: &notes, Acceptance: &acceptance},
		Movements: []movementJSON{{TaskKey: "MI-1", ToState: "ready", Reason: &reason}},
		Deliveries: []deliveryJSON{{
			ID:       1,
			TaskKey:  "MI-1",
			Summary:  "hecho en /home/someone/tmp",
			Evidence: []string{"`ls ~/Projects/mise`", "salida limpia"},
		}},
		Comments: []commentJSON{{ID: 1, TaskKey: "MI-1", Body: "mira /home/someone/x"}},
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
		if strings.Contains(value, "/home/someone") || strings.Contains(value, "~/") {
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
