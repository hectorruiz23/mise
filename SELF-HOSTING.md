# Self-hosting Mise

Mise is one Go binary and one SQLite file. It speaks plain HTTP on a loopback port and expects
something in front of it to terminate TLS. Nothing here belongs to one particular machine: the
placeholders are yours to fill in.

| Placeholder | Meaning |
|---|---|
| `<host>` | The hostname people and agents will use, for example `mise.example.com` |
| `<public-origin>` | That hostname with its scheme, `https://<host>`, no trailing slash |
| `<service-user>` | The unprivileged Unix user the service runs as |
| `/srv/mise` | Where the binary and the database live; any directory will do |

## 1. What you need

- A Linux host with systemd (or another supervisor you trust) and an unprivileged user for the service.
- Go 1.27.4 or newer to build it, unless you copy a prebuilt binary.
- A TLS-terminating reverse proxy you already run — Caddy, nginx, Traefik, anything that can forward to
  a loopback port — and a hostname that resolves to it.
- A few tens of megabytes of disk. The database is a single file that grows with your cards.

Node 26 (`npm ci && npm run build` in `web/`) is only needed if you change the panel.

## 2. Build

Two ways, the same binary inside both: the plain one, and the container.

```sh
cd api && make build-all     # panel, then the binary for this machine in api/build/mise
cd api && make release       # or a cross-compiled linux/arm64 build plus its .sha256
```

`make build-all` compiles the panel first and stages it into `api/internal/panel/dist`, because the
panel is embedded in the binary: there is no second process and no second origin.

```sh
docker build -t mise .                  # or just pull the image built by CI
MISE_PUBLIC_ORIGIN=https://mise.example.com MISE_DATA=/srv/mise/data docker compose up -d
```

The image is `linux/arm64` and its entrypoint is the same binary, running as uid 1000 so the mounted
data directory stays writable by the service user. `compose.yml` in the repository takes the three
variables it needs and nothing else; `/data` is the volume, and the database lives there.

To have a container redeploy by itself, put Watchtower next to it and enable it only for the labelled
service, so the updater never touches anything else on the host:

```yaml
  watchtower:
    image: containrrr/watchtower
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      WATCHTOWER_LABEL_ENABLE: "true"
      WATCHTOWER_POLL_INTERVAL: "300"
      WATCHTOWER_CLEANUP: "true"
```

with `com.centurylinklabs.watchtower.enable: "true"` on the `mise` service. Every five minutes it
compares the running digest with the registry and restarts on a change; a release is then: merge,
wait for the image, wait for the poll.

## 3. Layout on the host

```text
/srv/mise/
├── mise              the running binary, mode 0755
├── mise.previous     the previous release, kept for rollback
└── data/
    └── mise.db       the database; -wal and -shm appear beside it at runtime
```

```sh
sudo install -d -o <service-user> -g <service-user> -m 0700 /srv/mise/data
sudo install -o <service-user> -g <service-user> -m 0755 api/build/mise /srv/mise/mise
```

`/srv/mise/data` must be the only writable path the service needs.

## 4. Configuration

| Variable | Meaning | Default |
|---|---|---|
| `MISE_ADDR` | Listen address (`-addr` overrides it) | `127.0.0.1:8787` |
| `MISE_DB` | Path to the SQLite file (`-db` overrides it). **Required** | — |
| `MISE_PUBLIC_ORIGIN` | Public origin, scheme and host, no trailing slash. **Required in practice** | empty |
| `MISE_LOG_LEVEL` | `debug`, `info`, `warn` or `error` | `info` |
| `MISE_TRUST_PROXY` | Read the client address for the login limit from `X-Forwarded-For` | `false` |

`MISE_PUBLIC_ORIGIN` does two jobs: it decides whether the session cookie carries `Secure`, and it is
what the CSRF check compares against the request's `Origin` header. With it empty, every `POST`,
`PUT`, `PATCH` and `DELETE` answers `403 origin check failed` — the service fails closed, so a global
write silence is the symptom of a missing or misspelled origin, not of a broken build.

`MISE_TRUST_PROXY=1` is for the normal case: the binary on loopback and a proxy in front. Without it
every request reaches the login limit with the same socket address, so the ten failed attempts would
be one bucket for the whole internet and a stranger could lock you out for fifteen minutes by
posting junk to `/api/sessions`. Only set it when nothing but that proxy can reach the port — with
the header trusted from the open internet, a client chooses its own bucket. The limit counts failed
attempts only; a correct token never spends one.

Keep the listener on loopback and let the proxy own the public side. If the proxy rewrites the host,
the origin the browser sends will no longer match and mutations will be refused.

## 5. A systemd unit

Adjust `User`, `WorkingDirectory`, the environment lines and the paths; the hardening flags are
worth keeping as they are.

```ini
# /etc/systemd/system/mise.service  (a user unit under ~/.config/systemd/user also works)
[Unit]
Description=Mise task board
After=network.target

[Service]
Type=simple
User=<service-user>
WorkingDirectory=/srv/mise
Environment=MISE_DB=/srv/mise/data/mise.db
Environment=MISE_PUBLIC_ORIGIN=<public-origin>
# Only when nothing but the proxy can reach the port; see section 4.
Environment=MISE_TRUST_PROXY=1
ExecStart=/srv/mise/mise -addr 127.0.0.1:8787
Restart=always
RestartSec=2
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/srv/mise/data
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemd-analyze verify /etc/systemd/system/mise.service   # silent and exit 0 when it is valid
sudo systemctl daemon-reload && sudo systemctl enable --now mise
systemctl status mise --no-pager
curl -sS http://127.0.0.1:8787/api/health                      # {"ok":true,"db":"ok"}
```

`ProtectSystem=strict` makes the filesystem read-only except `ReadWritePaths`; `UMask=0077` keeps the
database and its WAL private to the service user.

## 6. The reverse proxy

Terminate TLS at the proxy and forward the whole request — method, `Host`, `Origin`, cookies — to
`127.0.0.1:8787`. Caddy does it in two lines:

```text
<host> {
    reverse_proxy 127.0.0.1:8787
}
```

nginx needs the usual `proxy_set_header Host $host;` and `proxy_set_header Origin $http_origin;` so
the CSRF check sees what the browser sent. Whatever you use, `https://<host>` must equal
`MISE_PUBLIC_ORIGIN` character for character: a trailing slash, `www.` or `http` instead of `https`
turns every mutation into a `403`.

## 7. The first human and the first agent

There is no bootstrap account and no user-management CLI: the first identity arrives through the same
signup flow the panel uses — request, approve the row in SQLite, claim the token.

Every `POST` needs an `Origin` header matching `MISE_PUBLIC_ORIGIN`, so the `curl` commands below set
it explicitly.

```sh
BASE='http://127.0.0.1:8787'
ORIGIN='<public-origin>'
EMAIL='you@example.com'

# 1. Request the signup. The answer carries the id and a one-time secret.
curl -sS -X POST "$BASE/api/signup-requests" \
  -H "Origin: $ORIGIN" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\"}"

# 2. Approve it directly in the database (the product has no approval UI for the first user).
SIGNUP_ID=1
sqlite3 /srv/mise/data/mise.db \
  "UPDATE signup_requests SET approved_at = strftime('%s','now') WHERE id = $SIGNUP_ID AND approved_at IS NULL AND consumed_at IS NULL; SELECT changes();"
# Expected: 1

# 3. Claim the token. This is the only time it is revealed.
SIGNUP_SECRET='<secret from step 1>'
curl -sS -X POST "$BASE/api/signup-requests/$SIGNUP_ID/claim" \
  -H "Origin: $ORIGIN" -H 'Content-Type: application/json' \
  -d "{\"secret\":\"$SIGNUP_SECRET\"}"
# Expected: {"user_id":1,"token":"<64-hex>"}

# 4. Check it.
curl -sS -H "Authorization: Bearer <64-hex>" "$BASE/api/me"
```

Open the board at `https://<host>`, paste the token into the login screen, and you are in. The token
is a bearer credential: treat it like a password. `POST /api/sessions` turns it into a 30-day cookie.

An identity created this way is always a `human`. To add an agent, repeat the three steps with the
agent's email and then flip the kind in SQLite — that reuses the product's own secret generation,
hashing and one-time claim instead of hand-writing a token hash:

```sh
AGENT_USER_ID=2
sqlite3 /srv/mise/data/mise.db \
  "UPDATE users SET kind = 'agent' WHERE id = $AGENT_USER_ID; DELETE FROM sessions WHERE user_id = $AGENT_USER_ID;"
```

An agent sends its token as `Authorization: Bearer <token>`. It can take work and deliver it, and it
cannot close a card: `review → done` is human-only in the domain layer.

## 8. Upgrade and rollback

Migrations run at startup, before the server accepts traffic, so an upgrade is: replace the binary,
restart, read the log.

```sh
cd /srv/mise
install -m 0755 mise-linux-arm64 mise.new     # the artifact you copied in
cp -p mise mise.previous
mv mise.new mise                              # rename, never overwrite a running binary in place
sudo systemctl restart mise
sleep 2
curl -sS http://127.0.0.1:8787/api/health
journalctl -u mise -n 20 --no-pager            # expect "database ready", then "listening"
```

Rollback is the copy kept on purpose:

```sh
cd /srv/mise
cp -p mise.previous mise.rollback && mv mise.rollback mise
sudo systemctl restart mise
curl -sS http://127.0.0.1:8787/api/health
```

## 9. Backups

SQLite runs in WAL mode, so `mise.db-wal` and `mise.db-shm` sit beside the database while the service
runs: a plain `cp mise.db` is **not** a safe backup, because recently committed transactions may still
be in the WAL. Use SQLite's online backup, which takes a consistent snapshot while the service keeps
serving:

```sh
sqlite3 /srv/mise/data/mise.db ".backup '/srv/mise/backup.db'"
sqlite3 /srv/mise/backup.db "PRAGMA integrity_check;"    # expect: ok
```

Nothing schedules that for you. Put it in a timer and copy the result somewhere else, or a dead disk
takes the cards, the history and the identities with it.

## 10. When it answers 401 or 403

- **`401` on everything** — no credential, an expired one, or a revoked token. A human token works for
  reads; `POST /api/sessions` needs it once to mint the cookie.
- **`403 origin check failed`** — `MISE_PUBLIC_ORIGIN` is empty, or it does not match the `Origin`
  the browser sends. This is the first thing to look at after moving the service behind a proxy.
- **`403 forbidden` with a valid agent token** — the agent tried something reserved to humans, such as
  closing a card; that refusal is the product working.
- **`database is locked`** — two processes pointing at the same file. Only one binary may own it.
- **Sessions stop working after a clock jump** — session expiry is computed from the system clock;
  fix the clock and log in again.
