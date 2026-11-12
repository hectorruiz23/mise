# Mise

Mise (*mise en place*) is a task board where a human and AI agents share the work: an agent leases a
card, does the job and hands it back with evidence; the human reviews and is the only actor who can
close it. No sprints, no estimates, no burndown — the board carries the backlog.

It is one Go binary and one SQLite file: no database server, no external service, and no second
process for the panel, which is compiled into the binary. Run it on a box you own and put a TLS
proxy in front; [SELF-HOSTING.md](SELF-HOSTING.md) is the whole checklist.

## How it works

- Six states: `backlog`, `ready`, `in_progress`, `blocked`, `review`, `done`. The panel shows five
  columns; the backlog is a sortable table of its own.
- Agents work by **lease**: a card in `ready` is taken for 15 minutes (renewable), so two agents
  never pick the same one.
- Work comes back as a **delivery**: a summary and evidence lines with real output. A claim without
  output is not evidence.
- `review → done` is **human only**, enforced in the domain layer, not in the panel.
- Writes are versioned: `PATCH` carries `If-Match`, and a stale version is a `409`, never a silent
  overwrite. Creation is idempotent with an `Idempotency-Key` bound to the card forever.
- Many users are possible (signup by request plus manual approval); the model assumes one human and
  a few agents working the same board.

## Layout

| Path | What it is |
|---|---|
| `api/` | The Go backend and the panel it serves, embedded in the binary |
| `web/` | React + TypeScript panel (Vite, Tailwind), built into `api/internal/panel/dist` |
| `mcp/` | A dependency-free MCP server (stdio) so agents call tools instead of composing curl |
| `AGENTS.md` | The contract a coding agent reads before touching anything |
| `SELF-HOSTING.md` | Build, systemd, reverse proxy, first users, upgrades, backups |

## Build

Go 1.27.4 and Node 26. No framework, no ORM, no router library — `net/http` and `database/sql`
only.

```sh
cd api && make verify                   # gofmt + go vet + go test ./...
cd web && npm ci && npm run typecheck   # panel types
cd api && make build-all                # panel, then the binary in api/build/mise
cd api && make install-hooks            # link the pre-commit hook into .git/hooks
```

`make install-hooks` is worth running once: `api/scripts/pre-commit` runs `make verify` and, when
the index touches `web/`, the panel typecheck.

## Run locally

`make dev` is the short version: it compiles a development build, seeds a temporary database with a
few cards and serves the board on a free port, already logged in as a local dev human. Nothing is
stored and nothing is needed from you — the database lives in a temporary directory and disappears on
`Ctrl+C`.

```sh
cd api && make dev
```

The development identity only exists in a binary built with `-tags dev`, which is what `make dev` does;
the image CI publishes has no dev code inside, so `MISE_DEV_USER` cannot switch anything on in
production. It also refuses to start unless the listener is loopback.

For the plain way — a throwaway database and an origin that matches exactly, since with
`MISE_PUBLIC_ORIGIN` empty or different every write answers `403`:

```sh
cd api && go build -o build/mise ./cmd/mise
MISE_ADDR=127.0.0.1:8080 MISE_DB=/tmp/mise.db MISE_PUBLIC_ORIGIN=http://127.0.0.1:8080 ./build/mise
```

`make accept` is the end-to-end suite: it starts the real binary on a temporary database, provisions
one human and one agent, and drives the HTTP API through the product flow.

## API

REST over `net/http`. The panel authenticates with a session cookie from `POST /api/sessions` (paste
a token, 30 days); agents send `Authorization: Bearer <token>`. Signup is by request plus manual
approval, `GET /api/health` is public, and everything else needs a credential.

| Route | What it does |
|---|---|
| `GET /api/health` | Liveness and database check. Public |
| `GET /api/me` | The authenticated actor |
| `GET /api/projects` | Projects and their keys |
| `GET /api/board` | Five columns keyed by state, plus counts |
| `GET /api/tasks?state=&project=&limit=` | One page, cursor in `next_cursor` |
| `POST /api/tasks` | Create (`Idempotency-Key` required, lands in `backlog`) |
| `GET /api/tasks/{key}` | Card with movements, leases, deliveries and comments |
| `PATCH /api/tasks/{key}` | Edit or move (`If-Match: <version>` required) |
| `GET /api/tasks/{key}/movements` | Movement history |
| `GET /api/tasks/{key}/leases` | Live and past leases |
| `POST /api/tasks/{key}/leases` | Take the card (15 minutes, renewable) |
| `PATCH /api/tasks/{key}/leases/{id}` | Renew |
| `DELETE /api/tasks/{key}/leases/{id}` | Release |
| `GET /api/tasks/{key}/deliveries` | Deliveries with their evidence |
| `POST /api/tasks/{key}/deliveries` | Hand the work back; moves the card to `review` |
| `GET /api/tasks/{key}/comments` | Comments |
| `POST /api/tasks/{key}/comments` | Comment (an agent needs a live lease) |
| `POST /api/sessions` | Token in, session cookie out |
| `DELETE /api/sessions/current` | Revoke the cookie |
| `POST /api/signup-requests` | Ask for an identity. Public, answered with a one-time secret |
| `GET /api/signup-requests/{id}` | Status of the request. Public |
| `POST /api/signup-requests/{id}/claim` | Exchange the secret for the token, once |

Errors are `{"error":"code","detail":"message"}`. These routes and the handler code are the
contract; a written spec belongs in the branch or the pull request that carries the change, never in
`main`.

## License

MIT. See [LICENSE](LICENSE).
