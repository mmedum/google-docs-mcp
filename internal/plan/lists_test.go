package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// listView renders what a fragment's lists send, and the bullet a
// paragraph near a list drops: insertions, deletions, bullets and indent
// clears. Text and named-style resets, which every paragraph gets, are
// left out.
func listView(t *testing.T, reqs []json.RawMessage) string {
	t.Helper()
	var out []string
	for _, r := range reqs {
		v := view(t, r)
		switch v.kind {
		case "insertText":
			out = append(out, fmt.Sprintf("ins@%d %q", v.index, v.body["text"]))
		case "deleteContentRange":
			out = append(out, "del"+rangeStr(v.rng))
		case "deleteParagraphBullets":
			out = append(out, "nobullets"+rangeStr(v.rng))
		case "createParagraphBullets":
			out = append(out, "bullets"+rangeStr(v.rng)+" "+v.body["bulletPreset"].(string))
		case "updateParagraphStyle":
			if v.body["fields"] != "indentStart,indentFirstLine" {
				break
			}
			if ps := v.body["paragraphStyle"].(map[string]any); len(ps) > 0 {
				dim := func(k string) any { return ps[k].(map[string]any)["magnitude"] }
				out = append(out, fmt.Sprintf("setindent%s %v/%v", rangeStr(v.rng), dim("indentStart"), dim("indentFirstLine")))
			} else {
				out = append(out, "indent"+rangeStr(v.rng))
			}
		}
	}
	return strings.Join(out, " ")
}

// The cases are the shapes spike F measured live; the requests after the
// insertion are each one's remedy.
func TestCompileFragmentListLevels(t *testing.T) {
	// A default bulleted list, as the Docs editor and this server make one.
	item := func(level int) *ListItem { return &ListItem{Level: level, Preset: BulletPreset} }
	cases := []struct {
		name, src string
		o         FragmentOptions
		text      string
		want      string
	}{
		{
			name: "a new list after a paragraph",
			src:  "- a\n  - b",
			o:    FragmentOptions{Suffix: true},
			text: "a\n\tb\n",
			want: "indent[10,14) bullets[10,14) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// The report: the items take the bullet of the item they go
			// before, which createParagraphBullets would skip. The top item
			// keeps it, staying in that list; the nested one is rebuilt
			// after a temporary paragraph that lets it start a level deeper
			// than the item before it.
			name: "before a top-level item",
			src:  "- a\n  - b",
			o: FragmentOptions{Suffix: true, Inherited: item(0),
				List: ListContext{At: item(0), Anchor: item(0), Preceding: []ListItem{{Level: 0, Preset: BulletPreset, Start: 4}}}},
			text: "a\n\tb\n",
			want: `nobullets[12,14) indent[12,14) ins@12 "\n" bullets[12,15) BULLET_DISC_CIRCLE_SQUARE del[12,13)`,
		},
		{
			name: "inside a list item",
			src:  "- a\n  - b",
			o:    FragmentOptions{Inline: true, Inherited: item(0), List: ListContext{At: item(0), Anchor: item(0)}},
			text: "a\n\tb",
			want: `nobullets[12,14) indent[12,14) ins@12 "\n" bullets[12,15) BULLET_DISC_CIRCLE_SQUARE del[12,13)`,
		},
		{
			// Inside a nested list, after a paragraph of the content: the
			// paragraph and the list both drop the level-1 bullet they took,
			// and the list starts afresh after the paragraph.
			name: "a list after a paragraph of the content, in a nested list",
			src:  "Para\n\n- a\n  - b",
			o: FragmentOptions{Suffix: true, Inherited: item(1),
				List: ListContext{At: item(1), Anchor: item(1), Preceding: []ListItem{{Level: 0, Preset: BulletPreset, Start: 4}}}},
			text: "Para\na\n\tb\n",
			want: "nobullets[10,14) indent[10,14) nobullets[15,19) indent[15,19) bullets[15,19) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// Google joins only a list of the same kind, so a numbered list
			// written into a bulleted one starts its own, from level 0.
			name: "a numbered list in a bulleted one",
			src:  "1. a\n    1. b",
			o: FragmentOptions{Suffix: true, Inherited: item(1),
				List: ListContext{At: item(1), Anchor: item(1), Preceding: []ListItem{{Level: 1, Preset: BulletPreset, Start: 6}, {Level: 0, Preset: BulletPreset, Start: 2}}}},
			text: "a\n\tb\n",
			want: "nobullets[10,14) indent[10,14) bullets[10,14) NUMBERED_DECIMAL_ALPHA_ROMAN",
		},
		{
			// Inherited level 1 suits only "c". "u" comes after it a level
			// up, which a list made after "c" cannot do, so "c" is rebuilt
			// too, and the window takes in "t" before it.
			name: "a kept item deeper than the item after it",
			src:  "- t\n  - c\n- u",
			o: FragmentOptions{Suffix: true, Inherited: item(1),
				List: ListContext{At: item(1), Anchor: item(0), Preceding: []ListItem{{Level: 0, Preset: BulletPreset, Start: 4}}}},
			text: "t\n\tc\nu\n",
			want: "nobullets[10,16) indent[10,16) bullets[10,16) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// Written inside a paragraph that is no list item, the first item
			// is that paragraph and keeps its style, so it takes no tabs and
			// no bullet; the list starts after it.
			name: "inside a plain paragraph, at a level",
			src:  "- one\n- two",
			o:    FragmentOptions{Inline: true, ListLevel: new(2)},
			text: "one\n\t\ttwo",
			want: `indent[14,19) ins@14 "\n" bullets[14,20) BULLET_DISC_CIRCLE_SQUARE del[14,15)`,
		},
		{
			// A checklist is no list a bulleted one joins, but the content is
			// placed at the item's level all the same: a list of its own, as
			// deep as the level-2 item it follows.
			name: "after a checklist's level-2 item",
			src:  "- x",
			o:    FragmentOptions{Suffix: true, List: ListContext{Anchor: &ListItem{Level: 2}, Preceding: []ListItem{{Level: 2, Start: 6}}}},
			text: "\t\tx\n",
			want: `indent[10,13) ins@10 "\n" bullets[10,14) BULLET_DISC_CIRCLE_SQUARE del[10,11)`,
		},
		{
			// A checklist is no list the server's presets join, so the
			// nested item before is left alone, never rebuilt as a bullet:
			// the new item starts a list of its own at the top.
			name: "after a checklist's nested item",
			src:  "- B",
			o: FragmentOptions{Suffix: true, Inherited: &ListItem{Level: 1},
				List: ListContext{At: &ListItem{Level: 1}, Anchor: &ListItem{Level: 0}, Preceding: []ListItem{{Level: 1, Start: 6}, {Level: 0, Start: 2}}}},
			text: "B\n",
			want: "nobullets[10,11) indent[10,11) bullets[10,11) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// Lowest first: the second list is made once the first has
			// consumed its tab, one place earlier than it was laid out.
			name: "two lists",
			src:  "- a\n  - b\n\nPara\n\n- c\n  - d",
			o:    FragmentOptions{Suffix: true},
			text: "a\n\tb\nPara\nc\n\td\n",
			want: "indent[10,14) bullets[10,14) BULLET_DISC_CIRCLE_SQUARE indent[19,23) bullets[19,23) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// A new marker starts a new markdown list, but Google joins two
			// bulleted lists in a row, so they are one list with one level.
			name: "two bulleted lists in a row",
			src:  "- a\n  - b\n\n* c",
			o:    FragmentOptions{Suffix: true},
			text: "a\n\tb\nc\n",
			want: "indent[10,16) bullets[10,16) BULLET_DISC_CIRCLE_SQUARE",
		},
		{
			// One level deeper than the item it follows: the list starts
			// deeper than the item before it, so a temporary paragraph
			// pins its fewest tabs.
			name: "nested under an item by list_level",
			src:  "- a\n  - b",
			o: FragmentOptions{Suffix: true, ListLevel: new(1),
				List: ListContext{Anchor: item(0), Preceding: []ListItem{{Level: 0, Preset: BulletPreset, Start: 4}}}},
			text: "\ta\n\t\tb\n",
			want: `indent[10,16) ins@10 "\n" bullets[10,17) BULLET_DISC_CIRCLE_SQUARE del[10,11)`,
		},
		{
			// Back to the top after a nested item, which is rebuilt at its
			// own level so the list can start above it.
			name: "back to the top by list_level",
			src:  "- t",
			o: FragmentOptions{Suffix: true, ListLevel: new(0),
				List: ListContext{Anchor: item(1), Preceding: []ListItem{{Level: 1, Preset: BulletPreset, Start: 6}, {Level: 0, Preset: BulletPreset, Start: 2}}}},
			text: "t\n",
			want: `ins@6 "\t" nobullets[6,12) indent[6,12) bullets[6,12) BULLET_DISC_CIRCLE_SQUARE`,
		},
		{
			// The same, with the rebuilt item indented by hand: it gets
			// its own indents back once its bullet is made again.
			name: "a rebuilt item keeps a hand-set indent",
			src:  "- t",
			o: FragmentOptions{Suffix: true, ListLevel: new(0),
				List: ListContext{Anchor: item(1), Preceding: []ListItem{{Level: 1, Preset: BulletPreset, Start: 6, IndentStart: 90, IndentFirstLine: 80}, {Level: 0, Preset: BulletPreset, Start: 2}}}},
			text: "t\n",
			want: `ins@6 "\t" nobullets[6,12) indent[6,12) bullets[6,12) BULLET_DISC_CIRCLE_SQUARE setindent[6,7) 90/80`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := CompileFragment(frag(t, tc.src), Loc{Index: 10}, tc.o)
			if err != nil {
				t.Fatal(err)
			}
			if c.Text != tc.text {
				t.Errorf("text = %q, want %q", c.Text, tc.text)
			}
			if got := listView(t, c.Requests[1:]); got != tc.want {
				t.Errorf("requests:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A replacement's new paragraphs take the bullet of the paragraph the
// text lands in, which the layout and the mode decide. The list is A
// (level 0, at 10), a1 (1, at 20), B (0, at 30), b1 (1, at 36); B is
// replaced by itself with a child.
func TestPlanReplaceListItem(t *testing.T) {
	bullet := func(level int, start int64) ListItem {
		return ListItem{Level: level, Preset: BulletPreset, Start: start}
	}
	at, after := bullet(0, 30), bullet(1, 36)
	list := ListContext{At: &at, After: &after, Anchor: &at, Preceding: []ListItem{bullet(1, 20), bullet(0, 10)}}
	op := Op{Kind: OpReplace, Seg: seg(), Target: &Rng{Start: 30, End: 36}, TargetIsBlock: true, TargetText: "B",
		Fragment: frag(t, "- B\n  - b0"), List: list}
	cases := []struct {
		name string
		mode Mode
		want string
	}{
		{
			// B gone, the text lands in b1 and takes its level-1 bullet:
			// right for b0, wrong for B, which a list made after a1 would
			// also put at level 1. So a1 is rebuilt with it, from A.
			name: "direct",
			mode: ModeDirect,
			want: `del[30,36) ins@30 "B\nb0\n" ins@20 "\t" nobullets[20,32) indent[20,32) bullets[20,32) BULLET_DISC_CIRCLE_SQUARE`,
		},
		{
			// B's deletion is only suggested, so the text lands in B and
			// takes its level-0 bullet: B keeps it, b0 is made after it.
			name: "suggest",
			mode: ModeSuggest,
			want: `del[30,36) ins@30 "B\n\tb0\n" nobullets[32,35) indent[32,35) ins@32 "\n" bullets[32,36) BULLET_DISC_CIRCLE_SQUARE del[32,33)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Plan([]Op{op}, Options{Mode: tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			if got := listView(t, res.Requests); got != tc.want {
				t.Errorf("requests:\n got %s\nwant %s", got, tc.want)
			}
		})
	}

	// The last block: the newline before it goes, and the text lands in
	// the paragraph before, a1 at level 1. B, a level up from a1, is
	// rebuilt with a1; with nothing listed before a1, from level 0.
	last := op
	last.Target = &Rng{Start: 90, End: 100}
	lastAt := bullet(0, 90)
	last.List = ListContext{At: &lastAt, Anchor: &lastAt, Preceding: []ListItem{bullet(1, 80)}}
	res, err := Plan([]Op{last}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := `del[89,99) ins@89 "\nB\nb0" ins@80 "\t" nobullets[80,92) indent[80,92) bullets[80,92) BULLET_DISC_CIRCLE_SQUARE`
	if got := listView(t, res.Requests); got != want {
		t.Errorf("last block:\n got %s\nwant %s", got, want)
	}
}

// A table or image slot is where it starts once the batch has landed: the
// tabs a list before it consumes move it back.
func TestCompileFragmentSlotAfterList(t *testing.T) {
	c, err := CompileFragment(frag(t, "- a\n  - b\n\n| x |\n|---|\n| 1 |"), Loc{Index: 10}, FragmentOptions{Suffix: true, Slots: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "a\n\tb\n\n" || len(c.Slots) != 1 || c.Slots[0].Index != 14 {
		t.Fatalf("text %q, slots %+v; want the slot at 14", c.Text, c.Slots)
	}
}

// Text replaced inside a list item, by content that is not plain text,
// merges into the item: it keeps its bullet and its style, with nothing
// deleted or reset over it.
func TestPlanReplaceTextInListItem(t *testing.T) {
	item := ListItem{Level: 1, Preset: BulletPreset, Start: 20}
	op := Op{Kind: OpReplace, Seg: seg(), Target: &Rng{Start: 32, End: 35}, TargetText: "foo",
		Fragment: frag(t, "**bar**"), List: ListContext{At: &item, Anchor: &item}}
	res, err := Plan([]Op{op}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(t, res); got != "del[32,35) ins@32 updateTextStyle[32,35) updateTextStyle[32,35)" {
		t.Fatalf("requests = %s", got)
	}
}

// Removing bullets clears the indent Google leaves behind, and gives an
// item indented by hand its own back, as a rebuilt item gets them.
func TestPlanBulletsNoneKeepsHandIndents(t *testing.T) {
	op := Op{Kind: OpBullets, Seg: seg(), Target: &Rng{Start: 10, End: 30}, Params: Params{Bullets: "none"},
		Items: []ListItem{{Level: 0, Preset: BulletPreset, Start: 10, IndentStart: 36, IndentFirstLine: 18}, {Level: 1, Preset: BulletPreset, Start: 20, IndentStart: 90, IndentFirstLine: 80}}}
	res, err := Plan([]Op{op}, Options{Mode: ModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := listView(t, res.Requests), "nobullets[10,30) indent[10,30) setindent[20,21) 90/80"; got != want {
		t.Errorf("requests:\n got %s\nwant %s", got, want)
	}
}

// list_level goes only where a list starts the content, at a level a list
// has, and a rebuild in suggest mode says what the person will see.
func TestPlanListLevel(t *testing.T) {
	list := ListContext{Anchor: &ListItem{Level: 1}, Preceding: []ListItem{{Level: 1, Preset: BulletPreset, Start: 6}, {Level: 0, Preset: BulletPreset, Start: 2}}}
	insert := func(src string, level int) Op {
		return Op{Kind: OpInsert, Seg: seg(), Insert: &Loc{Index: 10}, Fragment: frag(t, src), Params: Params{ListLevel: &level}, List: list}
	}
	for name, op := range map[string]Op{
		"on a footnote":        {Kind: OpFootnote, Seg: seg(), Insert: &Loc{Index: 10}, Fragment: frag(t, "- t"), Params: Params{ListLevel: new(0)}},
		"below the top":        insert("- t", -1),
		"deeper than a list":   insert("- t", 9),
		"content with no list": insert("Para", 0),
	} {
		if _, err := Plan([]Op{op}, Options{}); err == nil {
			t.Errorf("%s: planned, want refused", name)
		}
	}
	if _, err := Plan([]Op{insert("- t", 8)}, Options{}); err != nil {
		t.Errorf("level 8: %v", err)
	}
	for mode, want := range map[Mode]int{ModeDirect: 0, ModeSuggest: 1} {
		res, err := Plan([]Op{insert("- t", 0)}, Options{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Warnings) != want {
			t.Errorf("%s: warnings %q, want %d", mode, res.Warnings, want)
		}
	}
}
