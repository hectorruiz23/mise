#!/usr/bin/env python3
"""Read-only smoke test for the Mise MCP server.

Speaks JSON-RPC over stdio to mise_mcp.py, the way a client would, and checks the contract:
handshake, ten tools, the board loads and one card resolves. It never writes, so it is safe to
run against the real board at any time.

    python3 mcp/smoke.py            # exits 0 on success, 1 with the reason on failure
"""

import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SERVER = os.path.join(HERE, "mise_mcp.py")
FAILS = []

def check(ok: bool, label: str, detail: str = "") -> None:
    print(f'{"PASS" if ok else "FAIL"}  {label}{"" if ok else "  " + detail}')
    if not ok:
        FAILS.append(label)

def main() -> int:
    env = dict(os.environ)
    if not env.get("MISE_TOKEN"):
        for candidate in ("~/.config/mise/agent-token", "~/.config/mise/token"):
            path = os.path.expanduser(candidate)
            if os.path.isfile(path):
                env["MISE_TOKEN"] = open(path).read().strip()
                break

    proc = subprocess.Popen([sys.executable, SERVER], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, env=env)
    stdin, stdout = proc.stdin, proc.stdout
    assert stdin is not None and stdout is not None and proc.stderr is not None
    counter = {"id": 0}

    def call(method: str, params=None) -> dict:
        counter["id"] += 1
        frame = {"jsonrpc": "2.0", "id": counter["id"], "method": method}
        if params is not None:
            frame["params"] = params
        stdin.write(json.dumps(frame) + "\n")
        stdin.flush()
        line = stdout.readline()
        if not line:
            raise RuntimeError(f"server closed the pipe (stderr: {proc.stderr.read()[:300]})")
        return json.loads(line)

    def tool(name: str, **arguments):
        result = call("tools/call", {"name": name, "arguments": arguments})["result"]
        text = result["content"][0]["text"]
        return result.get("isError", False), (text if result.get("isError") else json.loads(text))

    init = call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                              "clientInfo": {"name": "smoke", "version": "1"}})["result"]
    check(init["serverInfo"]["name"] == "mise", "handshake names the server mise")
    check(bool(init.get("instructions")), "handshake carries the agent contract")

    stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
    stdin.flush()

    tools = call("tools/list")["result"]["tools"]
    names = [t["name"] for t in tools]
    check(len(names) == 10, "ten tools listed", f"got {len(names)}: {names}")
    for forbidden in ("approve", "delete_task", "create_project"):
        check(forbidden not in names, f"no {forbidden} tool (the API refuses it for agents)")
    check(all(t.get("inputSchema", {}).get("type") == "object" for t in tools),
          "every tool declares a JSON schema")

    errored, board = tool("board_overview")
    check(not errored, "board_overview answers", str(board)[:200] if errored else "")
    if not errored:
        states = {"backlog", "ready", "in_progress", "blocked", "review", "done"}
        check(set(board.get("columns", {})) == states, "the board has the six columns",
              str(sorted(board.get("columns", {}))))
        check(set(board.get("counts", {})) == states, "the board counts every column",
              str(sorted(board.get("counts", {}))))

        errored, listing = tool("list_tasks", limit=1)
        check(not errored and "tasks" in listing, "list_tasks answers", str(listing)[:200])
        if not errored and listing["tasks"]:
            key = listing["tasks"][0]["key"]
            errored, detail = tool("get_task", key=key)
            check(not errored and detail["task"]["key"] == key, f"get_task resolves {key}",
                  str(detail)[:200])

    errored, text = tool("move_task", key="XX-0", state="done")
    check(errored and "not_found" in text, "unknown card answers not_found", text[:120])

    stdin.close()
    proc.wait(timeout=5)

    print()
    if FAILS:
        print(f"{len(FAILS)} check(s) failed")
        return 1
    print("all checks passed")
    return 0

if __name__ == "__main__":
    sys.exit(main())
