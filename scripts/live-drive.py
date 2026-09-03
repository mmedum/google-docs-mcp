#!/usr/bin/env python3
"""Live end-to-end drive of the Phase 1 tools against the signed-in account.

Creates ONE scratch document titled "google-docs-mcp Phase 1 live test
(safe to delete)" and exercises create, read, direct/suggest/comment
edits, formatting, find, suggestion review, the overwrite guard, and
export through the MCP stdio protocol. Requires a completed `login` and,
for suggest mode, Developer Preview (GDOCS_PREVIEW=true, the default here).

    make build && python3 scripts/live-drive.py

Output goes to $LIVE_OUT (default /tmp/google-docs-mcp-live); the scratch
document's id is written there. Delete the document afterwards. This is a
manual check, not part of CI, and it prints document URLs and ids only to
your terminal.
"""
import json, subprocess, sys, os, re

OUT = os.environ.get("LIVE_OUT", "/tmp/google-docs-mcp-live")
os.makedirs(OUT, exist_ok=True)
env = dict(os.environ, GDOCS_PREVIEW=os.environ.get("GDOCS_PREVIEW", "true"), GDOCS_LOG_LEVEL="warn", GDOCS_EXPORT_DIR=OUT + "/exports")
p = subprocess.Popen(["./google-docs-mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open(OUT + "/stderr.txt", "w"), text=True, bufsize=1, env=env)
seq = 0
def rpc(method, params=None, wait=True):
    global seq
    seq += 1
    msg = {"jsonrpc": "2.0", "id": seq, "method": method}
    if params is not None:
        msg["params"] = params
    p.stdin.write(json.dumps(msg) + "\n"); p.stdin.flush()
    while True:
        line = p.stdout.readline()
        if not line:
            raise SystemExit("server closed")
        m = json.loads(line)
        if m.get("id") == seq:
            return m
def call(name, args):
    m = rpc("tools/call", {"name": name, "arguments": args})
    r = m.get("result") or {}
    text = "".join(c.get("text", "") for c in r.get("content", []))
    sc = r.get("structuredContent") or {}
    return r.get("isError", False), text, sc

rpc("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "phase1", "version": "0"}})
p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"); p.stdin.flush()

def show(label, err, text, limit=900):
    text = re.sub(r"/document/d/[A-Za-z0-9_-]+", "/document/d/<scratch>", text)
    text = re.sub(r"revision [A-Za-z0-9_-]{20,}", "revision <rev>", text)
    print(f"=== {label} (isError={err}) ===")
    print(text[:limit])

content = """# Phase 1 live test

This document was created by google-docs-mcp. It is safe to delete.

## Background

Revenue grew a lot in Q3. The team shipped **three** features.

- First point
- Second point
    - Nested point

## Next steps

1. Review the numbers
2. Send the summary

Closing line with a [link](https://example.com)."""
err, text, sc = call("create_document", {"title": "google-docs-mcp Phase 1 live test (safe to delete)", "content": content})
show("create", err, text)
doc = sc.get("id")
open(OUT + "/last-doc-id.txt", "w").write(doc or "")

err, text, _ = call("read_document", {"document": doc, "with_handles": True})
show("read after create", err, text, 1500)

err, text, sc = call("edit_document", {"document": doc, "mode": "direct", "ops": [
    {"op": "replace", "target": {"text": "Revenue grew a lot in Q3."}, "content": "Revenue grew substantially in Q3."},
    {"op": "insert", "location": {"at": "after", "of": {"heading": "Background", "include_heading": True}}, "content": "Inserted after the Background section.\n\n- with a bullet"},
    {"op": "append", "content": "Appended paragraph at the very end."},
]})
show("direct edit", err, text)

err, text, sc = call("edit_document", {"document": doc, "mode": "suggest", "ops": [
    {"op": "replace", "target": {"text": "three"}, "content": "four"},
    {"op": "delete", "target": {"text": "Second point"}},
]})
show("suggest edit", err, text)
suggestion_ids = sc.get("suggestion_ids", [])

err, text, sc = call("edit_document", {"document": doc, "mode": "comment", "ops": [
    {"op": "replace", "target": {"text": "Send the summary"}, "content": "Send the summary to the board"},
]})
show("comment mode", err, text)

err, text, _ = call("format_document", {"document": doc, "mode": "direct", "ops": [
    {"op": "text_style", "target": {"text": "Closing line"}, "bold": True, "color": "#1a73e8"},
    {"op": "paragraph_style", "target": {"text": "Appended paragraph at the very end."}, "alignment": "CENTER"},
]})
show("format", err, text)

err, text, _ = call("find_in_document", {"document": doc, "query": "point"})
show("find", err, text)

err, text, sc = call("list_suggestions", {"document": doc})
show("list suggestions", err, text)
if suggestion_ids:
    err, text, _ = call("review_suggestion", {"document": doc, "action": "reject", "ids": [suggestion_ids[-1]]})
    show("reject last suggestion", err, text)

err, text, _ = call("edit_document", {"document": doc, "mode": "direct", "dry_run": True, "ops": [{"op": "delete", "target": {"text": "Send the summary"}}]})
show("dry run delete over a commented range", err, text, 600)
err, text, _ = call("edit_document", {"document": doc, "mode": "direct", "ops": [{"op": "delete", "target": {"text": "Send the summary"}}]})
show("guard: direct delete over commented range", err, text, 400)

err, text, _ = call("export_document", {"document": doc, "format": "md", "max_chars": 1500})
show("export md", err, text, 1500)

err, text, _ = call("read_document", {"document": doc, "with_handles": True, "include_suggestions": True})
show("final read", err, text, 2000)
p.stdin.close(); p.wait(timeout=10)
