#!/usr/bin/env python3
"""Live end-to-end drive of the Phase 1 and Phase 2 tools against the
signed-in account.

Creates ONE scratch document titled "google-docs-mcp live test (safe to
delete)" and exercises create, read, direct/suggest/comment edits,
formatting, find, suggestion review, the overwrite guard, export, comment
threads, revisions and diffs, tables, objects and tabs through the MCP
stdio protocol. Requires a completed `login` and, for suggest mode and
anchored comments, Developer Preview (GDOCS_PREVIEW=true, the default
here). Deletion steps run only with GDOCS_ENABLE_DESTRUCTIVE=true.

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
DESTRUCTIVE = env.get("GDOCS_ENABLE_DESTRUCTIVE", "").lower() in ("1", "true", "yes", "on")
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
err, text, sc = call("create_document", {"title": "google-docs-mcp live test (safe to delete)", "content": content})
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
show("read after phase 1 steps", err, text, 2000)

# ---- Phase 2 -------------------------------------------------------------
err, text, sc = call("list_comments", {"document": doc})
show("list comments", err, text, 1500)
threads = sc.get("threads", [])
print("anchored flags:", [t.get("anchored") for t in threads], "handles:", [t.get("handle") for t in threads])

err, text, sc = call("add_comment", {"document": doc, "target": {"text": "Nested point"}, "content": "Live test: anchored comment on the nested bullet."})
show("add comment", err, text)
new_comment = sc.get("id")
err, text, sc = call("list_comments", {"document": doc})
ids = [t.get("id") for t in sc.get("threads", [])]
print("new comment id listed through Drive:", new_comment in ids, "(preview id", new_comment, ")")
if new_comment:
    err, text, _ = call("reply_comment", {"document": doc, "comment_id": new_comment, "content": "Reply from the live test."})
    show("reply", err, text)
    err, text, _ = call("reply_comment", {"document": doc, "comment_id": new_comment, "action": "resolve"})
    show("resolve", err, text)
    err, text, _ = call("reply_comment", {"document": doc, "comment_id": new_comment, "action": "reopen", "content": "Reopened."})
    show("reopen", err, text)
err, text, _ = call("read_document", {"document": doc, "with_handles": True, "include_comments": True, "heading": "Background"})
show("read with comments", err, text, 1500)

err, text, sc = call("list_revisions", {"document": doc, "limit": 5})
show("list revisions", err, text)
revs = sc.get("revisions", [])
if len(revs) >= 2:
    err, text, _ = call("diff_revisions", {"document": doc, "from": revs[-1]["id"], "to": revs[0]["id"]})
    show("diff revisions", err, text, 1500)
    err, text, _ = call("read_document", {"document": doc, "revision": revs[-1]["id"], "max_chars": 600})
    show("read old revision", err, text, 800)

err, text, sc = call("edit_table", {"document": doc, "mode": "direct", "ops": [
    {"op": "insert_table", "location": {"at": "after", "of": {"heading": "Next steps"}}, "rows": 2, "columns": 3, "data": [["Item", "Owner", "Due"], ["Numbers", "Ann", "Friday"]]},
]})
show("insert table with data", err, text, 1200)
err, text, sc = call("read_document", {"document": doc, "with_handles": True, "heading": "Next steps"})
show("read table", err, text, 1200)
m = re.search(r"\[(tbl\d+)\]", text)
tbl = m.group(1) if m else "tbl1"
err, text, _ = call("edit_table", {"document": doc, "mode": "direct", "ops": [
    {"op": "insert_rows", "table": tbl, "row": 2, "count": 1},
    {"op": "set_cells", "table": tbl, "cells": [{"cell": "r2c3", "content": "**Monday**"}]},
    {"op": "style_cells", "table": tbl, "from_cell": "r1c1", "to_cell": "r1c3", "background": "#e8f0fe"},
    {"op": "pin_header_rows", "table": tbl, "count": 1},
]})
show("table ops", err, text, 900)
err, text, _ = call("edit_table", {"document": doc, "mode": "suggest", "ops": [{"op": "set_cells", "table": tbl, "cells": [{"cell": "r3c1", "content": "Suggested cell text"}]}]})
show("suggested cell edit", err, text, 600)
err, text, _ = call("edit_table", {"document": doc, "mode": "direct", "ops": [{"op": "merge_cells", "table": tbl, "from_cell": "r3c1", "to_cell": "r3c3"}]})
show("merge cells", err, text, 600)

err, text, _ = call("insert_object", {"document": doc, "kind": "date", "date": "2026-09-03", "date_format": "iso", "location": {"at": "end", "of": {"text": "Appended paragraph at the very end."}}, "mode": "direct"})
show("insert date chip", err, text, 600)
err, text, _ = call("insert_object", {"document": doc, "kind": "image", "url": "https://www.gstatic.com/images/branding/product/1x/docs_2020q4_48dp.png", "width_pt": 48, "location": {"at": "after", "of": {"text": "Closing line"}}, "mode": "direct"})
show("insert image", err, text, 600)

err, text, sc = call("manage_tabs", {"document": doc, "action": "add", "title": "Appendix", "content": "# Appendix\n\nAdded by the live test."})
show("add tab", err, text)
new_tab = sc.get("tab_id")
err, text, _ = call("manage_tabs", {"document": doc, "action": "rename", "tab": "Appendix", "title": "Appendix A"})
show("rename tab", err, text)
err, text, _ = call("read_document", {"document": doc, "tab": "Appendix A", "with_handles": True})
show("read new tab", err, text, 600)
err, text, _ = call("edit_document", {"document": doc, "mode": "direct", "ops": [{"op": "create_footer", "content": "Live test footer"}]})
show("create footer", err, text, 400)
err, text, _ = call("edit_document", {"document": doc, "mode": "direct", "dry_run": True, "ops": [{"op": "delete_footer"}]})
show("delete footer dry run", err, text, 400)

if DESTRUCTIVE:
    if new_comment:
        err, text, _ = call("delete_comment", {"document": doc, "comment_id": new_comment})
        show("delete comment", err, text)
    if new_tab:
        err, text, _ = call("delete_tab", {"document": doc, "tab": new_tab})
        show("delete tab", err, text)
else:
    print("=== deletion steps skipped (set GDOCS_ENABLE_DESTRUCTIVE=true) ===")

err, text, _ = call("get_document", {"document": doc})
show("final get_document", err, text, 1200)
p.stdin.close(); p.wait(timeout=10)
