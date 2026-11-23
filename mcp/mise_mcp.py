#!/usr/bin/env python3
"""Mise MCP server — lets any agent work the board without reading Mise's source.

Design rules, and why:

- It wraps the HTTP API instead of the database. Every rule (the human gate, lease
  expiry, versions, idempotency) lives in the Go domain; duplicating any of it here
  would create a second source of truth that drifts.
- It only exposes what an agent may legitimately do. There is no "approve" and no
  "delete" tool: the API refuses them for agents, so offering them would just teach
  the agent to fail.
- Pure standard library, stdio transport. Drop the file anywhere Python 3.9+ exists
  and register it; nothing to install.

Token and URL come from the environment, then from disk:
  MISE_URL    then ~/.config/mise/url, then http://127.0.0.1:8787
  MISE_TOKEN  default ~/.config/mise/agent-token, then ~/.config/mise/token
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

PROTOCOL_VERSION = "2025-06-18"
SERVER_NAME = "mise"
SERVER_VERSION = "1.0.0"
DEFAULT_URL = "http://127.0.0.1:8787"
UA = "mise-mcp/1.0"

INSTRUCTIONS = """\
Mise is the owner's personal kanban. You are an agent: you may take work and deliver it,
but you may never approve or close it.

The loop, in order:
  1. list_tasks with state "ready" (or board_overview to see counts).
  2. acquire_lease on the task you pick. It gives you 15 minutes, renewable.
  3. Do the work. renew_lease if you need more time; release_lease if you give up.
  4. deliver with a summary and at least one concrete piece of evidence (a command and
     its output, a file path, a URL, a test result). The task moves to review.
  5. Stop there. The human reads the evidence and decides.

If you are blocked, move_task to "blocked" with a reason and release the lease.
A task in progress with no live lease can be taken by someone else. That is by design.
"""

def _fail(message: str) -> None:
    print(f"mise-mcp: {message}", file=sys.stderr)

def _url_from_disk() -> str:
    """Board URL from ~/.config/mise/url, for setups that keep config out of the command line."""
    path = os.path.expanduser("~/.config/mise/url")
    if os.path.isfile(path):
        try:
            with open(path, encoding="utf-8") as handle:
                return handle.read().strip()
        except OSError:
            return ""
    return ""

def load_config() -> tuple[str, str]:
    url = (os.environ.get("MISE_URL", "").strip() or _url_from_disk() or DEFAULT_URL).rstrip("/")
    token = os.environ.get("MISE_TOKEN", "").strip()
    if not token:
        for path in ("~/.config/mise/agent-token", "~/.config/mise/token"):
            candidate = os.path.expanduser(path)
            if os.path.isfile(candidate):
                token = open(candidate).read().strip()
                if token:
                    break
    if not token:
        _fail("no token: set MISE_TOKEN or create ~/.config/mise/agent-token")
    return url, token

class Api:
    """Thin HTTP client. Raises ApiError with the API's machine-readable code."""

    def __init__(self, url: str, token: str):
        self.url = url
        self.token = token

    def call(self, method: str, path: str, body=None, headers=None) -> dict:
        h = {
            "Authorization": f"Bearer {self.token}",
            "Accept": "application/json",
            "User-Agent": UA,
        }
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        h.update(headers or {})

        req = urllib.request.Request(f"{self.url}{path}", data=data, method=method, headers=h)
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", "replace")
            try:
                payload = json.loads(raw)
                raise ApiError(payload.get("error", f"http_{exc.code}"),
                               payload.get("detail", ""), exc.code) from None
            except json.JSONDecodeError:
                raise ApiError(f"http_{exc.code}", raw[:400], exc.code) from None
        except urllib.error.URLError as exc:
            raise ApiError("unreachable", str(exc.reason), 0) from None

class ApiError(Exception):
    def __init__(self, code: str, detail: str = "", status: int = 0):
        self.code = code
        self.detail = detail
        self.status = status
        super().__init__(f"{code}: {detail}" if detail else code)

    def as_text(self) -> str:
        text = f"error {self.code} (HTTP {self.status})" if self.status else f"error {self.code}"
        return f"{text}: {self.detail}" if self.detail else text

def tool_specs() -> list[dict]:
    def t(name, desc, props, required=(), extra=None):
        schema = {
            "type": "object",
            "properties": props,
            "required": list(required),
            "additionalProperties": False,
        }
        if extra:
            schema.update(extra)
        return {
            "name": name,
            "description": desc,
            "inputSchema": schema,
        }

    return [
        t("board_overview",
          "Counts and cards for the six columns (backlog, ready, in_progress, blocked, review, done). "
          "Use it first to see what is waiting.",
          {}),
        t("list_tasks",
          "List cards, optionally filtered. Cursor pagination: pass next_cursor from the previous answer.",
          {"state": {"type": "string",
                     "enum": ["backlog", "ready", "in_progress", "blocked", "review", "done"],
                     "description": "Only cards in this state"},
           "project": {"type": "string", "description": "Project key, e.g. MI"},
           "limit": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
           "cursor": {"type": "string", "description": "next_cursor from a previous page"}}),
        t("get_task",
          "Full detail of one card: fields, movements, leases, deliveries and comments.",
          {"key": {"type": "string", "description": "Card key, e.g. MI-12"}}, ["key"]),
        t("create_task",
          "Create a card. It always lands in backlog, whoever creates it: an agent may file "
          "work, and only the human promotes a card to ready. The idempotency key is generated "
          "for you; retrying the same call does not duplicate the card.",
          {"title": {"type": "string"},
           "project": {"type": "string", "description": "Project key, e.g. MI"},
           "notes": {"type": "string", "description": "Context, constraints, starting point"},
           "acceptance": {"type": "string", "description": "What 'done' means; be concrete"},
           "priority": {"type": "string", "enum": ["low", "normal", "high"], "default": "normal"},
           "size": {"type": "string", "enum": ["S", "M", "L"]}},
          ["title", "project"]),
        t("move_task",
          "Change state (or edit fields). Reads the current version and sends it as If-Match. "
          "Agents may not move review -> done: the API answers 403 by design.",
          {"key": {"type": "string"},
           "state": {"type": "string",
                     "enum": ["backlog", "ready", "in_progress", "blocked", "review", "done"]},
           "reason": {"type": "string", "description": "Required when entering or leaving blocked"},
           "title": {"type": "string"},
           "notes": {"type": "string"},
           "acceptance": {"type": "string"}},
          ["key"]),
        t("acquire_lease",
          "Take a card: it must be ready, or in progress with no live lease. Returns the lease id "
          "and when it expires (15 minutes).",
          {"key": {"type": "string"}}, ["key"]),
        t("renew_lease",
          "Extend a lease you own by 15 more minutes. It does not touch the card version.",
          {"key": {"type": "string"}, "lease_id": {"type": "integer"}}, ["key", "lease_id"]),
        t("release_lease",
          "Give the card back without delivering. It returns to being takeable.",
          {"key": {"type": "string"}, "lease_id": {"type": "integer"}}, ["key", "lease_id"]),
        t("deliver",
          "Deliver the work: a summary plus at least one verifiable piece of evidence. Moves the "
          "card to review and closes your lease. Pass exactly one of evidence (plain strings) or "
          "evidence_items ({label, raw} objects). When delivering a pull request for a dependency "
          "plan, include pr_url and one evidence_items entry labeled head_sha with its full head commit.",
          {"key": {"type": "string"},
           "summary": {"type": "string", "description": "What you did, in plain language"},
           "evidence": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 20,
                        "description": "Command and output, file path, URL, test result"},
           "evidence_items": {"type": "array", "minItems": 1, "maxItems": 20,
                              "items": {"type": "object",
                                        "properties": {"label": {"type": "string", "minLength": 1, "maxLength": 120},
                                                       "raw": {"type": "string", "minLength": 1, "maxLength": 4000}},
                                        "required": ["label", "raw"],
                                        "additionalProperties": False},
                              "description": "Structured evidence with a short label and the raw proof"},
           "head_sha": {"type": "string", "pattern": "^[0-9a-fA-F]{40}$",
                        "description": "Full head commit of the pull request; required when the project has required checks."},
           "pr_url": {"type": "string", "maxLength": 500,
                      "description": "Optional absolute http(s) link to the pull request behind the work"},
           "lease_id": {"type": "integer", "description": "Omit to let the server use your live lease"}},
          ["key", "summary"],
          {"anyOf": [{"required": ["evidence"]}, {"required": ["evidence_items"]}]}),
        t("comment",
          "Add a comment to a card. The body is stored as written.",
          {"key": {"type": "string"}, "body": {"type": "string"}}, ["key", "body"]),
        t("propose_plan",
          "Propose a plan for a project without creating any work: the cards are materialised only "
          "when the owner approves it in the panel. Pass draft_id and version to change a proposal you "
          "already made; the link stays the same and the version goes up. Returns the draft with its link.",
          {"draft_id": {"type": "integer", "description": "Existing proposal to update"},
           "version": {"type": "integer", "description": "Version you are editing; omit to use the current one"},
           "project": {"type": "string", "description": "Project key, e.g. MI"},
           "outcome": {"type": "string", "maxLength": 2000,
                       "description": "What ships when the plan is done, in the owner's words"},
           "scenario": {"type": "string",
                        "description": "Command that proves the plan works over the integrated app, e.g. make scenario"},
           "visual_review": {"type": "boolean",
                             "description": "True when the plan changes what the owner sees"},
           "visible_change": {"type": "string",
                              "description": "What makes the change visible in production; required with visual_review"},
           "reveal_key": {"type": "string",
                          "description": "Key of the task whose pull request reveals the change; required with visual_review"},
           "tasks": {"type": "array", "minItems": 2, "maxItems": 30,
                     "items": {"type": "object", "additionalProperties": False,
                               "required": ["key", "title"],
                               "properties": {
                                   "key": {"type": "string", "maxLength": 16,
                                           "description": "Stable key inside the proposal, e.g. T1"},
                                   "title": {"type": "string", "maxLength": 200},
                                   "notes": {"type": "string", "maxLength": 50000},
                                   "acceptance": {"type": "string", "maxLength": 10000},
                                   "size": {"type": "string", "enum": ["S", "M", "L"]}}}},
           "edges": {"type": "array",
                     "items": {"type": "object", "additionalProperties": False,
                               "required": ["from", "to"],
                               "properties": {
                                   "from": {"type": "string"},
                                   "to": {"type": "string"},
                                   "kind": {"type": "string", "enum": ["integration", "deployment"],
                                            "default": "integration"}}}}},
          ["outcome", "scenario", "tasks"]),
        t("get_plan_draft",
          "Read a proposal: its version, state, tasks, edges and which tasks can start together.",
          {"draft_id": {"type": "integer"}}, ["draft_id"]),
    ]

def _envelope(payload):
    """Give every write a stable shape.

    The API is inconsistent on purpose-free grounds: POST/PATCH return the bare task, while
    GET wraps it as {"task": ...}. An agent should not have to know which verb it used, so
    the server normalises the four shapes it can return.
    """
    if isinstance(payload, dict) and "task" not in payload:
        if "key" in payload and "state" in payload:
            return {"task": payload}
        if "summary" in payload and "evidence" in payload:
            return {"delivery": payload}
        if "body" in payload:
            return {"comment": payload}
        if "task_key" in payload and "expires_at" in payload:
            return {"lease": payload}
    return payload

def _lease_id(api: Api, key: str) -> int:
    """Find the caller's own live lease on a card."""
    detail = api.call("GET", f"/api/tasks/{key}")
    mine = api.call("GET", "/api/me")
    for lease in detail.get("leases", []):
        if lease.get("released_at") is None and lease.get("agent_id") == mine.get("id"):
            return int(lease["id"])
    raise ApiError("no_lease", f"you have no live lease on {key}: call acquire_lease first")

def run_tool(api: Api, name: str, args: dict) -> dict:
    if name == "board_overview":
        return api.call("GET", "/api/board")

    if name == "list_tasks":
        query = []
        for field in ("state", "project", "cursor"):
            if args.get(field):
                query.append(f"{field}={urllib.parse.quote(str(args[field]))}")
        query.append(f"limit={int(args.get('limit', 50))}")
        return api.call("GET", "/api/tasks?" + "&".join(query))

    if name == "get_task":
        return api.call("GET", f"/api/tasks/{args['key']}")

    if name == "create_task":
        body = {k: args[k] for k in ("title", "project", "notes", "acceptance", "priority", "size")
                if k in args}
        key = args.get("idempotency_key") or f"mcp-{uuid.uuid4()}"
        return _envelope(api.call("POST", "/api/tasks", body, {"Idempotency-Key": key}))

    if name == "move_task":
        current = api.call("GET", f"/api/tasks/{args['key']}")["task"]
        body = {k: args[k] for k in ("state", "reason", "title", "notes", "acceptance") if k in args}
        if not body:
            raise ApiError("invalid_input", "nothing to change: pass state or a field")
        return _envelope(api.call("PATCH", f"/api/tasks/{args['key']}", body,
                                  {"If-Match": f'"{current["version"]}"'}))

    if name == "acquire_lease":
        return _envelope(api.call("POST", f"/api/tasks/{args['key']}/leases", {}))

    if name in ("renew_lease", "release_lease"):
        method = "PATCH" if name == "renew_lease" else "DELETE"
        return _envelope(api.call(method, f"/api/tasks/{args['key']}/leases/{int(args['lease_id'])}",
                                  {} if method == "PATCH" else None))

    if name == "deliver":
        lease_id = args.get("lease_id")
        if lease_id is None:
            lease_id = _lease_id(api, args["key"])
        body = {"lease_id": int(lease_id), "summary": args["summary"]}
        has_evidence = "evidence" in args
        has_items = "evidence_items" in args
        if has_evidence == has_items:
            raise ApiError("invalid_input", "pass exactly one of evidence or evidence_items")
        if has_items:
            body["evidence_items"] = [{"label": item["label"], "raw": item["raw"]}
                                      for item in args["evidence_items"]]
        else:
            body["evidence"] = list(args["evidence"])
        if args.get("pr_url"):
            body["pr_url"] = args["pr_url"]
        if args.get("head_sha"):
            body["head_sha"] = args["head_sha"]
        return _envelope(api.call("POST", f"/api/tasks/{args['key']}/deliveries", body))

    if name == "comment":
        return _envelope(api.call("POST", f"/api/tasks/{args['key']}/comments", {"body": args["body"]}))

    if name == "propose_plan":
        fields = ("project", "outcome", "scenario", "tasks", "edges",
                  "visual_review", "visible_change", "reveal_key")
        body = {k: args[k] for k in fields if k in args}
        if args.get("draft_id"):
            current = api.call("GET", f"/api/plan-drafts/{int(args['draft_id'])}")
            body.setdefault("project", current["project"])
            version = int(args.get("version") or current["version"])
            draft = api.call("PUT", f"/api/plan-drafts/{int(args['draft_id'])}", body,
                             {"If-Match": f'"{version}"'})
        else:
            draft = api.call("POST", "/api/plan-drafts", body)
        draft["link"] = f"{api.url}/#draft/{draft['id']}"
        return draft

    if name == "get_plan_draft":
        draft = api.call("GET", f"/api/plan-drafts/{int(args['draft_id'])}")
        draft["link"] = f"{api.url}/#draft/{draft['id']}"
        return draft

    raise ApiError("unknown_tool", name)

def reply(msg_id, result=None, error=None) -> None:
    payload = {"jsonrpc": "2.0", "id": msg_id}
    if error is not None:
        payload["error"] = error
    else:
        payload["result"] = result
    sys.stdout.write(json.dumps(payload) + "\n")
    sys.stdout.flush()

def main() -> int:
    url, token = load_config()
    api = Api(url, token)
    _fail(f"ready against {url}")

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            _fail(f"ignoring malformed line: {line[:80]}")
            continue

        method = msg.get("method")
        msg_id = msg.get("id")
        params = msg.get("params") or {}

        if msg_id is None:
            continue

        if method == "initialize":
            reply(msg_id, {
                "protocolVersion": params.get("protocolVersion") or PROTOCOL_VERSION,
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
                "instructions": INSTRUCTIONS,
            })
        elif method == "ping":
            reply(msg_id, {})
        elif method == "tools/list":
            reply(msg_id, {"tools": tool_specs()})
        elif method == "tools/call":
            name = params.get("name", "")
            args = params.get("arguments") or {}
            try:
                result = run_tool(api, name, args)
                reply(msg_id, {"content": [{"type": "text",
                                            "text": json.dumps(result, ensure_ascii=False, indent=2)}],
                               "isError": False})
            except ApiError as exc:
                reply(msg_id, {"content": [{"type": "text", "text": exc.as_text()}], "isError": True})
            except Exception as exc:
                reply(msg_id, {"content": [{"type": "text", "text": f"internal error: {exc}"}],
                               "isError": True})
        elif method in ("resources/list", "prompts/list"):
            reply(msg_id, {method.split("/")[0]: []})
        else:
            reply(msg_id, error={"code": -32601, "message": f"method not found: {method}"})

    return 0

if __name__ == "__main__":
    sys.exit(main())
