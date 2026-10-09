package server_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/v2/internal/config"
	"github.com/mmedum/google-docs-mcp/v2/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/tools"
)

// The protocols a question goes out on: before 2026-07-28 the SDK asks
// with elicitation/create inside the call; from it, the call returns the
// question and comes back with the answer (§12a).
var protocols = []string{"2025-06-18", "2025-11-25", "2026-07-28"}

// answerer answers the questions a test client is asked, and keeps them.
type answerer struct {
	mu        sync.Mutex
	questions []*mcp.ElicitParams
	action    string
}

func (p *answerer) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, req.Params)
	return &mcp.ElicitResult{Action: p.action}, nil
}

func (p *answerer) asked() []*mcp.ElicitParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.questions)
}

// askFixture is the fake every asking case reaches its write in: a
// comment on a paragraph, one on a table row, a thread with a reply, and
// the inline image carrying a pending suggestion.
func askFixture(t *testing.T) *fakeAPI {
	t.Helper()
	raw := strings.Replace(string(doctest.RawFixture(t)), `"inlineObjectId": "kix.img1", "textStyle": {}}`,
		`"inlineObjectId": "kix.img1", "textStyle": {}, "suggestedInsertionIds": ["s9"]}`, 1)
	return &fakeAPI{raw: []byte(raw), comments: []*gapi.DriveComment{
		{ID: "dc1", Content: "hmm", QuotedFileContent: &gapi.QuotedText{Value: "Second point"}},
		{ID: "c1", Content: "check the figures", Author: &gapi.User{DisplayName: "Ann"},
			QuotedFileContent: &gapi.QuotedText{Value: "Alpha"},
			Replies:           []*gapi.DriveReply{{ID: "r1", Content: "agreed"}}},
	}}
}

// everything is every tool.
func everything() config.Config {
	return tools.FullSurface(config.Config{DefaultWriteMode: config.WriteDirect})
}

// connectAsking connects a client on protocol to a server over a fresh
// fake. A nil p declares no elicitation; opts adjust the client further.
func connectAsking(t *testing.T, cfg config.Config, protocol string, p *answerer, opts ...func(*mcp.ClientOptions)) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	api := askFixture(t)
	o := &mcp.ClientOptions{}
	if p != nil {
		o.ElicitationHandler = p.handle
	}
	for _, fn := range opts {
		fn(o)
	}
	return connectClient(t, api, cfg, protocol, o), api
}

// askCase is a call that clears a tool's own guards and reaches its
// write, how the fake counts that write, and words its question must
// carry.
type askCase struct {
	args   map[string]any
	writes func(*fakeAPI) int
	shows  []string
}

func batches(api *fakeAPI) int { return len(api.batches) }
func deletes(api *fakeAPI) int { return len(api.deleted) }

var askCases = map[string]askCase{
	"delete_tab": {
		args:   map[string]any{"document": fixtureID, "tab": "Notes", "confirm_tab": "Notes"},
		writes: batches,
		shows:  []string{"delete the tab `Notes` of `Quarterly Report`, with everything in it", "version history"},
	},
	"delete_comment": {
		args:   map[string]any{"document": fixtureID, "comment_id": "c1", "confirm_comment_id": "c1"},
		writes: deletes,
		shows:  []string{"delete a comment thread on `Quarterly Report`", "by `Ann`", "`check the figures`", "1 reply"},
	},
	"edit_document": {
		args: map[string]any{"document": fixtureID, "mode": "direct", "force": true,
			"ops": []any{map[string]any{"op": "delete", "target": map[string]any{"handle": "p7"}}}},
		writes: batches,
		shows:  []string{"run a forced edit on `Quarterly Report`", "destroys 1 comment (dc1)"},
	},
	"edit_table": {
		args: map[string]any{"document": fixtureID, "mode": "direct", "force": true,
			"ops": []any{map[string]any{"op": "delete_rows", "table": "tbl1", "row_numbers": []any{2}}}},
		writes: batches,
		shows:  []string{"edit_table: run a forced edit", "destroys 1 comment (c1)"},
	},
	"insert_object": {
		args:   map[string]any{"document": fixtureID, "action": "delete", "object": "kix.img1", "mode": "direct", "force": true},
		writes: batches,
		shows:  []string{"insert_object: run a forced edit", "suggestion (s9)"},
	},
	"review_suggestion": {
		args:   map[string]any{"document": fixtureID, "action": "accept", "all": true},
		writes: batches,
		shows:  []string{"accept all 2 pending suggestions in `Quarterly Report`", "accepting applies it"},
	},
}

func callTool(t *testing.T, cs *mcp.ClientSession, p *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), p)
	if err != nil {
		t.Fatalf("calling %s: %v", p.Name, err)
	}
	return res
}

// Declined, nothing is written; accepted, the write is made once. On
// every protocol, for every tool that asks, and the question says what
// the write would do.
func TestEveryAskingWriteWaitsForThePerson(t *testing.T) {
	for name, c := range askCases {
		for _, protocol := range protocols {
			for _, action := range []string{"decline", "cancel", "accept"} {
				p := &answerer{action: action}
				cs, api := connectAsking(t, everything(), protocol, p)
				res := callTool(t, cs, &mcp.CallToolParams{Name: name, Arguments: maps.Clone(c.args)})
				out := textOf(res)
				qs := p.asked()
				if len(qs) != 1 {
					t.Fatalf("%s %s %s: asked %d times: %s", name, protocol, action, len(qs), out)
				}
				for _, want := range c.shows {
					if !strings.Contains(qs[0].Message, want) {
						t.Errorf("%s: the question does not say %q:\n%s", name, want, qs[0].Message)
					}
				}
				n := c.writes(api)
				if action != "accept" {
					if !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") || n != 0 {
						t.Errorf("%s %s %s: %d writes: %s", name, protocol, action, n, out)
					}
					continue
				}
				if res.IsError || n != 1 {
					t.Errorf("%s %s accepted: %d writes: %s", name, protocol, n, out)
				}
			}
		}
	}
}

// The tools that ask are found by asking each registered tool: a call
// that carries an answer is refused by the middleware for a tool that
// asks nothing, and reaches the tool for one that does. Every one of
// them has a case, and every case is one of them.
func TestEveryAskingToolHasACase(t *testing.T) {
	cs, api := connectAsking(t, everything(), "2026-07-28", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) < 20 {
		t.Fatalf("%d tools listed; the whole surface was not registered", len(res.Tools))
	}
	asking := map[string]bool{}
	for _, tool := range res.Tools {
		out := textOf(callTool(t, cs, &mcp.CallToolParams{Name: tool.Name, Arguments: map[string]any{},
			InputResponses: accepted}))
		if !strings.Contains(out, "not a tool here that asks the person") {
			asking[tool.Name] = true
		}
	}
	if len(asking) < 6 {
		t.Fatalf("found %d asking tools", len(asking))
	}
	for name := range asking {
		if _, ok := askCases[name]; !ok {
			t.Errorf("%s asks the person and has no asking case", name)
		}
	}
	for name := range askCases {
		if !asking[name] {
			t.Errorf("%s has an asking case and does not ask", name)
		}
	}
	if batches(api)+deletes(api) != 0 {
		t.Error("a call that carried an unasked answer wrote")
	}
}

// A client that cannot ask gets no question, and the arguments are the
// guard; GDOCS_REQUIRE_PROMPT refuses the write instead.
func TestAClientThatCannotAsk(t *testing.T) {
	for _, require := range []bool{false, true} {
		cfg := everything()
		cfg.RequirePrompt = require
		cs, api := connectAsking(t, cfg, "", nil)
		c := askCases["delete_tab"]
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_tab", Arguments: maps.Clone(c.args)})
		n := c.writes(api)
		switch {
		case require && (!res.IsError || !strings.Contains(textOf(res), "GDOCS_REQUIRE_PROMPT") || n != 0):
			t.Errorf("required: %d writes: %s", n, textOf(res))
		case !require && (res.IsError || n != 1):
			t.Errorf("not required: %d writes: %s", n, textOf(res))
		}
	}
}

// A dry run, an edit that force lets destroy nothing, and a review of
// named suggestions ask nothing.
func TestWhatAsksNothing(t *testing.T) {
	p := &answerer{action: "decline"}
	cs, api := connectAsking(t, everything(), "2026-07-28", p)
	calls := []*mcp.CallToolParams{
		{Name: "edit_document", Arguments: map[string]any{"document": fixtureID, "mode": "direct", "force": true,
			"ops": []any{map[string]any{"op": "append", "content": "More."}}}},
		{Name: "review_suggestion", Arguments: map[string]any{"document": fixtureID, "action": "reject", "ids": []any{"s1"}}},
	}
	for name, c := range askCases {
		args := maps.Clone(c.args)
		args["dry_run"] = true
		calls = append(calls, &mcp.CallToolParams{Name: name, Arguments: args})
	}
	for _, call := range calls {
		if res := callTool(t, cs, call); res.IsError {
			t.Errorf("%s %v: %s", call.Name, call.Arguments, textOf(res))
		}
	}
	if qs := p.asked(); len(qs) != 0 {
		t.Errorf("asked %d questions: %s", len(qs), qs[0].Message)
	}
	if batches(api) != 2 || deletes(api) != 0 {
		t.Errorf("%d batches and %d deletes; the two writes that ask nothing make one each, and a dry run none",
			batches(api), deletes(api))
	}
}

// mrtr connects a 2026-07-28 client that hands each question back
// instead of answering it, so a test can answer by hand.
func mrtr(t *testing.T) (*mcp.ClientSession, *fakeAPI) {
	t.Helper()
	return connectAsking(t, everything(), "2026-07-28", &answerer{action: "accept"}, func(o *mcp.ClientOptions) {
		o.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	})
}

var accepted = mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: "accept"}}

// The first round only asks. The answer counts once, only with the state
// it was asked with, only for that call, and only while fresh.
func TestTheAnswerIsBoundToItsQuestion(t *testing.T) {
	cs, api := mrtr(t)
	c := askCases["delete_tab"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_tab", Arguments: c.args})
	q, ok := first.InputRequests["confirm"].(*mcp.ElicitParams)
	if !first.NeedsInput() || !ok || q.Mode != "form" || first.RequestState == "" || c.writes(api) != 0 {
		t.Fatalf("first round %+v; %d writes", first, c.writes(api))
	}
	state := first.RequestState

	blocked := func(p *mcp.CallToolParams, want string) {
		t.Helper()
		res := callTool(t, cs, p)
		if out := textOf(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, want) {
			t.Errorf("%s", out)
		}
	}
	other := map[string]any{"document": fixtureID, "tab": "Main", "confirm_tab": "Main"}
	blocked(&mcp.CallToolParams{Name: "delete_tab", Arguments: c.args, InputResponses: accepted},
		"answers to a question this server has not asked")
	blocked(&mcp.CallToolParams{Name: "delete_tab", Arguments: c.args, InputResponses: accepted, RequestState: state + "x"},
		"did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_tab", Arguments: c.args, InputResponses: accepted,
		RequestState: "e30." + strings.Split(state, ".")[1]}, "did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_tab", Arguments: other, InputResponses: accepted, RequestState: state},
		"another call")
	blocked(&mcp.CallToolParams{Name: "delete_comment", Arguments: askCases["delete_comment"].args, InputResponses: accepted,
		RequestState: state}, "another call")
	blocked(&mcp.CallToolParams{Name: "list_comments", Arguments: map[string]any{"document": fixtureID},
		InputResponses: accepted, RequestState: state}, "not a tool here that asks the person")
	if c.writes(api) != 0 || deletes(api) != 0 {
		t.Fatalf("%d writes before the answer", c.writes(api))
	}

	done := callTool(t, cs, &mcp.CallToolParams{Name: "delete_tab", Arguments: c.args, InputResponses: accepted, RequestState: state})
	if done.IsError || c.writes(api) != 1 {
		t.Fatalf("the verified retry: %s; %d writes", textOf(done), c.writes(api))
	}
	blocked(&mcp.CallToolParams{Name: "delete_tab", Arguments: c.args, InputResponses: accepted, RequestState: state}, "already used")
}

// What the person saw is what is written: a comment edited between the
// question and the answer is refused, and the next call asks again.
func TestAChangeAfterTheQuestionIsRefused(t *testing.T) {
	cs, api := mrtr(t)
	c := askCases["delete_comment"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: c.args})
	api.comments[1].Content = "something else entirely"
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: c.args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := textOf(res); !res.IsError || !strings.Contains(out, "changed after the person was asked") || c.writes(api) != 0 {
		t.Errorf("%s; %d writes", out, c.writes(api))
	}
}

// Any answer but an accept is refused before the call reads anything.
func TestARefusalIsRefusedBeforeTheWrite(t *testing.T) {
	for _, action := range []string{"decline", "cancel", "maybe"} {
		cs, api := mrtr(t)
		c := askCases["delete_comment"]
		first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: c.args})
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: c.args, RequestState: first.RequestState,
			InputResponses: mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: action}}})
		if out := textOf(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") || c.writes(api) != 0 {
			t.Errorf("%s: %s", action, out)
		}
	}
}

// Before 2026-07-28 the state stays in the process and the request's own
// context bounds the wait, so an accept inside the call is never late.
func TestAnAnswerInTheProcessIsNeverLate(t *testing.T) {
	c := askCases["delete_tab"]
	for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
		cs, api := connectAsking(t, everything(), protocol, &answerer{action: "accept"})
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_tab", Arguments: maps.Clone(c.args)})
		if res.IsError || c.writes(api) != 1 {
			t.Errorf("%s: an accept in the process was refused: %s", protocol, textOf(res))
		}
	}
}

// A forced edit whose batch meets a revision conflict after the person
// accepted is re-planned once, asks the same question, and goes through
// on the same answer.
func TestAConflictAfterTheAnswerReplansOnIt(t *testing.T) {
	cs, api := mrtr(t)
	c := askCases["edit_document"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "edit_document", Arguments: c.args})
	api.batchErrs = []error{&gapi.APIError{Status: 400, Message: "The provided revision id does not match the current revision"}}
	res := callTool(t, cs, &mcp.CallToolParams{Name: "edit_document", Arguments: c.args, InputResponses: accepted,
		RequestState: first.RequestState})
	if res.IsError || len(api.batches) != 2 {
		t.Errorf("%d batches: %s", len(api.batches), textOf(res))
	}
}

// A forced op in a later batch, after a grid change was written, is
// refused rather than asked about too late; the first batch asks nothing.
func TestAForcedOpInALaterBatchIsRefused(t *testing.T) {
	p := &answerer{action: "accept"}
	cs, api := connectAsking(t, everything(), "2026-07-28", p)
	res := callTool(t, cs, &mcp.CallToolParams{Name: "edit_table", Arguments: map[string]any{"document": fixtureID,
		"mode": "direct", "force": true, "ops": []any{
			map[string]any{"op": "insert_rows", "table": "tbl1", "row": 1, "count": 1},
			// The fake applies nothing, so the commented row is still row 2.
			map[string]any{"op": "delete_rows", "table": "tbl1", "row_numbers": []any{2}}}}})
	if out := textOf(res); len(p.asked()) != 0 || !strings.Contains(out, "runs in a later batch") || len(api.batches) != 1 {
		t.Errorf("asked %d, %d batches: %s", len(p.asked()), len(api.batches), out)
	}
}

// A tool that asks the person before every write carries Claude Code's
// requiresUserInteraction mark only for a client that cannot ask. With
// both, Claude Code put two prompts in front of every delete. Each
// protocol lists on one server, the client that can ask first, so a mark
// dropped from the server's own tool rather than from a copy shows up
// for the clients after it.
func TestTheMarkIsForAClientThatCannotAsk(t *testing.T) {
	asks := (&answerer{action: "accept"}).handle
	urlAlone := &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	for _, protocol := range protocols {
		srv := newServer(askFixture(t), everything())
		for _, tc := range []struct {
			name   string
			o      *mcp.ClientOptions
			marked bool
		}{
			{"form", &mcp.ClientOptions{ElicitationHandler: asks}, false},
			{"url alone", &mcp.ClientOptions{ElicitationHandler: asks, Capabilities: urlAlone}, true},
			{"no elicitation", &mcp.ClientOptions{}, true},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				res, err := connectTo(t, srv, protocol, tc.o).ListTools(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				seen := 0
				for _, tool := range res.Tools {
					if tool.Name != "delete_comment" && tool.Name != "delete_tab" {
						continue
					}
					seen++
					if a := tool.Annotations; a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
						t.Errorf("%s lost destructiveHint: %+v", tool.Name, a)
					}
					if marked := tool.Meta["anthropic/requiresUserInteraction"] == true; marked != tc.marked {
						t.Errorf("%s marked %t, want %t", tool.Name, marked, tc.marked)
					}
				}
				if seen != 2 {
					t.Errorf("listed %d of delete_comment and delete_tab, want 2", seen)
				}
			})
		}
	}
}
