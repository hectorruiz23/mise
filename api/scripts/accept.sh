#!/usr/bin/env bash

set -uo pipefail

api_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) || exit 1

if ! command -v go >/dev/null 2>&1; then
    echo "accept: go is not on PATH; put Go 1.27.4 or newer there (or export PATH) and retry" >&2
    exit 1
fi
GOTOOLCHAIN=${GOTOOLCHAIN:-local}
export GOTOOLCHAIN

tmp=$(mktemp -d "${TMPDIR:-/tmp}/mise-accept.XXXXXX" 2>/dev/null) \
    || tmp=$(mktemp -d /tmp/mise-accept.XXXXXX 2>/dev/null) \
    || {
        echo "cannot create a temporary directory" >&2
        exit 1
    }
server_pid=""
mkdir -p "$tmp/tmp" "$tmp/gocache" "$tmp/gotmp" || exit 1
TMPDIR="$tmp/tmp"
GOCACHE="$tmp/gocache"
GOTMPDIR="$tmp/gotmp"
export TMPDIR GOCACHE GOTMPDIR
db="$tmp/mise.db"
server_log="$tmp/server.log"
resp_body_file="$tmp/resp.body"
resp_headers="$tmp/resp.headers"

cleanup() {
    if [ -n "${server_pid:-}" ] && kill -0 "$server_pid" 2>/dev/null; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf "$tmp"
}
trap cleanup EXIT

status=""
body=""
check_num=0
check_name=""
passed=0

http() {
    local method="$1" path="$2"
    shift 2
    : >"$resp_headers"
    local curl_err="$tmp/curl.err"
    if ! status=$(curl -sS --max-time 20 -o "$resp_body_file" -D "$resp_headers" \
        -w '%{http_code}' -X "$method" "$origin$path" "$@" 2>"$curl_err"); then
        status="000"
        body="curl failed: $(cat "$curl_err")"
        printf '%s' "$body" >"$resp_body_file"
        return 1
    fi
    body=$(cat "$resp_body_file")
    return 0
}

header_value() {
    grep -i "^$1:" "$resp_headers" | head -n 1 | cut -d: -f2- \
        | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

json_value() {
    python3 -c 'import json, sys
d = json.load(open(sys.argv[1]))
print(eval(sys.argv[2], {"d": d}))' "$resp_body_file" "$1"
}

check_start() {
    check_num="$1"
    check_name="$2"
}

pass_check() {
    passed=$((passed + 1))
    echo "PASS  check $check_num  $check_name  ($1)"
}

fail_check() {
    echo "FAIL  check $check_num  $check_name  ($1)"
    echo "HTTP status: ${status:-none}"
    echo "----- response body -----"
    if [ -f "$resp_body_file" ]; then
        cat "$resp_body_file"
    else
        echo "(no response body)"
    fi
    echo
    echo "----- server log (tail) -----"
    if [ -f "$server_log" ]; then
        tail -n 25 "$server_log"
    else
        echo "(no server log)"
    fi
    exit 1
}

fatal() {
    echo "FAIL  $1" >&2
    exit 1
}

echo "Mise acceptance: building binary"
if ! (cd "$api_dir" && CGO_ENABLED=0 go build -o "$tmp/mise" ./cmd/mise); then
    fatal "build failed"
fi

port=$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()') || fatal "cannot allocate a free TCP port"
origin="http://127.0.0.1:$port"

echo "Mise acceptance: starting server on $origin"
MISE_ADDR="127.0.0.1:$port" \
    MISE_DB="$db" \
    MISE_PUBLIC_ORIGIN="$origin" \
    MISE_LOG_LEVEL=info \
    "$tmp/mise" >"$server_log" 2>&1 &
server_pid=$!

ready=0
deadline=$((SECONDS + 10))
while [ "$SECONDS" -lt "$deadline" ]; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
        break
    fi
    if code=$(curl -sS --max-time 1 -o "$tmp/health.body" -w '%{http_code}' \
        "$origin/api/health" 2>/dev/null); then
        if [ "$code" = "200" ]; then
            ready=1
            break
        fi
    fi
    sleep 0.1
done
if [ "$ready" -ne 1 ]; then
    echo "FAIL  server did not become healthy within 10 seconds" >&2
    echo "----- server log -----" >&2
    cat "$server_log" >&2 || true
    exit 1
fi

human_token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
agent_token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
human_hash=$(printf '%s' "$human_token" | python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')
agent_hash=$(printf '%s' "$agent_token" | python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')

if ! sqlite3 -bail "$db" <<SQL
.timeout 5000
INSERT INTO users (email, name, kind, token_hash, active, created_at)
VALUES ('human@example.com', 'Human', 'human', '$human_hash', 1, strftime('%s','now'));
INSERT INTO users (email, name, kind, token_hash, active, created_at)
VALUES ('agent@example.com', 'Agent', 'agent', '$agent_hash', 1, strftime('%s','now'));
SQL
then
    fatal "operator provisioning failed"
fi

human_auth="Authorization: Bearer $human_token"
agent_auth="Authorization: Bearer $agent_token"
json_ct="Content-Type: application/json"
origin_h="Origin: $origin"
task_key=""
task_version=""
lease_id=""

check_start 1 "GET /api/health"
http GET /api/health || fail_check "curl failure"
if [ "$status" = "200" ] && [ "$body" = '{"ok":true,"db":"ok"}' ]; then
    pass_check "200 $body"
else
    fail_check "status=$status body=$body"
fi

check_start 2 "GET /api/me without credential"
http GET /api/me || fail_check "curl failure"
error_code=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$status" = "401" ] && [ "$error_code" = "unauthorized" ]; then
    pass_check "401 envelope error=unauthorized"
else
    fail_check "status=$status body=$body"
fi

check_start 3 "POST /api/tasks without Idempotency-Key"
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    --data-binary '{"project":"MI","title":"First task"}' || fail_check "curl failure"
if [ "$status" = "428" ]; then
    pass_check "428"
else
    fail_check "status=$status body=$body"
fi

check_start 4 "POST /api/tasks with Idempotency-Key"
create_body='{"project":"MI","title":"First task"}'
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    -H 'Idempotency-Key: create-first' --data-binary "$create_body" || fail_check "curl failure"
location=$(header_value Location)
task_version=$(header_value X-Task-Version)
task_key=$(json_value 'd["key"]' 2>/dev/null || true)
if [ "$status" = "201" ] && [ "$location" = "/api/tasks/MI-1" ] \
    && [ "$task_version" = "1" ] && [ "$task_key" = "MI-1" ]; then
    pass_check "201 Location=$location X-Task-Version=$task_version"
else
    fail_check "status=$status Location=$location X-Task-Version=$task_version body=$body"
fi

check_start 5 "Idempotent replay of create"
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    -H 'Idempotency-Key: create-first' --data-binary "$create_body" || fail_check "curl failure"
replay_key=$(json_value 'd["key"]' 2>/dev/null || true)
task_count=$(sqlite3 "$db" 'select count(*) from tasks;' 2>/dev/null || true)
if [ "$status" = "201" ] && [ "$replay_key" = "$task_key" ] && [ "$task_count" = "1" ]; then
    pass_check "201 same key $replay_key, sqlite tasks=$task_count"
else
    fail_check "status=$status key=$replay_key tasks=$task_count body=$body"
fi

check_start 6 "Idempotency key reused with a different body"
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    -H 'Idempotency-Key: create-first' \
    --data-binary '{"project":"MI","title":"Different body"}' || fail_check "curl failure"
error_code=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$status" = "409" ] && [ "$error_code" = "idempotency_conflict" ]; then
    pass_check "409 idempotency_conflict"
else
    fail_check "status=$status body=$body"
fi

check_start 7 "PATCH preconditions"
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    --data-binary '{"title":"No precondition"}' || fail_check "curl failure"
missing_status=$status
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    -H 'If-Match: "99"' --data-binary '{"title":"Stale write"}' || fail_check "curl failure"
stale_status=$status
http GET "/api/tasks/$task_key" -H "$human_auth" || fail_check "curl failure"
title=$(json_value 'd["task"]["title"]' 2>/dev/null || true)
if [ "$missing_status" = "428" ] && [ "$stale_status" = "412" ] && [ "$title" = "First task" ]; then
    pass_check "no If-Match=428, stale=412, title unchanged"
else
    fail_check "missing=$missing_status stale=$stale_status title=$title"
fi

check_start 8 "PATCH ready with the current version"
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    -H 'If-Match: "1"' --data-binary '{"state":"ready"}' || fail_check "curl failure"
version_header=$(header_value X-Task-Version)
state=$(json_value 'd["state"]' 2>/dev/null || true)
task_version=$(json_value 'd["version"]' 2>/dev/null || true)
if [ "$status" = "200" ] && [ "$version_header" = "2" ] \
    && [ "$state" = "ready" ] && [ "$task_version" = "2" ]; then
    pass_check "200 state=ready X-Task-Version=$version_header"
else
    fail_check "status=$status state=$state header=$version_header body=$body"
fi

check_start 9 "Agent lease acquisition and conflict"
http POST "/api/tasks/$task_key/leases" -H "$agent_auth" -H "$json_ct" \
    --data-binary '{}' || fail_check "curl failure"
lease_id=$(json_value 'd["id"]' 2>/dev/null || true)
lease_agent=$(json_value 'd["agent"]["name"]' 2>/dev/null || true)
first_lease_status=$status
http POST "/api/tasks/$task_key/leases" -H "$agent_auth" -H "$json_ct" \
    --data-binary '{}' || fail_check "curl failure"
second_lease_status=$status
error_code=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$first_lease_status" = "201" ] && [ "$lease_agent" = "Agent" ] \
    && [ "$second_lease_status" = "409" ] && [ "$error_code" = "lease_conflict" ]; then
    pass_check "201 lease id=$lease_id, second=409 lease_conflict"
else
    fail_check "first=$first_lease_status second=$second_lease_status body=$body"
fi

check_start 10 "Agent delivery evidence validation"
http POST "/api/tasks/$task_key/deliveries" -H "$agent_auth" -H "$json_ct" \
    --data-binary "{\"lease_id\":$lease_id,\"summary\":\"First delivery\",\"evidence\":[]}" \
    || fail_check "curl failure"
empty_evidence_status=$status
valid_body=$(printf '{"lease_id":%s,"summary":"First delivery","evidence":["go test ./...","make accept"]}' "$lease_id")
http POST "/api/tasks/$task_key/deliveries" -H "$agent_auth" -H "$json_ct" \
    --data-binary "$valid_body" || fail_check "curl failure"
delivery_state=$(json_value 'd["task"]["state"]' 2>/dev/null || true)
delivery_version=$(json_value 'd["task"]["version"]' 2>/dev/null || true)
evidence_count=$(json_value 'len(d["delivery"]["evidence"])' 2>/dev/null || true)
if [ "$empty_evidence_status" = "400" ] && [ "$status" = "201" ] \
    && [ "$delivery_state" = "review" ] && [ "$evidence_count" = "2" ]; then
    task_version=$delivery_version
    pass_check "400 empty evidence, 201 review v$delivery_version with 2 evidence entries"
else
    fail_check "empty=$empty_evidence_status status=$status state=$delivery_state body=$body"
fi

check_start 11 "Human close and duplicate close"
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    -H "If-Match: \"$task_version\"" --data-binary '{"state":"done"}' || fail_check "curl failure"
close_status=$status
closed_state=$(json_value 'd["state"]' 2>/dev/null || true)
closed_version=$(json_value 'd["version"]' 2>/dev/null || true)
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    -H "If-Match: \"$closed_version\"" --data-binary '{"state":"done"}' || fail_check "curl failure"
second_close_status=$status
error_code=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$close_status" = "200" ] && [ "$closed_state" = "done" ] \
    && [ "$second_close_status" = "409" ] && [ "$error_code" = "invalid_transition" ]; then
    task_version=$closed_version
    pass_check "200 done v$closed_version, second=409 invalid_transition"
else
    fail_check "close=$close_status state=$closed_state second=$second_close_status body=$body"
fi

check_start 12 "Task detail actor and delivery"
http GET "/api/tasks/$task_key" -H "$human_auth" || fail_check "curl failure"
detail_ok=$(json_value '(len(d["deliveries"]) == 1 and d["deliveries"][0]["actor"]["id"] > 0 and d["deliveries"][0]["actor"]["name"] == "Agent" and d["deliveries"][0]["actor"]["kind"] == "agent")' 2>/dev/null || true)
detail_count=$(json_value 'len(d["deliveries"])' 2>/dev/null || true)
if [ "$status" = "200" ] && [ "$detail_ok" = "True" ] && [ "$detail_count" = "1" ]; then
    pass_check "200 deliveries=1 actor=Agent/agent"
else
    fail_check "status=$status deliveries=$detail_count actor_ok=$detail_ok body=$body"
fi

check_start 13 "Agent field edit forbidden and blocked transition allowed"
http PATCH "/api/tasks/$task_key" -H "$agent_auth" -H "$json_ct" \
    -H "If-Match: \"$task_version\"" --data-binary '{"title":"Agent edit"}' \
    || fail_check "curl failure"
agent_title_status=$status
http PATCH "/api/tasks/$task_key" -H "$human_auth" -H "$json_ct" \
    -H "If-Match: \"$task_version\"" --data-binary '{"state":"ready"}' \
    || fail_check "curl failure"
reopen_status=$status
reopen_version=$(json_value 'd["version"]' 2>/dev/null || true)
http POST "/api/tasks/$task_key/leases" -H "$agent_auth" -H "$json_ct" \
    --data-binary '{}' || fail_check "curl failure"
reacquire_status=$status
reacquire_version=$(header_value X-Task-Version)
http PATCH "/api/tasks/$task_key" -H "$agent_auth" -H "$json_ct" \
    -H "If-Match: \"$reacquire_version\"" \
    --data-binary '{"state":"blocked","reason":"x"}' || fail_check "curl failure"
blocked_status=$status
blocked_state=$(json_value 'd["state"]' 2>/dev/null || true)
blocked_version=$(json_value 'd["version"]' 2>/dev/null || true)
if [ "$agent_title_status" = "403" ] && [ "$reopen_status" = "200" ] \
    && [ "$reopen_version" = "6" ] && [ "$reacquire_status" = "201" ] \
    && [ "$blocked_status" = "200" ] && [ "$blocked_state" = "blocked" ]; then
    pass_check "title=403, reopen=200, lease=201, blocked=200 v$blocked_version"
else
    fail_check "title=$agent_title_status reopen=$reopen_status/$reopen_version lease=$reacquire_status blocked=$blocked_status/$blocked_state body=$body"
fi

check_start 14 "Signup request lifecycle"
signup_email="newhuman@example.com"
http POST /api/signup-requests -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"email\":\"$signup_email\"}" || fail_check "curl failure"
signup_status_code=$status
signup_id=$(json_value 'd["id"]' 2>/dev/null || true)
signup_secret=$(json_value 'd["secret"]' 2>/dev/null || true)
http GET "/api/signup-requests/$signup_id?secret=$signup_secret" || fail_check "curl failure"
pending_state=$(json_value 'd["status"]' 2>/dev/null || true)
http POST "/api/signup-requests/$signup_id/claim" -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"secret\":\"$signup_secret\"}" || fail_check "curl failure"
claim_before_status=$status
claim_before_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
sqlite3 -bail "$db" \
    "UPDATE signup_requests SET approved_at = strftime('%s','now') WHERE id = $signup_id AND approved_at IS NULL;" \
    || fatal "signup approval failed"
http POST "/api/signup-requests/$signup_id/claim" -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"secret\":\"$signup_secret\"}" || fail_check "curl failure"
claim_after_status=$status
claimed_user=$(json_value 'd["user_id"]' 2>/dev/null || true)
claimed_token=$(json_value 'd["token"]' 2>/dev/null || true)
http POST "/api/signup-requests/$signup_id/claim" -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"secret\":\"$signup_secret\"}" || fail_check "curl failure"
second_claim_status=$status
second_claim_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
http POST "/api/signup-requests/$signup_id/claim" -H "$origin_h" -H "$json_ct" \
    --data-binary '{"secret":"definitely-wrong"}' || fail_check "curl failure"
wrong_secret_status=$status
wrong_secret_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$signup_status_code" = "201" ] && [ "$pending_state" = "pending" ] \
    && [ "$claim_before_status" = "409" ] && [ "$claim_before_error" = "signup_pending" ] \
    && [ "$claim_after_status" = "201" ] && [ -n "$claimed_user" ] && [ -n "$claimed_token" ] \
    && [ "$second_claim_status" = "409" ] && [ "$second_claim_error" = "signup_consumed" ] \
    && [ "$wrong_secret_status" = "404" ] && [ "$wrong_secret_error" = "not_found" ]; then
    pass_check "201, pending, claim 409, approve, claim 201, second 409, wrong secret 404"
else
    fail_check "request=$signup_status_code status=$pending_state before=$claim_before_status after=$claim_after_status second=$second_claim_status wrong=$wrong_secret_status body=$body"
fi

check_start 15 "Login sets a session cookie and authenticates /api/me"
http POST /api/sessions -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"token\":\"$human_token\"}" || fail_check "curl failure"
login_status=$status
cookie_line=$(grep -i '^Set-Cookie: mise_session=' "$resp_headers" | head -n 1 | tr -d '\r')
session_cookie=$(printf '%s' "$cookie_line" | sed -E 's/^[^=]*=//; s/;.*$//')
cookie_ok=0
if printf '%s' "$cookie_line" | grep -qi 'HttpOnly' \
    && printf '%s' "$cookie_line" | grep -qi 'SameSite=Lax' \
    && printf '%s' "$cookie_line" | grep -qi 'Max-Age=2592000'; then
    cookie_ok=1
fi
http GET /api/me -H "Cookie: mise_session=$session_cookie" || fail_check "curl failure"
me_status=$status
me_name=$(json_value 'd["name"]' 2>/dev/null || true)
me_kind=$(json_value 'd["kind"]' 2>/dev/null || true)
if [ "$login_status" = "201" ] && [ "$cookie_ok" = "1" ] \
    && [ "$me_status" = "200" ] && [ "$me_name" = "Human" ] && [ "$me_kind" = "human" ]; then
    pass_check "201 cookie HttpOnly/SameSite=Lax/Max-Age=2592000, me=200"
else
    fail_check "login=$login_status cookie_ok=$cookie_ok me=$me_status name=$me_name kind=$me_kind"
fi

check_start 16 "Logout revokes the cookie"
http DELETE /api/sessions/current -H "Cookie: mise_session=$session_cookie" \
    -H "$origin_h" || fail_check "curl failure"
logout_status=$status
http GET /api/me -H "Cookie: mise_session=$session_cookie" || fail_check "curl failure"
revoked_status=$status
if [ "$logout_status" = "204" ] && [ "$revoked_status" = "401" ]; then
    pass_check "204, old cookie then 401"
else
    fail_check "logout=$logout_status old-cookie=$revoked_status body=$body"
fi

check_start 17 "Cookie mutation with a foreign Origin"
http POST /api/sessions -H "$origin_h" -H "$json_ct" \
    --data-binary "{\"token\":\"$human_token\"}" || fail_check "curl failure"
fresh_cookie=$(grep -i '^Set-Cookie: mise_session=' "$resp_headers" | head -n 1 | tr -d '\r' \
    | sed -E 's/^[^=]*=//; s/;.*$//')
http DELETE /api/sessions/current -H "Cookie: mise_session=$fresh_cookie" \
    -H 'Origin: http://evil.example' || fail_check "curl failure"
evil_status=$status
evil_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$evil_status" = "403" ] && [ "$evil_error" = "forbidden" ]; then
    pass_check "403 forbidden"
else
    fail_check "status=$evil_status body=$body"
fi

check_start 18 "Pagination cursor is bound to its filter"
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    -H 'Idempotency-Key: page-two' \
    --data-binary '{"project":"MI","title":"Pagination two"}' || fail_check "curl failure"
if [ "$status" != "201" ]; then
    fail_check "second task status=$status body=$body"
fi
http POST /api/tasks -H "$human_auth" -H "$json_ct" \
    -H 'Idempotency-Key: page-three' \
    --data-binary '{"project":"MI","title":"Pagination three"}' || fail_check "curl failure"
if [ "$status" != "201" ]; then
    fail_check "third task status=$status body=$body"
fi
http GET '/api/tasks?limit=1' -H "$human_auth" || fail_check "curl failure"
page_one_count=$(json_value 'len(d["tasks"])' 2>/dev/null || true)
first_cursor=$(json_value 'd["next_cursor"]' 2>/dev/null || true)
http GET "/api/tasks?limit=1&cursor=$first_cursor" -H "$human_auth" || fail_check "curl failure"
page_two_count=$(json_value 'len(d["tasks"])' 2>/dev/null || true)
http GET "/api/tasks?limit=1&state=backlog&cursor=$first_cursor" -H "$human_auth" \
    || fail_check "curl failure"
mismatch_status=$status
mismatch_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$page_one_count" = "1" ] && [ -n "$first_cursor" ] \
    && [ "$page_two_count" = "1" ] \
    && [ "$mismatch_status" = "400" ] && [ "$mismatch_error" = "invalid_input" ]; then
    pass_check "page1=1+cursor, page2=1, cursor with different filter=400"
else
    fail_check "page1=$page_one_count cursor=${first_cursor:0:12}... page2=$page_two_count mismatch=$mismatch_status"
fi

check_start 19 "Oversized request body"
python3 -c 'import sys
open(sys.argv[1], "wb").write(b"x" * (2 * 1024 * 1024))' "$tmp/big.bin" \
    || fatal "cannot create the oversized body"
http POST /api/tasks -H "$human_auth" -H "$json_ct" -H 'Expect:' \
    -H 'Idempotency-Key: oversized' --data-binary "@$tmp/big.bin" || fail_check "curl failure"
large_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
if [ "$status" = "413" ] && [ "$large_error" = "payload_too_large" ]; then
    pass_check "413 payload_too_large"
else
    fail_check "status=$status body=$body"
fi

check_start 20 "Unknown API route and the panel fallback"
http GET /api/nope || fail_check "curl failure"
not_found_error=$(json_value 'd.get("error", "")' 2>/dev/null || true)
not_found_detail=$(json_value 'd.get("detail", "")' 2>/dev/null || true)
if [ "$status" != "404" ] || [ -z "$not_found_error" ] || [ -z "$not_found_detail" ]; then
    fail_check "api 404 status=$status body=$body"
fi
if [ -f "$api_dir/internal/panel/dist/index.html" ]; then
    http GET / || fail_check "curl failure"
    content_type=$(header_value Content-Type)
    if [ "$status" = "200" ] && printf '%s' "$content_type" | grep -qi 'text/html'; then
        pass_check "404 JSON envelope; panel 200 text/html"
    else
        fail_check "panel status=$status content-type=$content_type"
    fi
else
    pass_check "404 JSON envelope; panel HTML skipped (internal/panel/dist has no index.html)"
fi

check_start 21 "Login rate limit"
last_status=""
last_retry=""
for ((attempt = 1; attempt <= 11; attempt++)); do
    http POST /api/sessions -H "$origin_h" -H "$json_ct" \
        --data-binary "{\"token\":\"wrong-token-$attempt\"}" || fail_check "curl failure"
    last_status=$status
    last_retry=$(header_value Retry-After)
done
if [ "$last_status" = "429" ] && [ -n "$last_retry" ]; then
    pass_check "attempt 11: 429 Retry-After=$last_retry"
else
    fail_check "attempt 11 status=$last_status Retry-After=$last_retry body=$body"
fi

check_start 22 "Durable state after the server stops"
kill "$server_pid" 2>/dev/null || true
for ((wait_tick = 0; wait_tick < 100; wait_tick++)); do
    if ! kill -0 "$server_pid" 2>/dev/null; then
        break
    fi
    sleep 0.1
done
if kill -0 "$server_pid" 2>/dev/null; then
    kill -9 "$server_pid" 2>/dev/null || true
fi
wait "$server_pid" 2>/dev/null || true
server_pid=""

tasks_count=$(sqlite3 "$db" 'select count(*) from tasks;' 2>/dev/null || true)
deliveries_count=$(sqlite3 "$db" 'select count(*) from deliveries;' 2>/dev/null || true)
movements_count=$(sqlite3 "$db" 'select count(*) from task_movements;' 2>/dev/null || true)
if [ "$tasks_count" = "3" ] && [ "$deliveries_count" = "1" ] && [ "$movements_count" = "10" ]; then
    pass_check "tasks=$tasks_count deliveries=$deliveries_count movements=$movements_count"
else
    fail_check "tasks=$tasks_count deliveries=$deliveries_count movements=$movements_count"
fi

if [ "$passed" -ne 22 ]; then
    echo "FAIL  $passed/22 checks passed" >&2
    exit 1
fi
echo "$passed/22 checks passed"
exit 0
