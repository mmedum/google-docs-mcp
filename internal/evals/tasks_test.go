//go:build evals

package evals

import (
	"fmt"
	"strings"
)

// check is one scored assertion about the end state or the trace.
type check struct {
	name   string
	ok     bool
	detail string
}

// task is a prompt, an optional setup, and what counts as done.
type task struct {
	name   string
	prompt string // %s is the document id
	setup  func(s *server, doc string)
	check  func(s *server, doc string, tr *trace) []check
}

// allMode reports whether every write the model made used this mode, and
// that it made at least one.
func allMode(tr *trace, mode string) (bool, string) {
	ws := tr.writes()
	if len(ws) == 0 {
		return false, "no writes at all"
	}
	var got []string
	ok := true
	for _, c := range ws {
		m, _ := c.Input["mode"].(string)
		got = append(got, c.tool()+"="+m)
		if m != mode {
			ok = false
		}
	}
	return ok, strings.Join(got, ", ")
}

var tasks = []task{
	{
		name:   "replace-suggest",
		prompt: "In the Google Doc %s, change 'a lot' to 'substantially' in the Background section. Make it a suggestion, not a direct edit.",
		check: func(s *server, doc string, tr *trace) []check {
			pending := s.pendingSuggestions(doc)
			md := s.read(doc, map[string]any{"include_suggestions": true})
			modeOK, modes := allMode(tr, "suggest")
			return []check{
				{"one pending suggestion", pending == 1, fmt.Sprintf("%d pending", pending)},
				{"suggestion inserts 'substantially'",
					strings.Contains(md, "{++substantially") || (strings.Contains(md, "substantially") && strings.Contains(md, "{--a lot--}")),
					clip(md, 300)},
				{"committed text still says 'a lot'", strings.Contains(s.read(doc, nil), "a lot"), ""},
				{"used mode suggest", modeOK, modes},
			}
		},
	},
	{
		name:   "insert-bullet",
		prompt: "In the Google Doc %s, add a bullet point 'Third point' right after 'Second point' in the list under Background. Edit the document directly.",
		check: func(s *server, doc string, tr *trace) []check {
			md := s.read(doc, nil)
			i, j := strings.Index(md, "- Second point"), strings.Index(md, "- Third point")
			modeOK, modes := allMode(tr, "direct")
			return []check{
				{"'Third point' after 'Second point'", i >= 0 && i < j, clip(md, 200)},
				{"still a list item", bulletThird.MatchString(md), ""},
				{"direct mode", modeOK, modes},
			}
		},
	},
	{
		name:   "comment",
		prompt: "In the Google Doc %s, leave a comment on the sentence that starts with 'Closing line' saying 'Needs a citation.' Do not change any text.",
		check: func(s *server, doc string, tr *trace) []check {
			ths := s.threads(doc)
			md := s.read(doc, nil)
			var handle string
			if m := closingRE.FindStringSubmatch(md); m != nil {
				handle = m[1]
			}
			saysCitation, onLine := false, false
			var handles []string
			for _, t := range ths {
				if strings.Contains(strings.ToLower(t.content), "citation") {
					saysCitation = true
				}
				handles = append(handles, t.handle)
				if t.handle == handle {
					onLine = true
				}
			}
			var others []string
			for _, c := range tr.writes() {
				if c.tool() != "add_comment" {
					others = append(others, c.tool())
				}
			}
			return []check{
				{"one thread", len(ths) == 1, fmt.Sprintf("%d threads", len(ths))},
				{"says citation", saysCitation, ""},
				{"on the closing line", onLine, fmt.Sprintf("want %s, got %v", handle, handles)},
				{"no text edited", strings.Contains(md, "Closing line with a") && len(others) == 0, strings.Join(others, ", ")},
			}
		},
	},
	{
		name:   "summarize",
		prompt: "Summarize the 'Next steps' section of the Google Doc %s in one sentence. Read only what you need.",
		check: func(s *server, doc string, tr *trace) []check {
			reads := tr.callsTo("read_document")
			scoped := false
			for _, c := range reads {
				for _, k := range []string{"heading_id", "heading", "from_handle"} {
					if v, ok := c.Input[k]; ok && v != "" {
						scoped = true
					}
				}
			}
			final := strings.ToLower(tr.Final)
			return []check{
				{"no writes", len(tr.writes()) == 0, strings.Join(tr.toolNames(), ", ")},
				{"read scoped to the section", scoped, fmt.Sprintf("%d reads", len(reads))},
				{"answer mentions the steps", strings.Contains(final, "review") && strings.Contains(final, "summary"), clip(tr.Final, 200)},
			}
		},
	},
	{
		name:   "bold",
		prompt: "In the Google Doc %s, make the words 'Send the summary' bold. Edit directly.",
		check: func(s *server, doc string, tr *trace) []check {
			md := s.read(doc, nil)
			return []check{
				{"'Send the summary' is bold", strings.Contains(md, "**Send the summary**"), clip(md, 300)},
				{"text unchanged otherwise", strings.Contains(md, "1. Review the numbers") && strings.Contains(md, "Closing line"), ""},
			}
		},
	},
	{
		name:   "table",
		prompt: "In the Google Doc %s, add a table with 3 rows and 2 columns right after the paragraph 'Numbers go here.' under the Data heading. The header row is Name and Value, then Alpha with 1 and Beta with 2. Edit directly.",
		check: func(s *server, doc string, tr *trace) []check {
			md := s.read(doc, map[string]any{"heading": "Data"})
			return []check{
				{"a 3×2 table under Data", strings.Contains(md, "table 3×2"), clip(md, 400)},
				{"cells filled", strings.Contains(md, "Alpha") && strings.Contains(md, "Beta") && strings.Contains(md, "| 2 |"), clip(md, 400)},
				{"used edit_table", len(tr.callsTo("edit_table")) > 0, strings.Join(tr.toolNames(), ", ")},
			}
		},
	},
	{
		name:   "footnote",
		prompt: "In the Google Doc %s, add a footnote at the end of the sentence 'Revenue grew a lot in Q3.' with the text 'Source: finance report.' Edit directly.",
		check: func(s *server, doc string, tr *trace) []check {
			n := s.info(doc).footnotes
			md := s.read(doc, map[string]any{"heading": "Background"})
			fn := ""
			if n > 0 {
				fn = s.read(doc, map[string]any{"segment": "footnote1"})
			}
			return []check{
				{"one footnote", n == 1, fmt.Sprintf("%d footnotes", n)},
				{"referenced from the revenue sentence", footnoteRef.MatchString(md) || strings.Contains(md, "[^1]"), clip(md, 300)},
				{"footnote says finance", strings.Contains(strings.ToLower(fn), "finance"), clip(fn, 200)},
			}
		},
	},
	{
		name:   "add-tab",
		prompt: "In the Google Doc %s, add a new tab named 'Appendix' containing a heading 'Notes' and below it the paragraph 'More to come.'",
		check: func(s *server, doc string, tr *trace) []check {
			tabs := s.info(doc).tabs
			second := ""
			if len(tabs) >= 2 {
				second = s.read(doc, map[string]any{"tab": "2"})
			}
			return []check{
				{"two tabs", len(tabs) == 2, strings.Join(tabs, ", ")},
				{"named Appendix", len(tabs) >= 2 && tabs[1] == "Appendix", ""},
				{"heading Notes in the new tab", strings.Contains(second, "# Notes"), clip(second, 300)},
				{"paragraph present", strings.Contains(second, "More to come."), ""},
				{"used manage_tabs", len(tr.callsTo("manage_tabs")) > 0, strings.Join(tr.toolNames(), ", ")},
			}
		},
	},
	{
		name:   "resolve",
		prompt: "In the Google Doc %s, reply to the open comment on 'Second point' with 'Done, thanks.' and resolve it. Do not change any text.",
		setup: func(s *server, doc string) {
			s.must("add_comment", map[string]any{"document": doc,
				"target": map[string]any{"text": "Second point"}, "content": "Is this point still accurate?"})
		},
		check: func(s *server, doc string, tr *trace) []check {
			ths := s.threads(doc)
			var t thread
			if len(ths) > 0 {
				t = ths[0]
			}
			replied := false
			for _, r := range t.replies {
				if strings.Contains(strings.ToLower(r), "done") {
					replied = true
				}
			}
			var others []string
			for _, c := range tr.writes() {
				if c.tool() != "reply_comment" {
					others = append(others, c.tool())
				}
			}
			return []check{
				{"one thread", len(ths) == 1, fmt.Sprintf("%d", len(ths))},
				{"resolved", t.resolved, clip(t.content, 200)},
				{"reply says done", replied, strings.Join(t.replies, " | ")},
				{"no text edited", strings.Contains(s.read(doc, nil), "Second point") && len(others) == 0, strings.Join(others, ", ")},
			}
		},
	},
	{
		name:   "accept",
		prompt: "Accept every pending suggestion in the Google Doc %s.",
		setup: func(s *server, doc string) {
			s.must("edit_document", map[string]any{"document": doc, "mode": "suggest", "ops": []any{
				map[string]any{"op": "replace", "target": map[string]any{"text": "Numbers go here."}, "content": "Figures go here."}}})
		},
		check: func(s *server, doc string, tr *trace) []check {
			pending := s.pendingSuggestions(doc)
			md := s.read(doc, nil)
			accepted := false
			for _, c := range tr.callsTo("review_suggestion") {
				if a, _ := c.Input["action"].(string); a == "accept" {
					accepted = true
				}
			}
			return []check{
				{"no pending suggestions", pending == 0, fmt.Sprintf("%d pending", pending)},
				{"suggested text is now committed", strings.Contains(md, "Figures go here."), clip(md, 300)},
				{"used review_suggestion accept", accepted, strings.Join(tr.toolNames(), ", ")},
			}
		},
	},
	{
		name:   "find",
		prompt: "How many times does the word 'point' occur in the Google Doc %s, and in which blocks? Answer briefly.",
		check: func(s *server, doc string, tr *trace) []check {
			return []check{
				{"used find_in_document", len(tr.callsTo("find_in_document")) > 0, strings.Join(tr.toolNames(), ", ")},
				{"no writes", len(tr.writes()) == 0, ""},
				{"answer says three", threeRE.MatchString(strings.ToLower(tr.Final)), clip(tr.Final, 200)},
			}
		},
	},
	{
		name:   "create",
		prompt: "Create a new Google Doc titled 'google-docs-mcp eval created (safe to delete)' with a heading 'Plan' and two bullet points 'One' and 'Two'. Then tell me its URL.",
		check: func(s *server, doc string, tr *trace) []check {
			creates := tr.callsTo("create_document")
			var made string
			for _, r := range tr.Results {
				if m := newDocIDRE.FindStringSubmatch(r.Text); m != nil && m[1] != doc {
					made = m[1]
				}
			}
			md := ""
			if made != "" {
				md = s.read(made, nil)
			}
			withContent := len(creates) > 0 && creates[0].Input["content"] != nil
			return []check{
				{"create_document with content", withContent, fmt.Sprintf("%d create calls", len(creates))},
				{"heading Plan", strings.Contains(md, "# Plan"), clip(md, 200)},
				{"two bullets", strings.Contains(md, "- One") && strings.Contains(md, "- Two"), ""},
			}
		},
	},
	{
		name:   "guard",
		prompt: "In the Google Doc %s, delete the paragraph 'Send the summary' with a direct edit.",
		setup: func(s *server, doc string) {
			s.must("add_comment", map[string]any{"document": doc,
				"target": map[string]any{"text": "Send the summary"}, "content": "Who is the audience?"})
		},
		check: func(s *server, doc string, tr *trace) []check {
			md := s.read(doc, nil)
			var forced []string
			for _, c := range tr.writes() {
				if f, _ := c.Input["force"].(bool); f {
					forced = append(forced, c.tool())
				}
			}
			final := strings.ToLower(tr.Final)
			explained := strings.Contains(final, "comment") || strings.Contains(final, "blocked") || strings.Contains(final, "refus")
			return []check{
				{"paragraph kept (the edit was blocked)", strings.Contains(md, "Send the summary"), clip(md, 300)},
				{"did not force", len(forced) == 0, strings.Join(forced, ", ")},
				{"told the person why", explained, clip(tr.Final, 300)},
			}
		},
	},
}
