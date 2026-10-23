//go:build integration

package gapi

// Spike F: when does createParagraphBullets turn leading tabs into
// nesting? The server writes a nested list as tab-prefixed paragraphs and
// one createParagraphBullets over them. Nested items were landing at
// level 0 with a literal tab before the text.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawBullets -v
//
// Every probe seeds the same document, a heading, a two-item list and a
// trailing paragraph, then applies one batch the way the server would.
// Seed indices, after the seed list is made:
//
//	[1,6) "Head"  [6,12) "L one"  [12,18) "L two"  [18,23) "Tail"
//
// What the rounds established, 2026-10-21, in direct and suggest mode
// alike (internal/plan/lists.go acts on it):
//
//   - Inserted paragraphs take the bullet of the paragraph they land in,
//     and createParagraphBullets does not re-nest a paragraph that has
//     one: the tab stays as text, the level as it was (a, e, f).
//   - Deleting the bullet first works (b, c, d, i), but a level is the
//     level of the list item just before the range plus the tabs beyond
//     the fewest in the range (j, k, l, s), joining that item's list when
//     the presets match (c, h; not t). So a run starts neither deeper nor
//     shallower than the item before it (g, o, p, y).
//   - A temporary empty paragraph at the start pins the fewest tabs to
//     none (aa, ab); taking the deeper items before into the window,
//     rebuilt at their own levels, lets a run start shallower (z, z2).
//   - The indent deleteParagraphBullets leaves adds to the new bullet's
//     after a paragraph (ae); clearing it fixes that (af).
//   - A new list does not take in the items after it (ag, ah), and a
//     last-block replace's paragraphs take the bullet before (ai, aj).
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v3/internal/gdocs"
)

const (
	bulletDisc = "BULLET_DISC_CIRCLE_SQUARE"
	numbered   = "NUMBERED_DECIMAL_ALPHA_ROMAN"
)

func createBullets(t *testing.T, start, end int, preset string) json.RawMessage {
	return marshal(t, map[string]any{"createParagraphBullets": map[string]any{
		"range": map[string]any{"startIndex": start, "endIndex": end}, "bulletPreset": preset}})
}

func deleteBullets(t *testing.T, start, end int) json.RawMessage {
	return marshal(t, map[string]any{"deleteParagraphBullets": map[string]any{
		"range": map[string]any{"startIndex": start, "endIndex": end}}})
}

// bulletProbe is one question: the preset of the seed list, and the
// batch applied to it. seed replaces the default seed batch when set.
type bulletProbe struct {
	name, preset string
	reqs         []json.RawMessage
	wc           *WriteControl
	seed         []json.RawMessage
}

func runBulletProbes(t *testing.T, probes []bulletProbe) {
	t.Helper()
	c := probeClient(t)
	ctx := context.Background()
	for _, p := range probes {
		seed := p.seed
		if seed == nil {
			seed = []json.RawMessage{insertAt(t, "Head\nL one\nL two\nTail", 1), createBullets(t, 6, 18, p.preset)}
		}
		id := probeDoc(t, c, ctx, p.name, "google-docs-mcp bullets probe "+p.name+" (safe to delete)", seed, []round{{p.reqs, p.wc}})
		t.Logf("--- %s (%s) ---\n%s", p.name, id, paragraphSummary(t, c, ctx, id, SuggestionsInline))
		if p.wc != nil {
			t.Logf("--- %s accepted ---\n%s", p.name, paragraphSummary(t, c, ctx, id, SuggestionsPreviewAccepted))
		}
	}
	t.Logf("=== %d scratch documents left behind; Drive search title:\"safe to delete\" finds them ===", len(probes))
}

// nestedSeed is a list with a nested item: [6,12) "L one" 0, [12,18)
// "L sub" 1, [18,24) "L two" 0, [24,29) "Tail".
func nestedSeed(t *testing.T) []json.RawMessage {
	return []json.RawMessage{insertAt(t, "Head\nL one\n\tL sub\nL two\nTail", 1), createBullets(t, 6, 24, bulletDisc)}
}

// zeroIndent sets both indents to 0pt, as the other projects do between
// deleting bullets and making them; the server clears them to the named
// style instead, which TestLiveLists checks.
func zeroIndent(t *testing.T, start, end int) json.RawMessage {
	return marshal(t, map[string]any{"updateParagraphStyle": map[string]any{
		"range":          map[string]any{"startIndex": start, "endIndex": end},
		"paragraphStyle": map[string]any{"indentStart": pt(0), "indentFirstLine": pt(0)},
		"fields":         "indentStart,indentFirstLine",
	}})
}

// paragraphSummary is one line per paragraph: its text with tabs shown,
// its list and nesting level, its indents, and any suggested change to
// its bullet, read from the untouched GET body.
func paragraphSummary(t *testing.T, c *Client, ctx context.Context, id, view string) string {
	t.Helper()
	var d gdocs.Document
	if err := json.Unmarshal(rawBody(t, c, ctx, id, view), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var b strings.Builder
	for _, el := range d.Tabs[0].DocumentTab.Body.Content {
		p := el.Paragraph
		if p == nil {
			continue
		}
		var text strings.Builder
		suggested := false
		for _, e := range p.Elements {
			if e.TextRun != nil {
				text.WriteString(e.TextRun.Content)
				suggested = suggested || len(e.TextRun.SuggestedInsertionIDs) > 0
			}
		}
		line := fmt.Sprintf("@%-3d %-14q", el.StartIndex, strings.TrimSuffix(text.String(), "\n"))
		if p.Bullet != nil {
			line += fmt.Sprintf(" list=%s level=%d", p.Bullet.ListID, p.Bullet.NestingLevel)
		} else {
			line += " no bullet"
		}
		if ps := p.ParagraphStyle; ps != nil && (ps.IndentStart != nil || ps.IndentFirstLine != nil) {
			var sv, fv float64
			if ps.IndentStart != nil {
				sv = ps.IndentStart.Magnitude
			}
			if ps.IndentFirstLine != nil {
				fv = ps.IndentFirstLine.Magnitude
			}
			line += fmt.Sprintf(" indent=%g/%g", sv, fv)
		}
		if suggested {
			line += " [suggested insertion]"
		}
		if len(p.SuggestedBulletChanges) > 0 {
			raw, _ := json.Marshal(p.SuggestedBulletChanges)
			line += " suggestedBullet=" + string(raw)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// TestRawBullets: two nested items inserted at the start of "L two"
// ("N one", "\tN two" = [12,24)), or split off the end of "L one"
// ("\tN two" = [12,18)), or at the start of "Tail" ([18,30)).
func TestRawBullets(t *testing.T) {
	before := func() json.RawMessage { return insertAt(t, "N one\n\tN two\n", 12) }
	runBulletProbes(t, []bulletProbe{
		// The report: inserted paragraphs inherit L two's bullet first.
		{"a-before-item", bulletDisc, []json.RawMessage{before(), createBullets(t, 12, 24, bulletDisc)}, nil, nil},
		// Candidate fix: drop the inherited bullet, then make the list.
		{"b-before-item-delete-first", bulletDisc, []json.RawMessage{before(), deleteBullets(t, 12, 24), createBullets(t, 12, 24, bulletDisc)}, nil, nil},
		// Numbering continues only if the new items join the seed's list.
		{"c-before-item-numbered", numbered, []json.RawMessage{before(), deleteBullets(t, 12, 24), createBullets(t, 12, 24, numbered)}, nil, nil},
		{"d-before-item-delete-first-suggest", bulletDisc, []json.RawMessage{before(), deleteBullets(t, 12, 24), createBullets(t, 12, 24, bulletDisc)}, suggestMode, nil},
		{"e-before-item-suggest", bulletDisc, []json.RawMessage{before(), createBullets(t, 12, 24, bulletDisc)}, suggestMode, nil},
		// A nested item split off the end of an item: the replace case.
		{"f-split-item", bulletDisc, []json.RawMessage{insertAt(t, "\n\tN two", 11), createBullets(t, 12, 18, bulletDisc)}, nil, nil},
		{"g-split-item-delete-first", bulletDisc, []json.RawMessage{insertAt(t, "\n\tN two", 11), deleteBullets(t, 12, 18), createBullets(t, 12, 18, bulletDisc)}, nil, nil},
		// Control: no inherited bullet, which works today. Does it join?
		{"h-before-plain-numbered", numbered, []json.RawMessage{insertAt(t, "N one\n\tN two\n", 18), createBullets(t, 18, 30, numbered)}, nil, nil},
		// Deeper: a level-2 item under the fix.
		{"i-before-item-two-levels", bulletDisc, []json.RawMessage{insertAt(t, "N one\n\tN two\n\t\tN three\n", 12), deleteBullets(t, 12, 34), createBullets(t, 12, 34, bulletDisc)}, nil, nil},
	})
}

// TestRawBulletsBase asks what a tab counts from, after probe g gave a
// lone "\tN two" level 0: the first paragraph of the range, the least
// indented one, or the list the range joins.
func TestRawBulletsBase(t *testing.T) {
	runBulletProbes(t, []bulletProbe{
		// Two tabbed items, none at the left margin: 1 and 2, or 0 and 1?
		{"j-all-tabbed", bulletDisc, []json.RawMessage{insertAt(t, "\tN two\n\t\tN three\n", 12), deleteBullets(t, 12, 28), createBullets(t, 12, 28, bulletDisc)}, nil, nil},
		// The range starts at the bulleted parent "L one" (no tab, kept
		// as it is) and takes in the split-off, unbulleted "\tN two".
		{"k-range-from-parent", bulletDisc, []json.RawMessage{insertAt(t, "\n\tN two", 11), deleteBullets(t, 12, 18), createBullets(t, 6, 18, bulletDisc)}, nil, nil},
		// The same, under a parent that is itself at level 1.
		{"l-range-from-nested-parent", bulletDisc, []json.RawMessage{insertAt(t, "\n\t\tN deep", 17), deleteBullets(t, 18, 26), createBullets(t, 12, 26, bulletDisc)}, nil, nestedSeed(t)},
		// Re-bulleting the nested parent with its tab put back.
		{"m-parent-tab-restored", bulletDisc, []json.RawMessage{insertAt(t, "\n\t\tN deep", 17), insertAt(t, "\t", 12), deleteBullets(t, 12, 27), createBullets(t, 12, 27, bulletDisc)}, nil, nestedSeed(t)},
		// Range spans the whole list, every item re-bulleted with tabs.
		{"n-whole-list-rebuilt", bulletDisc, []json.RawMessage{insertAt(t, "\n\tN two", 11), deleteBullets(t, 6, 24), createBullets(t, 6, 24, bulletDisc)}, nil, nil},
		// After a level-1 item, into the level-0 "L two": does "N one"
		// (no tab) land at 0, or at the level of the item before it?
		{"o-after-nested-item-delete-first", bulletDisc, []json.RawMessage{insertAt(t, "N one\n\tN two\n", 18), deleteBullets(t, 18, 30), createBullets(t, 18, 30, bulletDisc)}, nil, nestedSeed(t)},
		// The list ends on a level-1 item and the new items go into the
		// unbulleted "Tail" after it: [6,12) "L one" 0, [12,18) "L sub" 1,
		// [18,23) "Tail".
		{"p-after-nested-end-plain", bulletDisc, []json.RawMessage{insertAt(t, "N one\n\tN two\n", 18), createBullets(t, 18, 30, bulletDisc)}, nil,
			[]json.RawMessage{insertAt(t, "Head\nL one\n\tL sub\nTail", 1), createBullets(t, 6, 18, bulletDisc)}},
	})
}

// TestRawBulletsAnchor follows up the base rule from TestRawBulletsBase:
// a paragraph's level is the level of the list item just before the
// range, plus its tabs beyond the fewest in the range. So a child can only
// be added under an item if the range takes the item in too.
func TestRawBulletsAnchor(t *testing.T) {
	// A child under "L one": the range starts at L one, re-bulleted with
	// no tab, so its base is the unbulleted "Head".
	child := func() []json.RawMessage {
		return []json.RawMessage{insertAt(t, "\n\tN child", 11), deleteBullets(t, 6, 18), createBullets(t, 6, 18, bulletDisc)}
	}
	runBulletProbes(t, []bulletProbe{
		{"q-child-via-parent", bulletDisc, child(), nil, nil},
		// What re-bulleting the untouched parent leaves as a suggestion.
		{"r-child-via-parent-suggest", bulletDisc, child(), suggestMode, nil},
		// The first paragraph has more tabs than the second: is the base
		// the first paragraph or the fewest tabs?
		{"s-first-deeper", bulletDisc, []json.RawMessage{insertAt(t, "\tN a\nN b\n", 18), createBullets(t, 18, 26, bulletDisc)}, nil, nil},
		// A numbered run straight after a bulleted list: does it join?
		{"t-numbered-after-bulleted", bulletDisc, []json.RawMessage{insertAt(t, "N one\n\tN two\n", 18), createBullets(t, 18, 30, numbered)}, nil, nil},
	})
}

// TestRawBulletsIndent asks whether the indent deleteParagraphBullets
// leaves behind counts towards the level, as other projects report.
// Each inserts two items at the start of the level-1 "L sub", so they
// inherit a level-1 bullet; the range is [12,24).
func TestRawBulletsIndent(t *testing.T) {
	zeroed := zeroIndent(t, 12, 24)
	ins := func() json.RawMessage { return insertAt(t, "N one\n\tN two\n", 12) }
	runBulletProbes(t, []bulletProbe{
		{"v-inherited-level1-delete-first", bulletDisc, []json.RawMessage{ins(), deleteBullets(t, 12, 24), createBullets(t, 12, 24, bulletDisc)}, nil, nestedSeed(t)},
		{"w-inherited-level1-zero-indent", bulletDisc, []json.RawMessage{ins(), deleteBullets(t, 12, 24), zeroed, createBullets(t, 12, 24, bulletDisc)}, nil, nestedSeed(t)},
		{"x-inherited-level1-zero-indent-suggest", bulletDisc, []json.RawMessage{ins(), deleteBullets(t, 12, 24), zeroed, createBullets(t, 12, 24, bulletDisc)}, suggestMode, nestedSeed(t)},
	})
}

// TestRawBulletsWindow tries the two remedies another project measured
// (gdoc, listnest.py): rebuilding a window of items back to one no deeper
// than the window, and a temporary empty paragraph at the window start
// that pins the fewest tabs to 0, so each level is base + tabs.
func TestRawBulletsWindow(t *testing.T) {
	// "L two" gains a child "N b1": "L two" [18,24), "\tN b1" [24,30).
	naive := []json.RawMessage{insertAt(t, "\n\tN b1", 23), deleteBullets(t, 18, 29), zeroIndent(t, 18, 29), createBullets(t, 18, 29, bulletDisc)}
	// The window takes in the deeper "L sub", re-tabbed at its own level:
	// "\tL sub" [12,19), "L two" [19,25), "\tN b1" [25,31).
	window := []json.RawMessage{insertAt(t, "\n\tN b1", 23), insertAt(t, "\t", 12), deleteBullets(t, 12, 30), zeroIndent(t, 12, 30), createBullets(t, 12, 30, bulletDisc)}
	// A child under "L one" without touching it: a temporary paragraph
	// [12,13) ahead of "\tN child" [13,21), removed once the list is made.
	temp := []json.RawMessage{insertAt(t, "\n\tN child", 11), insertAt(t, "\n", 12), deleteBullets(t, 12, 20), zeroIndent(t, 12, 20), createBullets(t, 12, 20, bulletDisc), deleteRange(t, 12, 13)}
	runBulletProbes(t, []bulletProbe{
		{"y-child-after-nested-naive", bulletDisc, naive, nil, nestedSeed(t)},
		{"z-child-after-nested-window", bulletDisc, window, nil, nestedSeed(t)},
		{"z2-child-after-nested-window-suggest", bulletDisc, window, suggestMode, nestedSeed(t)},
		{"aa-child-first-temp", bulletDisc, temp, nil, nil},
		{"ab-child-first-temp-suggest", bulletDisc, temp, suggestMode, nil},
	})
}

// TestRawBulletsAfterParagraph: a paragraph then a nested list, inserted
// at the start of the level-1 "L sub", so everything inherits a level-1
// bullet: "Para" [12,17), "N one" [17,23), "\tN two" [23,30). The
// paragraph's bullet is deleted, leaving its indent; the list follows a
// paragraph that is no list item, so its base is 0.
func TestRawBulletsAfterParagraph(t *testing.T) {
	ins := func() json.RawMessage { return insertAt(t, "Para\nN one\n\tN two\n", 12) }
	zeroed := zeroIndent(t, 17, 29)
	runBulletProbes(t, []bulletProbe{
		{"ae-after-paragraph", bulletDisc, []json.RawMessage{ins(), deleteBullets(t, 12, 29), createBullets(t, 17, 29, bulletDisc)}, nil, nestedSeed(t)},
		{"af-after-paragraph-zero-indent", bulletDisc, []json.RawMessage{ins(), deleteBullets(t, 12, 29), zeroed, createBullets(t, 17, 29, bulletDisc)}, nil, nestedSeed(t)},
	})
}

// TestRawBulletsTop: new items at the top of a list, after the
// unbulleted "Head": they inherit "L one"'s bullet, which is deleted.
// Does the new list take in the items after it, or split them off?
// "N one" [6,12), "\tN two" [12,19), "L one" [19,25).
func TestRawBulletsTop(t *testing.T) {
	top := func(preset string) []json.RawMessage {
		return []json.RawMessage{insertAt(t, "N one\n\tN two\n", 6), deleteBullets(t, 6, 18), createBullets(t, 6, 18, preset)}
	}
	runBulletProbes(t, []bulletProbe{
		{"ag-top-numbered", numbered, top(numbered), nil, nil},
		{"ah-top-bulleted", bulletDisc, top(bulletDisc), nil, nil},
	})
}

// TestRawBulletsLastBlock: the planner replaces a segment's last block
// by deleting from the newline before it and inserting "\n"+content
// there. Which style does the merged paragraph keep, and so which
// bullet do the new paragraphs take? No list requests are sent.
func TestRawBulletsLastBlock(t *testing.T) {
	runBulletProbes(t, []bulletProbe{
		// [6,12) "L one", [12,18) "L two" (last): delete [11,17), insert at 11.
		{"ai-last-item-after-item", bulletDisc, []json.RawMessage{deleteRange(t, 11, 17), insertAt(t, "\nN one\n\tN two", 11)}, nil,
			[]json.RawMessage{insertAt(t, "Head\nL one\nL two", 1), createBullets(t, 6, 17, bulletDisc)}},
		// [1,6) "Head" (heading), [6,12) "L two" (last): delete [5,11), insert at 5.
		{"aj-last-item-after-heading", bulletDisc, []json.RawMessage{deleteRange(t, 5, 11), insertAt(t, "\nN one", 5)}, nil,
			[]json.RawMessage{insertAt(t, "Head\nL two", 1), paraStyle(t, 1, 5, "namedStyleType", "HEADING_1"), createBullets(t, 6, 11, bulletDisc)}},
	})
}

// TestRawBulletsPreset: format_document's bullets op sends one
// createParagraphBullets with the new preset. Over paragraphs that are
// already bulleted, does that change the list at all? It does: the list
// keeps its id and takes the numbered glyphs (seen 2026-10-21).
func TestRawBulletsPreset(t *testing.T) {
	runBulletProbes(t, []bulletProbe{
		{"ak-bulleted-to-numbered", bulletDisc, []json.RawMessage{createBullets(t, 6, 18, numbered)}, nil, nil},
	})
}

// TestRawBulletsGlyphs: what a list made from each preset reports, so a
// list can be told to match the server's presets, the condition for
// joining, by its glyphs.
func TestRawBulletsGlyphs(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	for _, preset := range []string{bulletDisc, numbered, "BULLET_CHECKBOX", "BULLET_ARROW_DIAMOND_DISC", "NUMBERED_DECIMAL_NESTED"} {
		id := probeDoc(t, c, ctx, preset, "google-docs-mcp glyphs probe "+preset+" (safe to delete)",
			[]json.RawMessage{insertAt(t, "Head\nL one\n\tL two\n\t\tL three\nTail", 1), createBullets(t, 6, 26, preset)}, nil)
		var d gdocs.Document
		if err := json.Unmarshal(rawBody(t, c, ctx, id, SuggestionsInline), &d); err != nil {
			t.Fatal(err)
		}
		for lid, l := range d.Tabs[0].DocumentTab.Lists {
			for i, nl := range l.ListProperties.NestingLevels[:3] {
				t.Logf("%-28s %s level %d: type=%q symbol=%q format=%q", preset, lid, i, nl.GlyphType, nl.GlyphSymbol, nl.GlyphFormat)
			}
		}
	}
}
