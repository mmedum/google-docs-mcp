package tools

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/v2/internal/render"
	"github.com/mmedum/google-docs-mcp/v2/internal/service"
)

// Asking the person (§12a). A write that cannot be undone through this
// server, or that destroys other people's work in a document, is put to the person through the
// client, as an MCP form elicitation, when the client declares it can
// ask. The form has no fields: accepting it is the confirmation. The
// question goes out the multi-round-trip way: the first call does every
// read, stops before the write and returns the question with a signed
// requestState; the call comes back with the answer and that state,
// reads again, and writes only on an accept. Clients on protocols before
// 2026-07-28 get the same through the SDK, which asks with
// elicitation/create and calls the handler again within the same
// request.

// askTTL is how long a question may wait for its answer when its state
// travels through the client, from 2026-07-28. An answer after it is
// refused, and the call is made again to ask again.
const askTTL = 5 * time.Minute

// inProcessTTL bounds a state that never leaves the process, before
// 2026-07-28: the request's own context bounds the wait, and this only
// how long its nonce is remembered.
const inProcessTTL = 24 * time.Hour

// statelessProtocol is the first revision whose client carries the
// requestState: the SDK's own test for asking the multi-round-trip way.
const statelessProtocol = "2026-07-28"

// askKey names the one input request.
const askKey = "confirm"

// emptyForm is the question's form: no fields, so the client's accept
// is the answer. The specification types properties as an open map with
// no minimum (§18).
var emptyForm = json.RawMessage(`{"type":"object","properties":{}}`)

// asking signs and redeems the requestState of every question this
// process asks, and knows which tools ask. The key is drawn per process,
// so a state is good only in the process that issued it, and each is
// redeemed at most once.
type asking struct {
	key []byte
	lg  *slog.Logger

	// tools are the tools registered to ask, and always those that ask
	// before every write rather than only when a condition holds. Both
	// are written only while the server is being built and read-only
	// once it serves.
	tools  map[string]bool
	always map[string]bool

	mu   sync.Mutex
	used map[string]int64 // nonce → when it expires, in Unix seconds
}

func newAsking(lg *slog.Logger) *asking {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &asking{key: key, lg: lg, used: map[string]int64{}, tools: map[string]bool{}, always: map[string]bool{}}
}

// asks reports whether the tool was registered to ask.
func (a *asking) asks(tool string) bool { return a.tools[tool] }

// askState is what a requestState carries. It binds the answer to the
// tool, to the call's arguments, and to what the question binds
// (render.Question.Bind).
type askState struct {
	Tool     string `json:"t"`
	Args     string `json:"a"`
	Question string `json:"q"`
	Nonce    string `json:"n"`
	Expires  int64  `json:"e"`
}

// sign is st as base64url JSON, a dot, and its HMAC-SHA256.
func (a *asking) sign(st askState) string {
	payload, _ := json.Marshal(st)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(a.mac(payload))
}

func (a *asking) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, a.key)
	m.Write(payload)
	return m.Sum(nil)
}

// redeem checks a requestState a client echoed and spends it. Every
// refusal is [blocked]: whatever the answer was, nothing is written on
// it.
func (a *asking) redeem(state, tool, args string, now time.Time) (askState, error) {
	var st askState
	enc := base64.RawURLEncoding
	p, s, ok := strings.Cut(state, ".")
	payload, err1 := enc.DecodeString(p)
	sum, err2 := enc.DecodeString(s)
	if !ok || err1 != nil || err2 != nil || !hmac.Equal(sum, a.mac(payload)) || json.Unmarshal(payload, &st) != nil {
		return st, service.Errorf("blocked",
			"the call came back with an answer to a question this server did not ask; nothing was changed. Call it again without one")
	}
	if st.Tool != tool || st.Args != args {
		return st, service.Errorf("blocked",
			"the answer came back with another call than the one the person was asked about; nothing was changed. Call it again to ask again")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for n, exp := range a.used {
		if now.Unix() > exp {
			delete(a.used, n)
		}
	}
	if now.Unix() > st.Expires {
		return st, service.Errorf("blocked",
			"the question to the person expired before the answer came back; nothing was changed. Call it again to ask again")
	}
	if _, spent := a.used[st.Nonce]; spent {
		return st, service.Errorf("blocked", "that answer was already used once; nothing was changed")
	}
	a.used[st.Nonce] = st.Expires
	return st, nil
}

// person is one call's way to the person: the service asks it before an
// asking write.
type person struct {
	a       *asking
	tool    string
	in      any
	canAsk  bool
	require bool
	// travels is whether the requestState goes through the client,
	// which is when askTTL applies.
	travels bool
	now     time.Time
	// argSum is the arguments' hash, computed when first needed.
	argSum string

	// answer is set when the call came back with a question this
	// process asked, verified and accepted.
	answer *answer
	// asked is set when the service asked and the question is to go out.
	asked *render.Question
}

type answer struct {
	question string
}

// errAsking stops the service before its write while the question goes
// out. It becomes the input request and never reaches a caller.
var errAsking = service.Errorf("blocked", "the person has not answered yet; nothing was changed")

// args is the hash of the call's arguments, as the handler decoded them.
func (p *person) args() string {
	if p.argSum == "" {
		raw, _ := json.Marshal(p.in)
		p.argSum = render.Sum(string(raw))
	}
	return p.argSum
}

// canAsk reports whether a client can put a question to the person. Form
// is what an empty elicitation capability declares; only a client that
// declares URL alone cannot show a form.
func canAsk(c *mcp.ClientCapabilities) bool {
	return c != nil && c.Elicitation != nil && (c.Elicitation.Form != nil || c.Elicitation.URL == nil)
}

// personFor sets up one call. A call that carries answers must also
// carry the requestState they belong to, which must be one this process
// issued for this tool and these arguments, unexpired and unspent: a
// client could otherwise answer a question before it was asked. Any
// answer but accept is refused here, before anything runs.
func (a *asking) personFor(req *mcp.CallToolRequest, tool string, in any, require bool) (*person, error) {
	p := &person{a: a, tool: tool, in: in, require: require, now: time.Now(), travels: true}
	if req == nil {
		return p, nil
	}
	p.canAsk = canAsk(req.ClientCapabilities())
	if req.Session != nil {
		if ip := req.Session.InitializeParams(); ip != nil {
			p.travels = ip.ProtocolVersion >= statelessProtocol
		}
	}
	var state string
	var responses mcp.InputResponseMap
	if req.Params != nil {
		state, responses = req.Params.RequestState, req.Params.InputResponses
	}
	switch {
	case state == "" && len(responses) == 0:
		return p, nil
	case state == "":
		return nil, service.Errorf("blocked",
			"the call came with answers to a question this server has not asked; nothing was changed. Call it again without them")
	}
	st, err := a.redeem(state, tool, p.args(), p.now)
	if err != nil {
		return nil, err
	}
	action := "none"
	if r, ok := responses[askKey].(*mcp.ElicitResult); ok && r != nil {
		switch r.Action {
		case "accept", "decline", "cancel":
			action = r.Action
		default:
			action = "other"
		}
	}
	a.lg.Info("person_answered", "tool", tool, "answer", action)
	if action != "accept" {
		// Refused here, before anything runs: a write whose question
		// depends on what the document holds may not reach its question on this
		// round, and a decline must stop it all the same.
		return nil, service.Errorf("blocked", "%s was not confirmed by the person: the client answered %s. "+
			"Nothing was changed. Do not call it again unless the person asks for it", tool, action)
	}
	p.answer = &answer{question: st.Question}
	return p, nil
}

// Asks implements service.Asker.
func (p *person) Asks() bool { return p.answer != nil || p.canAsk || p.require }

// Ask implements service.Asker.
//
// An accepted answer covers the question it was given for, as often as
// the call reaches it: a write re-planned after a conflict asks again,
// and the same question is still confirmed. A different one is not.
func (p *person) Ask(ctx context.Context, q render.Question) error {
	q = q.For(p.tool)
	if ans := p.answer; ans != nil {
		if ans.question != questionSum(q) {
			return service.Errorf("blocked", "what %s would do changed after the person was asked, so what "+
				"they saw is not what would be written; nothing was changed. Call it again to ask again", p.tool)
		}
		// From here the write may happen whether or not this round
		// returns, so a failure to reply is no longer "nothing was
		// changed".
		setStage(ctx, stageWriting)
		return nil
	}
	if !p.canAsk {
		if p.require {
			return service.Errorf("blocked", "%s needs the person to confirm it, and this client cannot "+
				"ask them; GDOCS_REQUIRE_PROMPT is set, so nothing was changed. Use a client that supports MCP "+
				"elicitation", p.tool)
		}
		return nil
	}
	p.asked = &q
	return errAsking
}

// inputRequest is the question as the result that asks it.
func (p *person) inputRequest() *mcp.CallToolResult {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	ttl := askTTL
	if !p.travels {
		ttl = inProcessTTL
	}
	st := askState{Tool: p.tool, Args: p.args(), Question: questionSum(*p.asked), Nonce: hex.EncodeToString(nonce),
		Expires: p.now.Add(ttl).Unix()}
	p.a.lg.Info("person_asked", "tool", p.tool)
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{askKey: &mcp.ElicitParams{
			Mode: "form", Message: p.asked.Text, RequestedSchema: emptyForm,
		}},
		RequestState: p.a.sign(st),
	}
}

// questionSum binds a state to what the question binds.
func questionSum(q render.Question) string { return render.Sum(q.Bind) }

// asked wraps the handler of a tool that asks the person before its
// write. It is the only way to install an asker, so a service write that
// asks, reached from any other tool, is refused as a defect rather than
// made unasked.
func asked[In, Out any](d Deps, name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	d.asking.tools[name] = true
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		p, err := d.asking.personFor(req, name, in, d.Config.RequirePrompt)
		if err != nil {
			return nil, zero, fail(err)
		}
		ctx = service.WithAsker(ctx, p)
		res, out, err := h(ctx, req, in)
		if p.asked != nil {
			// The service stopped before its write; the question goes out.
			setStage(ctx, stageWaiting)
			return p.inputRequest(), zero, nil
		}
		if err == nil && stageOf(ctx) == stageWriting {
			setStage(ctx, stageWritten)
		}
		return res, out, err
	}
}

// asksFirst closes the description of a tool that asks the person before
// its write, and asksWhen that of one that asks only when cond holds.
const asksFirst = " Before the write, the server asks the person through the client when it can" + asksTail

func asksWhen(cond string) string {
	return " " + cond + ", the server asks the person through the client first when it can" + asksTail
}

// asksTail is what both say about a client that cannot ask, and about a
// call the person did not confirm.
const asksTail = "; when the client cannot ask, ask the person yourself before calling. A call they did not confirm is " +
	"[blocked], and is not made again unless they ask."

// addAsking registers a tool that asks the person before its write: its
// description says so — only when cond holds, if one is given — and its
// handler gets the person to ask. The two halves come from one call so
// neither can be left out.
func addAsking[In, Out any](s *mcp.Server, d Deps, t *mcp.Tool, cond string, h mcp.ToolHandlerFor[In, Out]) {
	if cond == "" {
		t.Description += asksFirst
		d.asking.always[t.Name] = true
	} else {
		t.Description += asksWhen(cond)
	}
	mcp.AddTool(s, t, asked(d, t.Name, h))
}

// interactionKey is Claude Code's mark for a tool it prompts for on
// every call, even under an allow rule.
const interactionKey = "anthropic/requiresUserInteraction"

// interactionHint is receiving middleware for tools/list. A tool that
// asks the person before every write loses the mark when the client can
// ask: the server's question is the confirmation then, and it shows what
// the write destroys, where the client's prompt shows the arguments. The
// mark stays for a client that cannot ask, whose own prompt is then the
// only one. destructiveHint stays either way, as the client's soft gate.
// With both, the person would answer twice for one call.
func interactionHint(a *asking) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			list, ok := res.(*mcp.ListToolsResult)
			if err != nil || !ok {
				return res, err
			}
			lr, ok := req.(*mcp.ListToolsRequest)
			if !ok || !canAsk(lr.ClientCapabilities()) {
				return res, err
			}
			// The tools are the server's own; copy before changing one.
			out := *list
			out.Tools = make([]*mcp.Tool, len(list.Tools))
			for i, t := range list.Tools {
				out.Tools[i] = t
				if _, marked := t.Meta[interactionKey]; marked && a.always[t.Name] {
					c := *t
					c.Meta = maps.Clone(t.Meta)
					delete(c.Meta, interactionKey)
					if len(c.Meta) == 0 {
						c.Meta = nil
					}
					out.Tools[i] = &c
				}
			}
			return &out, nil
		}
	}
}

// The stages of one tools/call that asked, for askFailures: what a
// failure to reply means depends on how far the call got.
const (
	stageNone    int32 = iota
	stageWaiting       // the question is out; nothing is written before the answer
	stageWriting       // the answer confirmed the write, which may have happened
	stageWritten       // the handler returned after writing
)

type stageKey struct{}

func setStage(ctx context.Context, s int32) {
	if v, ok := ctx.Value(stageKey{}).(*atomic.Int32); ok {
		v.Store(s)
	}
}

func stageOf(ctx context.Context) int32 {
	if v, ok := ctx.Value(stageKey{}).(*atomic.Int32); ok {
		return v.Load()
	}
	return stageNone
}

// askFailures is receiving middleware for every tools/call. A call to a
// tool that asks nothing, carrying an answer, is refused before it runs.
// A call that asked the person and then failed as a JSON-RPC error
// rather than a tool result becomes a tool error: on protocols before
// 2026-07-28 the SDK asks with elicitation/create itself, and a client
// that answers with an error, or an answer that does not fit the form,
// fails the whole call that way. That call wrote nothing, since its
// write waits for the answer, and it becomes [blocked]. A failure after
// the answer confirmed the write — the reply could not be built or sent,
// the request was canceled — may follow a write, so it becomes
// [ambiguous_outcome] and is never "nothing was changed". The client's
// error text is not repeated.
func askFailures(a *asking) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if !ok {
				return next(ctx, method, req)
			}
			name := "the call"
			if call.Params != nil && toolName(call.Params.Name) {
				name = call.Params.Name
			}
			if p := call.Params; p != nil && (p.RequestState != "" || len(p.InputResponses) > 0) && !a.asks(p.Name) {
				return errorResult(service.Errorf("blocked", "%s is not a tool here that asks the person, "+
					"and the call came with an answer; nothing was done. Call it again without one", name)), nil
			}
			stage := &atomic.Int32{}
			res, err := next(context.WithValue(ctx, stageKey{}, stage), method, req)
			if err == nil || stage.Load() == stageNone {
				return res, err
			}
			var e error
			switch stage.Load() {
			case stageWaiting:
				e = service.Errorf("blocked", "%s was not confirmed by the person: the client could not "+
					"put the question to them, or its answer could not be read. Nothing was changed", name)
			case stageWritten:
				e = service.Errorf("ambiguous_outcome", "the person confirmed %s, and it was written (verdict: "+
					"written), but its result could not be returned. Do not make the call again", name)
			default:
				e = service.Errorf("ambiguous_outcome", "the person confirmed %s, and the server went on to "+
					"write, but the call ended before its result (verdict: unknown). Do not make the call again; "+
					"read what it acted on to see whether it took effect", name)
			}
			return errorResult(e), nil
		}
	}
}

// errorResult is err as a tool result, the way the SDK makes one from a
// handler's error.
func errorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(fail(err))
	return res
}

// toolName is true for a name shaped like this server's tools, which a
// message may repeat; anything else came from the client.
func toolName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
