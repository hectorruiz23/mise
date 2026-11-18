#!/usr/bin/env python3
"""End-to-end smoke test for the Mise MCP server against a throwaway local API.

Speaks JSON-RPC over stdio to mise_mcp.py, the way a client would, and checks the contract:
handshake, ten tools, the board loads, one card resolves, and a real delivery with structured
evidence reaches the API. It builds and starts its own instance under a temporary database, so
it never touches the owner's board.

    python3 mcp/smoke.py            # exits 0 on success, 1 with the reason on failure
"""

import json
import os
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
SERVER = os.path.join(HERE, "mise_mcp.py")
REPO_ROOT = os.path.dirname(HERE)
API_DIR = os.path.join(REPO_ROOT, "api")
FAILS = []

def check(ok: bool, label: str, detail: str = "") -> None:
    print(f'{"PASS" if ok else "FAIL"}  {label}{"" if ok else "  " + detail}')
    if not ok:
        FAILS.append(label)

def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])

def build_binary(tmp: str) -> str:
    configured = os.environ.get("MISE_SMOKE_BINARY", "").strip()
    if configured:
        if not os.path.isfile(configured):
            raise RuntimeError(f"MISE_SMOKE_BINARY does not exist: {configured}")
        return configured
    if not shutil.which("go"):
        raise RuntimeError("go is not on PATH and MISE_SMOKE_BINARY is unset")
    binary = os.path.join(tmp, "mise")
    result = subprocess.run(["go", "build", "-o", binary, "./cmd/mise"], cwd=API_DIR,
                            text=True, capture_output=True)
    if result.returncode != 0:
        raise RuntimeError(f"go build failed: {result.stderr.strip()[:600]}")
    return binary

class Board:
    def __init__(self, origin: str):
        self.origin = origin

    def call(self, method: str, path: str, token: str = "", body=None, headers=None):
        data = None
        request_headers = {"Origin": self.origin, "Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode()
            request_headers["Content-Type"] = "application/json"
        if token:
            request_headers["Authorization"] = f"Bearer {token}"
        request_headers.update(headers or {})
        request = urllib.request.Request(self.origin + path, data=data, method=method,
                                         headers=request_headers)
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                raw = response.read()
                return response.status, (json.loads(raw) if raw else {})
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", "replace")
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = {"error": f"http_{exc.code}", "detail": raw[:300]}
            return exc.code, payload

def db_execute(path: str, sql: str, params=()) -> None:
    for attempt in range(20):
        connection = None
        try:
            connection = sqlite3.connect(path, timeout=5)
            connection.execute("PRAGMA busy_timeout = 5000")
            connection.execute(sql, params)
            connection.commit()
            return
        except sqlite3.OperationalError as exc:
            if "locked" not in str(exc) or attempt == 19:
                raise
            time.sleep(0.1)
        finally:
            if connection is not None:
                connection.close()

def db_scalar(path: str, sql: str, params=()):
    connection = sqlite3.connect(path, timeout=5)
    try:
        connection.execute("PRAGMA busy_timeout = 5000")
        row = connection.execute(sql, params).fetchone()
        return row[0] if row else None
    finally:
        connection.close()

def start_server(binary: str, origin: str, db_path: str, log_path: str):
    env = dict(os.environ)
    env.update({
        "MISE_ADDR": origin.split("//", 1)[1],
        "MISE_DB": db_path,
        "MISE_PUBLIC_ORIGIN": origin,
        "MISE_LOG_LEVEL": "warn",
    })
    log = open(log_path, "w", encoding="utf-8")
    process = subprocess.Popen([binary], env=env, stdout=log, stderr=subprocess.STDOUT)
    try:
        deadline = time.time() + 15
        while time.time() < deadline:
            if process.poll() is not None:
                log.close()
                raise RuntimeError(f"API exited early:\n{open(log_path, encoding='utf-8').read()[-1200:]}")
            try:
                with urllib.request.urlopen(origin + "/api/health", timeout=1) as response:
                    if response.status == 200:
                        return process, log
            except (urllib.error.URLError, OSError, ValueError):
                pass
            time.sleep(0.1)
        log.close()
        raise RuntimeError(f"API did not become healthy:\n{open(log_path, encoding='utf-8').read()[-1200:]}")
    except Exception:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
        raise

def signup(board: Board, email: str):
    status, payload = board.call("POST", "/api/signup-requests", body={"email": email})
    if status != 201:
        raise RuntimeError(f"signup {email}: HTTP {status} {payload}")
    return int(payload["id"]), payload["secret"]

def claim(board: Board, request_id: int, secret: str, db_path: str) -> str:
    db_execute(db_path,
               "UPDATE signup_requests SET approved_at = strftime('%s','now') "
               "WHERE id = ? AND approved_at IS NULL", (request_id,))
    status, payload = board.call("POST", f"/api/signup-requests/{request_id}/claim",
                                 body={"secret": secret})
    if status != 201:
        raise RuntimeError(f"claim {request_id}: HTTP {status} {payload}")
    return payload["token"], int(payload["user_id"])

def provision(board: Board, db_path: str):
    human_id, human_secret = signup(board, f"smoke-human-{uuid.uuid4()}@example.com")
    human_token, _ = claim(board, human_id, human_secret, db_path)

    agent_id, agent_secret = signup(board, f"smoke-agent-{uuid.uuid4()}@example.com")
    agent_token, agent_user_id = claim(board, agent_id, agent_secret, db_path)
    db_execute(db_path, "UPDATE users SET kind = 'agent' WHERE id = ?", (agent_user_id,))
    return human_token, agent_token

class Mcp:
    def __init__(self, origin: str, token: str):
        env = dict(os.environ)
        env.update({"MISE_URL": origin, "MISE_TOKEN": token})
        self.process = subprocess.Popen([sys.executable, SERVER], stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                        text=True, env=env)
        self.counter = {"id": 0}

    def call(self, method: str, params=None) -> dict:
        self.counter["id"] += 1
        frame = {"jsonrpc": "2.0", "id": self.counter["id"], "method": method}
        if params is not None:
            frame["params"] = params
        self.process.stdin.write(json.dumps(frame) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            stderr = self.process.stderr.read()[:500] if self.process.stderr else ""
            raise RuntimeError(f"MCP server closed the pipe (stderr: {stderr})")
        return json.loads(line)

    def tool(self, name: str, **arguments):
        result = self.call("tools/call", {"name": name, "arguments": arguments})["result"]
        text = result["content"][0]["text"]
        return result.get("isError", False), (text if result.get("isError") else json.loads(text))

    def close(self) -> None:
        try:
            self.process.stdin.close()
        except OSError:
            pass
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)

def run() -> int:
    tmp = tempfile.mkdtemp(prefix="mise-smoke-")
    server = None
    log = None
    mcp = None
    try:
        binary = build_binary(tmp)
        port = free_port()
        origin = f"http://127.0.0.1:{port}"
        db_path = os.path.join(tmp, "mise.db")
        log_path = os.path.join(tmp, "server.log")

        server, log = start_server(binary, origin, db_path, log_path)
        check(True, "local API answers by itself")

        board = Board(origin)
        human_token, agent_token = provision(board, db_path)
        check(True, "human and agent signup completed on the local API")

        status, task = board.call("POST", "/api/tasks", token=human_token,
                                  body={"project": "MI", "title": "MCP structured delivery"},
                                  headers={"Idempotency-Key": f"smoke-{uuid.uuid4()}"})
        if status != 201:
            raise RuntimeError(f"create task: HTTP {status} {task}")
        status, task = board.call("PATCH", f"/api/tasks/{task['key']}", token=human_token,
                                  body={"state": "ready"}, headers={"If-Match": '"1"'})
        if status != 200:
            raise RuntimeError(f"move to ready: HTTP {status} {task}")
        status, lease = board.call("POST", f"/api/tasks/{task['key']}/leases",
                                   token=agent_token, body={})
        if status != 201:
            raise RuntimeError(f"acquire lease: HTTP {status} {lease}")

        mcp = Mcp(origin, agent_token)
        init = mcp.call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                       "clientInfo": {"name": "smoke", "version": "1"}})["result"]
        check(init["serverInfo"]["name"] == "mise", "handshake names the server mise")
        check(bool(init.get("instructions")), "handshake carries the agent contract")

        mcp.process.stdin.write(json.dumps({"jsonrpc": "2.0",
                                            "method": "notifications/initialized"}) + "\n")
        mcp.process.stdin.flush()

        tools = mcp.call("tools/list")["result"]["tools"]
        names = [tool["name"] for tool in tools]
        check(len(names) == 10, "ten tools listed", f"got {len(names)}: {names}")
        for forbidden in ("approve", "delete_task", "create_project"):
            check(forbidden not in names, f"no {forbidden} tool (the API refuses it for agents)")
        check(all(tool.get("inputSchema", {}).get("type") == "object" for tool in tools),
              "every tool declares a JSON schema")

        deliver_spec = next((tool for tool in tools if tool["name"] == "deliver"), None)
        check(deliver_spec is not None and
              "evidence_items" in deliver_spec["inputSchema"]["properties"],
              "deliver advertises evidence_items")

        errored, overview = mcp.tool("board_overview")
        check(not errored, "board_overview answers", str(overview)[:200] if errored else "")
        if not errored:
            states = {"backlog", "ready", "in_progress", "blocked", "review", "done"}
            check(set(overview.get("columns", {})) == states, "the board has the six columns",
                  str(sorted(overview.get("columns", {}))))
            check(set(overview.get("counts", {})) == states, "the board counts every column",
                  str(sorted(overview.get("counts", {}))))

        errored, listing = mcp.tool("list_tasks", limit=1)
        check(not errored and "tasks" in listing, "list_tasks answers", str(listing)[:200])
        if not errored and listing["tasks"]:
            key = listing["tasks"][0]["key"]
            errored, detail = mcp.tool("get_task", key=key)
            check(not errored and detail["task"]["key"] == key, f"get_task resolves {key}",
                  str(detail)[:200])

        wanted = [{"label": "tests", "raw": "go test ./..."},
                  {"label": "build", "raw": "make accept"}]
        errored, delivered = mcp.tool("deliver", key=task["key"],
                                      summary="Structured evidence through MCP",
                                      evidence_items=wanted)
        check(not errored, "deliver with evidence_items answers 201", str(delivered)[:300] if errored else "")
        if not errored:
            delivery = delivered["delivery"]
            raws = [item["raw"] for item in wanted]
            check(delivery["evidence"] == raws, "delivery carries evidence in order",
                  str(delivery.get("evidence")))
            check([item["raw"] for item in delivery["evidence_items"]] == raws,
                  "delivery carries evidence_items in order", str(delivery.get("evidence_items")))
            check([item["label"] for item in delivery["evidence_items"]] == ["tests", "build"],
                  "delivery keeps the labels", str(delivery.get("evidence_items")))
            check(delivered["task"]["state"] == "review", "delivery moves the card to review",
                  delivered["task"]["state"])

            status, detail = board.call("GET", f"/api/tasks/{task['key']}", token=human_token)
            check(status == 200 and len(detail.get("deliveries", [])) == 1,
                  "task detail exposes the delivery", f"HTTP {status}")
            if status == 200 and detail.get("deliveries"):
                stored = detail["deliveries"][0]
                check(stored["evidence"] == raws and
                      [item["raw"] for item in stored["evidence_items"]] == raws,
                      "task detail keeps both representations in order", str(stored)[:300])

            status, listing = board.call("GET", f"/api/tasks/{task['key']}/deliveries",
                                         token=human_token)
            check(status == 200 and len(listing) == 1 and
                  listing[0]["evidence"] == raws and
                  [item["raw"] for item in listing[0]["evidence_items"]] == raws,
                  "deliveries listing keeps both representations", f"HTTP {status}")

            raw = db_scalar(db_path, "SELECT evidence FROM deliveries ORDER BY id DESC LIMIT 1")
            stored_items = json.loads(raw) if raw else None
            check(isinstance(stored_items, list) and
                  isinstance(stored_items[0], dict) and
                  stored_items[0].get("label") == "tests",
                  "the database stores {label, raw} objects", str(raw)[:300])

        errored, text = mcp.tool("move_task", key="XX-0", state="done")
        check(errored and "not_found" in text, "unknown card answers not_found", text[:120])
    finally:
        if mcp is not None:
            mcp.close()
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)
        if log is not None:
            log.close()
        shutil.rmtree(tmp, ignore_errors=True)

    print()
    if FAILS:
        print(f"{len(FAILS)} check(s) failed")
        return 1
    print("all checks passed")
    return 0

def main() -> int:
    try:
        return run()
    except Exception as exc:
        print(f"FAIL  {exc}")
        return 1

if __name__ == "__main__":
    sys.exit(main())
