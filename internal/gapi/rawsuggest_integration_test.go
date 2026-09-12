//go:build integration

package gapi

// Spike B: what does the API really do with a suggested formatting
// change? Runs against the caller's own account and creates clearly
// named scratch documents, one per case so no case disturbs another.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawSuggestedStyle -v
//
// It reads the untouched HTTP body rather than going through
// documents.get's decoder, which is the point: the decoder is this
// server's own wire types, and the field in question is one they did not
// have. read_document format: raw re-marshals those types, so it could
// not tell "Google recorded nothing" from "we drop it on the way out" —
// and the issue that started this was filed on exactly that reading.
//
// What the rounds established, 2026-09-12:
//
//   - A SUGGEST-mode updateTextStyle DOES record a suggestion:
//     suggestedTextStyleChanges keyed by suggestion id, with a
//     textStyleSuggestionState naming the properties. The style beside it
//     is the style as it would be once accepted, every inherited
//     property filled in, so only the state says what changed.
//   - A direct updateTextStyle over a suggested INSERTION applies
//     normally, whether the range covers the inserted run exactly, part
//     of it, or straddles it and real text. The issue's premise — that a
//     pending insertion is what blocks the write — does not hold.
//   - A direct change IS silently dropped when a pending suggestion on
//     the range already suggests the same property AND value: bold over
//     a suggested bold, alignment CENTER over a suggested CENTER. The
//     batch is accepted and its reply is empty.
//   - But not always. A font size suggested at 20pt and then set
//     directly to 20pt does land, and an alignment set to a value the
//     suggestion does not name lands too. So the collision is real and
//     reproducible and its rule is not known. One case is deliberately
//     absent: bold false over a suggested bold proved nothing, because
//     on unbolded text it is indistinguishable from no change at all.
//
// That last point is why the server warns here rather than refusing:
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

	"github.com/mmedum/google-docs-mcp/internal/auth"
	"github.com/mmedum/google-docs-mcp/internal/credentials"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/userconfig"
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

// runProbes applies each probe to a scratch document of its own and logs
// the runs the API reports afterwards.
func runProbes(t *testing.T, probes []probe) {
	t.Helper()
	c := probeClient(t)
	ctx := context.Background()
	for _, p := range probes {
		created, err := c.CreateDocument(ctx, "google-docs-mcp suggested-style probe (safe to delete)")
		if err != nil {
			t.Fatalf("%s: create: %v", p.name, err)
		}
		id := created.DocumentID
		if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{
			Requests: []json.RawMessage{insertAt(t, p.seed, 1)}}); err != nil {
			t.Fatalf("%s: seed: %v", p.name, err)
		}
		for i, r := range p.rounds {
			res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: r.reqs, WriteControl: r.wc})
			if err != nil {
				t.Fatalf("%s: round %d: %v", p.name, i, err)
			}
			// An empty reply is the tell: the batch was accepted and the
			// API is saying nothing about what it did.
			reply, _ := json.Marshal(res.Replies)
			t.Logf("%s: round %d replies: %s", p.name, i, reply)
		}
		t.Logf("--- %s ---\n%s", p.name, rawElements(t, c, ctx, id))
	}
	t.Logf("=== %d scratch documents left behind; Drive search title:\"safe to delete\" finds them ===", len(probes))
}

// rawElements is the first tab's paragraph elements, from the untouched
// GET body rather than through documents.get's decoder — which is the
// whole point of this file.
func rawElements(t *testing.T, c *Client, ctx context.Context, id string) string {
	t.Helper()
	q := url.Values{}
	q.Set("includeTabsContent", "true")
	q.Set("suggestionsViewMode", SuggestionsInline)
	body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var whole map[string]any
	if err := json.Unmarshal(body, &whole); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, _ := json.Marshal(elementsOf(whole))
	return string(out)
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
// system parameter on documents.get, which matters now that the wire
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
