#!/usr/bin/env bash
set -uo pipefail

api_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) || exit 1
cd "$api_dir" || exit 1

if ! command -v go >/dev/null 2>&1; then
    echo "dev: go is not on PATH; export it and try again" >&2
    exit 1
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/mise-dev.XXXXXX") || exit 1
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
origin="http://127.0.0.1:$port"
db="$tmp/mise.db"
bin="$tmp/mise"
pid=""

cleanup() {
    [ -n "$pid" ] && kill "$pid" 2>/dev/null
    wait 2>/dev/null
    rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

echo "dev: compilando con la identidad de desarrollo (etiqueta dev)"
CGO_ENABLED=0 go build -tags dev -o "$bin" ./cmd/mise || exit 1

echo "dev: base temporal en $db"
MISE_ADDR="127.0.0.1:$port" \
    MISE_DB="$db" \
    MISE_PUBLIC_ORIGIN="$origin" \
    MISE_DEV_USER="${MISE_DEV_USER:-1}" \
    MISE_DEV_NAME="${MISE_DEV_NAME:-dev}" \
    MISE_LOG_LEVEL="${MISE_LOG_LEVEL:-info}" \
    "$bin" &
pid=$!

for _ in $(seq 1 100); do
    curl -sf -o /dev/null "$origin/api/health" && break
    sleep 0.1
done
if ! curl -sf -o /dev/null "$origin/api/health"; then
    echo "dev: el servidor no arrancó" >&2
    exit 1
fi

python3 - "$db" <<'PY'
import hashlib, os, sqlite3, sys, time

db = sqlite3.connect(sys.argv[1])
now = int(time.time())
db.execute("insert or ignore into projects (key, name, next_task_number, created_at) values ('MI', 'Mise', 1, ?)", (now,))
db.execute(
    "insert or ignore into users (id, email, name, kind, token_hash, active, created_at)"
    " values (1, 'dev@localhost', 'dev', 'human', ?, 1, ?)",
    (hashlib.sha256(os.urandom(32)).hexdigest(), now),
)
db.commit()
PY

header_version() {
    awk 'tolower($1) == "x-task-version:" { print $2 }' "$tmp/headers" | tr -d '\r'
}

create() {
    local title=$1 slug=$2
    curl -s -D "$tmp/headers" -o /dev/null -X POST "$origin/api/tasks" \
        -H "Origin: $origin" \
        -H 'Content-Type: application/json' \
        -H "Idempotency-Key: dev-$slug" \
        -d "{\"title\":\"$title\",\"project\":\"MI\",\"size\":\"S\"}"
    header_version
}

move() {
    local key=$1 state=$2 version=$3
    curl -s -D "$tmp/headers" -o /dev/null -X PATCH "$origin/api/tasks/$key" \
        -H "Origin: $origin" \
        -H 'Content-Type: application/json' \
        -H "If-Match: \"$version\"" \
        -d "{\"state\":\"$state\"}"
    header_version
}

echo "dev: sembrando tarjetas"
create "Revisar el login del panel" revisar-login >/dev/null
create "Arreglar la paginación del backlog" paginacion >/dev/null
create "Escribir el runbook del despliegue" runbook >/dev/null
create "Preparar la vista de actividad" actividad >/dev/null
create "Medir el tiempo de arranque" arranque >/dev/null
create "Probar el contenedor en la Pi" contenedor >/dev/null

v4=$(move "MI-4" ready 1)
v5=$(move "MI-5" ready 1)
v4=$(move "MI-4" in_progress "$v4")
v5=$(move "MI-5" in_progress "$v5")
echo "dev: dos tarjetas movidas (v$v4, v$v5)"

echo
echo "  tablero:   $origin"
echo "  sin login: entra sola la identidad de desarrollo (usuario 1, humano)"
echo "  datos:     $db (se borra al salir)"
echo

if [ "${MISE_DEV_OPEN:-1}" = "1" ] && command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$origin" >/dev/null 2>&1 &
fi

wait "$pid"
