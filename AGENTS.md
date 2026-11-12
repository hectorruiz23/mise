# AGENTS.md — contract for every coding agent in this repository

This file is permanent context. Read it before touching anything, and obey it over any other document.

## What this project is

Mise is a task board where a human and AI agents share work: agents lease tasks, work them and hand them back
with evidence; the human reviews and is the only actor who can close a task. Backend in Go, SQLite, one binary
that also serves the panel.

## Where things live

| Path | What it is |
|---|---|
| `api/` | The Go backend: domain, app, adapters, HTTP, migrations, and the panel it serves |
| `web/` | The React panel; `make stage-panel` copies its build into `api/internal/panel/dist` |
| `mcp/` | A stdio MCP server so agents work the board through tools instead of raw HTTP |
| `SELF-HOSTING.md` | Build, systemd, reverse proxy, first users, upgrades, backups |

## No documents are committed

Every change starts from a written spec, but the spec never lands in `main`: the brief for what you are building
lives in the branch you are on, or in the pull request that carries it, and once the change is merged only the
code remains. Closed product decisions are kept outside the repository: the working documents of a local
checkout are git-ignored on purpose. Read them when they are there, never copy them into the tree, never
delete them from disk, and never reopen a decision they already close. If you think one of them is wrong, say so
in your report and keep working on what is unambiguous.

**The repository carries four documents and a licence, and no more**: `README.md`, `AGENTS.md`,
`SELF-HOSTING.md`, `mcp/README.md` and `LICENSE`. Do not add another: no design notes, no analysis, no
milestone report, no changelog, no `NOTES.md`, no `docs/`. A document each agent writes on its own is a second
truth that nobody maintains, and it outlives the change it described. What you learned goes in the pull request
and in the commit message, which are attached to the diff and age with it. If something belongs in prose for
whoever runs this, it goes inside one of the four, in the section it already has.

## Hard rules

1. **English everywhere**: code, identifiers, schema, API, UI text, logs, documentation.
2. **No comments in the code.** Not in Go, TypeScript, CSS, shell, SQL, Python or CI. The code says what it
   does; why it changed belongs in the commit message and in the pull request. The only lines that may start
   with a comment marker are the ones a tool requires: a shebang, `//go:embed` and friends, `/// <reference`,
   `@ts-expect-error`. If a line of code needs explaining to be read, rename the thing instead.
   An **applied migration is frozen**: its bytes are the checksum every database recorded, so an existing file
   is never edited, comments included, and a schema change is always a new file with the next version.
3. **No database triggers.** All business rules live in Go. The database holds only `CHECK`, `FOREIGN KEY`,
   `UNIQUE` and index constraints.
4. **`domain` imports only the standard library and `internal/kernel`.** The kernel holds value types and rule
   errors, nothing else. No imports of `app`, `adapters`, `platform` or another slice's domain from `domain`.
5. **Interfaces are declared in `app`**, where they are consumed. There is no `ports` package and no `pkg/`.
6. **`adapters` own feature SQL and HTTP translation.** `platform` owns generic infrastructure (database open,
   migrations, server, middleware).
7. **No framework, no ORM, no router library.** `net/http` + `database/sql` + `modernc.org/sqlite` only.
8. **Never edit or delete a test to make it pass.** If a test encodes the wrong behaviour, say so in the report.
9. **Never commit to `main` or `master`.** Every change lands through a pull request: a branch, the diff, the
   review, the merge. No direct push, no force-push, no "it is only one line". The pre-commit hook refuses a
   commit made on either branch; `MISE_ALLOW_MAIN=1` is the owner's escape hatch, not yours. The owner reviews
   and merges, and publishing the mirror is his job too.
10. **Do not add features outside the current milestone.** If something is missing for your milestone, implement
    the simplest version that satisfies the spec and note it.
11. **No secrets in the repository.** No tokens, no cookies, no database files.

## Toolchain

Go 1.27.4 or newer on `PATH`, and Node 26 with npm for the panel. The production build is always
`CGO_ENABLED=0`. If you keep several Go versions installed, export `GOTOOLCHAIN=local` so the module
builds with the one you selected instead of downloading another.

## How to verify your work

From `api/`:

```sh
make verify     # gofmt + go vet + go test ./...
```

From `web/`, whenever you touched the panel (this is what the pre-commit hook runs too):

```sh
npm run typecheck
```

Run them before you report. Paste the **real output**. A claim without output is not evidence. Never report
success for a command you did not run.

The same two commands run automatically on `git commit` through `api/scripts/pre-commit`, which `make
install-hooks` links into `.git/hooks/pre-commit`. A rejected commit prints the failing command and how to run it
by hand.

## If you get stuck

- **Ambiguity**: choose the simple option, note it in the report, and continue. Do not deliberate about edge
  cases; do not open a design discussion with yourself.
- **Tests pass**: stop and deliver. No extra refactors, no bonus features, no "while I was here".
- **Something is impossible**: stop and report what blocks it, with the exact error.

## Report format

End with: what you changed (file by file), the commands you ran with their real output, what you did **not** do,
and anything you think is wrong in the spec.
