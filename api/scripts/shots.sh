#!/usr/bin/env bash
# Boot a throwaway board with data in it, so a screenshot shows something.
#
# It builds the binary with the dev identity, serves the staged panel on loopback
# against a temporary database, seeds one proposal, one approved plan and one
# environment, prints the URL and waits until you stop it. Nothing touches a real
# board: the database lives in a temporary directory and is removed on exit.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
api="$(dirname "$here")"
port="${MISE_SHOTS_PORT:-8899}"
origin="http://127.0.0.1:$port"
tmp="$(mktemp -d)"
server_pid=""

cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

if [ ! -f "$api/internal/panel/dist/index.html" ]; then
  echo "the panel is not staged: run 'make stage-panel' in api/ first" >&2
  exit 1
fi

echo "Mise shots: building the binary"
(cd "$api" && go build -tags dev -o "$tmp/mise" ./cmd/mise)

echo "Mise shots: starting the board on $origin"
MISE_DB="$tmp/mise.db" \
MISE_ADDR="127.0.0.1:$port" \
MISE_PUBLIC_ORIGIN="$origin" \
MISE_DEV_USER=1 \
MISE_DEV_NAME=Owner \
MISE_LOG_LEVEL=warn \
MISE_PLAN_REPOSITORY=example/project \
MISE_PLAN_BRANCH=main \
MISE_PLAN_WORKFLOW_ID=42 \
  "$tmp/mise" &
server_pid=$!

for _ in $(seq 1 150); do
  if curl -fsS "$origin/api/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "the board exited before answering" >&2
    exit 1
  fi
  sleep 0.1
done

post() {
  local path="$1" body="$2" extra=()
  if [ -n "${3:-}" ]; then
    extra=(-H "If-Match: \"$3\"")
  fi
  curl -fsS -X POST "$origin$path" \
    -H "Content-Type: application/json" -H "Origin: $origin" "${extra[@]}" -d "$body"
}

# The dev identity is a principal with id 1, but a fresh database has no such row
# and the proposals carry an author foreign key, so the row is written before seeding.
sqlite3 "$tmp/mise.db" "INSERT INTO users (id, email, name, kind, token_hash, created_at)
  VALUES (1, 'owner@example.test', 'Owner', 'human',
          '$(printf 'ab%.0s' {1..32})', strftime('%s','now'));"

post "/api/projects/MI/environments" '{"name":"production","url":"https://mise.example.test"}' >/dev/null

post "/api/plan-drafts" '{
  "project": "MI",
  "outcome": "The panel lists every proposal and every plan, so the owner finds them without a chat link.",
  "scenario": "make accept",
  "target_environment": "production",
  "visual_review": true,
  "visible_change": "Plans in the panel header lists the proposals and the plans.",
  "reveal_key": "T2",
  "tasks": [
    {"key": "T1", "title": "The API lists the proposals of a project", "size": "S",
     "acceptance": "GET /api/plan-drafts filters by project and never returns rows from another project."},
    {"key": "T2", "title": "Plans in the panel", "size": "M",
     "acceptance": "The header offers Plans and every row opens its own screen."}
  ],
  "edges": [{"from": "T1", "to": "T2", "kind": "integration"}]
}' > "$tmp/pending.json"

post "/api/plan-drafts" '{
  "project": "MI",
  "outcome": "The board shows what was closed, newest first.",
  "scenario": "make accept",
  "visual_review": true,
  "visible_change": "The board gains a Done tab with the closed cards.",
  "reveal_key": "T3",
  "tasks": [
    {"key": "T1", "title": "Prepare the tab"},
    {"key": "T2", "title": "Prepare the route"},
    {"key": "T3", "title": "Show the tab"}
  ],
  "edges": [
    {"from": "T1", "to": "T3", "kind": "integration"},
    {"from": "T2", "to": "T3", "kind": "integration"}
  ]
}' > "$tmp/approved.json"

approved_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$tmp/approved.json")"
approved_version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$tmp/approved.json")"
pending_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$tmp/pending.json")"

post "/api/plan-drafts/$approved_id/approve" '{}' "$approved_version" >/dev/null

cat <<BANNER

Mise shots: board ready at $origin
  pending proposal: #draft/$pending_id
  approved plan:    #draft/$approved_id
  panel:            $origin/#board

Press Ctrl-C to stop and throw the database away.
BANNER

wait "$server_pid"
