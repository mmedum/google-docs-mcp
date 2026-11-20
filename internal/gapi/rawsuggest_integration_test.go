//go:build integration

package gapi

// What the API does with a suggested formatting change (#46). Runs
// against the caller's own account and creates clearly named scratch
// documents, one per case so no case disturbs another.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawSuggestedStyle -v
//
// It reads the untouched HTTP body rather than going through
// documents.get's decoder, which is the point: the decoder is this
// server's own wire types, which may lack the field in question.
// read_document format: raw re-marshals those types, so it cannot tell
// "Google recorded nothing" from "we drop it on the way out".
//
// Findings:
//
//   - A SUGGEST-mode updateTextStyle DOES record a suggestion:
//     suggestedTextStyleChanges keyed by suggestion id, with a
//     textStyleSuggestionState naming the properties. The style beside it
//     is the style as it would be once accepted, every inherited
//     property filled in, so only the state says what changed.
//   - A direct updateTextStyle over a suggested INSERTION applies
//     normally, whether the range covers the inserted run exactly, part
//     of it, or straddles it and real text. A pending insertion does not
//     block the write.
//   - A direct change IS silently dropped when a pending suggestion on
//     the range already suggests the same property AND value: bold over
//     a suggested bold, alignment CENTER over a suggested CENTER. The
//     batch is accepted and its reply is empty.
//   - The rule follows the property (TestRawSuggestedStyleKinds, read
//     against TestRawSuggestedStyleKindControls, twice): at the
//     suggested value, bold and every paragraph property tried
//     (alignment, lineSpacing, spaceAbove) are dropped, while italic,
//     underline, strikethrough, smallCaps, baselineOffset,
//     foregroundColor, weightedFontFamily and fontSize land. A value
//     the suggestion does not name lands for baselineOffset,
//     foregroundColor, weightedFontFamily and fontSize
//     (TestRawSuggestedStyleKindOtherValue, TestRawSuggestedStyleValue)
//     and for alignment. A boolean set false and a reset are not
//     probed, for the reason that follows. One case is deliberately
//     absent: bold false over a suggested bold proves nothing, because
//     on unbolded text it is indistinguishable from no change at all.
//
//   - A suggested insertion in a table cell is the same as in the body.
//     A suggested row carries its id on the row, every cell of it and
//     every cell's newline (TestRawSuggestedTableInsertions).
//   - A reset over inserted text joins the insertion's id unless the
//     text fills its paragraph. Then Google extends the reset to the
//     paragraph mark, outside the insertion, and files it under a second
//     id (TestRawSuggestedResetWholeParagraph).
//
// The restyle point is why the server warns there rather than refusing:
// see guardRestyle in internal/plan/ops.go.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v4/internal/auth"
	"github.com/mmedum/google-docs-mcp/v4/internal/credentials"
	"github.com/mmedum/google-docs-mcp/v4/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v4/internal/userconfig"
)

func probeClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("GDOCS_INTEGRATION") == "" {
		t.Skip("set GDOCS_INTEGRATION=1 to run against Google")
	}
	uc, err := userconfig.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := auth.LoadClientSecret(uc.ClientSecretPath, auth.Scopes(false))
	if err != nil {
		t.Fatal(err)
	}
	tokenFile, _ := userconfig.TokenFilePath("default")
	store := &credentials.Store{Profile: "default", Keyring: credentials.OSKeyring(), FilePath: tokenFile}
	rt, _, err := store.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return New(auth.TokenSource(context.Background(), oc, rt, 60*time.Second), Options{Timeout: 60 * time.Second, UserAgent: "google-docs-mcp/probe"})
}

func TestRawSuggestedStyle(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()

	created, err := c.CreateDocument(ctx, "google-docs-mcp raw suggested-style probe (safe to delete)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.DocumentID

	if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{
		Requests: []json.RawMessage{insertAt(t, "Clean sentence here.\n", 1)}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The whole question, asked as plainly as it can be asked: a
	// SUGGEST-mode updateTextStyle over clean, untouched text.
	res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{
		Requests:     []json.RawMessage{textStyle(t, 1, 6, "bold", true)},
		WriteControl: suggestMode,
	})
	if err != nil {
		t.Fatalf("suggest-mode updateTextStyle: %v", err)
	}
	reply, _ := json.Marshal(res)
	t.Logf("--- batchUpdate reply for the SUGGEST-mode updateTextStyle ---\n%s", reply)

	// The untouched GET body, in both view modes.
	for _, view := range []string{SuggestionsInline, SuggestionsPreviewAccepted} {
		q := url.Values{}
		q.Set("includeTabsContent", "true")
		q.Set("suggestionsViewMode", view)
		body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?"+q.Encode(), nil)
		if err != nil {
			t.Fatalf("get %s: %v", view, err)
		}
		raw := string(body)
		t.Logf("--- %s: suggestedTextStyleChanges present: %t; textStyleSuggestionState present: %t ---",
			view, strings.Contains(raw, "suggestedTextStyleChanges"), strings.Contains(raw, "textStyleSuggestionState"))
		if i := strings.Index(raw, `"Clean`); i >= 0 {
			t.Logf("%s window:\n%s", view, raw[max(0, i-900):min(len(raw), i+900)])
		}
	}
	t.Logf("=== scratch document left behind; Drive search title:\"safe to delete\" finds it ===")
}

// --- the shared probe machinery ---------------------------------------
//
// Every round after the first has the same shape: one scratch document,
// a paragraph of seed text, a few batchUpdates, then the untouched GET
// body logged as runs. So the rounds are tables and this is the driver;
// a new question is a row, not another copy of the driver.

// suggestMode is the write control that turns a batch into a suggestion.
var suggestMode = &WriteControl{WriteMode: "SUGGEST"}

// round is one batchUpdate: its requests, and the mode it goes in.
type round struct {
	reqs []json.RawMessage
	wc   *WriteControl
}

// probe is one question: a seed paragraph and the batches to apply to it.
type probe struct {
	name, seed string
	rounds     []round
}

func marshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func insertAt(t *testing.T, text string, index int) json.RawMessage {
	return marshal(t, map[string]any{"insertText": map[string]any{
		"text": text, "location": map[string]any{"index": index}}})
}

func deleteRange(t *testing.T, start, end int) json.RawMessage {
	return marshal(t, map[string]any{"deleteContentRange": map[string]any{
		"range": map[string]any{"startIndex": start, "endIndex": end}}})
}

// textStyle sets one property over a range, with the fields mask naming
// exactly it — which is what makes a collision with a pending suggestion
// unambiguous.
func textStyle(t *testing.T, start, end int, prop string, value any) json.RawMessage {
	return marshal(t, map[string]any{"updateTextStyle": map[string]any{
		"range":     map[string]any{"startIndex": start, "endIndex": end},
		"textStyle": map[string]any{prop: value},
		"fields":    prop,
	}})
}

func paraStyle(t *testing.T, start, end int, prop string, value any) json.RawMessage {
	return marshal(t, map[string]any{"updateParagraphStyle": map[string]any{
		"range":          map[string]any{"startIndex": start, "endIndex": end},
		"paragraphStyle": map[string]any{prop: value},
		"fields":         prop,
	}})
}

func pt(v float64) map[string]any { return map[string]any{"magnitude": v, "unit": "PT"} }

// runProbes applies each probe to a scratch document of its own, logs
// the runs the API reports afterwards, and returns them by probe name.
func runProbes(t *testing.T, probes []probe) map[string]string {
	t.Helper()
	c := probeClient(t)
	ctx := context.Background()
	got := make(map[string]string, len(probes))
	for _, p := range probes {
		id := probeDoc(t, c, ctx, p.name, "google-docs-mcp suggested-style probe (safe to delete)",
			[]json.RawMessage{insertAt(t, p.seed, 1)}, p.rounds)
		got[p.name] = rawElements(t, c, ctx, id)
		t.Logf("--- %s ---\n%s", p.name, got[p.name])
		// Google's own answer to what the suggestions will do.
		t.Logf("--- %s accepted ---\n%s", p.name, rawView(t, c, ctx, id, SuggestionsPreviewAccepted))
	}
	t.Logf("=== %d scratch documents left behind; Drive search title:\"safe to delete\" finds them ===", len(probes))
	return got
}

// assertLanded fails unless each probe's run carries its property in its
// own textStyle, which is what the direct change landing looks like. The
// suggestion's textStyle lists every property from backgroundColor on,
// so the match cannot come from it. landsOverASuggestion in
// internal/plan/ops.go depends on this.
func assertLanded(t *testing.T, got map[string]string, props map[string]string) {
	t.Helper()
	for name, prop := range props {
		if !strings.Contains(got[name], `"textStyle":{"`+prop+`"`) {
			t.Errorf("%s: the direct %s did not land; take it out of landsOverASuggestion", name, prop)
		}
	}
}

// probeDoc makes one scratch document under title, sends the seed batch
// and then each round, logging the replies, and returns its id.
func probeDoc(t *testing.T, c *Client, ctx context.Context, name, title string, seed []json.RawMessage, rounds []round) string {
	t.Helper()
	created, err := c.CreateDocument(ctx, title)
	if err != nil {
		t.Fatalf("%s: create: %v", name, err)
	}
	id := created.DocumentID
	if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: seed}); err != nil {
		t.Fatalf("%s: seed: %v", name, err)
	}
	for i, r := range rounds {
		res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: r.reqs, WriteControl: r.wc})
		if err != nil {
			t.Fatalf("%s: round %d: %v", name, i, err)
		}
		// An empty reply is the tell: the batch was accepted and the
		// API is saying nothing about what it did.
		reply, _ := json.Marshal(res.Replies)
		t.Logf("%s: round %d replies: %s", name, i, reply)
	}
	return id
}

// rawElements is the first tab's paragraph elements, from the untouched
// GET body rather than through documents.get's decoder — which is the
// whole point of this file.
func rawElements(t *testing.T, c *Client, ctx context.Context, id string) string {
	t.Helper()
	return rawView(t, c, ctx, id, SuggestionsInline)
}

func rawView(t *testing.T, c *Client, ctx context.Context, id, view string) string {
	t.Helper()
	var whole map[string]any
	if err := json.Unmarshal(rawBody(t, c, ctx, id, view), &whole); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, _ := json.Marshal(elementsOf(whole))
	return string(out)
}

// rawBody is the untouched documents.get body, with every tab's content
// and suggestions in the given view.
func rawBody(t *testing.T, c *Client, ctx context.Context, id, view string) []byte {
	t.Helper()
	q := url.Values{}
	q.Set("includeTabsContent", "true")
	q.Set("suggestionsViewMode", view)
	body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return body
}

// elementsOf digs the first tab's body elements out of a decoded
// documents.get response, so the log is the runs and nothing else.
func elementsOf(whole map[string]any) any {
	tabs, _ := whole["tabs"].([]any)
	if len(tabs) == 0 {
		return whole["body"]
	}
	tab, _ := tabs[0].(map[string]any)
	dt, _ := tab["documentTab"].(map[string]any)
	body, _ := dt["body"].(map[string]any)
	content, _ := body["content"].([]any)
	var out []any
	for _, el := range content {
		m, _ := el.(map[string]any)
		if p, ok := m["paragraph"]; ok {
			pm, _ := p.(map[string]any)
			out = append(out, pm["elements"])
			// A paragraph's own style and suggestions are on the paragraph,
			// not its runs.
			ps, _ := pm["paragraphStyle"].(map[string]any)
			// The whole base style: whether a direct change landed shows
			// only there, beside the suggestion.
			out = append(out, map[string]any{"paragraphStyle": ps, "suggestedParagraphStyleChanges": pm["suggestedParagraphStyleChanges"]})
		}
	}
	return out
}

// --- the rounds -------------------------------------------------------

// TestRawSuggestedStyleCarriers asks what a direct restyling does when
// the target is a pending suggested insertion — the issue's premise. All
// four apply, so the premise is wrong.
//
// Seeds are chosen so an inserted word lands hard against the word
// before it and can therefore be targeted exactly: inserting "delta"
// after "Alpha" at index 7 makes the inserted run exactly [7,12).
func TestRawSuggestedStyleCarriers(t *testing.T) {
	runProbes(t, []probe{
		{"a  direct bold over an insertion that is half of a replace", "Alpha bravo charlie.\n", []round{
			{[]json.RawMessage{insertAt(t, "delta", 7), deleteRange(t, 12, 17)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 7, 12, "bold", true)}, nil},
		}},
		{"b  direct bold over a pure insertion already carrying a style suggestion", "Echo foxtrot golf.\n", []round{
			{[]json.RawMessage{insertAt(t, "hotel", 5)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 5, 10, "italic", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 5, 10, "bold", true)}, nil},
		}},
		{"c  direct bold over clean text already carrying a style suggestion", "India juliet kilo.\n", []round{
			{[]json.RawMessage{textStyle(t, 7, 13, "italic", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 7, 13, "bold", true)}, nil},
		}},
	})
}

// TestRawSuggestedStyleSameProperty is the collision: the same property
// suggested and then set directly. d, e and f are all dropped; g, with
// no suggestion in the way, applies.
func TestRawSuggestedStyleSameProperty(t *testing.T) {
	runProbes(t, []probe{
		{"d  clean text: suggest bold, then direct bold", "India juliet kilo.\n", []round{
			{[]json.RawMessage{textStyle(t, 7, 13, "bold", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 7, 13, "bold", true)}, nil},
		}},
		{"e  pure insertion: suggest bold, then direct bold", "Echo foxtrot golf.\n", []round{
			{[]json.RawMessage{insertAt(t, "hotel", 5)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 5, 10, "bold", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 5, 10, "bold", true)}, nil},
		}},
		{"f  replace pair: suggest bold, then direct bold", "Alpha bravo charlie.\n", []round{
			{[]json.RawMessage{insertAt(t, "delta", 7), deleteRange(t, 12, 17)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 7, 12, "bold", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 7, 12, "bold", true)}, nil},
		}},
		{"g  clean text: direct bold with no suggestion anywhere (control)", "Mike november oscar.\n", []round{
			{[]json.RawMessage{textStyle(t, 6, 14, "bold", true)}, nil},
		}},
	})
}

// TestRawSuggestedStyleParagraph carries the same question to
// updateParagraphStyle, and asks whether the value matters as well as
// the property. i is dropped; j and k apply.
//
// Note what is NOT here: bold false over a suggested bold. On unbolded
// text that is indistinguishable from no change at all, so it cannot
// answer anything — which is what an earlier round spent a document
// finding out.
func TestRawSuggestedStyleParagraph(t *testing.T) {
	runProbes(t, []probe{
		{"i  paragraph: suggest alignment CENTER, then direct alignment CENTER", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, nil},
		}},
		{"j  paragraph: suggest alignment CENTER, then direct alignment END", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "END")}, nil},
		}},
		{"k  paragraph: suggest alignment, then direct spaceAbove (other property)", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "spaceAbove", pt(12))}, nil},
		}},
	})
}

// TestRawSuggestedStyleValue settles property-versus-value with a
// property whose every value is observable. Both apply — which is what
// breaks the tidy rule the rounds above seemed to be converging on, and
// why the server warns rather than refuses.
func TestRawSuggestedStyleValue(t *testing.T) {
	runProbes(t, []probe{
		{"l  suggest 20pt, then direct 14pt (same property, other value)", "Probe line.\n", []round{
			{[]json.RawMessage{textStyle(t, 1, 10, "fontSize", pt(20))}, suggestMode},
			{[]json.RawMessage{textStyle(t, 1, 10, "fontSize", pt(14))}, nil},
		}},
		{"m  suggest 20pt, then direct 20pt (same property, same value)", "Probe line.\n", []round{
			{[]json.RawMessage{textStyle(t, 1, 10, "fontSize", pt(20))}, suggestMode},
			{[]json.RawMessage{textStyle(t, 1, 10, "fontSize", pt(20))}, nil},
		}},
	})
}

// TestPrettyPrintFalseIsAccepted checks Google's standard prettyPrint
// system parameter on documents.get, which matters because the wire
// types keep the bytes they were decoded from: indented JSON is retained
// indentation. It asserts the response is accepted, parses, and is
// smaller than the indented one.
func TestPrettyPrintFalseIsAccepted(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	created, err := c.CreateDocument(ctx, "google-docs-mcp prettyPrint probe (safe to delete)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.DocumentID
	if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{
		Requests: []json.RawMessage{insertAt(t, "A paragraph with enough words in it to measure.\n", 1)}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sizes := map[string]int{}
	for _, pretty := range []string{"", "false"} {
		q := url.Values{}
		q.Set("includeTabsContent", "true")
		q.Set("suggestionsViewMode", SuggestionsInline)
		if pretty != "" {
			q.Set("prettyPrint", pretty)
		}
		body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?"+q.Encode(), nil)
		if err != nil {
			t.Fatalf("prettyPrint=%q: %v", pretty, err)
		}
		var d gdocs.Document
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatalf("prettyPrint=%q: decode: %v", pretty, err)
		}
		if d.DocumentID == "" {
			t.Errorf("prettyPrint=%q: decoded no document id", pretty)
		}
		// The bytes the wire types kept must still be the API's own.
		if len(d.Tabs) > 0 && d.Tabs[0].DocumentTab != nil {
			for _, el := range d.Tabs[0].DocumentTab.Body.Content {
				if len(el.RawJSON()) == 0 {
					t.Errorf("prettyPrint=%q: an element kept no raw bytes", pretty)
					break
				}
			}
		}
		label := "default"
		if pretty != "" {
			label = "prettyPrint=false"
		}
		sizes[label] = len(body)
		t.Logf("%-18s %d bytes, newlines: %d", label, len(body), strings.Count(string(body), "\n"))
	}
	if sizes["prettyPrint=false"] >= sizes["default"] {
		t.Errorf("prettyPrint=false did not shrink the body: %v", sizes)
	}
}

// TestRawSuggestedReset asks why inserting formatted content in suggest
// mode leaves two suggestions. The planner inserts the text and then
// resets its style, so it does not inherit the bold of the word before
// it. The reset, sent with fields "*", comes back under a suggestion id
// of its own, while a bold update right after it joins the insertion's.
// Each probe inserts " new" after a directly bolded "Alpha".
func TestRawSuggestedReset(t *testing.T) {
	bold := round{reqs: []json.RawMessage{textStyle(t, 1, 6, "bold", true)}}
	insert := func(fields string) round {
		reqs := []json.RawMessage{insertAt(t, " new", 6)}
		if fields != "" {
			reqs = append(reqs, reset(t, 6, 10, fields))
		}
		return round{reqs: reqs, wc: suggestMode}
	}
	runProbes(t, []probe{
		{name: "no reset", seed: "Alpha beta.\n", rounds: []round{bold, insert("")}},
		{name: "reset *", seed: "Alpha beta.\n", rounds: []round{bold, insert("*")}},
		{name: "reset named", seed: "Alpha beta.\n", rounds: []round{bold, insert(
			"bold,italic,underline,strikethrough,smallCaps,backgroundColor,foregroundColor,fontSize,weightedFontFamily,baselineOffset,link")}},
		// What the planner sends for " new **x**": the reset over the
		// whole insertion, then bold over part of it; and the reverse.
		{name: "reset then bold", seed: "Alpha beta.\n", rounds: []round{bold, {wc: suggestMode, reqs: []json.RawMessage{
			insertAt(t, " new x", 6), reset(t, 6, 12, "*"), textStyle(t, 11, 12, "bold", true)}}}},
		{name: "bold then reset", seed: "Alpha beta.\n", rounds: []round{bold, {wc: suggestMode, reqs: []json.RawMessage{
			insertAt(t, " new x", 6), textStyle(t, 11, 12, "bold", true), reset(t, 6, 11, "*")}}}},
	})
}

// TestRawSuggestedResetWholeParagraph asks when the reset gets a
// suggestion id of its own (#109). Text inserted into an empty paragraph
// and reset takes two ids, the second also on the paragraph mark; the
// control inserts the same text before other text in the paragraph.
func TestRawSuggestedResetWholeParagraph(t *testing.T) {
	insertReset := round{wc: suggestMode, reqs: []json.RawMessage{insertAt(t, "new", 8), reset(t, 8, 11, "*")}}
	runProbes(t, []probe{
		{name: "empty paragraph", seed: "Alpha.\n", rounds: []round{insertReset}},
		{name: "before other text", seed: "Alpha.\nold\n", rounds: []round{insertReset}},
	})
}

// reset clears the named text properties over a range, "*" for all of
// them, which is what the planner sends after inserting a fragment.
func reset(t *testing.T, start, end int, fields string) json.RawMessage {
	return marshal(t, map[string]any{"updateTextStyle": map[string]any{
		"range": map[string]any{"startIndex": start, "endIndex": end}, "textStyle": map[string]any{}, "fields": fields}})
}

// TestRawAcceptedView reads documents the probes above left behind
// (GDOCS_PROBE_DOCS, comma-separated ids) in the inline view and in the
// accepted preview, and writes nothing. It asks whether a run's own
// textStyle in SUGGESTIONS_INLINE is the style before the suggestion,
// which the documentation does not say.
func TestRawAcceptedView(t *testing.T) {
	ids := os.Getenv("GDOCS_PROBE_DOCS")
	if ids == "" {
		t.Skip("set GDOCS_PROBE_DOCS to probe document ids")
	}
	c := probeClient(t)
	ctx := context.Background()
	for _, id := range strings.Split(ids, ",") {
		for _, view := range []string{SuggestionsInline, SuggestionsPreviewAccepted} {
			t.Logf("VIEW %s %s\n%s", id[:6], view, rawView(t, c, ctx, id, view))
		}
	}
}

// TestRawDirectOverPendingReset is the restyle guard's question for
// inserted text: a suggested insertion after bold "Alpha" whose own
// suggestion resets the text to plain, then a DIRECT bold, italic or
// bold-off over it. Does the direct change survive into the accepted
// result, or does the pending reset win?
func TestRawDirectOverPendingReset(t *testing.T) {
	bold := round{reqs: []json.RawMessage{textStyle(t, 1, 6, "bold", true)}}
	suggested := round{wc: suggestMode, reqs: []json.RawMessage{insertAt(t, " new", 6), reset(t, 6, 10, "*")}}
	direct := func(prop string, v bool) round { return round{reqs: []json.RawMessage{textStyle(t, 6, 10, prop, v)}} }
	runProbes(t, []probe{
		{name: "direct bold", seed: "Alpha beta.\n", rounds: []round{bold, suggested, direct("bold", true)}},
		{name: "direct italic", seed: "Alpha beta.\n", rounds: []round{bold, suggested, direct("italic", true)}},
		{name: "direct bold off", seed: "Alpha beta.\n", rounds: []round{bold, suggested, direct("bold", false)}},
	})
}

// TestRawSuggestedHeading asks whether a paragraph inserted as a
// suggestion keeps its own paragraph style under its insertion id, the
// way inserted text keeps its text style: what the planner sends for a
// markdown "# Heading" in suggest mode.
func TestRawSuggestedHeading(t *testing.T) {
	heading := marshal(t, map[string]any{"updateParagraphStyle": map[string]any{
		"range":          map[string]any{"startIndex": 13, "endIndex": 21},
		"paragraphStyle": map[string]any{"namedStyleType": "HEADING_1"},
		"fields":         "namedStyleType",
	}})
	runProbes(t, []probe{
		{"suggested heading", "Alpha beta.\n", []round{{wc: suggestMode, reqs: []json.RawMessage{insertAt(t, "Heading\n", 13), heading}}}},
	})
}

// TestRawDirectOverPendingBold is the same-value case of the restyle
// guard for inserted text: "x" inserted as a suggestion with bold set
// (as "**x**" is), then a DIRECT bold true or false over it. The first is
// the shape #46 saw dropped on existing text. Beside it, a suggested bold
// on existing italic text, to see whether that entry's style holds only
// the change or every inherited property.
func TestRawDirectOverPendingBold(t *testing.T) {
	insertBold := round{wc: suggestMode, reqs: []json.RawMessage{
		insertAt(t, " x", 6), reset(t, 6, 8, "*"), textStyle(t, 7, 8, "bold", true)}}
	runProbes(t, []probe{
		{"direct bold over suggested bold", "Alpha beta.\n", []round{insertBold, {reqs: []json.RawMessage{textStyle(t, 7, 8, "bold", true)}}}},
		{"direct bold off over suggested bold", "Alpha beta.\n", []round{insertBold, {reqs: []json.RawMessage{textStyle(t, 7, 8, "bold", false)}}}},
		{"suggested bold on existing italic", "Alpha beta.\n", []round{
			{reqs: []json.RawMessage{textStyle(t, 1, 6, "italic", true)}},
			{wc: suggestMode, reqs: []json.RawMessage{textStyle(t, 1, 6, "bold", true)}}}},
	})
}

// TestRawSuggestedStyleKinds asks whether the collision follows the kind
// of property (#106): each case suggests one property and then sets the
// same value directly, across booleans, an enum, a color and a font.
// The paragraph properties are in TestRawSuggestedStyleKindControls.
func TestRawSuggestedStyleKinds(t *testing.T) {
	same := func(name string, req func() json.RawMessage) probe {
		return probe{name, "Probe line.\n", []round{{[]json.RawMessage{req()}, suggestMode}, {[]json.RawMessage{req()}, nil}}}
	}
	red := map[string]any{"color": map[string]any{"rgbColor": map[string]any{"red": 1}}}
	got := runProbes(t, []probe{
		same("n  italic", func() json.RawMessage { return textStyle(t, 1, 10, "italic", true) }),
		same("o  underline", func() json.RawMessage { return textStyle(t, 1, 10, "underline", true) }),
		same("p  strikethrough", func() json.RawMessage { return textStyle(t, 1, 10, "strikethrough", true) }),
		same("q  smallCaps", func() json.RawMessage { return textStyle(t, 1, 10, "smallCaps", true) }),
		same("r  baselineOffset SUPERSCRIPT", func() json.RawMessage { return textStyle(t, 1, 10, "baselineOffset", "SUPERSCRIPT") }),
		same("s  foregroundColor red", func() json.RawMessage { return textStyle(t, 1, 10, "foregroundColor", red) }),
		same("t  weightedFontFamily Georgia", func() json.RawMessage {
			return textStyle(t, 1, 10, "weightedFontFamily", map[string]any{"fontFamily": "Georgia"})
		}),
	})
	assertLanded(t, got, map[string]string{
		"n  italic": "italic", "o  underline": "underline", "p  strikethrough": "strikethrough",
		"q  smallCaps": "smallCaps", "r  baselineOffset SUPERSCRIPT": "baselineOffset",
		"s  foregroundColor red": "foregroundColor", "t  weightedFontFamily Georgia": "weightedFontFamily",
	})
}

// TestRawSuggestedStyleKindOtherValue is the other half for the
// value-typed properties that land: a suggestion of one value, then a
// different value set directly.
func TestRawSuggestedStyleKindOtherValue(t *testing.T) {
	then := func(name, prop string, suggested, direct any) probe {
		return probe{name, "Probe line.\n", []round{
			{[]json.RawMessage{textStyle(t, 1, 10, prop, suggested)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 1, 10, prop, direct)}, nil}}}
	}
	color := func(r, b float64) map[string]any {
		return map[string]any{"color": map[string]any{"rgbColor": map[string]any{"red": r, "blue": b}}}
	}
	got := runProbes(t, []probe{
		then("r2 baselineOffset SUPERSCRIPT, then SUBSCRIPT", "baselineOffset", "SUPERSCRIPT", "SUBSCRIPT"),
		then("s2 foregroundColor red, then blue", "foregroundColor", color(1, 0), color(0, 1)),
		then("t2 weightedFontFamily Georgia, then Courier New", "weightedFontFamily",
			map[string]any{"fontFamily": "Georgia"}, map[string]any{"fontFamily": "Courier New"}),
	})
	assertLanded(t, got, map[string]string{
		"r2 baselineOffset SUPERSCRIPT, then SUBSCRIPT":   "baselineOffset",
		"s2 foregroundColor red, then blue":               "foregroundColor",
		"t2 weightedFontFamily Georgia, then Courier New": "weightedFontFamily",
	})
}

// TestRawSuggestedStyleKindControls is what each property looks like
// suggested and nothing more, beside the bold case known to drop, so a
// run in TestRawSuggestedStyleKinds can be read as landed or dropped.
func TestRawSuggestedStyleKindControls(t *testing.T) {
	only := func(name string, req json.RawMessage) probe {
		return probe{name, "Probe line.\n", []round{{[]json.RawMessage{req}, suggestMode}}}
	}
	runProbes(t, []probe{
		only("n0 italic suggested only", textStyle(t, 1, 10, "italic", true)),
		only("x0 bold suggested only", textStyle(t, 1, 10, "bold", true)),
		{"x1 bold suggested, then direct bold (known to drop)", "Probe line.\n", []round{
			{[]json.RawMessage{textStyle(t, 1, 10, "bold", true)}, suggestMode},
			{[]json.RawMessage{textStyle(t, 1, 10, "bold", true)}, nil}}},
		only("u0 lineSpacing suggested only", paraStyle(t, 1, 10, "lineSpacing", 200)),
		{"u1 lineSpacing suggested, then direct", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "lineSpacing", 200)}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "lineSpacing", 200)}, nil}}},
		{"i1 alignment CENTER suggested, then direct (known to drop)", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "alignment", "CENTER")}, nil}}},
		{"v1 spaceAbove suggested, then direct", "Probe line.\n", []round{
			{[]json.RawMessage{paraStyle(t, 1, 10, "spaceAbove", pt(12))}, suggestMode},
			{[]json.RawMessage{paraStyle(t, 1, 10, "spaceAbove", pt(12))}, nil}}},
	})
}

// TestRawSuggestedTableInsertions asks what a suggested insertion looks
// like inside a table (#109): text inserted into a cell, the same text
// with the planner's reset after it, and a row inserted below the first.
// Each case gets a fresh document holding a direct 2×2 table, and the
// table is logged as Google returns it, suggestion ids included.
func TestRawSuggestedTableInsertions(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		reqs func(table, cell int) []json.RawMessage
	}{
		{"text into a cell", func(_, cell int) []json.RawMessage {
			return []json.RawMessage{insertAt(t, "new", cell)}
		}},
		{"text into a cell, then the planner's reset", func(_, cell int) []json.RawMessage {
			return []json.RawMessage{insertAt(t, "new", cell), reset(t, cell, cell+3, "*")}
		}},
		{"a row below the first", func(table, _ int) []json.RawMessage {
			return []json.RawMessage{marshal(t, map[string]any{"insertTableRow": map[string]any{
				"tableCellLocation": map[string]any{"tableStartLocation": map[string]any{"index": table}, "rowIndex": 0, "columnIndex": 0},
				"insertBelow":       true}})}
		}},
	} {
		id := probeDoc(t, c, ctx, tc.name, "google-docs-mcp suggested-table probe (safe to delete)",
			[]json.RawMessage{insertAt(t, "Probe line.\n", 1), marshal(t, map[string]any{"insertTable": map[string]any{
				"rows": 2, "columns": 2, "endOfSegmentLocation": map[string]any{}}})}, nil)
		table, cell := tableIndices(t, rawBody(t, c, ctx, id, SuggestionsInline))
		res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: tc.reqs(table, cell), WriteControl: suggestMode})
		if err != nil {
			// A refusal is an answer too: the planner would have to refuse
			// the same op in suggest mode.
			t.Logf("--- %s: suggest-mode batch refused: %v ---", tc.name, err)
			continue
		}
		reply, _ := json.Marshal(res.Replies)
		t.Logf("--- %s: replies %s ---", tc.name, reply)
		for _, view := range []string{SuggestionsInline, SuggestionsPreviewAccepted} {
			t.Logf("%s %s:\n%s", tc.name, view, tableOf(t, rawBody(t, c, ctx, id, view)))
		}
	}
	t.Logf("=== scratch documents left behind; Drive search title:\"safe to delete\" finds them ===")
}

// firstTable is the first table element in the first tab's body.
func firstTable(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var whole map[string]any
	if err := json.Unmarshal(body, &whole); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tabs, _ := whole["tabs"].([]any)
	tab, _ := tabs[0].(map[string]any)
	dt, _ := tab["documentTab"].(map[string]any)
	b, _ := dt["body"].(map[string]any)
	content, _ := b["content"].([]any)
	for _, el := range content {
		if m, _ := el.(map[string]any); m["table"] != nil {
			return m
		}
	}
	t.Fatal("the document holds no table")
	return nil
}

// tableIndices is the table's start index and the start of the first
// cell's first paragraph, read back rather than computed, since where
// insertTable puts the table is Google's choice.
func tableIndices(t *testing.T, body []byte) (table, cell int) {
	t.Helper()
	el := firstTable(t, body)
	tbl, _ := el["table"].(map[string]any)
	rows, _ := tbl["tableRows"].([]any)
	row, _ := rows[0].(map[string]any)
	cells, _ := row["tableCells"].([]any)
	c0, _ := cells[0].(map[string]any)
	content, _ := c0["content"].([]any)
	p0, _ := content[0].(map[string]any)
	start, _ := el["startIndex"].(float64)
	cs, _ := p0["startIndex"].(float64)
	return int(start), int(cs)
}

func tableOf(t *testing.T, body []byte) string {
	t.Helper()
	out, _ := json.Marshal(firstTable(t, body))
	return string(out)
}
