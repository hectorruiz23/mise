# Mise MCP server

A Model Context Protocol server so any agent — Hermes, Codex, Claude Code, Cursor — can work the
Mise board **without reading its source and without an owner in the loop explaining how**.

`mise_mcp.py` is a single file with no dependencies: Python 3.9+, standard library only, stdio
transport. Copy it anywhere, point a client at it, done.

---

## Why it wraps the HTTP API instead of the database

Every rule that matters — the human gate on `review → done`, lease expiry, versions, idempotency —
lives in the Go domain on the Pi. A server that touched SQLite directly would be a second source of
truth, and the two would drift within a week. Here, the server is a thin translator: it builds the
right HTTP call, sets the headers the API demands, and reports the API's own error codes verbatim.

Two consequences worth knowing:

- **Nothing is decided here.** If the API says 403, the agent sees 403, with the API's message.
- **It cannot outgrow the API.** A new capability appears in this server only after it exists in the
  API, which is the property you want from a public interface.

## Why some tools do not exist

There is no `approve`, no `delete_task` and no `create_project`. The API refuses those for agents,
so offering them would teach an agent to fail. The absence is the contract: an agent that reads the
tool list learns what it may do without trying anything.

---

## Registering it

The token is read from the environment, then from disk:
`MISE_TOKEN` → `~/.config/mise/agent-token` → `~/.config/mise/token`.
URL: `MISE_URL`, then `~/.config/mise/url`, then `http://127.0.0.1:8787`.

**Use the agent token.** A human token works (and is required for `create_task`), but then every
movement in the history is signed by the human and the audit trail lies.

### Hermes

```yaml
# ~/.hermes/config.yaml
mcp_servers:
  mise:
    command: "python3"
    args: ["/path/to/mise/mcp/mise_mcp.py"]
    timeout: 60
```

Tools appear as `mcp_mise_*`. Restart Hermes to pick it up.

### Codex

```toml
# ~/.codex/config.toml
[mcp_servers.mise]
command = "python3"
args = ["/path/to/mise/mcp/mise_mcp.py"]
```

### Claude Code

```json
{
  "mcpServers": {
    "mise": {
      "command": "python3",
      "args": ["/path/to/mise/mcp/mise_mcp.py"]
    }
  }
}
```

In `.mcp.json` at the project root, or in `~/.claude.json` for every project.

### dsh (DeepSeek Harness)

`dsh` does not take a `command` block in a profile patch: it needs a plugin row inserted. Install the
client once per profile and add the row:

```sh
dsh plugin --profile headless add @deepseek-ai/dsh-mcp-client@0.2.1-rc.5
```

```yaml
# ~/.dsh/profiles/headless/cordis.patch.yml  (same block for the web profile)
- insert:
    - id: mcp-mise
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: mise
        transport: stdio
        command: python3
        args: ['/path/to/mise/mcp/mise_mcp.py']
        toolCallTimeoutMs: 30000
```

Two traps: a bare `- id: mcp-mise` in that file merely *edits* an existing entry and fails with
`patch: entry "mcp-mise" not found`, and entries written into `cordis.yml` are silently ignored.
Tools appear as `mcp__mise__<tool>`.

### Any other client

Command `python3 <path>/mise_mcp.py`, transport `stdio`, no arguments, no env required if the token
file exists.

---

## The tools

| Tool | What it does | Agent may |
|---|---|---|
| `board_overview` | Six columns with counts and cards | yes |
| `list_tasks` | Filter by state and project, cursor pagination | yes |
| `get_task` | Fields, movements, leases, deliveries, comments | yes |
| `create_task` | Create a card in backlog (idempotent) | **no** — human only, 403 |
| `move_task` | Change state or edit fields, sends `If-Match` for you | yes, except `review → done` |
| `acquire_lease` | Take a card: 15 minutes, renewable | yes |
| `renew_lease` | Extend your lease, does not touch the card version | yes |
| `release_lease` | Give the card back without delivering | yes |
| `deliver` | Summary + evidence, optional `pr_url`, moves the card to review | yes |
| `comment` | Add a comment | yes |

Every write answers with a stable envelope — `{"task": …}`, `{"lease": …}`, `{"delivery": …}`,
`{"comment": …}` — because the raw API is not consistent: `POST`/`PATCH` return the bare task while
`GET` wraps it. The server normalises that so an agent never has to know which verb it used.

## The loop an agent should follow

The server ships this as the `instructions` field of `initialize`, so clients that display it get
the contract for free:

1. `list_tasks state=ready` (or `board_overview`).
2. `acquire_lease` on the one it picks.
3. Work. `renew_lease` when needed, `release_lease` if it gives up.
4. `deliver` with a summary and at least one verifiable piece of evidence.
5. Stop. A human reads the evidence and decides.

A task in progress with no live lease can be taken by someone else. That is not a bug: it is how
a crashed agent stops holding a card hostage.

## Quirks the server already handles

These bite anyone talking to the API directly:

- **Cloudflare rejects Python's default User-Agent** with `403 error code: 1010`. The server sends
  `mise-mcp/1.0`.
- `PATCH` requires `If-Match`; the server reads the current version first and sends it, so a stale
  client gets a clean `412` instead of a lost update.
- `POST /api/tasks` requires `Idempotency-Key`; the server generates a UUID per call.
- `deliver` resolves your live lease automatically when `lease_id` is omitted.

## Verifying it

```sh
python3 mcp/smoke.py     # leads with PASS/FAIL; read-only, so safe against the real board
```

Thirteen checks: handshake, the agent contract travels with it, ten tools, no approve/delete tool,
every schema declared, board with its six columns, a card resolves, an unknown card answers
`not_found`.

The full write cycle — create, lease, deliver, agent refused on close, human closes — was run once
by hand against the Pi and is not kept in the repo on purpose: it writes real cards.

Or by hand, one tool at a time:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | MISE_URL=http://127.0.0.1:8787 MISE_TOKEN=$(cat ~/.config/mise/agent-token) python3 mise_mcp.py
```

Expected: an `initialize` result naming the server `mise`, then ten tool definitions. Anything
writing to stdout that is not a JSON-RPC frame breaks the client — debug lines go to stderr.

## Not done yet

- **No HTTP transport.** stdio only, so each agent spawns its own process. An HTTP/StreamableHTTP
  endpoint would let one instance serve several agents; it also puts the MCP server behind
  Cloudflare, which is where the User-Agent rule comes back.
- **No resources or prompts.** `resources/list` and `prompts/list` answer empty by design; the
  instructions field carries the workflow instead.
- **No tool-level authorisation.** The server does not check whether you are human or agent before
  calling: the API does, and duplicating that check is how the two drift apart.
