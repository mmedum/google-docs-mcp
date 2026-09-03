#!/usr/bin/env python3
"""Agent evals: drive Claude Code headless against scratch documents
through this server and score the end state.

Each task seeds ONE scratch document (title "google-docs-mcp eval …
(safe to delete)") through the server's own tools, runs `claude -p` with
only this server's tools available, then checks the document through the
server again and inspects the tool-call trace. Nothing here is part of
CI: it needs a completed `login`, Developer Preview for suggestion mode
(GDOCS_PREVIEW=true, the default here), a working `claude` CLI, and it
spends API usage (about 10-20 cents per task at list price with the
default model; EVAL_MODEL=sonnet is cheaper).

    make build && python3 scripts/evals/run.py            # every task
    python3 scripts/evals/run.py replace-suggest add-tab   # some tasks
    python3 scripts/evals/run.py --list
    python3 scripts/evals/run.py --report                  # rebuild report.md from saved traces

Output goes to $LIVE_OUT/evals (default /tmp/google-docs-mcp-live): a
JSON trace per task and report.md. Document ids and URLs are printed to
your terminal only. Delete the scratch documents afterwards.
"""
import json, os, re, subprocess, sys, time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BIN = os.path.join(ROOT, "google-docs-mcp")
OUT = os.path.join(os.environ.get("LIVE_OUT", "/tmp/google-docs-mcp-live"), "evals")
os.makedirs(OUT, exist_ok=True)
SERVER_ENV = dict(os.environ, GDOCS_PREVIEW=os.environ.get("GDOCS_PREVIEW", "true"), GDOCS_LOG_LEVEL="warn")
MODEL = os.environ.get("EVAL_MODEL", "")
BUDGET = os.environ.get("EVAL_BUDGET_USD", "1.50")
MCP_NAME = "gdocs"


class Server:
    """A stdio MCP client for the server, used to seed and to score."""

    def __init__(self):
        self.p = subprocess.Popen([BIN], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open(os.path.join(OUT, "server-stderr.txt"), "a"), text=True, bufsize=1, env=SERVER_ENV)
        self.seq = 0
        self.rpc("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "evals", "version": "0"}})
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.p.stdin.flush()

    def rpc(self, method, params=None):
        self.seq += 1
        msg = {"jsonrpc": "2.0", "id": self.seq, "method": method}
        if params is not None:
            msg["params"] = params
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise SystemExit("server closed")
            m = json.loads(line)
            if m.get("id") == self.seq:
                return m

    def call(self, name, args):
        r = self.rpc("tools/call", {"name": name, "arguments": args}).get("result") or {}
        text = "".join(c.get("text", "") for c in r.get("content", []))
        return r.get("isError", False), text, r.get("structuredContent") or {}

    def must(self, name, args):
        err, text, sc = self.call(name, args)
        if err:
            raise SystemExit(f"seed step {name} failed: {text}")
        return text, sc

    def tools(self):
        return {t["name"] for t in (self.rpc("tools/list").get("result") or {}).get("tools", [])}

    def close(self):
        self.p.stdin.close()
        self.p.wait(timeout=10)


SEED = """# Eval document

This document was created by google-docs-mcp evals. It is safe to delete.

## Background

Revenue grew a lot in Q3. The team shipped **three** features.

- First point
- Second point
- Nested point

## Data

Numbers go here.

## Next steps

1. Review the numbers
2. Send the summary

Closing line with a [link](https://example.com)."""


def seed(server, name, content=SEED):
    text, sc = server.must("create_document", {"title": f"google-docs-mcp eval {name} (safe to delete)", "content": content})
    doc = sc.get("id")
    if not doc:
        raise SystemExit("create_document returned no id: " + text)
    return doc


def read(server, doc, **args):
    return server.must("read_document", dict({"document": doc, "with_handles": True}, **args))[0]


def pending_suggestions(server, doc):
    text, _ = server.must("list_suggestions", {"document": doc})
    m = re.match(r"(\d+) pending suggestion", text)
    return int(m.group(1)) if m else -1


def info(server, doc):
    """get_document's text as numbers: footnotes and tab titles."""
    text, _ = server.must("get_document", {"document": doc})
    m = re.search(r"(\d+) footnotes", text)
    return {"footnotes": int(m.group(1)) if m else -1, "tabs": re.findall(r'^- tab \d+ "(.*?)"', text, re.M)}


def threads(server, doc):
    """list_comments' text as dicts: id, handle, resolved, content, replies."""
    text, _ = server.must("list_comments", {"document": doc})
    out = []
    for line in text.splitlines()[1:]:
        if line.startswith("- "):
            head, _, content = line[2:].partition(": ")
            m = re.search(r"\[(\S+?)\]", head)
            out.append({"id": head.split()[0], "handle": m.group(1) if m and not m.group(1).startswith(("resolved", "deleted")) else "",
                        "resolved": "[resolved]" in head, "content": content, "replies": []})
        elif line.startswith("    ↳ ") and out:
            out[-1]["replies"].append(line[6:].partition(": ")[2])
    return out


# ---- running Claude Code ---------------------------------------------------

def run_claude(prompt, workdir):
    cfg = os.path.join(workdir, "mcp.json")
    with open(cfg, "w") as f:
        json.dump({"mcpServers": {MCP_NAME: {"command": BIN, "env": {"GDOCS_PREVIEW": SERVER_ENV["GDOCS_PREVIEW"], "GDOCS_LOG_LEVEL": "warn"}}}}, f)
    cmd = ["claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--mcp-config", cfg, "--strict-mcp-config",
           "--allowedTools", f"mcp__{MCP_NAME}__*", "--max-budget-usd", BUDGET, "--no-session-persistence"]
    if MODEL:
        cmd += ["--model", MODEL]
    env = {k: v for k, v in os.environ.items() if not k.startswith("CLAUDE")}
    env["GDOCS_PREVIEW"] = SERVER_ENV["GDOCS_PREVIEW"]
    started = time.time()
    proc = subprocess.run(cmd, cwd=workdir, env=env, capture_output=True, text=True, timeout=900)
    trace = {"calls": [], "results": [], "final": "", "cost": None, "turns": None, "seconds": round(time.time() - started, 1), "exit": proc.returncode, "stderr": proc.stderr[-2000:]}
    byid = {}
    for line in proc.stdout.splitlines():
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        msg = ev.get("message") or {}
        for c in msg.get("content") or []:
            if ev.get("type") == "assistant" and c.get("type") == "tool_use":
                byid[c.get("id")] = len(trace["calls"])
                trace["calls"].append({"name": c.get("name"), "input": c.get("input")})
                trace["results"].append({"error": False, "text": ""})
            if ev.get("type") == "user" and c.get("type") == "tool_result":
                body = c.get("content")
                text = body if isinstance(body, str) else "".join(x.get("text", "") for x in (body or []) if isinstance(x, dict))
                i = byid.get(c.get("tool_use_id"))
                if i is not None:
                    trace["results"][i] = {"error": bool(c.get("is_error")), "text": text[:4000]}
        if ev.get("type") == "result":
            trace["final"] = ev.get("result") or ""
            trace["cost"] = ev.get("total_cost_usd")
            trace["turns"] = ev.get("num_turns")
    return trace


def tool(call):
    return (call.get("name") or "").replace(f"mcp__{MCP_NAME}__", "")


def calls_to(trace, name):
    return [c for c in trace["calls"] if tool(c) == name]


def ops_of(trace, *tools):
    out = []
    for c in trace["calls"]:
        if tool(c) in tools:
            for op in (c.get("input") or {}).get("ops") or []:
                out.append((c, op))
    return out


def writes(trace):
    return [c for c in trace["calls"] if tool(c) in WRITE_TOOLS]


WRITE_TOOLS = {"edit_document", "format_document", "edit_table", "insert_object", "manage_tabs", "delete_tab", "add_comment", "reply_comment", "delete_comment", "review_suggestion", "create_document"}


# ---- tasks -----------------------------------------------------------------
# Each task: name, prompt (with {doc}), optional setup(server, doc), check(server, doc, trace) -> [(check, ok, detail)].

def t_replace_suggest(server, doc, trace):
    pending = pending_suggestions(server, doc)
    md = read(server, doc, include_suggestions=True)
    return [
        ("one pending suggestion", pending == 1, f"{pending} pending"),
        ("suggestion inserts 'substantially'", "{++substantially" in md or "substantially" in md and "{--a lot--}" in md, md[:300]),
        ("committed text still says 'a lot'", "a lot" in read(server, doc), ""),
        ("used mode suggest", all((c["input"] or {}).get("mode") == "suggest" for c in writes(trace)) and bool(writes(trace)), str([(tool(c), (c["input"] or {}).get("mode")) for c in writes(trace)])),
    ]


def t_insert_bullet(server, doc, trace):
    md = read(server, doc)
    i, j = md.find("- Second point"), md.find("- Third point")
    return [
        ("'Third point' bullet after 'Second point'", 0 <= i < j, md[i:j + 20] if i >= 0 else md[:200]),
        ("still a list item", re.search(r"\n\[p\d+\] - Third point", md) is not None, ""),
        ("direct mode", all((c["input"] or {}).get("mode") == "direct" for c in writes(trace)) and bool(writes(trace)), ""),
    ]


def t_comment(server, doc, trace):
    ths = threads(server, doc)
    md = read(server, doc)
    m = re.search(r"\[(p\d+)\] Closing line", md)
    handle = m.group(1) if m else ""
    return [
        ("one thread", len(ths) == 1, f"{len(ths)} threads"),
        ("says citation", any("citation" in t["content"].lower() for t in ths), str([t["content"] for t in ths])),
        ("on the closing line", any(t["handle"] == handle for t in ths), f"want {handle}, got {[t['handle'] for t in ths]}"),
        ("no text edited", "Closing line with a" in md and len([c for c in writes(trace) if tool(c) != "add_comment"]) == 0, str([tool(c) for c in writes(trace)])),
    ]


def t_summarize(server, doc, trace):
    reads = calls_to(trace, "read_document")
    scoped = [c for c in reads if any((c["input"] or {}).get(k) for k in ("heading_id", "heading", "from_handle"))]
    return [
        ("no writes", not writes(trace), str([tool(c) for c in writes(trace)])),
        ("read scoped to the section", bool(scoped), str([c["input"] for c in reads])),
        ("answer mentions the steps", "review" in trace["final"].lower() and "summary" in trace["final"].lower(), trace["final"][:200]),
    ]


def t_bold(server, doc, trace):
    md = read(server, doc)
    return [
        ("'Send the summary' is bold", "**Send the summary**" in md, md[md.find("Send") - 10:md.find("Send") + 30]),
        ("text unchanged otherwise", "1. Review the numbers" in md and "Closing line" in md, ""),
    ]


def t_table(server, doc, trace):
    md = read(server, doc, heading="Data")
    return [
        ("a 3×2 table under Data", "table 3×2" in md, md[:400]),
        ("cells filled", "Alpha" in md and "Beta" in md and "| 2 |" in md, md[:400]),
        ("used edit_table", bool(calls_to(trace, "edit_table")), str([tool(c) for c in trace["calls"]])),
    ]


def t_footnote(server, doc, trace):
    n = info(server, doc)["footnotes"]
    md = read(server, doc, heading="Background")
    fn = ""
    if n:
        fn = read(server, doc, segment="footnote1")
    return [
        ("one footnote", n == 1, f"{n} footnotes"),
        ("referenced from the revenue sentence", re.search(r"Q3\.?\[\^1\]|\[\^1\].*Q3", md) is not None or "[^1]" in md, md[:300]),
        ("footnote says finance", "finance" in fn.lower(), fn[:200]),
    ]


def t_add_tab(server, doc, trace):
    tabs = info(server, doc)["tabs"]
    second = ""
    if len(tabs) >= 2:
        second = read(server, doc, tab="2")
    return [
        ("two tabs", len(tabs) == 2, str(tabs)),
        ("named Appendix", len(tabs) >= 2 and tabs[1] == "Appendix", ""),
        ("heading Notes in the new tab", "# Notes" in second, second[:300]),
        ("paragraph present", "More to come." in second, ""),
        ("used manage_tabs", bool(calls_to(trace, "manage_tabs")), str([tool(c) for c in trace["calls"]])),
    ]


def setup_comment(server, doc):
    server.must("add_comment", {"document": doc, "target": {"text": "Second point"}, "content": "Is this point still accurate?"})


def t_resolve(server, doc, trace):
    ths = threads(server, doc)
    t = ths[0] if ths else {"resolved": False, "replies": []}
    return [
        ("one thread", len(ths) == 1, f"{len(ths)}"),
        ("resolved", t["resolved"], str(t)[:200]),
        ("reply says done", any("done" in r.lower() for r in t["replies"]), str(t["replies"])[:200]),
        ("no text edited", "Second point" in read(server, doc) and not [c for c in writes(trace) if tool(c) != "reply_comment"], str([tool(c) for c in writes(trace)])),
    ]


def setup_suggestion(server, doc):
    server.must("edit_document", {"document": doc, "mode": "suggest", "ops": [{"op": "replace", "target": {"text": "Numbers go here."}, "content": "Figures go here."}]})


def t_accept(server, doc, trace):
    pending = pending_suggestions(server, doc)
    md = read(server, doc)
    return [
        ("no pending suggestions", pending == 0, f"{pending} pending"),
        ("suggested text is now committed", "Figures go here." in md, md[md.find("Data"):md.find("Data") + 80]),
        ("used review_suggestion accept", any((c["input"] or {}).get("action") == "accept" for c in calls_to(trace, "review_suggestion")), str([tool(c) for c in trace["calls"]])),
    ]


def t_find(server, doc, trace):
    return [
        ("used find_in_document", bool(calls_to(trace, "find_in_document")), str([tool(c) for c in trace["calls"]])),
        ("no writes", not writes(trace), ""),
        ("answer says three", re.search(r"\b(3|three)\b", trace["final"].lower()) is not None, trace["final"][:200]),
    ]


def t_create(server, doc, trace):
    creates = calls_to(trace, "create_document")
    new = ""
    for r in trace["results"]:
        m = re.search(r"/document/d/([A-Za-z0-9_-]{20,})", r["text"])
        if m and m.group(1) != doc:
            new = m.group(1)
    md = read(server, new) if new else ""
    return [
        ("create_document with content", bool(creates) and bool((creates[0]["input"] or {}).get("content")), str([c["input"] for c in creates])[:300]),
        ("heading Plan", "# Plan" in md, md[:200]),
        ("two bullets", "- One" in md and "- Two" in md, ""),
    ]


def setup_guard(server, doc):
    server.must("add_comment", {"document": doc, "target": {"text": "Send the summary"}, "content": "Who is the audience?"})


def t_guard(server, doc, trace):
    md = read(server, doc)
    forced = [c for c in writes(trace) if (c["input"] or {}).get("force")]
    return [
        ("paragraph kept (edit was blocked)", "Send the summary" in md, md[md.find("Next steps"):md.find("Next steps") + 120]),
        ("did not force", not forced, str([c["input"] for c in forced])[:300]),
        ("told the person why", any(w in trace["final"].lower() for w in ("comment", "blocked", "refus")), trace["final"][:300]),
    ]


TASKS = [
    dict(name="replace-suggest", prompt="In the Google Doc {doc}, change 'a lot' to 'substantially' in the Background section. Make it a suggestion, not a direct edit.", check=t_replace_suggest),
    dict(name="insert-bullet", prompt="In the Google Doc {doc}, add a bullet point 'Third point' right after 'Second point' in the list under Background. Edit the document directly.", check=t_insert_bullet),
    dict(name="comment", prompt="In the Google Doc {doc}, leave a comment on the sentence that starts with 'Closing line' saying 'Needs a citation.' Do not change any text.", check=t_comment),
    dict(name="summarize", prompt="Summarize the 'Next steps' section of the Google Doc {doc} in one sentence. Read only what you need.", check=t_summarize),
    dict(name="bold", prompt="In the Google Doc {doc}, make the words 'Send the summary' bold. Edit directly.", check=t_bold),
    dict(name="table", prompt="In the Google Doc {doc}, add a table with 3 rows and 2 columns right after the paragraph 'Numbers go here.' under the Data heading. The header row is Name and Value, then Alpha with 1 and Beta with 2. Edit directly.", check=t_table),
    dict(name="footnote", prompt="In the Google Doc {doc}, add a footnote at the end of the sentence 'Revenue grew a lot in Q3.' with the text 'Source: finance report.' Edit directly.", check=t_footnote),
    dict(name="add-tab", prompt="In the Google Doc {doc}, add a new tab named 'Appendix' containing a heading 'Notes' and below it the paragraph 'More to come.'", check=t_add_tab),
    dict(name="resolve", prompt="In the Google Doc {doc}, reply to the open comment on 'Second point' with 'Done, thanks.' and resolve it. Do not change any text.", setup=setup_comment, check=t_resolve),
    dict(name="accept", prompt="Accept every pending suggestion in the Google Doc {doc}.", setup=setup_suggestion, check=t_accept),
    dict(name="find", prompt="How many times does the word 'point' occur in the Google Doc {doc}, and in which blocks? Answer briefly.", check=t_find),
    dict(name="create", prompt="Create a new Google Doc titled 'google-docs-mcp eval created (safe to delete)' with a heading 'Plan' and two bullet points 'One' and 'Two'. Then tell me its URL.", check=t_create),
    dict(name="guard", prompt="In the Google Doc {doc}, delete the paragraph 'Send the summary' with a direct edit.", setup=setup_guard, check=t_guard),
]


# ---- measures across tasks ---------------------------------------------------

def measures(traces, known):
    m = {"read_document calls": 0, "reads with format text": 0, "reads with with_handles": 0, "reads whole body (no scope)": 0, "reads with max_chars set": 0,
         "targets by text": 0, "targets by handle": 0, "targets by heading": 0, "targets by cell": 0, "dry runs": 0, "tool errors": 0, "unknown tools": 0,
         "get_document first": 0, "tool searches (deferred tool lookups)": 0}
    for name, tr in traces.items():
        if tr["calls"] and tool(tr["calls"][0]) == "get_document":
            m["get_document first"] += 1
        for c in tr["calls"]:
            t, inp = tool(c), c["input"] or {}
            if t == "ToolSearch":
                m["tool searches (deferred tool lookups)"] += 1
            if c["name"].startswith("mcp__") and t not in known:
                m["unknown tools"] += 1
            if t == "read_document":
                m["read_document calls"] += 1
                if (inp.get("format") or "").lower() == "text":
                    m["reads with format text"] += 1
                if inp.get("with_handles"):
                    m["reads with with_handles"] += 1
                if inp.get("max_chars"):
                    m["reads with max_chars set"] += 1
                if not any(inp.get(k) for k in ("heading_id", "heading", "from_handle", "tab", "segment", "continue_from")):
                    m["reads whole body (no scope)"] += 1
            if inp.get("dry_run"):
                m["dry runs"] += 1
            for op in inp.get("ops") or []:
                for key in ("target", "location"):
                    tg = op.get(key) or {}
                    if key == "location":
                        tg = tg.get("of") or {}
                    if tg.get("text"):
                        m["targets by text"] += 1
                    if tg.get("handle") or tg.get("from_handle"):
                        m["targets by handle"] += 1
                    if tg.get("heading_id") or tg.get("heading"):
                        m["targets by heading"] += 1
                    if tg.get("cell"):
                        m["targets by cell"] += 1
        m["tool errors"] += sum(1 for r in tr["results"] if r["error"])
    return m


def main():
    args = sys.argv[1:]
    if args == ["--list"]:
        for t in TASKS:
            print(t["name"])
        return
    if not os.path.exists(BIN):
        raise SystemExit("build first: make build")
    server = Server()
    known = server.tools()
    rows, traces = [], {}
    if args == ["--report"]:
        for t in TASKS:
            path = os.path.join(OUT, f"{t['name']}.json")
            if os.path.exists(path):
                saved = json.load(open(path))
                rows.append((t["name"], all(ok for _, ok, _ in saved["checks"]), saved["checks"], saved["trace"]))
                traces[t["name"]] = saved["trace"]
        server.close()
        write_report(rows, traces, known)
        return
    selected = [t for t in TASKS if not args or t["name"] in args]
    if args and len(selected) != len(args):
        raise SystemExit("unknown task; --list shows them")
    workdir = os.path.join(OUT, "work")
    os.makedirs(workdir, exist_ok=True)
    for t in selected:
        doc = seed(server, t["name"])
        if t.get("setup"):
            t["setup"](server, doc)
        print(f"== {t['name']} (doc {doc})", flush=True)
        trace = run_claude(t["prompt"].format(doc=doc), workdir)
        trace["doc"] = doc
        try:
            checks = t["check"](server, doc, trace)
        except SystemExit as e:
            checks = [("scoring", False, str(e))]
        passed = all(ok for _, ok, _ in checks)
        traces[t["name"]] = trace
        with open(os.path.join(OUT, f"{t['name']}.json"), "w") as f:
            json.dump({"prompt": t["prompt"], "checks": checks, "trace": trace}, f, indent=1)
        rows.append((t["name"], passed, checks, trace))
        for name, ok, detail in checks:
            print(f"   {'ok  ' if ok else 'FAIL'} {name}" + ("" if ok else f"  ({detail[:160]})"), flush=True)
        print(f"   {len(trace['calls'])} tool calls, {trace['turns']} turns, ${trace['cost'] or 0:.2f}, {trace['seconds']}s", flush=True)
    server.close()
    write_report(rows, traces, known)


def write_report(rows, traces, known):
    lines = ["# Agent eval report", "", f"model: {MODEL or 'default'}; {sum(1 for r in rows if r[1])}/{len(rows)} tasks passed; "
             f"total ${sum(r[3]['cost'] or 0 for r in rows):.2f}", "", "| task | result | checks | calls | turns | cost | s |", "|---|---|---|---|---|---|---|"]
    for name, passed, checks, tr in rows:
        failed = ", ".join(n for n, ok, _ in checks if not ok)
        lines.append(f"| {name} | {'pass' if passed else 'FAIL'} | {sum(1 for _, ok, _ in checks if ok)}/{len(checks)} {failed} | {len(tr['calls'])} | {tr['turns']} | {tr['cost'] or 0:.2f} | {tr['seconds']} |")
    lines += ["", "## Tool use across tasks", ""]
    for k, v in measures(traces, known).items():
        lines.append(f"- {k}: {v}")
    lines += ["", "## Tool call sequences", ""]
    for name, _, _, tr in rows:
        lines.append(f"- {name}: " + " → ".join(tool(c) for c in tr["calls"]))
    for name, _, checks, _ in rows:
        for check, ok, detail in checks:
            if not ok:
                lines.append(f"- {name}: FAILED {check}: {detail[:300]}")
    report = "\n".join(lines)
    with open(os.path.join(OUT, "report.md"), "w") as f:
        f.write(report + "\n")
    print("\n" + report)


if __name__ == "__main__":
    main()
